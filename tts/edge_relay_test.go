package tts

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRelayEdgeStreamContinuesAfterErrChanClosed：errChan 正常关闭后必须继续转发 chunk。
func TestRelayEdgeStreamContinuesAfterErrChanClosed(t *testing.T) {
	chunks := make(chan *TTSChunk, 1)
	errs := make(chan error)
	close(errs) // 模拟 streamSingle 正常结束：无错误、直接关闭
	out := make(chan *TTSChunk, 1)

	done := make(chan error, 1)
	go func() { done <- relayEdgeStream(context.Background(), chunks, errs, out) }()

	// 旧实现会在 errs 关闭时立即 return，这里之后发送的 chunk 会丢失
	time.Sleep(50 * time.Millisecond)
	chunks <- &TTSChunk{Type: "audio", Data: []byte("A")}
	close(chunks)

	select {
	case c := <-out:
		if string(c.Data) != "A" {
			t.Fatalf("转发内容 = %q, 期望 \"A\"", c.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("errChan 关闭后丢弃了后续音频块（多段早退 bug）")
	}
	if err := <-done; err != nil {
		t.Fatalf("relayEdgeStream 返回错误: %v", err)
	}
}

// TestRelayEdgeStreamPropagatesError：真实错误必须原样返回。
func TestRelayEdgeStreamPropagatesError(t *testing.T) {
	var chunks chan *TTSChunk // nil channel：确保 select 只可能命中 errs，测试无竞态
	errs := make(chan error, 1)
	errs <- errors.New("websocket: close 1007 SSML is invalid")
	close(errs)

	err := relayEdgeStream(context.Background(), chunks, errs, make(chan *TTSChunk, 1))
	if err == nil || !strings.Contains(err.Error(), "SSML is invalid") {
		t.Fatalf("期望透出原始错误，实际: %v", err)
	}
}

// TestEdgeMultiSegmentReal：真实 Edge 下，多段整段 MP3 字节数 == 各段单独合成之和。
// 默认跳过，EDGETTS_NET_TEST=1 才联网。
func TestEdgeMultiSegmentReal(t *testing.T) {
	if os.Getenv("EDGETTS_NET_TEST") != "1" {
		t.Skip("设置 EDGETTS_NET_TEST=1 才执行真实网络测试")
	}
	text := strings.Repeat("这是用于验证分段拼接完整性的中文测试句子，需要足够长以触发分段。", 61)
	cfg := *NewEdge().Config
	cfg.ContinuousPlayback = false
	whole, err := newEdgeWithConfig(text, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Texts) < 2 {
		t.Fatalf("文本未切成多段：%d", len(whole.Texts))
	}
	collect := func(inst *EdgeTTS) []byte {
		ch, ech := inst.Stream(context.Background())
		var out []byte
		for {
			select {
			case c, ok := <-ch:
				if !ok {
					return out
				}
				if c.Type == "audio" {
					out = append(out, c.Data...)
				}
			case err, ok := <-ech:
				if ok && err != nil {
					t.Fatalf("stream err: %v", err)
				}
			}
		}
	}
	wholeBytes := collect(whole)
	sum := 0
	for i, seg := range whole.Texts {
		one, err := newEdgeWithConfig(string(seg), &cfg)
		if err != nil {
			t.Fatal(err)
		}
		n := len(collect(one))
		sum += n
		t.Logf("第 %d 段 MP3 = %d 字节", i+1, n)
	}
	t.Logf("整段 MP3 = %d 字节，各段之和 = %d 字节", len(wholeBytes), sum)
	if len(wholeBytes) != sum {
		t.Fatalf("多段音频不完整：%d != %d", len(wholeBytes), sum)
	}
}
