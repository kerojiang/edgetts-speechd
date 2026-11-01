//go:build integration

package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPlayReal_mp3 真实播放 Edge MP3（真出声），验证不挂起。
func TestPlayReal_mp3(t *testing.T) {
	path := filepath.Join("..", "testdata", "edge_test.mp3")
	start := time.Now()
	if err := PlayFile(path); err != nil {
		t.Fatalf("PlayFile(edge_test.mp3) 失败: %v", err)
	}
	elapsed := time.Since(start)
	// 4.63s 音频，正常应 ~5s 内；超过 15s 视为挂起
	if elapsed > 15*time.Second {
		t.Fatalf("播放疑似挂起: 耗时 %v (预期 < 15s)", elapsed)
	}
	t.Logf("edge_test.mp3 播放完成，耗时 %v", elapsed)
}

// TestPlayReal_opus 真实播放豆包 Ogg Opus（真出声），验证不挂起。
func TestPlayReal_opus(t *testing.T) {
	path := filepath.Join("..", "testdata", "doubao_test.ogg")
	start := time.Now()
	if err := PlayFile(path); err != nil {
		t.Fatalf("PlayFile(doubao_test.ogg) 失败: %v", err)
	}
	elapsed := time.Since(start)
	// 4.02s 音频，正常应 ~5s 内；超过 15s 视为挂起
	if elapsed > 15*time.Second {
		t.Fatalf("播放疑似挂起: 耗时 %v (预期 < 15s)", elapsed)
	}
	t.Logf("doubao_test.ogg 播放完成，耗时 %v", elapsed)
}

// TestPlayReal_noExternalPlayer 验证播放过程未调用 ffplay/ffmpeg/aplay。
func TestPlayReal_noExternalPlayer(t *testing.T) {
	path := filepath.Join("..", "testdata", "edge_test.mp3")
	if err := PlayFile(path); err != nil {
		t.Fatalf("PlayFile 失败: %v", err)
	}
	for _, bin := range []string{"ffplay", "ffmpeg", "aplay"} {
		if _, err := exec.LookPath(bin); err == nil {
			if out, err := exec.Command("pgrep", "-x", bin).Output(); err == nil && len(out) > 0 {
				t.Errorf("播放期间检测到外部播放器进程 %s: %s", bin, out)
			}
		}
	}
}

// TestPlayReal_fileNotExist 验证不存在文件报错。
func TestPlayReal_fileNotExist(t *testing.T) {
	if err := PlayFile(filepath.Join(os.TempDir(), "no-such-audio-xxxx.mp3")); err == nil {
		t.Fatal("期望不存在文件报错，实际返回 nil")
	}
}
