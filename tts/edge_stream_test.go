package tts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// TestWriteFileAtomicSuccess：成功后目标文件内容正确，且不残留临时文件。
func TestWriteFileAtomicSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.mp3")
	if err := writeFileAtomic(path, func(w io.Writer) error {
		_, err := w.Write([]byte("audio-bytes"))
		return err
	}); err != nil {
		t.Fatalf("writeFileAtomic 失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取目标文件失败: %v", err)
	}
	if string(data) != "audio-bytes" {
		t.Fatalf("内容 = %q, 期望 %q", data, "audio-bytes")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("目录中残留文件: %v", entries)
	}
}

// TestWriteFileAtomicPreservesTargetOnError：写入失败时目标文件必须保持原样，临时文件必须清理。
func TestWriteFileAtomicPreservesTargetOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.mp3")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("合成失败")
	err := writeFileAtomic(path, func(w io.Writer) error {
		_, _ = w.Write([]byte("partial"))
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, 期望 %v", err, wantErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取目标文件失败: %v", err)
	}
	if string(data) != "original" {
		t.Fatalf("失败时破坏了目标文件，内容 = %q", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("失败后残留临时文件: %v", entries)
	}
}

// fakeTimeoutError 模拟 net.Error 的读超时。
type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string   { return "i/o timeout" }
func (fakeTimeoutError) Timeout() bool   { return true }
func (fakeTimeoutError) Temporary() bool { return true }

// TestClassifyReadError：读超时与普通错误都必须上报，且保留原始错误。
// Edge 的正常结束走 turn.end（handleTextMessage 返回 errStreamComplete），
// 因此任何 ReadMessage 错误都意味着音频被截断。
func TestClassifyReadError(t *testing.T) {
	timeoutErr := fakeTimeoutError{}
	got := classifyReadError(timeoutErr)
	if got == nil {
		t.Fatal("读超时必须返回错误")
	}
	if !errors.Is(got, timeoutErr) {
		t.Fatalf("未保留原始错误: %v", got)
	}
	if !strings.Contains(got.Error(), "超时") {
		t.Fatalf("错误信息未说明超时: %v", got)
	}

	plainErr := errors.New("connection reset by peer")
	got = classifyReadError(plainErr)
	if !errors.Is(got, plainErr) {
		t.Fatalf("普通错误未保留原文: %v", got)
	}
	if !strings.Contains(got.Error(), "failed to read WebSocket message") {
		t.Fatalf("普通错误缺少上下文: %v", got)
	}
}

// TestWriteFileAtomicPreservesMode：替换已有文件时保留原权限，新建文件用 0644。
func TestWriteFileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.mp3")
	if err := os.WriteFile(existing, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(existing, func(w io.Writer) error {
		_, err := w.Write([]byte("new"))
		return err
	}); err != nil {
		t.Fatalf("writeFileAtomic 失败: %v", err)
	}
	info, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("已有文件权限 = %o, 期望 640", got)
	}

	fresh := filepath.Join(dir, "fresh.mp3")
	if err := writeFileAtomic(fresh, func(w io.Writer) error {
		_, err := w.Write([]byte("new"))
		return err
	}); err != nil {
		t.Fatalf("writeFileAtomic 失败: %v", err)
	}
	info, err = os.Stat(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("新文件权限 = %o, 期望 644", got)
	}
}
