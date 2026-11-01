package tts

import (
	"testing"

	"github.com/gorilla/websocket"
)

// TestDoubaoSynthesizeStream 验证流式合成：多段文本并行合成，按序输出到 channel。
func TestDoubaoSynthesizeStream(t *testing.T) {
	startFakeDoubao(t, func(conn *websocket.Conn) {
		runFakeHandshake(t, conn)
		readEvent(t, conn) // StartTask
		readEvent(t, conn) // 第二个事件
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("SEG")})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	d := NewDoubao()
	d.Config.Cookie = "test=1"

	// 长文本（>400 字节，切成多段）
	var sb []byte
	for i := 1; i <= 15; i++ {
		sb = append(sb, []byte("这是流式合成的第几句话。")...)
	}
	text := string(sb)

	out := make(chan []byte, 16)
	done := make(chan error, 1)
	go func() {
		defer close(out)
		done <- d.SynthesizeStream(text, out)
	}()

	var got [][]byte
	for data := range out {
		got = append(got, data)
	}
	if err := <-done; err != nil {
		t.Fatalf("SynthesizeStream 失败: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("期望多段流式输出，实际 %d 段", len(got))
	}
	for i, seg := range got {
		if string(seg) != "SEG" {
			t.Errorf("第 %d 段内容 = %q, 期望 %q", i, seg, "SEG")
		}
	}
	t.Logf("流式输出 %d 段（每段独立 Ogg 流）", len(got))
}
