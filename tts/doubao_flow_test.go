package tts

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// TestSplitDoubaoText 验证超长中文在缺少标点与空格时仍按 UTF-8 边界拆分。
func TestSplitDoubaoText(t *testing.T) {
	text := strings.Repeat("测", 4000) // 12000 字节，无标点无空格

	segments := splitDoubaoText(text)
	if len(segments) < 2 {
		t.Fatalf("期望拆成多段，实际 %d 段", len(segments))
	}

	var joined strings.Builder
	for i, segment := range segments {
		if len(segment) > doubaoMaxTextBytes {
			t.Errorf("第 %d 段长度 %d 超过上限 %d", i, len(segment), doubaoMaxTextBytes)
		}
		if !utf8.ValidString(segment) {
			t.Errorf("第 %d 段不是合法 UTF-8", i)
		}
		if segment == "" {
			t.Errorf("第 %d 段为空", i)
		}
		joined.WriteString(segment)
	}
	if joined.String() != text {
		t.Errorf("拼接后与原文不一致：got %d 字节，want %d 字节", joined.Len(), len(text))
	}
}

// startFakeDoubao 启动模拟的豆包语音服务，并把客户端地址指向该服务。
func startFakeDoubao(t *testing.T, script func(conn *websocket.Conn)) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		script(conn)
	}))
	t.Cleanup(server.Close)

	old := doubaoWSEndpoint
	doubaoWSEndpoint = "ws://" + strings.TrimPrefix(server.URL, "http://")
	t.Cleanup(func() { doubaoWSEndpoint = old })
}

// readEvent 读取一条客户端消息并返回事件名。
func readEvent(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("设置读取超时失败: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取客户端消息失败: %v", err)
	}
	event, err := decodeEventName(raw)
	if err != nil {
		t.Fatalf("解析客户端消息失败: %v", err)
	}
	return event
}

// decodeEventName 从 protobuf 请求中取出 event 字段（字段 5）。
func decodeEventName(data []byte) (string, error) {
	offset := 0
	for offset < len(data) {
		tag, next, err := readPBVarint(data, offset)
		if err != nil {
			return "", err
		}
		offset = next
		field := int(tag >> 3)
		wire := int(tag & 7)
		if wire != pbWireBytes {
			return "", errors.New("fake 服务端只处理 bytes 字段")
		}
		length, next, err := readPBVarint(data, offset)
		if err != nil {
			return "", err
		}
		offset = next
		if length > uint64(len(data)-offset) {
			return "", errors.New("长度越界")
		}
		chunk := data[offset : offset+int(length)]
		offset += int(length)
		if field == 5 {
			return string(chunk), nil
		}
	}
	return "", errors.New("未找到 event 字段")
}

// sendResponse 向客户端发送一条 protobuf 响应。
func sendResponse(t *testing.T, conn *websocket.Conn, response pbResponse) {
	t.Helper()
	message := encodePBResponseForTest(response)
	if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
		t.Fatalf("发送响应失败: %v", err)
	}
}

// encodePBResponseForTest 按响应字段编号编码。
func encodePBResponseForTest(response pbResponse) []byte {
	var b []byte
	b = appendPBString(b, 1, response.TaskID)
	b = appendPBString(b, 2, response.MessageID)
	b = appendPBString(b, 3, response.Namespace)
	b = appendPBString(b, 4, response.Event)
	b = appendPBUint(b, 5, uint64(response.StatusCode))
	b = appendPBString(b, 6, response.StatusText)
	b = appendPBString(b, 7, response.Payload)
	b = appendPBBytes(b, 8, response.Data)
	return b
}

// runFakeHandshake 完成 StartTask/StartSession 的最小握手。
func runFakeHandshake(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if event := readEvent(t, conn); event != "StartTask" {
		t.Fatalf("首个事件 = %q, 期望 StartTask", event)
	}
	sendResponse(t, conn, pbResponse{TaskID: "t1", Event: "TaskStarted", StatusCode: 20000000})

	if event := readEvent(t, conn); event != "StartSession" {
		t.Fatalf("第二个事件 = %q, 期望 StartSession", event)
	}
	sendResponse(t, conn, pbResponse{TaskID: "t1", Event: "SessionStarted", StatusCode: 20000000})
}

// TestDoubaoSynthesizeSuccess 验证完整流程会拼接 TTSResponse 数据。
func TestDoubaoSynthesizeSuccess(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		runFakeHandshake(t, conn)

		if event := readEvent(t, conn); event != "BidirectionalTTS" {
			t.Fatalf("第三个事件 = %q, 期望 BidirectionalTTS", event)
		}
		if event := readEvent(t, conn); event != "EndTTS" {
			t.Fatalf("第四个事件 = %q, 期望 EndTTS", event)
		}

		sendResponse(t, conn, pbResponse{Event: "TTSSentenceStart", StatusCode: 20000000})
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("OggS-1")})
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("-2")})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	engine := NewDoubao()
	audio, err := engine.synthesize("你好。", "test=1")
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	if string(audio) != "OggS-1-2" {
		t.Fatalf("音频 = %q, 期望 %q", string(audio), "OggS-1-2")
	}
}

// TestDoubaoSynthesizeRequiresTTSEnded 验证读到 TTSEnded 前不允许把部分音频当成功。
func TestDoubaoSynthesizeRequiresTTSEnded(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		runFakeHandshake(t, conn)
		readEvent(t, conn)
		readEvent(t, conn)

		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("OggS")})
		// 不发送 TTSEnded，直接断开
	})

	engine := NewDoubao()
	if _, err := engine.synthesize("你好。", "test=1"); err == nil {
		t.Fatal("未收到 TTSEnded 时期望返回错误，实际为 nil")
	}
}

// TestDoubaoSynthesizeEmptyAudio 验证空音频不算成功。
func TestDoubaoSynthesizeEmptyAudio(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		runFakeHandshake(t, conn)
		readEvent(t, conn)
		readEvent(t, conn)

		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	engine := NewDoubao()
	if _, err := engine.synthesize("你好。", "test=1"); err == nil {
		t.Fatal("空音频时期望返回错误，实际为 nil")
	}
}

// TestDoubaoSynthesizeSurfacesStatusCode 验证非成功状态码会被透出。
func TestDoubaoSynthesizeSurfacesStatusCode(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		readEvent(t, conn)
		sendResponse(t, conn, pbResponse{
			Event:      "UnknownEvent",
			StatusCode: 45000001,
			StatusText: "quota exceeded",
		})
	})

	engine := NewDoubao()
	_, err := engine.synthesize("你好。", "test=1")
	if err == nil {
		t.Fatal("期望返回错误，实际为 nil")
	}
	if !strings.Contains(err.Error(), "45000001") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("错误信息未透出状态码与文本: %v", err)
	}
}

// TestDoubaoSynthesizeAuthError 验证登录态失效错误可被 errors.Is 识别。
func TestDoubaoSynthesizeAuthError(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		readEvent(t, conn)
		sendResponse(t, conn, pbResponse{
			Event:      "SessionFailed",
			StatusCode: DoubaoStatusCodeAuth,
			StatusText: "session auth, userID or appID is empty",
		})
	})

	engine := NewDoubao()
	_, err := engine.synthesize("你好。", "test=1")
	if !errors.Is(err, ErrDoubaoAuth) {
		t.Fatalf("期望 ErrDoubaoAuth，实际 %v", err)
	}
}

// TestDoubaoGetAudioFileToFallsBackToOgg 验证 ffmpeg 不可用时回退到 .ogg。
func TestDoubaoGetAudioFileToFallsBackToOgg(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		runFakeHandshake(t, conn)
		readEvent(t, conn)
		readEvent(t, conn)
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("OggS")})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	// 把 PATH 置空，确保找不到 ffmpeg
	t.Setenv("PATH", "/nonexistent")

	dir := t.TempDir()
	target := dir + "/output.mp3"

	engine := NewDoubao()
	engine.Config.Cookie = "test=1"

	got, err := engine.GetAudioFileTo("你好。", target)
	if err != nil {
		t.Fatalf("期望回退成功，实际错误: %v", err)
	}
	if got != dir+"/output.ogg" {
		t.Fatalf("回退路径 = %q, 期望 %q", got, dir+"/output.ogg")
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("读取回退文件失败: %v", err)
	}
	if string(data) != "OggS" {
		t.Fatalf("回退文件内容 = %q, 期望 %q", string(data), "OggS")
	}
}

// TestNewEdgeWithConfigPropagates 验证 Edge 引擎的参数会传入内部实例。
func TestNewEdgeWithConfigPropagates(t *testing.T) {
	base := NewTTSConfig("zh-CN-XiaoxiaoNeural")
	base.Rate = "+10%"
	base.Volume = "-5%"
	base.Pitch = "-20Hz"

	engine, err := newEdgeWithConfig("你好。", base)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if engine.Config.Rate != "+10%" || engine.Config.Volume != "-5%" || engine.Config.Pitch != "-20Hz" {
		t.Fatalf("配置未传递: %+v", engine.Config)
	}
	if len(engine.Texts) == 0 {
		t.Fatal("文本未处理")
	}
	if len(engine.OriginalTexts) != len(engine.Texts) {
		t.Fatalf("OriginalTexts = %d, Texts = %d", len(engine.OriginalTexts), len(engine.Texts))
	}
}
