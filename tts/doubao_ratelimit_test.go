package tts

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDoubaoRateLimit 实测豆包合成接口的请求频率上限与限流/封禁表现。
//
// 默认跳过；仅在设置环境变量 DOBAO_RATE_TEST=1 时运行（真连豆包，消耗额度）。
//
// 策略：并发档位从 1 逐级升到 32，每档发一轮（等于并发数个请求）。
// 任何一档出现失败立即停止后续档位，避免触发账号级封禁，并报告该档错误原文。
//
// 结果只描述本次实测观察，不外推为服务端正式配额。
func TestDoubaoRateLimit(t *testing.T) {
	if os.Getenv("DOUBAO_RATE_TEST") != "1" {
		t.Skip("限流实测默认跳过；设置 DOUBAO_RATE_TEST=1 才运行（会真连豆包）")
	}

	d := NewDoubao()
	text := "限流测试短句。"

	// 档位可由 DOUBAO_RATE_TEST_LEVELS 覆盖（CSV），便于单独补测高档位。
	levels := []int{1, 2, 4, 8, 16, 32}
	if raw := os.Getenv("DOUBAO_RATE_TEST_LEVELS"); raw != "" {
		levels = nil
		for _, part := range strings.Split(raw, ",") {
			var v int
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &v); err == nil && v > 0 {
				levels = append(levels, v)
			}
		}
		if len(levels) == 0 {
			t.Fatalf("DOUBAO_RATE_TEST_LEVELS 无效: %q", raw)
		}
	}
	// 持续压力：DOUBAO_RATE_TEST_SUSTAIN="8,16" 表示对每个并发数持续 10 秒。
	var sustainLevels []int
	if raw := os.Getenv("DOUBAO_RATE_TEST_SUSTAIN"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			var v int
			if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &v); err == nil && v > 0 {
				sustainLevels = append(sustainLevels, v)
			}
		}
	}

	// 预热一次，排除首次建连/缓存冷启动对数据的干扰。
	start := time.Now()
	if _, err := d.synthesizeWithRetry(text); err != nil {
		t.Fatalf("预热失败（基础可用性不成立，停止测试）: %v", err)
	}
	t.Logf("预热完成，耗时 %v", time.Since(start))

	type roundResult struct {
		concurrency int
		total       int
		ok          int
		fail        int
		elapsed     time.Duration
		rps         float64
		firstErr    string
	}

	var results []roundResult
	stopped := false

	for _, conc := range levels {
		if stopped {
			break
		}

		var mu sync.Mutex
		var firstErr error
		var okCount, failCount int

		begin := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < conc; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, err := d.synthesizeWithRetry(text)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failCount++
					if firstErr == nil {
						firstErr = err
					}
					return
				}
				okCount++
			}(i)
		}
		wg.Wait()
		elapsed := time.Since(begin)

		r := roundResult{
			concurrency: conc,
			total:       conc,
			ok:          okCount,
			fail:        failCount,
			elapsed:     elapsed,
			rps:         float64(conc) / elapsed.Seconds(),
		}
		if firstErr != nil {
			r.firstErr = firstErr.Error()
		}
		results = append(results, r)

		t.Logf("并发 %2d：成功 %d/%d，耗时 %v（实际 %.1f req/s）%s",
			conc, okCount, conc, elapsed.Round(time.Millisecond), r.rps,
			formatErrSuffix(r.firstErr))

		if failCount > 0 {
			stopped = true
			t.Logf("并发 %d 出现失败，停止加压（保护账号）", conc)
		}
	}

	// 持续压力：固定并发数连续请求 10s，观察累计请求量是否触发限流。
	for _, conc := range sustainLevels {
		if stopped {
			break
		}
		const dur = 10 * time.Second
		deadline := time.Now().Add(dur)

		var mu sync.Mutex
		var okCount, failCount int
		var firstErr error
		var wg sync.WaitGroup

		begin := time.Now()
		for i := 0; i < conc; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for time.Now().Before(deadline) {
					_, err := d.synthesizeWithRetry(text)
					mu.Lock()
					if err != nil {
						failCount++
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						return
					}
					okCount++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(begin)

		suffix := ""
		if firstErr != nil {
			suffix = formatErrSuffix(firstErr.Error())
		}
		t.Logf("持续并发 %2d（%v）：成功 %d，失败 %d，实际 %.1f req/s%s",
			conc, dur, okCount, failCount, float64(okCount+failCount)/elapsed.Seconds(), suffix)
		if failCount > 0 {
			stopped = true
			t.Logf("持续并发 %d 出现失败，停止加压（保护账号）", conc)
		}
	}

	// 汇总：给出本次实测的“未失败最高并发/速率”与“首个失败档”。
	var maxClean *roundResult
	var firstFail *roundResult
	for i := range results {
		if results[i].fail == 0 {
			maxClean = &results[i]
		} else if firstFail == nil {
			firstFail = &results[i]
		}
	}

	t.Log("===== 实测汇总 =====")
	if maxClean != nil {
		t.Logf("未出现失败的最高档：并发 %d，约 %.1f req/s", maxClean.concurrency, maxClean.rps)
	} else {
		t.Log("未出现失败的档位：无")
	}
	if firstFail != nil {
		t.Logf("首个失败档：并发 %d，失败 %d/%d，错误：%s",
			firstFail.concurrency, firstFail.fail, firstFail.total, firstFail.firstErr)
	} else {
		t.Logf("本轮未观察到失败（最高测到并发 %d，未继续加压）", results[len(results)-1].concurrency)
	}
}

// formatErrSuffix 把首个错误整理成日志后缀（空串表示无错误）。
func formatErrSuffix(errText string) string {
	if errText == "" {
		return ""
	}
	errText = strings.ReplaceAll(errText, "\n", " ")
	if len(errText) > 300 {
		errText = errText[:300] + "…"
	}
	return fmt.Sprintf("；首个错误：%s", errText)
}
