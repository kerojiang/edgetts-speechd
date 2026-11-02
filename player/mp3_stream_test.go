package player

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestNewMP3StreamDecoderFromPipe：分块写入 io.Pipe 的 MP3 能被流式解码。
func TestNewMP3StreamDecoderFromPipe(t *testing.T) {
	data := readFixture(t, "edge_test.mp3")
	pr, pw := io.Pipe()
	go func() {
		const chunk = 1024
		for i := 0; i < len(data); i += chunk {
			end := i + chunk
			if end > len(data) {
				end = len(data)
			}
			if _, err := pw.Write(data[i:end]); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
		pw.Close()
	}()

	dec, err := newMP3StreamDecoder(pr)
	if err != nil {
		t.Fatalf("newMP3StreamDecoder 失败: %v", err)
	}
	if got := dec.SampleRate(); got != 24000 {
		t.Errorf("采样率 = %d, 期望 24000", got)
	}
	total := 0
	buf := make([]byte, 8192)
	for {
		n, rerr := dec.Read(buf)
		total += n
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("流式解码失败: %v", rerr)
		}
	}
	want, err := decodeMP3(data)
	if err != nil {
		t.Fatalf("decodeMP3 基准失败: %v", err)
	}
	if total != len(want.pcm) {
		t.Errorf("流式 PCM = %d 字节, 期望 %d（与整段解码一致）", total, len(want.pcm))
	}
}

// TestNewMP3StreamDecoderEmpty：空流失效，必须报错。
func TestNewMP3StreamDecoderEmpty(t *testing.T) {
	pr, pw := io.Pipe()
	pw.Close()
	if _, err := newMP3StreamDecoder(pr); err == nil {
		t.Fatal("空流应返回错误")
	}
}

// TestNewMP3StreamDecoderPipeError：写端带错误关闭时，解码读取必须透出该错误。
func TestNewMP3StreamDecoderPipeError(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write(readFixture(t, "edge_test.mp3")[:512])
		pw.CloseWithError(errors.New("合成中断"))
	}()
	dec, err := newMP3StreamDecoder(pr)
	if err != nil {
		return // 头部阶段就失败也算透出错误
	}
	buf := make([]byte, 8192)
	for {
		_, rerr := dec.Read(buf)
		if rerr != nil {
			if !strings.Contains(rerr.Error(), "合成中断") {
				t.Fatalf("错误未透出原文: %v", rerr)
			}
			return
		}
	}
}
