/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubao.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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

// GetAudioFile 合成音频并保存到默认路径。
func (d *DoubaoTTS) GetAudioFile(text string) (string, error) {
	return d.GetAudioFileTo(text, DEFAULT_FILE)
}

// GetAudioFileTo 合成音频并保存到指定路径。
// 目标为 .ogg/.opus 时直接写入原始数据，其余情况尝试转码为 MP3；
// 转码不可用时保留 .ogg 原始文件并返回其路径。
func (d *DoubaoTTS) GetAudioFileTo(text string, outputPath string) (string, error) {
	if strings.TrimSpace(outputPath) == "" {
		outputPath = DEFAULT_FILE
	}

	audio, err := d.GetAudioBytes(text)
	if err != nil {
		return "", err
	}

	if isOggOutput(outputPath) {
		if err := writeAudioFile(outputPath, audio); err != nil {
			return "", err
		}
		return outputPath, nil
	}

	tempFile, err := os.CreateTemp("", "edgetts-doubao-*.ogg")
	if err != nil {
		return "", fmt.Errorf("创建临时音频文件失败: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(audio); err != nil {
		tempFile.Close()
		return "", fmt.Errorf("写入临时音频文件失败: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("关闭临时音频文件失败: %w", err)
	}

	if err := transcodeToMP3(tempPath, outputPath); err != nil {
		fallback := replaceExt(outputPath, ".ogg")
		if writeErr := writeAudioFile(fallback, audio); writeErr != nil {
			return "", fmt.Errorf("转码失败（%v）且保存原始音频失败: %w", err, writeErr)
		}
		fmt.Printf("[WARN] MP3 转码失败（%v），已保留原始音频：%s\n", err, fallback)
		return fallback, nil
	}

	return outputPath, nil
}

// GetAudioBytes 合成音频并返回原始 Ogg Opus 字节。
func (d *DoubaoTTS) GetAudioBytes(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("待合成文本为空")
	}

	segments := splitDoubaoText(text)
	if len(segments) == 0 {
		return nil, errors.New("待合成文本为空")
	}

	var audio []byte
	for _, segment := range segments {
		data, err := d.synthesizeWithRetry(segment)
		if err != nil {
			return nil, err
		}
		audio = append(audio, data...)
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
		cookie, err := resolveDoubaoCookie(d.Config.Cookie)
		if err != nil {
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

// splitDoubaoText 把文本切成不超过 doubaoMaxTextBytes 的片段。
// 先按句子切分；对缺少标点的超长片段再按 UTF-8 字符边界硬切。
func splitDoubaoText(text string) []string {
	var segments []string
	for _, segment := range SplitTextBySentencesWithMaxLength(text, doubaoMaxTextBytes) {
		segments = append(segments, splitByRuneLimit(segment, doubaoMaxTextBytes)...)
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
func doubaoAuthError(response pbResponse) error {
	if response.StatusCode == DoubaoStatusCodeAuth ||
		strings.Contains(strings.ToLower(response.StatusText), "session auth") {
		return fmt.Errorf("%w: status_code=%d status_text=%s",
			ErrDoubaoAuth, response.StatusCode, response.StatusText)
	}
	return nil
}

// isOggOutput 判断输出路径是否要求原始 Ogg Opus。
func isOggOutput(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ogg", ".opus":
		return true
	default:
		return false
	}
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

// replaceExt 替换文件扩展名；没有扩展名时追加。
func replaceExt(path, ext string) string {
	oldExt := filepath.Ext(path)
	if oldExt == "" {
		return path + ext
	}
	return strings.TrimSuffix(path, oldExt) + ext
}

// transcodeToMP3 调用 ffmpeg 把 Ogg Opus 转成 MP3。
func transcodeToMP3(source, target string) error {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return errors.New("未找到 ffmpeg")
	}

	output, err := exec.Command(ffmpegPath,
		"-y",
		"-loglevel", "error",
		"-i", source,
		"-codec:a", "libmp3lame",
		"-b:a", "48k",
		target,
	).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return fmt.Errorf("ffmpeg 执行失败: %w: %s", err, detail)
		}
		return fmt.Errorf("ffmpeg 执行失败: %w", err)
	}
	return nil
}
