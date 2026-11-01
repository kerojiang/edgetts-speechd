package main

import "testing"

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
