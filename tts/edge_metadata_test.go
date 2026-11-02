package tts

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestHandleTextMessageRespectsCancel：ctx 取消后，metadata 块不得阻塞在无缓冲发送上。
// 否则 streamSingle 的 goroutine 会永久泄漏，defer wsConn.Close() 也不会执行。
func TestHandleTextMessageRespectsCancel(t *testing.T) {
	e := NewEdge()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chunks := make(chan *TTSChunk) // 无缓冲且无人接收
	audioWasReceived := false
	msg := []byte("Path:audio.metadata\r\n\r\n" +
		`{"Metadata":[{"Type":"WordBoundary","Data":{"Offset":0,"Duration":1000000,"Text":{"Text":"你好"}}}]}`)

	done := make(chan error, 1)
	go func() { done <- e.handleTextMessage(ctx, msg, chunks, &audioWasReceived) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, 期望 context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后仍阻塞在 metadata 发送（goroutine 泄漏）")
	}
}
