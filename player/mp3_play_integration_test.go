//go:build integration

package player

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPlayMP3Stream 分块喂入真实 Edge MP3，验证边收边播且不挂起。
func TestPlayMP3Stream(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "edge_test.mp3"))
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	pr, pw := io.Pipe()
	go func() {
		const chunk = 4096
		for i := 0; i < len(data); i += chunk {
			end := i + chunk
			if end > len(data) {
				end = len(data)
			}
			if _, werr := pw.Write(data[i:end]); werr != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		pw.Close()
	}()

	start := time.Now()
	if err := PlayMP3Stream(pr); err != nil {
		t.Fatalf("PlayMP3Stream 失败: %v", err)
	}
	elapsed := time.Since(start)
	// fixture 约 4.6s；超过 20s 视为挂起
	if elapsed < 3*time.Second || elapsed > 20*time.Second {
		t.Fatalf("播放时长异常: %v（期望约 4.6s）", elapsed)
	}
	t.Logf("PlayMP3Stream 完成，耗时 %v", elapsed)
}
