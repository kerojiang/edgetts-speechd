package tts

import (
	"os"
	"testing"
)

// useTempConfigDir 把 Cookie 配置目录指向临时目录，并在测试结束后恢复。
func useTempConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	doubaoConfigDirOverride = dir
	t.Cleanup(func() { doubaoConfigDirOverride = "" })
}

// TestSaveAndLoadDoubaoCookie 验证 Cookie 落盘后可读回，且文件权限为 0600。
func TestSaveAndLoadDoubaoCookie(t *testing.T) {
	useTempConfigDir(t)

	cookie := "sessionid=abc123; ttwid=中文值; odin_tt=x=y"

	if err := saveDoubaoCookie(cookie); err != nil {
		t.Fatalf("保存 Cookie 失败: %v", err)
	}

	got, err := loadCachedDoubaoCookie()
	if err != nil {
		t.Fatalf("读取 Cookie 失败: %v", err)
	}
	if got != cookie {
		t.Fatalf("Cookie 不一致\n got: %q\nwant: %q", got, cookie)
	}

	info, err := os.Stat(doubaoConfigPath())
	if err != nil {
		t.Fatalf("stat 配置文件失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("配置文件权限 = %o, 期望 600", perm)
	}
}

// TestResolveDoubaoCookiePriority 验证 explicit > DOUBAO_COOKIE > 本地缓存。
func TestResolveDoubaoCookiePriority(t *testing.T) {
	useTempConfigDir(t)

	if err := saveDoubaoCookie("cached=1"); err != nil {
		t.Fatalf("保存缓存失败: %v", err)
	}

	t.Setenv("DOUBAO_COOKIE", "env=1")

	got, err := resolveDoubaoCookie("")
	if err != nil {
		t.Fatalf("解析 Cookie 失败: %v", err)
	}
	if got != "env=1" {
		t.Errorf("环境变量应优先于缓存，得到 %q", got)
	}

	got, err = resolveDoubaoCookie("explicit=1")
	if err != nil {
		t.Fatalf("解析 Cookie 失败: %v", err)
	}
	if got != "explicit=1" {
		t.Errorf("显式参数应最高优先，得到 %q", got)
	}

	t.Setenv("DOUBAO_COOKIE", "")

	got, err = resolveDoubaoCookie("")
	if err != nil {
		t.Fatalf("解析 Cookie 失败: %v", err)
	}
	if got != "cached=1" {
		t.Errorf("应回退到本地缓存，得到 %q", got)
	}
}

// TestClearDoubaoCookie 验证清空后缓存不再存在。
func TestClearDoubaoCookie(t *testing.T) {
	useTempConfigDir(t)

	if err := saveDoubaoCookie("cached=1"); err != nil {
		t.Fatalf("保存缓存失败: %v", err)
	}
	if err := clearDoubaoCookie(); err != nil {
		t.Fatalf("清空缓存失败: %v", err)
	}
	if _, err := loadCachedDoubaoCookie(); !os.IsNotExist(err) {
		t.Fatalf("期望 os.IsNotExist，实际 %v", err)
	}
	// 重复清空不应报错
	if err := clearDoubaoCookie(); err != nil {
		t.Fatalf("重复清空缓存报错: %v", err)
	}
}
