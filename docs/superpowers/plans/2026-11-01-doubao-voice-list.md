# 豆包音色列表（`-engine doubao -l`）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `edgetts -engine doubao -l` 列出豆包全部可用音色，并支持 `-dv-tab` 按分类筛选。

**Architecture:** 在 `tts` 包新增一个普通 HTTP 客户端，调用豆包插件的 `user_voice/recommend` 接口；5 个分类并发、分类内串行分页，结果按 `style_id` 去重合并。认证失效复用现有 Cookie 链路（清缓存 → 从 Edge 重取 → 重试一次）。`main.go` 的 `-l` 按 `-engine` 分流。

**Tech Stack:** Go 1.26、标准库 `net/http` / `sync` / `testing`，不新增依赖。

**Spec:** `docs/superpowers/specs/2026-11-01-doubao-voice-list-design.md`

## Global Constraints

- 不新增第三方依赖，测试只用标准库 `testing`
- 测试与代码注释使用中文
- 接口地址默认 `https://www.doubao.com/alice/user_voice/recommend`，必须通过包级变量可替换
- 请求必须带 `Origin: chrome-extension://capohkkfagimodmlpnahjoijgoocdjhd`
- 分页 `page_index` 从 1 开始，`page_size=30`，`has_more=false` 或空列表停止
- 过滤空 `style_id` 和以 `BV` 开头的 `style_id`
- 认证失效语义与合成路径一致：`code == 40200001`、`msg` 含 `session auth`、或 HTTP 401/403
- 不改变 Edge `-l` 的输出与行为

## Review Focus

- HTTP 非 200 或响应不是合法 JSON 时，必须返回包含状态码/解析原因的错误，不能返回空列表当成功
- 服务端 `code` 非 0 且非认证错误时，错误信息必须带 `code` 与 `msg`，便于用户判断
- 分类并发中任一分类失败时，整体必须失败并指明分类，不能静默丢弃该分类结果
- `has_more` 一直为 `true` 时必须有页数上限，不能无限请求
- 分页结果跨页/跨分类重复时，输出中 `style_id` 只能出现一次，且分类合并不丢失

---

### Task 1: 分类定义、参数解析与分页拉取

**Files:**
- Create: `tts/doubaoVoiceList.go`
- Test: `tts/doubaoVoiceList_test.go`

**Interfaces:**
- Consumes: `DoubaoAID`、`DoubaoOrigin`、`DoubaoStatusCodeAuth`、`ErrDoubaoAuth`（均在 `tts` 包已有）
- Produces:
  - `type DoubaoTab struct { Key string; RecommendType int; TabKey string }`
  - `type DoubaoVoice struct { StyleID string; Name string; Tabs []string }`
  - `func DoubaoTabs() []DoubaoTab`
  - `func SelectDoubaoTabs(key string) ([]DoubaoTab, error)`
  - `func ListDoubaoVoices(cookie string, tabs []DoubaoTab) ([]DoubaoVoice, error)`
  - 包级变量 `doubaoVoiceListEndpoint string`

- [ ] **Step 1: 写失败测试**

在 `tts/doubaoVoiceList_test.go` 写入：

```go
package tts

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

	if _, err := SelectDoubaoTabs("bogus"); err == nil {
		t.Fatal("非法分类应返回错误")
	}
}

// TestListDoubaoVoicesPaginationAndDedup 验证分页、跨分类去重、BV 与空 ID 过滤。
func TestListDoubaoVoicesPaginationAndDedup(t *testing.T) {
	var pages []int

	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("请求方法 = %s, 期望 POST", r.Method)
		}
		if r.Header.Get("Origin") != DoubaoOrigin {
			t.Errorf("Origin = %q, 期望 %q", r.Header.Get("Origin"), DoubaoOrigin)
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
		pages = append(pages, body.PageIndex)

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
	if voices[1].StyleID != "zh_male_b_bigtts" {
		t.Errorf("第二条 = %+v, 期望 zh_male_b_bigtts", voices[1])
	}
	if len(pages) != 4 {
		t.Errorf("总请求次数 = %d, 期望 4（2 个分类各 2 页）: %v", len(pages), pages)
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestDoubaoTabsDefinition|TestSelectDoubaoTabs|TestListDoubaoVoices' -v`
Expected: FAIL，编译错误 `undefined: DoubaoTabs` 等

- [ ] **Step 3: 实现 `tts/doubaoVoiceList.go`**

实现要求：

- `DoubaoTabs()` 返回按固定顺序 `rec, female, male, characters, accent` 的 5 项
- `SelectDoubaoTabs(key)`：空字符串返回全部；否则按 `Key` 精确匹配；无匹配返回 `fmt.Errorf("未知的豆包音色分类: %q（可选：rec、female、male、characters、accent）", key)`
- `ListDoubaoVoices`：空 `tabs` 时使用全部；每个分类一个 goroutine；用 `sync.WaitGroup` 等待，结果写入按索引预分配的切片以保证分类顺序
- 分类内分页：`page_index` 从 1 开始递增，`page_size` 固定 30；`has_more` 为 false 或列表为空时停止；`page_index > 100` 时返回错误 `fmt.Errorf("豆包音色分类 %s 分页超过上限", tab.Key)`
- 请求：`http.NewRequest("POST", doubaoVoiceListEndpoint+"?"+query.Encode(), body)`；query 含 `aid`、`real_aid`、`device_platform=web`、`pkg_type=release_version`、`version_code=20800`、`pc_version=3.9.0`、`region=CN`、`sys_region=CN`、`use-olympus-account=1`、`samantha_web=1`、`language=zh`；header 含 `Content-Type: application/json`、`Origin: DoubaoOrigin`、`Cookie`
- 请求体 JSON 字段：`page_index`、`page_size`、`recommend_type`，`tab_key` 仅在非空时写入（用 `omitempty`）
- 响应结构体字段：`code`、`msg`、`data.ugc_voice_list[].style_id`、`data.ugc_voice_list[].name`、`data.has_more`
- 错误处理：HTTP 非 200 → `fmt.Errorf("豆包音色接口返回 HTTP %d", resp.StatusCode)`；401/403 → `fmt.Errorf("%w: 音色接口 HTTP %d", ErrDoubaoAuth, resp.StatusCode)`；JSON 解析失败 → `fmt.Errorf("解析豆包音色响应失败: %w", err)`；`code` 为 40200001 或 msg 小写后含 `session auth` → `fmt.Errorf("%w: code=%d msg=%s", ErrDoubaoAuth, code, msg)`；其余非 0 → `fmt.Errorf("豆包音色接口错误: code=%d msg=%s", code, msg)`
- 归并：遍历 `tabs` 顺序，`style_id` 为空或以 `BV` 开头则跳过；首次出现记录 `StyleID`/`Name`，再次出现只追加分类 `Key`（不重复追加）
- 并发错误：每个分类的失败包装为 `fmt.Errorf("分类 %s 拉取失败: %w", tab.Key, err)`，任一失败即返回该错误（测试断言错误含分类名）
- 客户端：`&http.Client{Timeout: 30 * time.Second}`

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestDoubaoTabsDefinition|TestSelectDoubaoTabs|TestListDoubaoVoices' -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubaoVoiceList.go tts/doubaoVoiceList_test.go
git commit -m "feat(doubao): 拉取并按分类归并豆包音色列表"
```

---

### Task 2: 认证失效重试

**Files:**
- Modify: `tts/doubaoVoiceList.go`
- Test: `tts/doubaoVoiceList_test.go`

**Interfaces:**
- Consumes: `ListDoubaoVoices`（Task 1）、`resolveDoubaoCookie`、`extractAndSaveDoubaoCookie`、`clearDoubaoCookie`、`ErrDoubaoAuth`（已有）
- Produces: `func ListDoubaoVoicesWithCookie(explicitCookie string, tabs []DoubaoTab) ([]DoubaoVoice, error)`

- [ ] **Step 1: 写失败测试**

在 `tts/doubaoVoiceList_test.go` 追加：

```go
// TestListDoubaoVoicesWithCookieRetry 验证登录态失效后清缓存并从 Edge 重取一次。
func TestListDoubaoVoicesWithCookieRetry(t *testing.T) {
	useTempConfigDir(t)

	call := 0
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
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
	if call != 2 {
		t.Errorf("接口调用次数 = %d, 期望 2", call)
	}
}

// TestListDoubaoVoicesWithCookieNonAuthError 验证非认证错误不触发重试。
func TestListDoubaoVoicesWithCookieNonAuthError(t *testing.T) {
	useTempConfigDir(t)

	call := 0
	startFakeVoiceList(t, func(w http.ResponseWriter, r *http.Request) {
		call++
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
	if extracted != 0 || call != 1 {
		t.Errorf("非认证错误不应重试：extracted=%d call=%d", extracted, call)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestListDoubaoVoicesWithCookie' -v`
Expected: FAIL，`undefined: ListDoubaoVoicesWithCookie`

- [ ] **Step 3: 实现 `ListDoubaoVoicesWithCookie`**

实现要求：

- 第一次 Cookie 用 `resolveDoubaoCookie(explicitCookie)`；解析失败直接返回
- 调 `ListDoubaoVoices(cookie, tabs)`
- 成功返回；失败且 `!errors.Is(err, ErrDoubaoAuth)` 时直接返回
- 认证失败时：`_ = clearDoubaoCookie()`，再 `extractAndSaveDoubaoCookie()`；失败则返回 `fmt.Errorf("豆包登录态失效且重新获取 Cookie 失败: %w（原始错误: %v）", err, authErr)`
- 用新 Cookie 再调一次 `ListDoubaoVoices`，失败则包装为 `fmt.Errorf("豆包登录态失效，请重新登录 Edge 中的豆包或设置 %s: %w", envDoubaoCookie, err)`

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestListDoubaoVoicesWithCookie' -v`
Expected: 两个测试 PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubaoVoiceList.go tts/doubaoVoiceList_test.go
git commit -m "feat(doubao): 音色列表在登录态失效时自动重取 Cookie"
```

---

### Task 3: CLI 接入与文档

**Files:**
- Modify: `main.go:99`（帮助文本 `-l` 一行）、`main.go:107-110`（豆包选项）、`main.go:130`（flag 定义）、`main.go:149-161`（`-l` 分支）
- Modify: `README.md`（豆包音色说明与示例）

**Interfaces:**
- Consumes: `tts.ListDoubaoVoicesWithCookie`、`tts.SelectDoubaoTabs`、`tts.DoubaoVoice`
- Produces: 无新导出接口

- [ ] **Step 1: 修改 `main.go` 的 flag 定义**

在 `doubaoRefresh` 之后新增：

```go
doubaoTab := flag.String("dv-tab", "", "列出豆包音色时限定分类：rec/female/male/characters/accent")
```

- [ ] **Step 2: 修改 `-l` 分支**

把现有分支替换为按引擎分流：`edge` 保持原输出；`doubao` 先 `tts.SelectDoubaoTabs(*doubaoTab)`，再 `tts.ListDoubaoVoicesWithCookie(*doubaoCookie, tabs)`，输出：

```go
fmt.Println("豆包可用的语音列表:")
for _, v := range voices {
    fmt.Printf("%s (%s) - %s\n", v.StyleID, strings.Join(v.Tabs, ","), v.Name)
}
```

错误分支打印 `获取语音列表失败：%v` 并 `return`（与现有风格一致）

- [ ] **Step 3: 更新帮助文本**

- `-l` 描述改为「列出当前引擎所有可用语音」
- 豆包选项段新增 `-dv-tab TAB     列出豆包音色时限定分类 (rec/female/male/characters/accent)`
- 示例新增两行：

```go
fmt.Printf("  %s -engine doubao -l\n", name)
fmt.Printf("  %s -engine doubao -l -dv-tab female\n", name)
```

- [ ] **Step 4: 编译与静态检查**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 无输出，命令成功

- [ ] **Step 5: 实测全量与分类列表**

Run: `go run . -engine doubao -l | head -5`
Expected: 首行 `豆包可用的语音列表:`，随后每行形如 `zh_female_wenroutaozi_uranus_bigtts (rec,female) - 温柔桃子（升级版）`

Run: `go run . -engine doubao -l -dv-tab male | wc -l`
Expected: 大于 100（男生分类实测 182 条，加标题行）

Run: `go run . -engine doubao -l -dv-tab bogus`
Expected: 输出包含 `获取语音列表失败` 与 `未知的豆包音色分类`

- [ ] **Step 6: 更新 `README.md`**

在豆包使用说明处补充：

- 列出豆包音色：`edgetts -engine doubao -l`
- 按分类：`edgetts -engine doubao -l -dv-tab female`
- 分类可选值：`rec`、`female`、`male`、`characters`、`accent`
- 输出的第一列即 `-dv` 可用的 `style_id`

- [ ] **Step 7: 提交**

```bash
git add main.go README.md
git commit -m "feat(cli): 支持 -engine doubao -l 列出豆包音色"
```
