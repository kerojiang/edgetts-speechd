/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubao.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// 豆包 VoiceGenie TTS 客户端。
//
// 协议流程：
//   StartTask → TaskStarted → StartSession → SessionStarted
//   → BidirectionalTTS + EndTTS → TTSResponse* → TTSEnded
//
// 返回的音频为 Ogg Opus（24kHz/32kbps）分片，按顺序拼接即为完整音频。

// ErrDoubaoAuth 表示豆包登录态失效，需要重新获取 Cookie。
var ErrDoubaoAuth = errors.New("豆包登录态失效")

// doubaoReadTimeout 是单条 WebSocket 消息的读取超时。
const doubaoReadTimeout = 30 * time.Second

// doubaoUserAgent 使用桌面 Chrome UA，与插件环境保持一致。
const doubaoUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36 Edg/156.0.0.0"

// doubaoWSEndpoint 是语音合成 WebSocket 地址，测试中可替换。
var doubaoWSEndpoint = DoubaoWSSEndpoint

// DoubaoTTS 实现 Core 接口的豆包语音合成。
type DoubaoTTS struct {
	Config *DoubaoConfig
	mutex  sync.Mutex
}

// NewDoubao 创建带默认配置的豆包 TTS 实例。
func NewDoubao() *DoubaoTTS {
	return &DoubaoTTS{Config: NewDoubaoConfig(DoubaoDefaultVoice)}
}

// Ensure DoubaoTTS implements the Core interface
var _ Core = (*DoubaoTTS)(nil)

// GetAudioFile 合成音频并保存到默认路径（Ogg Opus）。
func (d *DoubaoTTS) GetAudioFile(text string) (string, error) {
	return d.GetAudioFileTo(text, DOUBAO_DEFAULT_FILE)
}

// GetAudioFileTo 合成音频并保存到指定路径。
// 豆包只产出 Ogg Opus 原始字节，直接写入目标路径（由 Go 原生播放层解码，无需外部转码）。
func (d *DoubaoTTS) GetAudioFileTo(text string, outputPath string) (string, error) {
	if strings.TrimSpace(outputPath) == "" {
		outputPath = DOUBAO_DEFAULT_FILE
	}

	audio, err := d.GetAudioBytes(text)
	if err != nil {
		return "", err
	}

	if err := writeAudioFile(outputPath, audio); err != nil {
		return "", err
	}
	return outputPath, nil
}

// SynthesizeStream 流式合成文本：把文本切成多段并行合成，按顺序写入 out。
// 每段是独立 Ogg Opus 流（含 OpusHead）。合成方负责 close(out)。
// 首段合成完成即写入 out，使播放方可立即开始（首声 ≈ 首段合成时间）。
// 任一段失败返回错误（已写入的段仍由播放方处理）。
func (d *DoubaoTTS) SynthesizeStream(text string, out chan<- []byte) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("待合成文本为空")
	}

	segments := splitDoubaoText(text)
	if len(segments) == 0 {
		return errors.New("待合成文本为空")
	}

	// 每段一个缓冲 channel，并行合成
	segChans := make([]chan []byte, len(segments))
	for i := range segChans {
		segChans[i] = make(chan []byte, 1)
	}

	sem := make(chan struct{}, doubaoParallelLimit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, segment := range segments {
		wg.Add(1)
		go func(idx int, seg string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := d.synthesizeWithRetry(seg)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				close(segChans[idx])
				return
			}
			segChans[idx] <- data
		}(i, segment)
	}

	// 按序读出并写入 out（流式：段完成即写，保序；写阻塞为背压，播放方取走才继续）
	var writeErr error
	for i := range segChans {
		data, ok := <-segChans[i]
		if !ok {
			// 段合成失败
			if firstErr != nil {
				writeErr = fmt.Errorf("第 %d 段合成失败: %w", i+1, firstErr)
			} else {
				writeErr = fmt.Errorf("第 %d 段合成失败", i+1)
			}
			break
		}
		out <- data
	}
	wg.Wait()
	return writeErr
}

// GetAudioBytes 合成音频并返回原始 Ogg Opus 字节。
// 多段文本并行合成（保序拼接），大幅缩短长文本合成耗时。
func (d *DoubaoTTS) GetAudioBytes(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("待合成文本为空")
	}

	segments := splitDoubaoText(text)
	if len(segments) == 0 {
		return nil, errors.New("待合成文本为空")
	}

	// 单段直接串行合成（无并行开销，且避免无意义 goroutine）
	if len(segments) == 1 {
		return d.synthesizeWithRetry(segments[0])
	}

	// 多段并行合成，按输入顺序保序拼接
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make([][]byte, len(segments))
	errs := make([]error, len(segments))
	sem := make(chan struct{}, doubaoParallelLimit)

	var wg sync.WaitGroup
	for i, segment := range segments {
		wg.Add(1)
		go func(idx int, seg string) {
			defer wg.Done()
			// 若某段已失败，跳过尚未开始的分段
			select {
			case <-ctx.Done():
				errs[idx] = ctx.Err()
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			data, err := d.synthesizeWithRetry(seg)
			if err != nil {
				errs[idx] = err
				cancel() // 失败则取消剩余排队
				return
			}
			results[idx] = data
		}(i, segment)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("第 %d 段合成失败: %w", i+1, err)
		}
	}

	var audio []byte
	for _, r := range results {
		audio = append(audio, r...)
	}
	if len(audio) == 0 {
		return nil, errors.New("豆包未返回音频数据")
	}
	return audio, nil
}

// synthesizeWithRetry 在登录态失效时清空缓存并重试一次。
func (d *DoubaoTTS) synthesizeWithRetry(text string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt < 2; attempt++ {
		cookie, err := d.attemptCookie(attempt)
		if err != nil {
			if lastErr != nil {
				return nil, fmt.Errorf("豆包登录态失效且重新获取 Cookie 失败: %w（原始错误: %w）",
					err, lastErr)
			}
			return nil, err
		}

		audio, err := d.synthesize(text, cookie)
		if err == nil {
			return audio, nil
		}
		lastErr = err

		if !errors.Is(err, ErrDoubaoAuth) {
			return nil, err
		}
		// 登录态失效：删除缓存，下一轮 resolveDoubaoCookie 会重新从 Edge 提取。
		_ = clearDoubaoCookie()
	}

	return nil, fmt.Errorf("豆包登录态失效，请重新登录 Edge 中的豆包或设置 %s: %w",
		envDoubaoCookie, lastErr)
}

// attemptCookie 返回本次尝试使用的 Cookie。
// 第一次按「显式参数 > 环境变量 > 缓存 > Edge 提取」解析；
// 登录态失效后的重试跳过显式参数与环境变量，强制重新从 Edge 提取，
// 避免一直复用同一个失效值。
func (d *DoubaoTTS) attemptCookie(attempt int) (string, error) {
	if attempt == 0 {
		return resolveDoubaoCookie(d.Config.Cookie)
	}
	return extractAndSaveDoubaoCookie()
}

// synthesize 完成一次完整的 WebSocket 语音合成。
func (d *DoubaoTTS) synthesize(text string, cookie string) ([]byte, error) {
	wsURL, err := doubaoWSURL()
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	header.Set("Origin", DoubaoOrigin)
	header.Set("Cookie", cookie)
	header.Set("User-Agent", doubaoUserAgent)

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, fmt.Errorf("%w: WebSocket 握手被拒绝（HTTP %d）", ErrDoubaoAuth, resp.StatusCode)
		}
		if resp != nil {
			return nil, fmt.Errorf("连接豆包语音服务失败（HTTP %d）: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("连接豆包语音服务失败: %w", err)
	}
	defer conn.Close()

	taskID := ""
	sessionSent := false
	var audio []byte

	send := func(event, payload string) error {
		message := encodePBRequest(pbRequest{
			Appkey:    DoubaoAppKey,
			Namespace: DoubaoNamespace,
			Event:     event,
			Payload:   payload,
			TaskID:    taskID,
		})
		if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
			return fmt.Errorf("发送 %s 失败: %w", event, err)
		}
		return nil
	}

	if err := send("StartTask", ""); err != nil {
		return nil, err
	}

	for {
		if err := conn.SetReadDeadline(time.Now().Add(doubaoReadTimeout)); err != nil {
			return nil, fmt.Errorf("设置读取超时失败: %w", err)
		}

		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("读取豆包语音响应失败: %w", err)
		}
		if messageType != websocket.BinaryMessage {
			continue
		}

		response, err := decodePBResponse(raw)
		if err != nil {
			return nil, fmt.Errorf("解析豆包语音响应失败: %w", err)
		}

		if authErr := doubaoAuthError(response); authErr != nil {
			return nil, authErr
		}
		if statusErr := doubaoStatusError(response); statusErr != nil {
			return nil, statusErr
		}

		switch response.Event {
		case "TaskStarted":
			if response.TaskID != "" {
				taskID = response.TaskID
			}
			payload, err := d.buildStartSessionPayload()
			if err != nil {
				return nil, err
			}
			if err := send("StartSession", payload); err != nil {
				return nil, err
			}

		case "SessionStarted":
			if sessionSent {
				continue
			}
			sessionSent = true

			ttsPayload, err := json.Marshal(map[string]string{"text": text})
			if err != nil {
				return nil, fmt.Errorf("序列化合成文本失败: %w", err)
			}
			if err := send("BidirectionalTTS", string(ttsPayload)); err != nil {
				return nil, err
			}
			if err := send("EndTTS", ""); err != nil {
				return nil, err
			}

		case "TTSResponse":
			if len(response.Data) > 0 {
				audio = append(audio, response.Data...)
			}

		case "TTSEnded":
			if len(audio) == 0 {
				return nil, errors.New("豆包返回 TTSEnded 但未包含音频数据")
			}
			return audio, nil

		case "SessionFailed", "TaskFailed":
			return nil, fmt.Errorf("豆包语音服务返回 %s: status_code=%d status_text=%s",
				response.Event, response.StatusCode, response.StatusText)
		}
	}
}

// buildStartSessionPayload 构造 StartSession 的 JSON 负载。
func (d *DoubaoTTS) buildStartSessionPayload() (string, error) {
	postProcess := map[string]any{
		"pitch":       d.Config.Pitch,
		"speech_rate": d.Config.Rate,
	}

	extraJSON, err := json.Marshal(map[string]any{
		"post_process":      postProcess,
		"music_ext_for_tts": "",
		"network_level":     8,
		"chat_next":         nil,
	})
	if err != nil {
		return "", fmt.Errorf("序列化 extra 失败: %w", err)
	}

	payload := map[string]any{
		"business":        1,
		"conversation_id": "",
		"chat": map[string]any{
			"bot_id":               "",
			"conversation_id":      "",
			"question_id":          "",
			"message_id":           "",
			"new_conversation":     false,
			"chat_next":            nil,
			"enable_toast_reading": "true",
			"extra":                map[string]any{"chat_next": nil},
		},
		"request_type":        4,
		"enable_text_reading": true,
		"interrupt_type":      0,
		"query_mode":          2,
		"tts": map[string]any{
			"speaker": d.Config.Voice,
			"audio_config": map[string]any{
				"bit_rate":    32000,
				"format":      "ogg_opus",
				"sample_rate": 24000,
			},
			"extra": map[string]any{
				"chat_next":    nil,
				"post_process": postProcess,
			},
		},
		"extra": map[string]any{
			"enable_toast_reading": "true",
			"chat_next":            nil,
			"extra":                string(extraJSON),
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化 StartSession 负载失败: %w", err)
	}
	return string(raw), nil
}

// doubaoWSURL 构造带随机设备标识的连接地址。
func doubaoWSURL() (string, error) {
	query := url.Values{}
	query.Set("api_app_key", DoubaoAppKey)
	query.Set("namespace", DoubaoNamespace)
	query.Set("version_code", "20800")
	query.Set("language", "zh")
	query.Set("device_platform", "web")
	query.Set("pkg_type", "release_version")
	query.Set("pc_version", "3.9.0")
	query.Set("region", "CN")
	query.Set("sys_region", "CN")
	query.Set("samantha_web", "1")
	query.Set("use-olympus-account", "1")
	query.Set("aid", DoubaoAID)
	query.Set("real_aid", DoubaoAID)
	query.Set("browser_language", "zh-CN")
	query.Set("is_new_user", "0")

	for _, key := range []string{"web_id", "tea_uuid", "web_tab_id", "device_id"} {
		id, err := randomHex(16)
		if err != nil {
			return "", fmt.Errorf("生成 %s 失败: %w", key, err)
		}
		query.Set(key, id)
	}

	return doubaoWSEndpoint + "?" + query.Encode(), nil
}

// randomHex 生成 n 字节的随机十六进制字符串。
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// splitDoubaoText 把文本切成不超过 doubaoParallelChunkBytes 的片段（并行合成粒度）。
// 先按句子切分；对缺少标点的超长片段再按 UTF-8 字符边界硬切。
func splitDoubaoText(text string) []string {
	var segments []string
	for _, segment := range SplitTextBySentencesWithMaxLength(text, doubaoParallelChunkBytes) {
		segments = append(segments, splitByRuneLimit(segment, doubaoParallelChunkBytes)...)
	}
	return segments
}

// splitByRuneLimit 按字符边界切分文本，保证每段不超过 maxBytes 且不切碎 UTF-8。
func splitByRuneLimit(text string, maxBytes int) []string {
	if text == "" {
		return nil
	}
	if len(text) <= maxBytes {
		return []string{text}
	}

	var segments []string
	var current strings.Builder

	for _, r := range text {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = 1
		}
		if current.Len()+size > maxBytes {
			segments = append(segments, current.String())
			current.Reset()
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	return segments
}

// doubaoStatusError 把非成功的状态码转换成错误。
// 已知进度事件即使带非成功状态码也继续走流程，避免误判。
func doubaoStatusError(response pbResponse) error {
	if response.StatusCode == 0 || response.StatusCode == DoubaoStatusCodeSuccess {
		return nil
	}

	switch response.Event {
	case "TaskStarted", "SessionStarted", "TTSResponse", "TTSEnded", "TTSSentenceStart", "TTSSentenceEnd":
		return nil
	}

	return fmt.Errorf("豆包语音服务错误: event=%s status_code=%d status_text=%s",
		response.Event, response.StatusCode, response.StatusText)
}

// doubaoAuthError 判断响应是否表示登录态失效。
//
// 已用无效 Cookie 直连真实服务核实：合成链路的失效响应固定为
// status_code=40200001 status_text=session auth, userID or appID is empty。
// 列表接口的 710012001 只出现在 HTTP 列表接口，不应在此判定。
func doubaoAuthError(response pbResponse) error {
	if response.StatusCode == DoubaoStatusCodeAuth ||
		strings.Contains(strings.ToLower(response.StatusText), "session auth") {
		return fmt.Errorf("%w: status_code=%d status_text=%s",
			ErrDoubaoAuth, response.StatusCode, response.StatusText)
	}
	return nil
}

// writeAudioFile 写入音频文件并创建上级目录。
func writeAudioFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建输出目录失败: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入音频文件失败: %w", err)
	}
	return nil
}

// replaceExt 已随 ffmpeg 转码移除（豆包只产出 Ogg Opus，由 Go 原生播放层直接解码）。
// transcodeToMP3 已随 ffmpeg 转码移除。
