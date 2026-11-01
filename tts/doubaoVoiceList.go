/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubaoVoiceList.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 豆包音色列表。
//
// 官方浏览器插件通过 POST /alice/user_voice/recommend 拉取音色，
// 参数取自插件代码：recommend_type 与 tab_key 决定分类，分页用 page_index/has_more。
// 返回的 style_id 可直接作为 TTS 的 speaker 使用。

const (
	// DoubaoVoiceListEndpoint 是音色列表接口默认地址。
	DoubaoVoiceListEndpoint = "https://www.doubao.com/alice/user_voice/recommend"
	// doubaoVoicePageSize 是单页请求条数，与插件保持一致。
	doubaoVoicePageSize = 30
	// doubaoVoiceMaxPages 是单个分类的分页上限，防止 has_more 异常导致死循环。
	doubaoVoiceMaxPages = 100
)

// doubaoVoiceListEndpoint 是音色列表接口地址，测试中可替换。
var doubaoVoiceListEndpoint = DoubaoVoiceListEndpoint

// DoubaoTab 描述音色列表的一个分类。
type DoubaoTab struct {
	Key           string // 分类标识，如 rec、female
	RecommendType int    // 接口的 recommend_type
	TabKey        string // 接口的 tab_key，推荐分类为空
}

// DoubaoVoice 是列表中的一个音色。
type DoubaoVoice struct {
	StyleID string   // 音色标识，可直接用于 -dv
	Name    string   // 中文名称
	Tabs    []string // 该音色出现的分类
}

// doubaoVoiceItem 是接口返回的单个音色。
type doubaoVoiceItem struct {
	StyleID string `json:"style_id"`
	Name    string `json:"name"`
}

// doubaoVoiceListResponse 是接口响应结构。
type doubaoVoiceListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		UGCVoiceList []doubaoVoiceItem `json:"ugc_voice_list"`
		HasMore      bool              `json:"has_more"`
	} `json:"data"`
}

// DoubaoTabs 返回全部音色分类，顺序固定。
func DoubaoTabs() []DoubaoTab {
	return []DoubaoTab{
		{Key: "rec", RecommendType: 1},
		{Key: "female", RecommendType: 10, TabKey: "female"},
		{Key: "male", RecommendType: 10, TabKey: "male"},
		{Key: "characters", RecommendType: 10, TabKey: "characters"},
		{Key: "accent", RecommendType: 10, TabKey: "accent"},
	}
}

// SelectDoubaoTabs 按分类标识选择分类；key 为空时返回全部分类。
func SelectDoubaoTabs(key string) ([]DoubaoTab, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return DoubaoTabs(), nil
	}

	for _, tab := range DoubaoTabs() {
		if tab.Key == key {
			return []DoubaoTab{tab}, nil
		}
	}

	return nil, fmt.Errorf("未知的豆包音色分类: %q（可选：rec、female、male、characters、accent）", key)
}

// ListDoubaoVoices 并发拉取各分类的音色，并按 style_id 去重合并分类。
func ListDoubaoVoices(cookie string, tabs []DoubaoTab) ([]DoubaoVoice, error) {
	if len(tabs) == 0 {
		tabs = DoubaoTabs()
	}

	client := &http.Client{Timeout: 30 * time.Second}

	results := make([][]DoubaoVoice, len(tabs))
	errs := make([]error, len(tabs))

	var wg sync.WaitGroup
	for i, tab := range tabs {
		wg.Add(1)
		go func(index int, tab DoubaoTab) {
			defer wg.Done()

			voices, err := fetchDoubaoVoicesByTab(client, cookie, tab)
			if err != nil {
				errs[index] = fmt.Errorf("分类 %s 拉取失败: %w", tab.Key, err)
				return
			}
			results[index] = voices
		}(i, tab)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	return mergeDoubaoVoices(tabs, results), nil
}

// fetchDoubaoVoicesByTab 翻完一个分类的全部分页，并过滤无效音色。
func fetchDoubaoVoicesByTab(client *http.Client, cookie string, tab DoubaoTab) ([]DoubaoVoice, error) {
	var voices []DoubaoVoice

	for pageIndex := 1; ; pageIndex++ {
		if pageIndex > doubaoVoiceMaxPages {
			return nil, fmt.Errorf("豆包音色分类 %s 分页超过上限", tab.Key)
		}

		response, err := fetchDoubaoVoicePage(client, cookie, tab, pageIndex)
		if err != nil {
			return nil, err
		}

		for _, item := range response.Data.UGCVoiceList {
			styleID := strings.TrimSpace(item.StyleID)
			// 空标识无法用于合成；BV 前缀是插件 UI 也会过滤的条目。
			if styleID == "" || strings.HasPrefix(styleID, "BV") {
				continue
			}
			voices = append(voices, DoubaoVoice{
				StyleID: styleID,
				Name:    strings.TrimSpace(item.Name),
			})
		}

		if !response.Data.HasMore || len(response.Data.UGCVoiceList) == 0 {
			return voices, nil
		}
	}
}

// fetchDoubaoVoicePage 请求单页音色列表。
func fetchDoubaoVoicePage(client *http.Client, cookie string, tab DoubaoTab, pageIndex int) (*doubaoVoiceListResponse, error) {
	body := struct {
		PageIndex     int    `json:"page_index"`
		PageSize      int    `json:"page_size"`
		RecommendType int    `json:"recommend_type"`
		TabKey        string `json:"tab_key,omitempty"`
	}{
		PageIndex:     pageIndex,
		PageSize:      doubaoVoicePageSize,
		RecommendType: tab.RecommendType,
		TabKey:        tab.TabKey,
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化豆包音色请求失败: %w", err)
	}

	query := url.Values{}
	query.Set("aid", DoubaoAID)
	query.Set("real_aid", DoubaoAID)
	query.Set("device_platform", "web")
	query.Set("pkg_type", "release_version")
	query.Set("version_code", "20800")
	query.Set("pc_version", "3.9.0")
	query.Set("region", "CN")
	query.Set("sys_region", "CN")
	query.Set("use-olympus-account", "1")
	query.Set("samantha_web", "1")
	query.Set("language", "zh")

	req, err := http.NewRequest(http.MethodPost, doubaoVoiceListEndpoint+"?"+query.Encode(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("构造豆包音色请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", DoubaoOrigin)
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求豆包音色接口失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: 音色接口 HTTP %d", ErrDoubaoAuth, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("豆包音色接口返回 HTTP %d", resp.StatusCode)
	}

	var result doubaoVoiceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析豆包音色响应失败: %w", err)
	}

	if result.Code == DoubaoStatusCodeAuth ||
		strings.Contains(strings.ToLower(result.Msg), "session auth") {
		return nil, fmt.Errorf("%w: code=%d msg=%s", ErrDoubaoAuth, result.Code, result.Msg)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("豆包音色接口错误: code=%d msg=%s", result.Code, result.Msg)
	}

	return &result, nil
}

// mergeDoubaoVoices 按分类顺序归并结果，style_id 只保留首次出现，分类去重追加。
func mergeDoubaoVoices(tabs []DoubaoTab, results [][]DoubaoVoice) []DoubaoVoice {
	merged := make([]DoubaoVoice, 0)
	positions := make(map[string]int)

	for i, tab := range tabs {
		for _, voice := range results[i] {
			if pos, ok := positions[voice.StyleID]; ok {
				if !containsString(merged[pos].Tabs, tab.Key) {
					merged[pos].Tabs = append(merged[pos].Tabs, tab.Key)
				}
				continue
			}

			positions[voice.StyleID] = len(merged)
			merged = append(merged, DoubaoVoice{
				StyleID: voice.StyleID,
				Name:    voice.Name,
				Tabs:    []string{tab.Key},
			})
		}
	}

	return merged
}

// containsString 判断切片中是否包含指定字符串。
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
