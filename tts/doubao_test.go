package tts

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestDoubaoConfigValidate 验证说话人与语速范围校验。
func TestDoubaoConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		config  *DoubaoConfig
		wantErr bool
	}{
		{"默认说话人合法", NewDoubaoConfig(""), false},
		{"空说话人非法", &DoubaoConfig{Voice: "   ", Rate: 1}, true},
		{"语速过低非法", &DoubaoConfig{Voice: "v", Rate: 0.5}, true},
		{"语速过高非法", &DoubaoConfig{Voice: "v", Rate: 2.5}, true},
		{"语速下边界合法", &DoubaoConfig{Voice: "v", Rate: 0.7}, false},
		{"语速上边界合法", &DoubaoConfig{Voice: "v", Rate: 2.0}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望成功，实际错误: %v", err)
			}
		})
	}
}

// TestNewDoubaoConfigDefaults 验证默认说话人、语速与音调。
func TestNewDoubaoConfigDefaults(t *testing.T) {
	config := NewDoubaoConfig("")
	if config.Voice != DoubaoDefaultVoice {
		t.Errorf("Voice = %q, 期望 %q", config.Voice, DoubaoDefaultVoice)
	}
	if config.Rate != 1 {
		t.Errorf("Rate = %v, 期望 1", config.Rate)
	}
	if config.Pitch != 0 {
		t.Errorf("Pitch = %v, 期望 0", config.Pitch)
	}
}

// TestDoubaoSessionPayload 验证 StartSession 的 JSON 结构与协议字段。
func TestDoubaoSessionPayload(t *testing.T) {
	engine := NewDoubao()
	engine.Config.Voice = "zh_female_wenroutaozi_uranus_bigtts"
	engine.Config.Rate = 1.5
	engine.Config.Pitch = -3

	raw, err := engine.buildStartSessionPayload()
	if err != nil {
		t.Fatalf("构造 StartSession 负载失败: %v", err)
	}

	var payload struct {
		Business         int    `json:"business"`
		ConversationID   string `json:"conversation_id"`
		RequestType      int    `json:"request_type"`
		EnableTextReadng bool   `json:"enable_text_reading"`
		TTS              struct {
			Speaker     string `json:"speaker"`
			AudioConfig struct {
				BitRate    int    `json:"bit_rate"`
				Format     string `json:"format"`
				SampleRate int    `json:"sample_rate"`
			} `json:"audio_config"`
			Extra struct {
				PostProcess struct {
					Pitch      float64 `json:"pitch"`
					SpeechRate float64 `json:"speech_rate"`
				} `json:"post_process"`
			} `json:"extra"`
		} `json:"tts"`
		Extra struct {
			Extra string `json:"extra"`
		} `json:"extra"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("负载不是合法 JSON: %v\n%s", err, raw)
	}

	if payload.Business != 1 {
		t.Errorf("business = %d, 期望 1", payload.Business)
	}
	if payload.RequestType != 4 {
		t.Errorf("request_type = %d, 期望 4", payload.RequestType)
	}
	if !payload.EnableTextReadng {
		t.Errorf("enable_text_reading = false, 期望 true")
	}
	if payload.TTS.Speaker != "zh_female_wenroutaozi_uranus_bigtts" {
		t.Errorf("tts.speaker = %q", payload.TTS.Speaker)
	}
	if payload.TTS.AudioConfig.Format != "ogg_opus" ||
		payload.TTS.AudioConfig.SampleRate != 24000 ||
		payload.TTS.AudioConfig.BitRate != 32000 {
		t.Errorf("audio_config 不符合协议: %+v", payload.TTS.AudioConfig)
	}
	if payload.TTS.Extra.PostProcess.SpeechRate != 1.5 {
		t.Errorf("speech_rate = %v, 期望 1.5", payload.TTS.Extra.PostProcess.SpeechRate)
	}
	if payload.TTS.Extra.PostProcess.Pitch != -3 {
		t.Errorf("pitch = %v, 期望 -3", payload.TTS.Extra.PostProcess.Pitch)
	}

	var inner struct {
		PostProcess struct {
			SpeechRate float64 `json:"speech_rate"`
		} `json:"post_process"`
	}
	if err := json.Unmarshal([]byte(payload.Extra.Extra), &inner); err != nil {
		t.Fatalf("extra.extra 不是合法 JSON: %v", err)
	}
	if inner.PostProcess.SpeechRate != 1.5 {
		t.Errorf("extra.extra.post_process.speech_rate = %v, 期望 1.5", inner.PostProcess.SpeechRate)
	}
}

// TestDoubaoWSURL 验证连接参数齐全且可被解析。
func TestDoubaoWSURL(t *testing.T) {
	raw, err := doubaoWSURL()
	if err != nil {
		t.Fatalf("构造连接地址失败: %v", err)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("连接地址非法: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "frontier-audio-web-ws.doubao.com" {
		t.Fatalf("地址不匹配: %s", raw)
	}

	query := parsed.Query()
	expected := map[string]string{
		"api_app_key":         DoubaoAppKey,
		"namespace":           DoubaoNamespace,
		"aid":                 DoubaoAID,
		"real_aid":            DoubaoAID,
		"version_code":        "20800",
		"pc_version":          "3.9.0",
		"pkg_type":            "release_version",
		"samantha_web":        "1",
		"use-olympus-account": "1",
		"region":              "CN",
		"sys_region":          "CN",
		"device_platform":     "web",
		"language":            "zh",
		"browser_language":    "zh-CN",
	}
	for key, want := range expected {
		if got := query.Get(key); got != want {
			t.Errorf("参数 %s = %q, 期望 %q", key, got, want)
		}
	}
	for _, key := range []string{"web_id", "tea_uuid", "web_tab_id", "device_id"} {
		if strings.TrimSpace(query.Get(key)) == "" {
			t.Errorf("参数 %s 为空", key)
		}
	}
}

// TestDoubaoAuthError 验证登录态失效可被识别。
func TestDoubaoAuthError(t *testing.T) {
	err := doubaoAuthError(pbResponse{
		StatusCode: 40200001,
		StatusText: "session auth, userID or appID is empty",
	})
	if !errors.Is(err, ErrDoubaoAuth) {
		t.Fatalf("期望 ErrDoubaoAuth，实际 %v", err)
	}

	err = doubaoAuthError(pbResponse{StatusCode: 20000000})
	if err != nil {
		t.Fatalf("成功状态不应报错，实际 %v", err)
	}
}

// TestIsOggOutput 已随 isOggOutput 移除（豆包不再按扩展名决定输出格式）。
