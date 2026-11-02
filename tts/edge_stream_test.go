package tts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// TestForwardEdgeAudioWritesOnlyAudio：只透传 audio 块，顺序不变。
func TestForwardEdgeAudioWritesOnlyAudio(t *testing.T) {
	chunks := make(chan *TTSChunk, 5)
	errs := make(chan error, 1)
	chunks <- &TTSChunk{Type: "WordBoundary"}
	chunks <- &TTSChunk{Type: "audio", Data: []byte("AAA")}
	chunks <- &TTSChunk{Type: "SentenceBoundary"}
	chunks <- &TTSChunk{Type: "audio", Data: []byte("BB")}
	chunks <- &TTSChunk{Type: "audio", Data: []byte("CCC")}
	close(chunks)
	close(errs)

	var buf bytes.Buffer
	if err := forwardEdgeAudio(context.Background(), chunks, errs, &buf); err != nil {
		t.Fatalf("forwardEdgeAudio 失败: %v", err)
	}
	if got := buf.String(); got != "AAABBCCC" {
		t.Errorf("输出 = %q, 期望 %q", got, "AAABBCCC")
	}
}

// TestForwardEdgeAudioPropagatesError：流中途错误必须返回，且不挂起。
func TestForwardEdgeAudioPropagatesError(t *testing.T) {
	chunks := make(chan *TTSChunk, 1)
	errs := make(chan error, 1)
	errs <- errors.New("websocket: close 1007 SSML is invalid")
	close(errs)
	close(chunks)

	var buf bytes.Buffer
	err := forwardEdgeAudio(context.Background(), chunks, errs, &buf)
	if err == nil || !strings.Contains(err.Error(), "SSML is invalid") {
		t.Fatalf("期望透出原始错误，实际: %v", err)
	}
}

// TestForwardEdgeAudioRejectsEmpty：零音频必须报错（不能“成功静默”）。
func TestForwardEdgeAudioRejectsEmpty(t *testing.T) {
	chunks := make(chan *TTSChunk)
	errs := make(chan error)
	close(chunks)
	close(errs)
	if err := forwardEdgeAudio(context.Background(), chunks, errs, io.Discard); err == nil {
		t.Fatal("零音频应返回错误")
	}
}

// TestSynthesizeStreamRejectsBlankText：空白文本在联网前就报错。
func TestSynthesizeStreamRejectsBlankText(t *testing.T) {
	e := NewEdge()
	if err := e.SynthesizeStream(context.Background(), "   ", io.Discard); err == nil {
		t.Fatal("空白文本应返回错误")
	}
}

// TestEdgeSynthesizeStreamMultiSegmentReal：真实网络下 SynthesizeStream 输出包含全部段。
// 默认跳过，EDGETTS_NET_TEST=1 才联网。
func TestEdgeSynthesizeStreamMultiSegmentReal(t *testing.T) {
	if os.Getenv("EDGETTS_NET_TEST") != "1" {
		t.Skip("设置 EDGETTS_NET_TEST=1 才执行真实网络测试")
	}
	text := strings.Repeat("这是用于验证流式输出完整性的中文测试句子，需要足够长以触发分段。", 61)
	cfg := *NewEdge().Config
	cfg.ContinuousPlayback = false
	inst, err := newEdgeWithConfig(text, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(inst.Texts) < 2 {
		t.Fatalf("文本未切成多段：%d", len(inst.Texts))
	}

	var whole bytes.Buffer
	if err := inst.SynthesizeStream(context.Background(), text, &whole); err != nil {
		t.Fatalf("SynthesizeStream 失败: %v", err)
	}

	var first bytes.Buffer
	if err := inst.SynthesizeStream(context.Background(), string(inst.Texts[0]), &first); err != nil {
		t.Fatalf("单段 SynthesizeStream 失败: %v", err)
	}
	t.Logf("整段 MP3 = %d 字节，仅第 1 段 = %d 字节", whole.Len(), first.Len())
	if whole.Len() <= first.Len() {
		t.Fatalf("流式输出只包含第 1 段：%d <= %d", whole.Len(), first.Len())
	}
}
