package tts

import (
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// startFakeEdgeWS 启动假的 Edge TTS WebSocket 服务，按协议返回一段音频后结束会话。
func startFakeEdgeWS(t *testing.T, payload []byte) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("升级 WebSocket 失败: %v", err)
			return
		}
		defer conn.Close()

		// 客户端先发 speech.config，再发 ssml。
		for i := 0; i < 2; i++ {
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Errorf("读取客户端消息失败: %v", err)
				return
			}
		}

		// 二进制音频帧：2 字节头长度 + 头部 + 负载。
		// 头部第一行会被 2 字节长度前缀覆盖，因此按真实协议把 X-RequestId 放在首行。
		header := []byte("X-RequestId:abc\r\nPath:audio\r\nContent-Type:audio/mpeg\r\n\r\n")
		frame := make([]byte, 2)
		binary.BigEndian.PutUint16(frame, uint16(len(header)))
		frame = append(frame, header...)
		frame = append(frame, payload...)
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Errorf("发送音频帧失败: %v", err)
			return
		}

		// turn.end 表示本轮结束。
		if err := conn.WriteMessage(websocket.TextMessage, []byte("X-RequestId:abc\r\nPath:turn.end\r\n\r\n")); err != nil {
			t.Errorf("发送 turn.end 失败: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	old := WSSURL
	WSSURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/edge/v1?TrustedClientToken=test"
	t.Cleanup(func() { WSSURL = old })
}

// captureStdout 捕获函数执行期间写入 os.Stdout 的全部内容。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("关闭写端失败: %v", err)
	}
	os.Stdout = old

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("读取 stdout 失败: %v", err)
	}
	return string(out)
}

// TestEdgeSynthesisPrintsNothingOnStdout 验证成功合成时不向 stdout 输出任何调试信息。
func TestEdgeSynthesisPrintsNothingOnStdout(t *testing.T) {
	payload := []byte("fake-mp3-payload")
	startFakeEdgeWS(t, payload)

	engine := NewEdge()
	engine.Config.Voice = "zh-CN-XiaoxiaoNeural"

	output := filepath.Join(t.TempDir(), "out.mp3")

	var synthErr error
	out := captureStdout(t, func() {
		_, synthErr = engine.GetAudioFileTo("你好。", output)
	})

	if synthErr != nil {
		t.Fatalf("合成失败: %v", synthErr)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("成功合成时 stdout 应为空，实际输出: %q", out)
	}

	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("读取输出文件失败: %v", err)
	}
	if string(written) != string(payload) {
		t.Errorf("写入音频 = %q, 期望 %q", written, payload)
	}
}
