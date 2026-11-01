package tts

import "testing"

// TestBuildCookieHeader 验证过滤、去重与值原样拼接。
func TestBuildCookieHeader(t *testing.T) {
	items := []cdpCookie{
		{Name: "sessionid", Value: "a=b", Domain: ".doubao.com"},
		{Name: "ttwid", Value: "中文值", Domain: ".doubao.com"},
		{Name: "other", Value: "1", Domain: ".example.com"},
		{Name: "sessionid", Value: "重复值", Domain: "doubao.com"},
		{Name: "empty", Value: "", Domain: ".doubao.com"},
	}

	got := buildCookieHeader(items)
	want := "sessionid=a=b; ttwid=中文值"
	if got != want {
		t.Fatalf("Cookie 头不匹配\n got: %q\nwant: %q", got, want)
	}
}

// TestBuildCookieHeaderFiltersAll 验证没有豆包 Cookie 时返回空串。
func TestBuildCookieHeaderFiltersAll(t *testing.T) {
	items := []cdpCookie{
		{Name: "a", Value: "1", Domain: ".example.com"},
	}
	if got := buildCookieHeader(items); got != "" {
		t.Fatalf("期望空串，得到 %q", got)
	}
}
