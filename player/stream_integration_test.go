//go:build integration

package player

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPlayOggChunks 流式播放多个 Ogg Opus 段（真出声），验证不挂起。
func TestPlayOggChunks(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "doubao_test.ogg"))
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	// 2 段（同一 Ogg 流两次，每段含 OpusHead，验证流式逐段解码）
	chunks := make(chan []byte, 4)
	go func() {
		chunks <- data
		chunks <- data
		close(chunks)
	}()

	start := time.Now()
	if err := PlayOggChunks(chunks); err != nil {
		t.Fatalf("PlayOggChunks 失败: %v", err)
	}
	elapsed := time.Since(start)
	// 2 段 × 4.02s = ~8s；超过 20s 视为挂起
	if elapsed > 20*time.Second {
		t.Fatalf("流式播放疑似挂起: 耗时 %v", elapsed)
	}
	t.Logf("流式播放 2 段完成，耗时 %v", elapsed)
}
