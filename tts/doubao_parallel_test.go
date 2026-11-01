package tts

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

// TestDoubaoGetAudioBytesParallel 验证多句合成是并发的（同时活跃连接 >= 2）。
// 串行实现 maxActive=1，此测试将失败（RED）；并行实现后通过（GREEN）。
func TestDoubaoGetAudioBytesParallel(t *testing.T) {
	var active, maxActive int32
	var mu sync.Mutex

	startFakeDoubao(t, func(conn *websocket.Conn) {
		cur := atomic.AddInt32(&active, 1)
		mu.Lock()
		if int(cur) > int(atomic.LoadInt32(&maxActive)) {
			atomic.StoreInt32(&maxActive, cur)
		}
		mu.Unlock()
		defer atomic.AddInt32(&active, -1)

		runFakeHandshake(t, conn)
		readEvent(t, conn) // StartTask
		readEvent(t, conn) // 第二个事件（见 TestDoubaoGetAudioFileToWritesOgg）
		sendResponse(t, conn, pbResponse{Event: "TTSResponse", StatusCode: 20000000, Data: []byte("AUDIO")})
		sendResponse(t, conn, pbResponse{Event: "TTSEnded", StatusCode: 20000000})
	})

	d := NewDoubao()
	d.Config.Cookie = "test=1"

	// 多句文本（>400 字节，确保切成多段并行合成）
	var sb []byte
	for i := 1; i <= 15; i++ {
		sb = append(sb, []byte("这是并行合成的第几句话。")...)
	}
	text := string(sb)
	t.Logf("测试文本 %d 字节", len(text))

	audio, err := d.GetAudioBytes(text)
	if err != nil {
		t.Fatalf("GetAudioBytes 失败: %v", err)
	}
	if len(audio) == 0 {
		t.Fatal("音频为空")
	}
	if max := atomic.LoadInt32(&maxActive); max < 2 {
		t.Errorf("期望并发连接数 >= 2，实际 maxActive=%d（合成是串行的）", max)
	}
	t.Logf("合成成功，字节数=%d，最大并发连接=%d", len(audio), atomic.LoadInt32(&maxActive))
}
