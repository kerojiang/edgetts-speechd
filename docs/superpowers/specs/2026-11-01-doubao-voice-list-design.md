# 豆包音色列表（`-engine doubao -l`）设计

## 背景与意图

`edgetts -l` 目前只能列出 Edge 的音色。豆包的音色 ID 只能从官方插件或网页里翻找，用户无法在命令行里看到有哪些可选 `-dv` 值。

豆包 Web 端（浏览器插件 v1.38.0）通过 `POST https://www.doubao.com/alice/user_voice/recommend` 拉取音色列表。本设计让 `-engine doubao -l` 复用这条接口，输出可直接用于 `-dv` 的 `style_id`。

## 目标

- `edgetts -engine doubao -l` 列出豆包可用音色，默认覆盖全部 5 个分类并去重
- `-dv-tab` 可限定分类：`rec` / `female` / `male` / `characters` / `accent`
- 复用现有 Cookie 解析链路：显式参数 > `DOUBAO_COOKIE` > 缓存 > 从 Edge 提取
- 登录态失效时与合成路径一致：清缓存 → 强制从 Edge 重取 → 重试一次
- 过滤空 `style_id` 和 `style_id` 以 `BV` 开头的条目（与插件 UI 的过滤行为一致）

## 非目标

- 不实现音色试听、搜索、收藏
- 不改变 Edge `-l` 的现有输出格式
- 不把列表结果写入缓存文件
- 不新增第三方依赖

## 约束

- 接口无公开文档，参数取自插件代码：
  - `static/js/49724.js` 模块 `914729`：`Bv` → `POST ${d.al}/user_voice/recommend`，`d.al = https://www.doubao.com/alice`
  - `static/js/77660.js` 模块 `755501`：`loadVoicesByTab` 传 `page_index` / `page_size` / `recommend_type` / `tab_key`
- 请求头需要 `Origin: chrome-extension://capohkkfagimodmlpnahjoijgoocdjhd`；实测无需额外签名，只要有效 Cookie
- 分页从 `page_index=1` 开始，`page_size=30`，按响应 `data.has_more` 继续
- 响应条目字段：`style_id`（即 TTS `speaker`）、`name`、`tag_list[].tag_value`

## 方案对比

| 方案 | 说明 | 取舍 |
|---|---|---|
| A（采用） | 默认并发拉 5 个分类并去重，另加 `-dv-tab` 限定分类 | 默认完整；需并发代码，约 3–6 秒 |
| B | 默认只拉推荐第一页，`-dv-all` 才全量 | 快，但默认不全 |
| C | 只列推荐分类并翻完 | 实现最简，但漏掉大量女声/男声/角色/口音音色 |

选 A 的理由：用户要回答的是「有哪些可选」，默认应给全；分类参数只是缩小范围的手段。

## 实现要点

### 新文件 `tts/doubaoVoiceList.go`

```go
type DoubaoVoice struct {
    StyleID string   // style_id，可直接用于 -dv
    Name    string   // 中文名称
    Tabs    []string // 出现的分类（rec/female/male/characters/accent）
}

type DoubaoTab struct {
    Key           string // "rec" 等
    RecommendType int    // rec=1，其余=10
    TabKey        string // rec 为空，其余为 female/male/characters/accent
}

func DoubaoTabs() []DoubaoTab
func ListDoubaoVoices(cookie string, tabs []DoubaoTab) ([]DoubaoVoice, error)
```

- `doubaoVoiceListEndpoint` 为包级变量，默认 `https://www.doubao.com/alice/user_voice/recommend`，测试可替换
- 每个分类一个 goroutine（`sync.WaitGroup`），分类内分页串行；结果按分类顺序归并，`style_id` 去重并合并 `Tabs`
- `page_size=30`，`has_more=false` 或空列表停止；`page_index` 硬上限 50 防死循环（实测 rec 分类 13 页）
- HTTP 客户端超时 30 秒；整次拉取用 `context.WithTimeout` 兜底（默认 2 分钟），非 200 返回错误
- 响应必须包含 `data` 与 `data.ugc_voice_list` 键（用 `json.RawMessage` 判键）：`{}`、`data:null`、字段改名都报错，不当作空列表
- `code != 0` 时：`code == 40200001`、`code == 710012001`、`msg` 含 `session auth` / `登录已过期` / `请重新登录` / `未登录`、或 HTTP 401/403 → 返回 `ErrDoubaoAuth`；其余返回带 code/msg 的错误
  - 710012001 是音色列表接口实测的登录过期码（`msg=登录已过期，请重新登录`），与合成链路的 40200001 不同
  - 合成链路已用无效 Cookie 直连真实服务核实，失效响应固定为 `status_code=40200001 status_text=session auth,...`，故合成侧不判定 710012001
- 认证重试封装为 `listDoubaoVoicesWithRetry(cookie string, tabs []DoubaoTab)`：第一次用 `resolveDoubaoCookie`，失败且 `errors.Is(err, ErrDoubaoAuth)` 时 `clearDoubaoCookie()` + `extractAndSaveDoubaoCookie()` 后重试一次

### `main.go`

- `-l` 分支按 `-engine` 分流：`edge` 保持原逻辑；`doubao` 调 `tts.ListDoubaoVoices`
- 抽成 `runList(stdout, stderr, engine, tab, cookie, refresh, deps)`，依赖用 `listDeps` 注入，便于单测；零值依赖返回错误而不是 panic
- `edge` 引擎下给出 `-dv-tab` / `-doubao-refresh` 时，向 stderr 输出 `[WARN]` 说明已忽略
- 列表失败写 stderr 并以退出码 1 结束
- 新增 `-dv-tab`（默认空 = 全部）；非法值报错并返回
- 输出格式沿用 Edge 风格，便于 `awk '{print $1}'` 取 ID：

```
豆包可用的语音列表:
zh_female_wenroutaozi_uranus_bigtts (rec,female) - 温柔桃子（升级版）
```

- 帮助文本：`-l` 改为「列出当前引擎所有可用语音」，补充 `-dv-tab`

### 测试（`tts/doubaoVoiceList_test.go`）

- 分页与去重：`httptest.Server` 返回两页，同 `style_id` 跨分类出现，验证合并与 `BV` 过滤
- 认证重试：第一次返回 `40200001`，第二次成功，验证调用了 Edge 提取（注入 fake extractor）
- 认证码覆盖：`40200001`、`710012001`、HTTP 401/403 均识别为 `ErrDoubaoAuth`；非认证错误不重试
- 响应结构严格性：`{}`、`data:null`、缺 `ugc_voice_list` 键、`ugc_voice_list:null`、`data` 非对象均报错；合法空列表不报错
- 分页上限与整体超时：`has_more` 恒 true 时报「分页超过上限」；超时返回 `context.DeadlineExceeded`
- 分类参数校验：非法 `-dv-tab` 返回错误；分类名大小写不敏感
- CLI 层（`main_test.go`）：输出格式、刷新日志走 stderr、四类错误、Edge 回归、零值依赖、Edge 下的豆包参数警告

## 留给实现者的决定

- 并发失败时是否取消其余分类：采用「任一分类失败即整体失败」；`context` 仅用于整次拉取的总超时，不做跨分类提前取消
- 输出分类顺序固定为 `rec,female,male,characters,accent`
- 不做终端宽度对齐，不引入表格库
