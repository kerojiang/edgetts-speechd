package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"edgetts-speechd/tts"
)

// TestParseDoubaoRate 验证豆包语速参数解析。
func TestParseDoubaoRate(t *testing.T) {
	cases := []struct {
		input   string
		want    float64
		wantErr bool
	}{
		{"", 1, false},
		{"+0%", 1, false},
		{"+10%", 1.1, false},
		{"-5%", 0.95, false},
		{"1.2", 1.2, false},
		{"%", 0, true},
		{"abc", 0, true},
	}

	for _, tc := range cases {
		got, err := parseDoubaoRate(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDoubaoRate(%q) 期望错误，实际 %v", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDoubaoRate(%q) 返回错误: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDoubaoRate(%q) = %v, 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestParseDoubaoPitch 验证豆包音调参数解析。
func TestParseDoubaoPitch(t *testing.T) {
	cases := []struct {
		input   string
		want    float64
		wantErr bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"-3", -3, false},
		{"-50Hz", -50, false},
		{"+20Hz", 20, false},
		{"x", 0, true},
	}

	for _, tc := range cases {
		got, err := parseDoubaoPitch(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDoubaoPitch(%q) 期望错误，实际 %v", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDoubaoPitch(%q) 返回错误: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDoubaoPitch(%q) = %v, 期望 %v", tc.input, got, tc.want)
		}
	}
}

// TestProgramName 验证帮助信息中的程序名取自可执行文件名。
func TestProgramName(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })

	os.Args = []string{"/usr/local/bin/edgetts", "你好"}
	if got := programName(); got != "edgetts" {
		t.Errorf("programName() = %q, 期望 %q", got, "edgetts")
	}

	os.Args = nil
	if got := programName(); got != "edgetts" {
		t.Errorf("空 os.Args 时 programName() = %q, 期望 %q", got, "edgetts")
	}
}

// TestCleanTextPreservesChinesePunctuation 验证中文标点不会被清理掉。
func TestCleanTextPreservesChinesePunctuation(t *testing.T) {
	input := "你好，世界。这是测试！真的吗？"
	if got := cleanText(input); got != input {
		t.Fatalf("cleanText = %q, 期望 %q", got, input)
	}
}

// TestCleanTextRemovesControlCharacters 验证控制字符会被替换、空白会被规范化。
func TestCleanTextRemovesControlCharacters(t *testing.T) {
	got := cleanText("你好\x00世界\a  测试")
	want := "你好世界 测试"
	if got != want {
		t.Fatalf("cleanText = %q, 期望 %q", got, want)
	}
}

// TestRunListDoubaoPrintsVoices 验证豆包列表输出格式与分类参数传递。
func TestRunListDoubaoPrintsVoices(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cleared := false

	deps := listDeps{
		clearDoubaoCookie: func() error { cleared = true; return nil },
		listDoubaoVoices: func(cookie string, tabs []tts.DoubaoTab) ([]tts.DoubaoVoice, error) {
			if cookie != "sessionid=1" {
				t.Errorf("cookie = %q", cookie)
			}
			if len(tabs) != 1 || tabs[0].Key != "female" {
				t.Errorf("tabs = %+v", tabs)
			}
			return []tts.DoubaoVoice{{StyleID: "zh_female_a_bigtts", Name: "音色A", Tabs: []string{"female"}}}, nil
		},
	}

	if err := runList(&stdout, &stderr, "doubao", "female", "sessionid=1", false, deps); err != nil {
		t.Fatalf("runList 返回错误: %v", err)
	}

	want := "豆包可用的语音列表:\nzh_female_a_bigtts (female) - 音色A\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, 期望 %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Errorf("未刷新时 stderr 应为空，实际 %q", stderr.String())
	}
	if cleared {
		t.Error("未指定 -doubao-refresh 时不应清缓存")
	}
}

// TestRunListRefreshWritesInfoToStderr 验证刷新提示不污染列表输出。
func TestRunListRefreshWritesInfoToStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := listDeps{
		clearDoubaoCookie: func() error { return nil },
		listDoubaoVoices: func(string, []tts.DoubaoTab) ([]tts.DoubaoVoice, error) {
			return nil, nil
		},
	}

	if err := runList(&stdout, &stderr, "doubao", "", "", true, deps); err != nil {
		t.Fatalf("runList 返回错误: %v", err)
	}
	if stdout.String() != "豆包可用的语音列表:\n" {
		t.Errorf("stdout = %q, 期望仅包含列表标题", stdout.String())
	}
	if !strings.Contains(stderr.String(), "已清空本地豆包 Cookie 缓存") {
		t.Errorf("stderr 应包含刷新提示，实际 %q", stderr.String())
	}
}

// TestRunListDoubaoErrors 验证非法分类、未知引擎与拉取失败都返回错误。
func TestRunListDoubaoErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer

	t.Run("非法分类", func(t *testing.T) {
		deps := listDeps{
			clearDoubaoCookie: func() error { return nil },
			listDoubaoVoices: func(string, []tts.DoubaoTab) ([]tts.DoubaoVoice, error) {
				t.Fatal("非法分类不应发起请求")
				return nil, nil
			},
		}
		if err := runList(&stdout, &stderr, "doubao", "bogus", "", false, deps); err == nil {
			t.Fatal("期望返回错误")
		}
	})

	t.Run("未知引擎", func(t *testing.T) {
		if err := runList(&stdout, &stderr, "bogus", "", "", false, listDeps{}); err == nil {
			t.Fatal("期望返回错误")
		}
	})

	t.Run("拉取失败", func(t *testing.T) {
		deps := listDeps{
			clearDoubaoCookie: func() error { return nil },
			listDoubaoVoices: func(string, []tts.DoubaoTab) ([]tts.DoubaoVoice, error) {
				return nil, errors.New("网络不可用")
			},
		}
		if err := runList(&stdout, &stderr, "doubao", "", "", false, deps); err == nil {
			t.Fatal("期望返回错误")
		}
	})

	t.Run("清缓存失败", func(t *testing.T) {
		deps := listDeps{
			clearDoubaoCookie: func() error { return errors.New("权限不足") },
		}
		if err := runList(&stdout, &stderr, "doubao", "", "", true, deps); err == nil {
			t.Fatal("期望返回错误")
		}
	})
}

// TestRunListEdge 验证 Edge 列表仍按原格式输出。
func TestRunListEdge(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := listDeps{
		listEdgeVoices: func() ([]tts.Voice, error) {
			return []tts.Voice{{ShortName: "zh-CN-XiaoxiaoNeural", Locale: "zh-CN", FriendlyName: "晓晓"}}, nil
		},
	}

	if err := runList(&stdout, &stderr, "edge", "", "", false, deps); err != nil {
		t.Fatalf("runList 返回错误: %v", err)
	}

	want := "Edge TTS 可用的语音列表:\nzh-CN-XiaoxiaoNeural (zh-CN) - 晓晓\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, 期望 %q", stdout.String(), want)
	}
}

// TestRunListZeroDepsReturnsError 验证依赖为零值时返回错误而不是 panic。
func TestRunListZeroDepsReturnsError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	cases := []struct {
		name    string
		engine  string
		refresh bool
	}{
		{"豆包未刷新", "doubao", false},
		{"豆包刷新", "doubao", true},
		{"Edge", "edge", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("零值依赖导致 panic: %v", r)
				}
			}()

			if err := runList(&stdout, &stderr, tc.engine, "accent", "", tc.refresh, listDeps{}); err == nil {
				t.Fatal("零值依赖应返回错误")
			}
		})
	}
}

// TestRunListEdgeWarnsIgnoredDoubaoOptions 验证 Edge 引擎忽略豆包参数时给出警告。
func TestRunListEdgeWarnsIgnoredDoubaoOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := listDeps{
		listEdgeVoices: func() ([]tts.Voice, error) {
			return []tts.Voice{{ShortName: "zh-CN-XiaoxiaoNeural", Locale: "zh-CN", FriendlyName: "晓晓"}}, nil
		},
	}

	if err := runList(&stdout, &stderr, "edge", "female", "", true, deps); err != nil {
		t.Fatalf("runList 返回错误: %v", err)
	}

	want := "Edge TTS 可用的语音列表:\nzh-CN-XiaoxiaoNeural (zh-CN) - 晓晓\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, 期望 %q", stdout.String(), want)
	}

	logs := stderr.String()
	if !strings.Contains(logs, "-dv-tab") {
		t.Errorf("应警告 -dv-tab 被忽略，实际 stderr = %q", logs)
	}
	if !strings.Contains(logs, "-doubao-refresh") {
		t.Errorf("应警告 -doubao-refresh 被忽略，实际 stderr = %q", logs)
	}
}

// TestRunListEdgeNoWarnWithoutDoubaoOptions 验证未给豆包参数时 Edge 不产生警告。
func TestRunListEdgeNoWarnWithoutDoubaoOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := listDeps{
		listEdgeVoices: func() ([]tts.Voice, error) { return nil, nil },
	}

	if err := runList(&stdout, &stderr, "edge", "", "", false, deps); err != nil {
		t.Fatalf("runList 返回错误: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("不应有警告，实际 stderr = %q", stderr.String())
	}
}
