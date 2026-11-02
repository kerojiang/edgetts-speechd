// Package timing 提供默认关闭的阶段耗时诊断输出。
//
// 设置环境变量 EDGETTS_TIMING=1 时，各阶段会把耗时写到 stderr；
// 未设置时完全静默，不影响「成功不输出」的约定。
package timing

import (
	"fmt"
	"os"
	"time"
)

var (
	// start 是进程内计时基准点（包初始化时刻，接近进程启动）。
	start = time.Now()
	// enabled 由环境变量在进程启动时固定，运行期不再查询。
	enabled = os.Getenv("EDGETTS_TIMING") != ""
)

// Logf 在启用诊断时输出带相对耗时的阶段信息到 stderr。
func Logf(format string, args ...any) {
	if !enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "[timing %6dms] ", time.Since(start).Milliseconds())
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
