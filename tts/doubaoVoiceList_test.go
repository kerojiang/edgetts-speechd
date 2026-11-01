package tts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// startFakeVoiceList 启动模拟音色列表服务，并把客户端地址指向该服务。
func startFakeVoiceList(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	old := doubaoVoiceListEndpoint
	doubaoVoiceListEndpoint = server.URL
	t.Cleanup(func() { doubaoVoiceListEndpoint = old })
}

// TestDoubaoTabsDefinition 验证 5 个分类的 recommend_type 与 tab_key 映射。
func TestDoubaoTabsDefinition(t *testing.T) {
	tabs := DoubaoTabs()
	if len(tabs) != 5 {
		t.Fatalf("分类数 = %d, 期望 5", len(tabs))
	}

	want := map[string]struct {
		recommendType int
		tabKey        string
	}{
		"rec":        {1, ""},
		"female":     {10, "female"},
		"male":       {10, "male"},
		"characters": {10, "characters"},
		"accent":     {10, "accent"},
	}
	for _, tab := range tabs {
		expected, ok := want[tab.Key]
		if !ok {
			t.Fatalf("未知分类 %q", tab.Key)
		}
		if tab.RecommendType != expected.recommendType || tab.TabKey != expected.tabKey {
			t.Errorf("分类 %q = (%d, %q), 期望 (%d, %q)",
				tab.Key, tab.RecommendType, tab.TabKey, expected.recommendType, expected.tabKey)
		}
	}
}

// TestSelectDoubaoTabs 验证空值取全部、合法值取单个、非法值报错。
func TestSelectDoubaoTabs(t *testing.T) {
	all, err := SelectDoubaoTabs("")
	if err != nil || len(all) != 5 {
		t.Fatalf("空值应返回 5 个分类，得到 %d 个，err=%v", len(all), err)
	}

	one, err := SelectDoubaoTabs("female")
	if err != nil || len(one) != 1 || one[0].Key != "female" {
		t.Fatalf("female 应返回单个分类，得到 %+v，err=%v", one, err)
	}

	upper, err := SelectDoubaoTabs("FEMALE")
	if err != nil || len(upper) != 1 || upper[0].Key != "female" {
		t.Fatalf("分类名应不区分大小写，得到 %+v，err=%v", upper, err)
	}

	if _, err := SelectDoubaoTabs("bogus"); err == nil {
		t.Fatal("非法分类应返回错误")
	}
}

// TestListDoubaoVoicesPaginationAndDedup 验证分页、跨分类去重、BV 与空 ID 过滤。
func TestListDoubaoVoicesPaginationAndDedup(t *testing.T) {
	var mu sync.Mutex
	var pages []int

	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if r.Header.Get("Origin") != DoubaoOrigin {
			t.Errorf("Origin = %q, 期望 %q", r.Header.Get("Origin"), DoubaoOrigin)
		}
		if r.Header.Get("User-Agent") != doubaoUserAgent {
			t.Errorf("User-Agent = %q, 期望 %q", r.Header.Get("User-Agent"), doubaoUserAgent)
		}

		var body struct {
			PageIndex     int    `json:"page_index"`
			PageSize      int    `json:"page_size"`
			RecommendType int    `json:"recommend_type"`
			TabKey        string `json:"tab_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("解析请求体失败: %v", err)
		}
		if body.PageSize != 30 {
			t.Errorf("page_size = %d, 期望 30", body.PageSize)
		}
		mu.Lock()
		pages = append(pages, body.PageIndex)
		mu.Unlock()

		items := []map[string]string{}
		hasMore := false
		if body.PageIndex == 1 {
			items = append(items,
				map[string]string{"style_id": "zh_female_a_bigtts", "name": "音色A"},
				map[string]string{"style_id": "BV223_streaming", "name": "应被过滤"},
			)
			hasMore = true
		} else {
			items = append(items,
				map[string]string{"style_id": "zh_female_a_bigtts", "name": "音色A"},
				map[string]string{"style_id": "", "name": "空ID"},
				map[string]string{"style_id": "zh_male_b_bigtts", "name": "音色B"},
			)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "",
			"data": map[string]any{
				"ugc_voice_list": items,
				"has_more":       hasMore,
			},
		})
	})

	tabs := []DoubaoTab{
		{Key: "rec", RecommendType: 1},
		{Key: "female", RecommendType: 10, TabKey: "female"},
	}
	voices, err := ListDoubaoVoices("sessionid=test", tabs)
	if err != nil {
		t.Fatalf("拉取音色失败: %v", err)
	}

	if len(voices) != 2 {
		t.Fatalf("去重后数量 = %d, 期望 2: %+v", len(voices), voices)
	}
	if voices[0].StyleID != "zh_female_a_bigtts" || len(voices[0].Tabs) != 2 {
		t.Errorf("第一条 = %+v, 期望跨分类合并分类", voices[0])
	}
	if got := strings.Join(voices[0].Tabs, ","); got != "rec,female" {
		t.Errorf("分类合并 = %q, 期望 %q", got, "rec,female")
	}
	if voices[1].StyleID != "zh_male_b_bigtts" {
		t.Errorf("第二条 = %+v, 期望 zh_male_b_bigtts", voices[1])
	}
	mu.Lock()
	total := len(pages)
	mu.Unlock()
	if total != 4 {
		t.Errorf("总请求次数 = %d, 期望 4（2 个分类各 2 页）", total)
	}
}

// TestListDoubaoVoicesErrors 验证 HTTP 500、code 非 0、非法 JSON 都返回错误。
func TestListDoubaoVoicesErrors(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "HTTP500",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
		{
			name: "业务错误",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":123456,"msg":"参数错误","data":{}}`))
			},
		},
		{
			name: "非法JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`not json`))
			},
		},
		{
			name: "空对象",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{}`))
			},
		},
		{
			name: "data为null",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":0,"data":null}`))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			startFakeVoiceList(t, tc.handler)
			_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{{Key: "rec", RecommendType: 1}})
			if err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

// TestListDoubaoVoicesAuth 验证认证错误被标记为 ErrDoubaoAuth。
func TestListDoubaoVoicesAuth(t *testing.T) {
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":40200001,"msg":"session auth failed","data":{}}`))
	})

	_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if !errors.Is(err, ErrDoubaoAuth) {
		t.Fatalf("期望 ErrDoubaoAuth，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "40200001") {
		t.Errorf("错误信息应包含状态码: %v", err)
	}
}

// TestListDoubaoVoicesTabFailure 验证单个分类失败会导致整体失败并指明分类。
func TestListDoubaoVoicesTabFailure(t *testing.T) {
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TabKey string `json:"tab_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.TabKey == "female" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"ugc_voice_list":[{"style_id":"zh_male_a_bigtts","name":"音色"}],"has_more":false}}`))
	})

	_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{
		{Key: "male", RecommendType: 10, TabKey: "male"},
		{Key: "female", RecommendType: 10, TabKey: "female"},
	})
	if err == nil {
		t.Fatal("分类失败时整体应返回错误")
	}
	if !strings.Contains(err.Error(), "female") {
		t.Errorf("错误信息应指明失败分类: %v", err)
	}
}

// TestListDoubaoVoicesPageLimit 验证 has_more 恒为 true 时不会无限分页。
func TestListDoubaoVoicesPageLimit(t *testing.T) {
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"ugc_voice_list":[{"style_id":"zh_female_a_bigtts","name":"音色A"}],"has_more":true}}`))
	})

	_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if err == nil {
		t.Fatal("超过分页上限应返回错误")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("错误信息应说明分页上限: %v", err)
	}
}

// TestListDoubaoVoicesWithCookieRetry 验证登录态失效后清缓存并从 Edge 重取一次。
func TestListDoubaoVoicesWithCookieRetry(t *testing.T) {
	useTempConfigDir(t)

	var call atomic.Int64
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		if call.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"code":40200001,"msg":"session auth failed","data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"ugc_voice_list":[{"style_id":"zh_female_a_bigtts","name":"音色A"}],"has_more":false}}`))
	})

	extracted := 0
	old := doubaoEdgeExtractor
	doubaoEdgeExtractor = func() (string, error) {
		extracted++
		return "edge=1", nil
	}
	t.Cleanup(func() { doubaoEdgeExtractor = old })

	voices, err := ListDoubaoVoicesWithCookie("bad=1", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if err != nil {
		t.Fatalf("重试后应成功，得到 %v", err)
	}
	if len(voices) != 1 || voices[0].StyleID != "zh_female_a_bigtts" {
		t.Fatalf("结果 = %+v", voices)
	}
	if extracted != 1 {
		t.Errorf("Edge 提取次数 = %d, 期望 1", extracted)
	}
	if call.Load() != 2 {
		t.Errorf("接口调用次数 = %d, 期望 2", call.Load())
	}
}

// TestListDoubaoVoicesWithCookieNonAuthError 验证非认证错误不触发重试。
func TestListDoubaoVoicesWithCookieNonAuthError(t *testing.T) {
	useTempConfigDir(t)

	var call atomic.Int64
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		call.Add(1)
		_, _ = w.Write([]byte(`{"code":123456,"msg":"参数错误","data":{}}`))
	})

	extracted := 0
	old := doubaoEdgeExtractor
	doubaoEdgeExtractor = func() (string, error) {
		extracted++
		return "edge=1", nil
	}
	t.Cleanup(func() { doubaoEdgeExtractor = old })

	if _, err := ListDoubaoVoicesWithCookie("bad=1", []DoubaoTab{{Key: "rec", RecommendType: 1}}); err == nil {
		t.Fatal("期望返回错误")
	}
	if extracted != 0 || call.Load() != 1 {
		t.Errorf("非认证错误不应重试：extracted=%d call=%d", extracted, call.Load())
	}
}

// TestListDoubaoVoicesHTTPAuthStatus 验证 HTTP 401/403 被标记为 ErrDoubaoAuth。
func TestListDoubaoVoicesHTTPAuthStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})

			_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{{Key: "rec", RecommendType: 1}})
			if !errors.Is(err, ErrDoubaoAuth) {
				t.Fatalf("HTTP %d 期望 ErrDoubaoAuth，得到 %v", status, err)
			}
		})
	}
}

// TestListDoubaoVoicesEmptyTabsFetchesAll 验证分类为空时请求全部 5 个分类。
func TestListDoubaoVoicesEmptyTabsFetchesAll(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}

	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TabKey string `json:"tab_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen[body.TabKey] = true
		mu.Unlock()

		_, _ = w.Write([]byte(`{"code":0,"data":{"ugc_voice_list":[],"has_more":false}}`))
	})

	if _, err := ListDoubaoVoices("sessionid=test", nil); err != nil {
		t.Fatalf("拉取失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"", "female", "male", "characters", "accent"} {
		if !seen[key] {
			t.Errorf("未请求分类 %q，实际 %v", key, seen)
		}
	}
}

// TestListDoubaoVoicesTimeout 验证整体超时生效。
func TestListDoubaoVoicesTimeout(t *testing.T) {
	old := doubaoVoiceListTimeout
	doubaoVoiceListTimeout = 20 * time.Millisecond
	t.Cleanup(func() { doubaoVoiceListTimeout = old })

	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	})

	_, err := ListDoubaoVoices("sessionid=test", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 DeadlineExceeded，得到 %v", err)
	}
}

// TestListDoubaoVoicesWithCookieRetryNonAuthError 验证重试遇到非认证错误时原样返回。
func TestListDoubaoVoicesWithCookieRetryNonAuthError(t *testing.T) {
	useTempConfigDir(t)

	var call atomic.Int64
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		if call.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"code":40200001,"msg":"session auth failed","data":{}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	old := doubaoEdgeExtractor
	doubaoEdgeExtractor = func() (string, error) { return "edge=1", nil }
	t.Cleanup(func() { doubaoEdgeExtractor = old })

	_, err := ListDoubaoVoicesWithCookie("bad=1", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if strings.Contains(err.Error(), "登录态失效") {
		t.Errorf("非认证错误不应报登录态失效: %v", err)
	}
}

// TestListDoubaoVoicesWithCookieExtractFailureKeepsAuth 验证重取 Cookie 失败时仍可识别认证错误。
func TestListDoubaoVoicesWithCookieExtractFailureKeepsAuth(t *testing.T) {
	useTempConfigDir(t)

	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":40200001,"msg":"session auth failed","data":{}}`))
	})

	old := doubaoEdgeExtractor
	doubaoEdgeExtractor = func() (string, error) { return "", errors.New("浏览器不可用") }
	t.Cleanup(func() { doubaoEdgeExtractor = old })

	_, err := ListDoubaoVoicesWithCookie("bad=1", []DoubaoTab{{Key: "rec", RecommendType: 1}})
	if !errors.Is(err, ErrDoubaoAuth) {
		t.Fatalf("期望保留 ErrDoubaoAuth，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "浏览器不可用") {
		t.Errorf("应保留提取失败原因: %v", err)
	}
}
