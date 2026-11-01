package player

import (
	"os"
	"path/filepath"
	"testing"
)

// fixturePath 返回 testdata 下的真实音频文件路径（由 edgetts 生成）。
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "testdata", name)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, name))
	if err != nil {
		t.Fatalf("读取测试音频 %s 失败: %v", name, err)
	}
	return data
}

func TestDetectFormat_mp3(t *testing.T) {
	data := readFixture(t, "edge_test.mp3")
	if got := detectFormat(data); got != formatMP3 {
		t.Fatalf("detectFormat(edge_test.mp3) = %v, 期望 formatMP3", got)
	}
}

func TestDetectFormat_opus(t *testing.T) {
	data := readFixture(t, "doubao_test.ogg")
	if got := detectFormat(data); got != formatOpus {
		t.Fatalf("detectFormat(doubao_test.ogg) = %v, 期望 formatOpus", got)
	}
}

func TestDetectFormat_unknown(t *testing.T) {
	if got := detectFormat([]byte("hello world")); got != formatUnknown {
		t.Fatalf("detectFormat(unknown) = %v, 期望 formatUnknown", got)
	}
}

func TestDecodeAudio_mp3(t *testing.T) {
	data := readFixture(t, "edge_test.mp3")
	dec, err := decodeAudio(data)
	if err != nil {
		t.Fatalf("decodeAudio(edge_test.mp3) 失败: %v", err)
	}
	if dec.sampleRate != 24000 {
		t.Errorf("MP3 采样率 = %d, 期望 24000", dec.sampleRate)
	}
	if dec.channels != 2 {
		t.Errorf("MP3 声道数 = %d, 期望 2（go-mp3 上混立体声）", dec.channels)
	}
	if len(dec.pcm) == 0 {
		t.Error("MP3 解码后 PCM 为空")
	}
}

func TestDecodeAudio_opus(t *testing.T) {
	data := readFixture(t, "doubao_test.ogg")
	dec, err := decodeAudio(data)
	if err != nil {
		t.Fatalf("decodeAudio(doubao_test.ogg) 失败: %v", err)
	}
	if dec.sampleRate != 24000 {
		t.Errorf("Opus 采样率 = %d, 期望 24000", dec.sampleRate)
	}
	// decodeOpus 将豆包 mono 上混为立体声输出（2ch 交错 PCM）
	if dec.channels != 2 {
		t.Errorf("Opus 输出声道数 = %d, 期望 2（mono 上混立体声）", dec.channels)
	}
	if len(dec.pcm) == 0 {
		t.Error("Opus 解码后 PCM 为空")
	}
}
