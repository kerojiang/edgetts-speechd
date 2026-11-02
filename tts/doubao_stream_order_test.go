package tts

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSynthesizeStreamFirstSegmentAlone 验证首段单独合成：
// 其余段的连接必须等首段响应完成后才建立，避免并发建连放大首段 TTFB 长尾。
func TestSynthesizeStreamFirstSegmentAlone(t *testing.T) {
	var mu sync.Mutex
	var connAt []time.Time
	var firstDoneAt time.Time
	var count int32

	startFakeDoubao(t, func(conn *websocket.Conn) {
		n := atomic.AddInt32(&count, 1)
		runFakeHandshake(t, conn)
		readEvent(t, conn) // BidirectionalTTS

		mu.Lock()
		connAt = append(connAt, time.Now())
		mu.Unlock()

		if n == 1 {
			time.Sleep(120 * time.Millisecond)
			sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("AUDIO")})
			sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
			mu.Lock()
			firstDoneAt = time.Now()
			mu.Unlock()
			return
		}
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("AUDIO")})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	d := NewDoubao()
	d.Config.Cookie = "test=1"

	// 构造多段文本（每句约 60 字节，总和远超 100 字节阈值，切成多段）
	var text string
	for i := 0; i < 10; i++ {
		text += "第一段专属标记内容测试测试测试测试测试。"
	}
	// 用不同文本区分首段：首段与其余段内容不同更利于排查，但这里顺序即段序。

	out := make(chan []byte, 32)
	done := make(chan error, 1)
	go func() {
		defer close(out)
		done <- d.SynthesizeStream(text, out)
	}()

	for range out {
	}
	if err := <-done; err != nil {
		t.Fatalf("SynthesizeStream 失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(connAt) < 2 {
		t.Fatalf("期望至少 2 个连接，实际 %d", len(connAt))
	}
	if firstDoneAt.IsZero() {
		t.Fatal("首段未完成")
	}
	if connAt[1].Before(firstDoneAt) {
		t.Errorf("第 2 个连接在首段完成前就建立（提前 %v），首段未独占",
			firstDoneAt.Sub(connAt[1]))
	}
}
