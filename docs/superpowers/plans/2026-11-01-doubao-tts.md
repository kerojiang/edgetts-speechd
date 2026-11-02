# 豆包 TTS 集成实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 edgetts-speechd 中新增豆包 VoiceGenie TTS 引擎，支持本地持久化 Cookie、失效自动重取，并通过 `-engine doubao` 与现有 Edge TTS 共存。

**Architecture:** 新增 `tts/doubao*.go` 四个文件：手写 protobuf 编解码、Cookie 管理、Edge CDP 自动提取、WebSocket 客户端。`DoubaoTTS` 实现现有 `tts.Core` 接口；`main.go` 增加引擎选择参数。

**Tech Stack:** Go 1.26、`github.com/gorilla/websocket`、`encoding/json`、标准库 `crypto`、`os/exec`（ffmpeg，可选）。

**Spec:** `docs/design-doubao-tts.md`

## Global Constraints

- 只使用现有依赖 `github.com/gorilla/websocket v1.5.3`，不新增 Go 模块依赖。
- 协议常量必须与设计文档一致：appkey `GOqQpfo1fO7slHv8`、aid/real_aid `586864`、namespace `VoiceGenie`、默认说话人 `zh_female_wenroutaozi_uranus_bigtts`。
- Cookie 缓存文件权限必须是 `0600`，路径 `~/.config/edgetts-speechd/doubao.json`。
- Cookie 失效判定以服务端 `status_code == 40200001` 或 `SessionFailed` 文本包含 `session auth` 为准，失效后自动重取并重试一次。
- 不打印完整 Cookie 到标准输出或日志。
- 所有注释使用中文，遵循项目现有风格。

## Review Focus

- Cookie 包含分号、等号或中文时，`Cookie` 请求头必须原样传递，不能被 `url.Values` 二次编码。
- 服务端返回空的 `TTSResponse.data` 或只有 `TTSSentenceStart` 时，不能误判成功，必须继续读到 `TTSEnded`。
- 文本包含 emoji、控制字符、超长文本（>4096 字符）时，`BidirectionalTTS` 不能崩，需要按句子拆分。
- `ffmpeg` 不存在或转码失败时，必须保留原始 `.ogg` 文件并返回可用路径。
- 临时 headless Edge 启动失败、端口被占用或 CDP 超时时，必须清理临时目录并回退到明确错误。

---

### Task 1: protobuf 编解码

**Files:**
- Create: `tts/doubaoProto.go`
- Test: `tts/doubaoProto_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type pbRequest struct { Token, Appkey, Namespace, Version, Event, Payload, TaskID string; Data []byte; SeqID *uint64 }`
  - `func encodePBRequest(r pbRequest) []byte`
  - `type pbResponse struct { TaskID, MessageID, Namespace, Event, StatusText, Payload string; StatusCode int32; Data []byte; SeqID uint64 }`
  - `func decodePBResponse(data []byte) (pbResponse, error)`

- [ ] **Step 1: 写失败测试**

```go
func TestPBRequestRoundTrip(t *testing.T) {
    // 期望编码中依次出现 field tag: 0x0a,0x12,0x1a,0x22,0x2a,0x32,0x3a,0x42,0x48
}

func TestDecodePBResponse(t *testing.T) {
    // 构造响应：task_id="t1" event="TaskStarted" status_code=20000000 data="OggS"
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestPB' -v`
Expected: FAIL，`undefined: encodePBRequest`

- [ ] **Step 3: 实现手写 varint 编解码**

按设计文档字段编号实现 `putVarint`、`readVarint`、`encodePBRequest`、`decodePBResponse`。遇到未知 wire type 时跳过；长度越界时返回错误。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestPB' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubaoProto.go tts/doubaoProto_test.go
git commit -m "feat(doubao): 添加 VoiceGenie protobuf 编解码"
```

---

### Task 2: Cookie 持久化与优先级

**Files:**
- Create: `tts/doubaoCookie.go`
- Test: `tts/doubaoCookie_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `type doubaoCredentials struct { Cookie string \`json:"cookie"\`; UpdatedAt time.Time \`json:"updated_at"\` }`
  - `func doubaoConfigPath() string`
  - `func loadCachedDoubaoCookie() (string, error)`
  - `func saveDoubaoCookie(cookie string) error`
  - `func clearDoubaoCookie() error`
  - `func resolveDoubaoCookie(explicit string) (string, error)`：优先级 explicit → 环境变量 `DOUBAO_COOKIE` → 缓存 → 自动提取

- [ ] **Step 1: 写失败测试**

```go
func TestResolveDoubaoCookiePriority(t *testing.T) {
    // explicit 非空时直接返回
    // 环境变量设置时优先于缓存
}

func TestSaveAndLoadDoubaoCookie(t *testing.T) {
    // 使用 t.TempDir() 覆盖配置目录，保存后读回一致，文件权限 0600
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestDoubaoCookie|TestResolve' -v`
Expected: FAIL

- [ ] **Step 3: 实现 Cookie 读写**

配置目录用 `os.UserConfigDir()`；测试通过包级变量 `doubaoConfigDirOverride` 覆盖。保存时 `os.MkdirAll` + `os.WriteFile(..., 0600)`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestDoubaoCookie|TestResolve' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubaoCookie.go tts/doubaoCookie_test.go
git commit -m "feat(doubao): 添加 Cookie 本地持久化与优先级解析"
```

---

### Task 3: 从 Edge 自动提取 Cookie

**Files:**
- Create: `tts/doubaoEdgeCookie.go`
- Test: `tts/doubaoEdgeCookie_test.go`

**Interfaces:**
- Consumes: `saveDoubaoCookie`（Task 2）
- Produces:
  - `func extractDoubaoCookieFromEdge() (string, error)`
  - `func buildCookieHeader(items []cdpCookie) string`

- [ ] **Step 1: 写失败测试**

```go
func TestBuildCookieHeader(t *testing.T) {
    // 同名 Cookie 只保留第一次出现
    // 非 doubao.com 域名被过滤
    // 值含分号/等号时原样拼接
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestBuildCookieHeader' -v`
Expected: FAIL

- [ ] **Step 3: 实现 Edge 提取**

替换设计文档中的流程图：

1. `os.MkdirTemp` 创建临时目录，复制 `Local State` 与 `Default/Cookies`。
2. 找空闲端口：`net.Listen("tcp", "127.0.0.1:0")` 后关闭。
3. 启动 `msedge --user-data-dir=<tmp> --remote-debugging-port=<port> --headless=new --no-first-run --no-default-browser-check --disable-gpu about:blank`。
4. 轮询 `http://127.0.0.1:<port>/json/list`，最多 15 秒。
5. 用 gorilla/websocket 连接 page target，发送 `{"id":1,"method":"Storage.getCookies"}`。
6. 过滤 `doubao.com`，调用 `buildCookieHeader`。
7. `defer` 关闭进程、删除临时目录。

非 Linux 平台返回 `errors.New("自动提取仅支持 Linux，请设置 DOUBAO_COOKIE")`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestBuildCookieHeader' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubaoEdgeCookie.go tts/doubaoEdgeCookie_test.go
git commit -m "feat(doubao): 支持从 Edge 自动提取登录 Cookie"
```

---

### Task 4: 豆包 TTS 客户端

**Files:**
- Create: `tts/doubaoConfig.go`
- Create: `tts/doubao.go`
- Test: `tts/doubao_test.go`

**Interfaces:**
- Consumes: `pbRequest`、`encodePBRequest`、`decodePBResponse`（Task 1）；`resolveDoubaoCookie`、`clearDoubaoCookie`（Task 2）；`extractDoubaoCookieFromEdge`（Task 3）
- Produces:
  - `type DoubaoConfig struct { Voice string; Rate float64; Pitch float64; Cookie string }`
  - `func NewDoubaoConfig(voice string) *DoubaoConfig`
  - `func (c *DoubaoConfig) Validate() error`
  - `type DoubaoTTS struct { Config *DoubaoConfig; ... }`
  - `func NewDoubao() *DoubaoTTS`
  - `func (d *DoubaoTTS) GetAudioBytes(text string) ([]byte, error)`
  - `func (d *DoubaoTTS) GetAudioFile(text string) (string, error)`
  - `func (d *DoubaoTTS) GetAudioFileTo(text, outputPath string) (string, error)`
  - `var _ Core = (*DoubaoTTS)(nil)`

- [ ] **Step 1: 写失败测试**

```go
func TestDoubaoConfigValidate(t *testing.T) {
    // 空说话人报错
    // speech_rate 超出 0.7~2.0 报错
}

func TestDoubaoSessionPayload(t *testing.T) {
    // 生成 JSON 中 tts.speaker 与 tts.audio_config 字段正确
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./tts -run 'TestDoubao' -v`
Expected: FAIL

- [ ] **Step 3: 实现配置与客户端**

- 连接参数按设计文档 2.1 构造；随机 ID 用 `crypto/rand`。
- 请求头 `Origin: chrome-extension://capohkkfagimodmlpnahjoijgoocdjhd`，`Cookie` 原样放入。
- 流程：StartTask → 等 TaskStarted → StartSession → 等 SessionStarted → BidirectionalTTS → EndTTS → 收集 TTSResponse.data → 等 TTSEnded。
- 读取超时 30 秒；`SessionFailed`/`TaskFailed` 转成 `doubaoServerError`，`status_code == 40200001` 标记 `ErrDoubaoAuth`。
- `GetAudioBytes` 捕获 `ErrDoubaoAuth` 后：`clearDoubaoCookie()` → `extractDoubaoCookieFromEdge()` → 重试一次。
- 文本按句拆分复用现有 `SplitTextBySentences`，每段单独一轮 BidirectionalTTS/EndTTS，音频按序拼接。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./tts -run 'TestDoubao' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add tts/doubao.go tts/doubaoConfig.go tts/doubao_test.go
git commit -m "feat(doubao): 实现 VoiceGenie WebSocket TTS 客户端"
```

---

### Task 5: CLI 集成

**Files:**
- Modify: `main.go`
- Modify: `docs/design-doubao-tts.md`（如参数有调整）

**Interfaces:**
- Consumes: `tts.NewDoubao`、`tts.DoubaoConfig`（Task 4）
- Produces: 无

- [ ] **Step 1: 增加命令行参数**

```go
engine := flag.String("engine", "edge", "TTS 引擎：edge 或 doubao")
doubaoVoice := flag.String("dv", "", "豆包说话人")
doubaoCookie := flag.String("doubao-cookie", "", "直接指定豆包 Cookie")
doubaoRefresh := flag.Bool("doubao-refresh", false, "强制重新从 Edge 提取 Cookie")
```

- [ ] **Step 2: 运行时选择引擎**

`-engine doubao` 时构造 `tts.NewDoubao()`，设置 `Voice/Rate/Pitch/Cookie`；`-r` 的百分比转换为 `1 + n/100`；`-doubao-refresh` 先清空缓存再解析。

- [ ] **Step 3: 编译并手工验证**

Run:
```bash
go build -o edgetts .
./edgetts -engine doubao -o /tmp/doubao-test.mp3 "你好，这是豆包语音测试。"
./edgetts -engine edge -o /tmp/edge-test.mp3 "你好，这是 Edge 语音测试。"
ffprobe -hide_banner /tmp/doubao-test.mp3
```
Expected: 两个文件都生成；豆包文件可被 ffprobe 识别为 MP3。

- [ ] **Step 4: 提交**

```bash
git add main.go
git commit -m "feat: 新增 -engine 参数支持豆包 TTS"
```

---

### Task 6: 全量验证与文档收尾

**Files:**
- Modify: `README.md`
- Modify: `docs/design-doubao-tts.md`

- [ ] **Step 1: 更新 README**

补充豆包引擎用法、Cookie 获取方式、失效处理说明。

- [ ] **Step 2: 运行全量检查**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: 全部通过。

- [ ] **Step 3: 端到端验证**

Run:
```bash
./edgetts -engine doubao -dv zh_female_wenroutaozi_uranus_bigtts -o /tmp/doubao-e2e.ogg "测试豆包语音输出。"
ffprobe -hide_banner /tmp/doubao-e2e.ogg
```
Expected: 输出包含 `Ogg`、`opus`、`24000 Hz`。

- [ ] **Step 4: 提交**

```bash
git add README.md docs/design-doubao-tts.md
git commit -m "docs: 补充豆包 TTS 使用说明"
```
