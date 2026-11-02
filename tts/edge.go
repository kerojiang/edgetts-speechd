/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edge-tts
 * 文件名称:edge.go
 * 修改日期:2026/02/21 19:38:22
 * 作者:kerojiang
 */

package tts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"edgetts-speechd/timing"
)

// EdgeTTS implements the COre interface for Edge TTS service
type EdgeTTS struct {
	Config        *TTSConfig
	Texts         [][]byte
	OriginalTexts [][]byte // Store original text segments for continuous playback
	State         *CommunicateState
	Proxy         string
	HTTPClient    *http.Client
	mutex         sync.Mutex
}

// TTSChunk represents a chunk of TTS data
type TTSChunk struct {
	Type     string `json:"type"` // "audio", "WordBoundary", "SentenceBoundary"
	Data     []byte `json:"data,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	Duration int64  `json:"duration,omitempty"`
	Text     string `json:"text,omitempty"`
}

// CommunicateState represents the state of communication
type CommunicateState struct {
	PartialText        []byte `json:"partial_text"`
	OffsetCompensation int64  `json:"offset_compensation"`
	LastDurationOffset int64  `json:"last_duration_offset"`
	StreamWasCalled    bool   `json:"stream_was_called"`
}

// VoiceInfo represents voice information
type VoiceInfo struct {
	Name           string `json:"Name"`
	ShortName      string `json:"ShortName"`
	Gender         string `json:"Gender"`
	Locale         string `json:"Locale"`
	SuggestedCodec string `json:"SuggestedCodec"`
	FriendlyName   string `json:"FriendlyName"`
	Status         string `json:"Status"`
}

// Constants
const (
	BaseURL             = "speech.platform.bing.com/consumer/speech/synthesize/readaloud"
	TrustedClientToken  = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	DefaultVoice        = "en-US-EmmaMultilingualNeural"
	ChromiumFullVersion = "143.0.3650.75"
	SecMSGECVersion     = "2-" + ChromiumFullVersion
	WinEpoch            = 11644473600
	SToNS               = 1e9
)

var (
	WSSURL    = "wss://" + BaseURL + "/edge/v1?TrustedClientToken=" + TrustedClientToken
	VoiceList = "https://" + BaseURL + "/voices/list?trustedclienttoken=" + TrustedClientToken

	ChromiumMajorVersion = func() string {
		parts := strings.Split(ChromiumFullVersion, ".")
		if len(parts) > 0 {
			return parts[0]
		}
		return "143"
	}()

	BaseHeaders = map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36" +
			" (KHTML, like Gecko) Chrome/" + ChromiumMajorVersion + ".0.0.0 Safari/537.36" +
			" Edg/" + ChromiumMajorVersion + ".0.0.0",
		"Accept-Encoding": "gzip, deflate, br, zstd",
		"Accept-Language": "en-US,en;q=0.9",
	}

	WSSHeaders = func() map[string]string {
		headers := map[string]string{
			"Pragma":                "no-cache",
			"Cache-Control":         "no-cache",
			"Origin":                "chrome-extension://jdiccldimpdaibmpdkjnbmckianbfold",
			"Sec-WebSocket-Version": "13",
		}
		for k, v := range BaseHeaders {
			headers[k] = v
		}
		return headers
	}()

	VoiceHeaders = func() map[string]string {
		headers := map[string]string{
			"Authority": "speech.platform.bing.com",
			"Sec-CH-UA": "\" Not;A Brand\";v=\"99\", \"Microsoft Edge\";v=\"" + ChromiumMajorVersion + "\"," +
				" \"Chromium\";v=\"" + ChromiumMajorVersion + "\"",
			"Sec-CH-UA-Mobile": "?0",
			"Accept":           "*/*",
			"Sec-Fetch-Site":   "none",
			"Sec-Fetch-Mode":   "cors",
			"Sec-Fetch-Dest":   "empty",
		}
		for k, v := range BaseHeaders {
			headers[k] = v
		}
		return headers
	}()
)

// DRM related variables and functions
var (
	clockSkewSeconds = 0.0
)

// AdjClockSkewSeconds adjusts the clock skew in seconds
func AdjClockSkewSeconds(skewSeconds float64) {
	clockSkewSeconds += skewSeconds
}

// GetUnixTimestamp gets the current timestamp with clock skew correction
func GetUnixTimestamp() float64 {
	return float64(time.Now().Unix()) + clockSkewSeconds
}

// ParseRFC2616Date parses an RFC 2616 date string into a Unix timestamp
func ParseRFC2616Date(date string) (float64, error) {
	t, err := time.Parse("Mon, 02 Jan 2006 15:04:05 MST", date)
	if err != nil {
		return 0, err
	}
	return float64(t.Unix()), nil
}

// HandleClientResponseError handles a client response error
func HandleClientResponseError(resp *http.Response) error {
	serverDate := resp.Header.Get("Date")
	if serverDate == "" {
		return fmt.Errorf("no server date in headers")
	}

	serverDateParsed, err := ParseRFC2616Date(serverDate)
	if err != nil {
		return fmt.Errorf("failed to parse server date: %w", err)
	}

	clientDate := GetUnixTimestamp()
	AdjClockSkewSeconds(serverDateParsed - clientDate)

	return nil
}

// GenerateSecMSGEC generates the Sec-MS-GEC token value.
// 自 2026-10 起微软要求 v2 格式（<16 hex>-<64 hex>），旧 v1 令牌会被 403 拒绝。
func GenerateSecMSGEC() string {
	return generateSecMSGECV2(GetUnixTimestamp())
}

// GenerateMUID generates a random MUID
func GenerateMUID() string {
	rand.Seed(time.Now().UnixNano())
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return strings.ToUpper(hex.EncodeToString(bytes))
}

// HeadersWithMUID returns headers with MUID cookie added
func HeadersWithMUID(headers map[string]string) map[string]string {
	combinedHeaders := make(map[string]string)
	for k, v := range headers {
		combinedHeaders[k] = v
	}

	if _, exists := combinedHeaders["Cookie"]; !exists {
		combinedHeaders["Cookie"] = fmt.Sprintf("muid=%s;", GenerateMUID())
	}

	return combinedHeaders
}

// NewEdge creates a new EdgeTTS instance with default configuration
func NewEdge() *EdgeTTS {
	config := NewTTSConfig(DEFAULT_VOICE)
	return &EdgeTTS{
		Config:        config,
		Texts:         [][]byte{}, // Initialize with empty slice
		OriginalTexts: [][]byte{}, // Initialize with empty slice
		State: &CommunicateState{
			StreamWasCalled: false,
		},
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// NewEdgeTTS creates a new EdgeTTS instance
func NewEdgeTTS(text, voice string) (*EdgeTTS, error) {
	// Use default values if not provided
	if voice == "" {
		voice = DEFAULT_VOICE
	}

	return newEdgeWithConfig(text, NewTTSConfig(voice))
}

// newEdgeWithConfig 使用给定配置构造 EdgeTTS 实例。
// 复制配置，避免调用方后续修改影响已构造的实例。
func newEdgeWithConfig(text string, base *TTSConfig) (*EdgeTTS, error) {
	config := *base
	if config.Voice == "" {
		config.Voice = DEFAULT_VOICE
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Process text
	processedText := removeIncompatibleCharacters(text)
	escapedText := escapeHTML(processedText)

	var texts [][]byte
	var originalTexts [][]byte

	if config.SentenceSplit {
		// Split text by sentences first, then by byte length if needed
		textChunks := SplitTextBySentencesWithMaxLength(escapedText, 4096)
		for _, chunk := range textChunks {
			texts = append(texts, []byte(chunk))
			originalTexts = append(originalTexts, []byte(chunk))
		}
	} else {
		// Use original byte-length based splitting
		texts = splitTextByByteLength([]byte(escapedText), 4096)
		originalTexts = texts // Same as processed texts
	}

	return &EdgeTTS{
		Config:        &config,
		Texts:         texts,
		OriginalTexts: originalTexts,
		State: &CommunicateState{
			StreamWasCalled: false,
		},
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}, nil
}

// synthesizeTo 把 text 的合成结果写入 w，使用给定配置（newEdgeWithConfig 会复制，不会修改 cfg）。
// 所有取音频的入口（写文件 / 取字节 / 流式播放）都走这里，
// 以保证错误优先透出、零音频必定报错。
func synthesizeTo(ctx context.Context, text string, cfg *TTSConfig, w io.Writer) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("待合成文本为空")
	}
	inst, err := newEdgeWithConfig(text, cfg)
	if err != nil {
		return fmt.Errorf("构造 Edge 合成实例失败: %w", err)
	}
	chunks, errs := inst.Stream(ctx)
	return forwardEdgeAudio(ctx, chunks, errs, w)
}

// GetAudioFile implements the COre interface method
func (e *EdgeTTS) GetAudioFile(text string) (string, error) {
	return e.GetAudioFileTo(text, DEFAULT_FILE)
}

// GetAudioFileTo implements the COre interface method - saves to specified path
// 采用同目录临时文件 + 成功后 rename：合成失败时不会留下 0 字节文件，也不会破坏已有文件。
func (e *EdgeTTS) GetAudioFileTo(text string, outputPath string) (string, error) {
	if err := writeFileAtomic(outputPath, func(w io.Writer) error {
		return synthesizeTo(context.Background(), text, e.Config, w)
	}); err != nil {
		return "", err
	}
	return outputPath, nil
}

// GetAudioBytes gets audio as bytes (corrected method name)
func (e *EdgeTTS) GetAudioBytes(text string) ([]byte, error) {
	var buf bytes.Buffer
	if err := synthesizeTo(context.Background(), text, e.Config, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFileAtomic 先写同目录临时文件，成功后 rename 到目标路径。
// 失败时删除临时文件并保持目标文件原样（避免 os.Create 提前截断已有文件）。
func writeFileAtomic(path string, write func(io.Writer) error) error {
	// os.CreateTemp 固定创建 0600，这里沿用目标文件已有权限；
	// 目标不存在时用 0644（与原 os.Create + 常规 umask 的结果一致）。
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置临时文件权限失败: %w", err)
	}

	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("替换目标文件失败: %w", err)
	}
	committed = true
	return nil
}

// Stream streams audio and metadata from the service
func (e *EdgeTTS) Stream(ctx context.Context) (<-chan *TTSChunk, <-chan error) {
	chunkChan := make(chan *TTSChunk)
	errChan := make(chan error, 1)

	go func() {
		defer close(chunkChan)
		defer close(errChan)

		e.mutex.Lock()
		if e.State.StreamWasCalled {
			e.mutex.Unlock()
			errChan <- fmt.Errorf("stream can only be called once")
			return
		}
		e.State.StreamWasCalled = true
		e.mutex.Unlock()

		if e.Config.ContinuousPlayback {
			// Continuous playback mode: send all text as one request
			if len(e.OriginalTexts) == 0 {
				errChan <- fmt.Errorf("no text segments available for continuous playback")
				return
			}
			// Use the first text segment as PartialText for compatibility
			e.State.PartialText = e.OriginalTexts[0]

			chunks, errs := e.streamSingle(ctx)
			if err := relayEdgeStream(ctx, chunks, errs, chunkChan); err != nil {
				errChan <- err
			}
		} else {
			// Standard mode: process each text segment separately
			for _, partialText := range e.Texts {
				e.State.PartialText = partialText

				chunks, errs := e.streamSingle(ctx)
				if err := relayEdgeStream(ctx, chunks, errs, chunkChan); err != nil {
					errChan <- err
					return
				}
			}
		}
	}()

	return chunkChan, errChan
}

// relayEdgeStream 把单个 streamSingle 的 chunk/err 转发到 out。
// 关键：errChan 正常关闭（无错误）时必须置 nil 并继续读 chunkChan，
// 否则会在多段合成时提前返回、静默丢弃后续段（历史 bug）。
func relayEdgeStream(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, out chan<- *TTSChunk) error {
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				// chunks 已关闭：先取走 errs 中可能已缓冲的最终错误。
				// streamSingle 先发错误再关闭通道，若此处直接 return nil，
				// select 随机命中本分支会静默丢弃真实错误（音频被截断却报告成功）。
				select {
				case err := <-errs:
					if err != nil {
						return err
					}
				default:
				}
				return nil
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return ctx.Err()
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil // 正常结束：不再监听，继续读直到 chunks 关闭
				continue
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// forwardEdgeAudio 把 Edge 音频块按序写入 w；零音频或流错误时返回错误。
func forwardEdgeAudio(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, w io.Writer) error {
	written := 0
	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				select {
				case err := <-errs:
					if err != nil {
						return err
					}
				default:
				}
				if written == 0 {
					return fmt.Errorf("Edge 未返回任何音频数据")
				}
				return nil
			}
			if chunk == nil || chunk.Type != "audio" || len(chunk.Data) == 0 {
				continue
			}
			n, err := w.Write(chunk.Data)
			if err != nil {
				return fmt.Errorf("写入音频流失败: %w", err)
			}
			if written == 0 {
				timing.Logf("tts: Edge 首个音频块（%d 字节）", n)
			}
			written += n
		case err, ok := <-errs:
			if !ok {
				errs = nil // 正常关闭：继续读 chunks
				continue
			}
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// classifyReadError 把 WebSocket 读取错误转换为需要上报的错误。
// Edge 的正常结束由 turn.end 文本消息触发（handleTextMessage 返回 errStreamComplete），
// 所以任何读取错误——包括读超时——都表明音频被截断，不能当作正常结束。
func classifyReadError(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("WebSocket 读取超时（音频可能不完整）: %w", err)
	}
	return fmt.Errorf("failed to read WebSocket message: %w", err)
}

// SynthesizeStream 流式合成 Edge 文本：把音频块直接写入 w。
// 播放模式固定标准分段（ContinuousPlayback=false）：实测 true 对 >4096 字节
// 文本只发第一段并报 websocket close 1007 SSML is invalid。
func (e *EdgeTTS) SynthesizeStream(ctx context.Context, text string, w io.Writer) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("待合成文本为空")
	}
	cfg := *e.Config
	cfg.ContinuousPlayback = false
	return synthesizeTo(ctx, text, &cfg, w)
}

// Helper methods for WebSocket communication

func (e *EdgeTTS) createWebSocketConnection(ctx context.Context) (*websocket.Conn, error) {
	// Get base headers and add MUID
	headers := HeadersWithMUID(WSSHeaders)

	// Create header map without duplicates
	header := http.Header{}

	// Add headers, avoiding duplicates
	for key, value := range headers {
		// Skip Sec-WebSocket-Version as it's handled by the dialer
		if strings.ToLower(key) != "sec-websocket-version" {
			header.Set(key, value)
		}
	}

	// Log connection details
	secGEC := GenerateSecMSGEC()
	// Connection details

	// Construct WebSocket URL
	wsURL := fmt.Sprintf("%s&ConnectionId=%s&Sec-MS-GEC=%s&Sec-MS-GEC-Version=%s",
		WSSURL,
		connectID(),
		secGEC,
		SecMSGECVersion)

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
		// Let the dialer handle Sec-WebSocket-Version
	}

	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			// WebSocket handshake failed
		}
		return nil, fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	// WebSocket connected
	return conn, nil
}

func (e *EdgeTTS) sendCommandRequest(conn *websocket.Conn) error {
	wordBoundary := e.Config.Boundary == "WordBoundary"
	wd := "true"
	sq := "false"
	if !wordBoundary {
		wd = "false"
		sq = "true"
	}

	command := fmt.Sprintf("X-Timestamp:%s\r\n"+
		"Content-Type:application/json; charset=utf-8\r\n"+
		"Path:speech.config\r\n\r\n"+
		`{"context":{"synthesis":{"audio":{"metadataoptions":{"sentenceBoundaryEnabled":"%s","wordBoundaryEnabled":"%s"},"outputFormat":"audio-24khz-48kbitrate-mono-mp3"}}}}`,
		dateToString(), sq, wd)

	// Sending command request
	return conn.WriteMessage(websocket.TextMessage, []byte(command))
}

func (e *EdgeTTS) sendSSMLRequest(conn *websocket.Conn) error {
	var ssml string

	if e.Config.ContinuousPlayback && len(e.OriginalTexts) > 0 {
		// Use continuous playback SSML with all original text segments
		ssml = mkssmlContinuous(e.Config, e.OriginalTexts)
	} else {
		// Use standard SSML with current partial text
		ssml = mkssml(e.Config, e.State.PartialText)
	}

	request := ssmlHeadersPlusData(connectID(), dateToString(), ssml)

	// Sending SSML request
	return conn.WriteMessage(websocket.TextMessage, []byte(request))
}

func (e *EdgeTTS) streamSingle(ctx context.Context) (<-chan *TTSChunk, <-chan error) {
	chunkChan := make(chan *TTSChunk)
	errChan := make(chan error, 1)

	go func() {
		defer close(chunkChan)
		defer close(errChan)

		// Create WebSocket connection with retry logic
		var wsConn *websocket.Conn
		var err error

		for attempt := 0; attempt < 3; attempt++ {
			wsConn, err = e.createWebSocketConnection(ctx)
			if err == nil {
				break
			}

			if attempt < 2 {
				// WebSocket connection attempt failed
				time.Sleep(time.Duration(attempt+1) * time.Second)
			}
		}

		if err != nil {
			errChan <- fmt.Errorf("failed to create WebSocket connection after 3 attempts: %w", err)
			return
		}
		defer wsConn.Close()

		// Send command request
		if err := e.sendCommandRequest(wsConn); err != nil {
			errChan <- fmt.Errorf("failed to send command request: %w", err)
			return
		}

		// Send SSML request
		if err := e.sendSSMLRequest(wsConn); err != nil {
			errChan <- fmt.Errorf("failed to send SSML request: %w", err)
			return
		}

		audioWasReceived := false
		messageCount := 0

		// Read messages from WebSocket
		for {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			default:
				// Set read deadline to handle connection timeouts
				wsConn.SetReadDeadline(time.Now().Add(30 * time.Second))

				messageType, data, err := wsConn.ReadMessage()
				if err != nil {
					// 正常结束由 turn.end 文本消息触发（handleTextMessage 返回 errStreamComplete），
					// 因此这里任何读取错误（含超时）都意味着音频被截断，必须上报。
					errChan <- classifyReadError(err)
					return
				}

				messageCount++
				// Received message

				switch messageType {
				case websocket.TextMessage:
					if err := e.handleTextMessage(data, chunkChan, &audioWasReceived); err != nil {
						if err == errStreamComplete {
							// 正常完成，不向 errChan 发送错误
							return
						}
						errChan <- fmt.Errorf("failed to handle text message: %w", err)
						return
					}
				case websocket.BinaryMessage:
					audioChunk, err := e.handleBinaryMessage(data)
					if err != nil {
						errChan <- fmt.Errorf("failed to handle binary message: %w", err)
						return
					}
					if audioChunk != nil {
						audioWasReceived = true
						select {
						case chunkChan <- audioChunk:
						case <-ctx.Done():
							errChan <- ctx.Err()
							return
						}
					}
				}
			}
		}
	}()

	return chunkChan, errChan
}

// Error to signal stream completion
var errStreamComplete = fmt.Errorf("stream completed")

func (e *EdgeTTS) handleTextMessage(data []byte, chunkChan chan<- *TTSChunk, audioWasReceived *bool) error {
	parameters, msgData := getHeadersAndData(data, bytes.Index(data, []byte("\r\n\r\n")))

	path, exists := parameters["Path"]
	if !exists {
		return fmt.Errorf("missing Path in message")
	}

	// Handling text message

	switch string(path) {
	case "audio.metadata":
		chunk, err := e.parseMetadata(msgData)
		if err != nil {
			return fmt.Errorf("failed to parse metadata: %w", err)
		}
		chunkChan <- chunk

		// Update state
		e.State.LastDurationOffset = chunk.Offset + chunk.Duration
		e.State.OffsetCompensation = e.State.LastDurationOffset + 8750000 // Add padding

	case "turn.end":
		// Reset for next request
		e.State.OffsetCompensation = e.State.LastDurationOffset + 8750000
		*audioWasReceived = true
		return errStreamComplete // Signal successful completion

	case "response", "turn.start":
		// Ignore these messages
		// Ignoring message
		return nil

	default:
		return fmt.Errorf("unknown path: %s", path)
	}

	return nil
}

func (e *EdgeTTS) handleBinaryMessage(data []byte) (*TTSChunk, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("binary message too short")
	}

	headerLength := int(data[0])<<8 | int(data[1])
	if headerLength > len(data) {
		return nil, fmt.Errorf("invalid header length")
	}

	parameters, msgData := getHeadersAndData(data, headerLength)

	path, exists := parameters["Path"]
	if !exists || string(path) != "audio" {
		return nil, fmt.Errorf("unexpected binary message path: %s", path)
	}

	contentType, hasContentType := parameters["Content-Type"]
	if !hasContentType {
		if len(msgData) == 0 {
			return nil, nil // Empty message, skip
		}
		return nil, fmt.Errorf("binary message with no Content-Type but with data")
	}

	if string(contentType) != "audio/mpeg" {
		return nil, fmt.Errorf("unexpected Content-Type: %s", contentType)
	}

	if len(msgData) == 0 {
		return nil, fmt.Errorf("missing audio data")
	}

	// Received audio chunk

	return &TTSChunk{
		Type: "audio",
		Data: msgData,
	}, nil
}

func (e *EdgeTTS) parseMetadata(data []byte) (*TTSChunk, error) {
	var metadata struct {
		Metadata []struct {
			Type string `json:"Type"`
			Data struct {
				Offset   int64 `json:"Offset"`
				Duration int64 `json:"Duration"`
				Text     struct {
					Text string `json:"Text"`
				} `json:"text"`
			} `json:"Data"`
		} `json:"Metadata"`
	}

	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	for _, metaObj := range metadata.Metadata {
		switch metaObj.Type {
		case "WordBoundary", "SentenceBoundary":
			currentOffset := metaObj.Data.Offset + e.State.OffsetCompensation
			chunk := &TTSChunk{
				Type:     metaObj.Type,
				Offset:   currentOffset,
				Duration: metaObj.Data.Duration,
				Text:     unescapeHTML(metaObj.Data.Text.Text),
			}
			// Metadata info
			return chunk, nil
		case "SessionEnd":
			continue
		default:
			return nil, fmt.Errorf("unknown metadata type: %s", metaObj.Type)
		}
	}

	return nil, fmt.Errorf("no WordBoundary metadata found")
}

// Utility functions

func getHeadersAndData(data []byte, headerLength int) (map[string][]byte, []byte) {
	headers := make(map[string][]byte)
	headerData := data[:headerLength]

	lines := bytes.Split(headerData, []byte("\r\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		parts := bytes.SplitN(line, []byte(":"), 2)
		if len(parts) == 2 {
			key := bytes.TrimSpace(parts[0])
			value := bytes.TrimSpace(parts[1])
			headers[string(key)] = value
		}
	}

	return headers, data[headerLength+2:]
}

func removeIncompatibleCharacters(text string) string {
	chars := []rune(text)

	for i, char := range chars {
		code := int(char)
		if (code >= 0 && code <= 8) || (code >= 11 && code <= 12) || (code >= 14 && code <= 31) {
			chars[i] = ' '
		}
	}

	return string(chars)
}

func escapeHTML(text string) string {
	// Simple HTML escaping
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	text = strings.ReplaceAll(text, "'", "&#39;")
	return text
}

func unescapeHTML(text string) string {
	// Simple HTML unescaping
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	return text
}

func connectID() string {
	// Generate UUID-like string
	b := make([]byte, 16)
	// In a real implementation, use crypto/rand
	for i := range b {
		b[i] = byte(rand.Intn(256))
	}
	return fmt.Sprintf("%x%x%x%x%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func dateToString() string {
	return time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
}

func mkssml(config *TTSConfig, escapedText []byte) string {
	return fmt.Sprintf("<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'>"+
		"<voice name='%s'>"+
		"<prosody pitch='%s' rate='%s' volume='%s'>"+
		"%s"+
		"</prosody>"+
		"</voice>"+
		"</speak>",
		config.Voice, config.Pitch, config.Rate, config.Volume, string(escapedText))
}

// mkssmlContinuous creates SSML for continuous playback without pauses between segments
func mkssmlContinuous(config *TTSConfig, textSegments [][]byte) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<speak version='1.0' xmlns='http://www.w3.org/2001/10/synthesis' xml:lang='en-US'>"))
	builder.WriteString(fmt.Sprintf("<voice name='%s'>", config.Voice))
	builder.WriteString(fmt.Sprintf("<prosody pitch='%s' rate='%s' volume='%s'>", config.Pitch, config.Rate, config.Volume))

	// Add all segments without breaks between them for continuous playback
	for i, segment := range textSegments {
		builder.WriteString(string(segment))
		// Add a small pause only between segments, not at the end
		if i < len(textSegments)-1 {
			builder.WriteString(" <break strength='none'/> ")
		}
	}

	builder.WriteString("</prosody>")
	builder.WriteString("</voice>")
	builder.WriteString("</speak>")

	return builder.String()
}

func ssmlHeadersPlusData(requestID, timestamp, ssml string) string {
	return fmt.Sprintf("X-RequestId:%s\r\n"+
		"Content-Type:application/ssml+xml\r\n"+
		"X-Timestamp:%sZ\r\n"+
		"Path:ssml\r\n\r\n"+
		"%s",
		requestID, timestamp, ssml)
}

func splitTextByByteLength(text []byte, maxLength int) [][]byte {
	if len(text) <= maxLength {
		return [][]byte{bytes.TrimSpace(text)}
	}

	var chunks [][]byte
	remaining := text

	for len(remaining) > maxLength {
		// Find split point
		splitAt := findLastNewlineOrSpaceWithinLimit(remaining, maxLength)
		if splitAt < 0 {
			splitAt = findSafeUTF8SplitPoint(remaining, maxLength)
		}
		splitAt = adjustSplitPointForXMLEntity(remaining, splitAt)

		if splitAt <= 0 {
			splitAt = maxLength
		}

		chunk := bytes.TrimSpace(remaining[:splitAt])
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}

		remaining = remaining[splitAt:]
	}

	if len(remaining) > 0 {
		chunk := bytes.TrimSpace(remaining)
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

func findLastNewlineOrSpaceWithinLimit(text []byte, limit int) int {
	// Find last newline
	newlineIdx := bytes.LastIndex(text[:limit], []byte("\n"))
	if newlineIdx >= 0 {
		return newlineIdx + 1
	}

	// Find last space
	spaceIdx := bytes.LastIndex(text[:limit], []byte(" "))
	if spaceIdx >= 0 {
		return spaceIdx + 1
	}

	return -1
}

func findSafeUTF8SplitPoint(text []byte, limit int) int {
	for i := limit; i > 0; i-- {
		if utf8.Valid(text[:i]) {
			return i
		}
	}
	return 0
}

func adjustSplitPointForXMLEntity(text []byte, splitAt int) int {
	for splitAt > 0 {
		ampIdx := bytes.LastIndex(text[:splitAt], []byte("&"))
		if ampIdx < 0 {
			break
		}

		semiIdx := bytes.Index(text[ampIdx:splitAt], []byte(";"))
		if semiIdx >= 0 {
			// Found terminated entity
			break
		}

		// Move split point to before &
		splitAt = ampIdx
	}

	return splitAt
}

// SplitTextBySentences splits text by sentence-ending punctuation marks
// Supports Chinese and English sentence endings: 。！？.!?;
func SplitTextBySentences(text string) []string {
	if len(text) == 0 {
		return []string{}
	}

	var sentences []string
	var currentSentence strings.Builder

	runes := []rune(text)
	i := 0
	for i < len(runes) {
		r := runes[i]
		currentSentence.WriteRune(r)

		// Check if current character is a sentence ending punctuation
		if isSentenceEnding(r) {
			// Look ahead to see if there are more punctuation marks or spaces
			sentenceEnd := i
			j := i + 1
			for j < len(runes) {
				nextRune := runes[j]
				if isSentenceEnding(nextRune) || nextRune == ' ' || nextRune == '\t' || nextRune == '\n' || nextRune == '\r' {
					sentenceEnd = j
					j++
				} else {
					break
				}
			}

			// Extract the sentence
			sentence := strings.TrimSpace(currentSentence.String())
			if len(sentence) > 0 {
				sentences = append(sentences, sentence)
			}

			// Reset for next sentence
			currentSentence.Reset()
			i = sentenceEnd + 1 // Move to next character after the sentence ending
		} else {
			i++
		}
	}

	// Add remaining text as last sentence if it's not empty
	remaining := strings.TrimSpace(currentSentence.String())
	if len(remaining) > 0 {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// isSentenceEnding checks if a rune is a sentence ending punctuation mark
func isSentenceEnding(r rune) bool {
	switch r {
	case '。', '！', '？', '.', '!', '?', ';', '；':
		return true
	default:
		return false
	}
}

// SplitTextBySentencesWithMaxLength splits text by sentences and ensures each chunk doesn't exceed maxLength
func SplitTextBySentencesWithMaxLength(text string, maxLength int) []string {
	if len(text) <= maxLength {
		return []string{text}
	}

	// First split by sentences
	sentences := SplitTextBySentences(text)

	var chunks []string
	var currentChunk strings.Builder

	for _, sentence := range sentences {
		sentenceBytes := []byte(sentence)

		// If adding this sentence would exceed the limit
		if currentChunk.Len()+len(sentenceBytes) > maxLength && currentChunk.Len() > 0 {
			// Save current chunk
			chunk := strings.TrimSpace(currentChunk.String())
			if len(chunk) > 0 {
				chunks = append(chunks, chunk)
			}
			currentChunk.Reset()
		}

		// If sentence itself is longer than maxLength, split it by words
		if len(sentenceBytes) > maxLength {
			words := splitSentenceToWords(sentence)
			for _, word := range words {
				wordBytes := []byte(word)
				if currentChunk.Len()+len(wordBytes) > maxLength && currentChunk.Len() > 0 {
					chunk := strings.TrimSpace(currentChunk.String())
					if len(chunk) > 0 {
						chunks = append(chunks, chunk)
					}
					currentChunk.Reset()
				}
				currentChunk.WriteString(word)
				currentChunk.WriteString(" ")
			}
		} else {
			currentChunk.WriteString(sentence)
			currentChunk.WriteString(" ")
		}
	}

	// Add remaining chunk
	chunk := strings.TrimSpace(currentChunk.String())
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}

	return chunks
}

// splitSentenceToWords splits a sentence into words (simple implementation)
func splitSentenceToWords(sentence string) []string {
	// Simple word splitting - can be enhanced with proper word segmentation
	words := strings.Fields(sentence)
	if len(words) == 0 {
		return []string{sentence}
	}
	return words
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Voice listing functionality
type Voice struct {
	Name           string `json:"Name"`
	ShortName      string `json:"ShortName"`
	Gender         string `json:"Gender"`
	Locale         string `json:"Locale"`
	SuggestedCodec string `json:"SuggestedCodec"`
	FriendlyName   string `json:"FriendlyName"`
	Status         string `json:"Status"`
}

// ListVoices retrieves all available voices
func ListVoices() ([]Voice, error) {
	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Prepare request
	req, err := http.NewRequest("GET", VoiceList, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	headers := HeadersWithMUID(VoiceHeaders)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Decode response
	var voices []Voice
	if err := json.NewDecoder(resp.Body).Decode(&voices); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return voices, nil
}

// FilterVoicesByLocale filters voices by locale/language
func FilterVoicesByLocale(voices []Voice, locale string) []Voice {
	var filtered []Voice
	for _, voice := range voices {
		if voice.Locale == locale {
			filtered = append(filtered, voice)
		}
	}
	return filtered
}

// FilterVoicesByGender filters voices by gender
func FilterVoicesByGender(voices []Voice, gender string) []Voice {
	var filtered []Voice
	for _, voice := range voices {
		if voice.Gender == gender {
			filtered = append(filtered, voice)
		}
	}
	return filtered
}

// GetVoiceByName finds a voice by its short name
func GetVoiceByName(voices []Voice, name string) (*Voice, error) {
	for _, voice := range voices {
		if voice.ShortName == name {
			return &voice, nil
		}
	}
	return nil, fmt.Errorf("voice not found: %s", name)
}
