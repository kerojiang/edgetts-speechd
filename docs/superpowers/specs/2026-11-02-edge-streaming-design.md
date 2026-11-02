# Edge TTS 流式播放设计（2026-11-02）

## 意图

Edge 引擎当前是「完整合成写文件 → 再播放」，长文本首声等于完整合成时间（实测 740 字约 2.45s，God 版 5220 字节约 20.4s）。目标：像豆包一样**首声约 1s**，边收边播。

## 实测依据

| 项目 | 结果 |
|---|---|
| Edge 首个音频包（740 字，Continuous=true） | 1.25s |
| Edge 首个音频包（740 字，Continuous=false） | 1.09s |
| Edge 完整合成（740 字单段） | 2.45s |
| Edge 首个音频包（5220 字节 / 2 段，false） | 1.28s |
| **多段文本（6039 字节 / 2 段，false）** | **旧实现只返回第 1 段**：整段 MP3 1665648 字节 == 第 1 段，第 2 段 874800 字节丢失 |
| 各段单独合成后手动拼接 MP3 | go-mp3 连续解码成功：PCM 40647168 字节（第 1 段 26650368，比例 1.53，两段都在） |
| go-mp3 用 io.Pipe 流式解码 | 成功（PCM 444672 字节，24000Hz） |
| `ContinuousPlayback=true` + 文本 >4096 字节 | **失败**：`websocket: close 1007 SSML is invalid` |

结论：Edge 服务端原生流式返回音频；go-mp3 可流式解码；流式路径必须用 `ContinuousPlayback=false`；**必须先修复多段早退缺陷**。

## 实施前发现并修复的阻塞性缺陷（2026-11-02）

**现象**：>4096 字节文本（切成 2 段）只播放第 1 段，无任何错误输出。

**定位**：`Stream` 标准模式内层 `select` 同时监听 `chunkChan` 与 `streamErrChan`。`streamSingle` 正常结束时会关闭 `errChan`（不发送错误），内层 select 命中该 case 读到 `nil` 后直接 `return`，后续段不再合成。

**证据**：临时打点显示只打印 `DEBUG 段 1/2`，段 2 从未开始；整段 MP3 字节数恰好等于第 1 段。

**修复**：抽出 `relayEdgeStream`，`errs` 关闭时置 `nil` 并继续读 `chunks`，直到 `chunks` 关闭；`Stream` 两个分支共用该函数。

**回归**：`tts/edge_relay_test.go` 的 `TestRelayEdgeStreamContinuesAfterErrChanClosed`（可确定复现）；真实网络 `TestEdgeMultiSegmentReal` 断言整段字节 == 各段之和。

## 方案（已选定：A）

`EdgeTTS.Stream`（已有）→ 只转发 `Type=="audio"` 的块 → `io.Pipe` → `mp3.NewDecoder` → oto 播放。

### 改动

1. `player` 新增 `PlayMP3Stream(r io.Reader) error`：流式解码 MP3 并播放，阻塞到结束。
2. `tts` 新增 `(e *EdgeTTS) SynthesizeStream(ctx context.Context, text string, w io.Writer) error`：
   - 内部按 `text` 现场切分（`newEdgeWithConfig`），因为 `NewEdge()` 的 `Texts` 为空；
   - 固定 `ContinuousPlayback=false`；
   - 把 `Type=="audio"` 的块写入 `w`；命名与豆包的 `SynthesizeStream` 对齐。
3. `main.go` Edge 播放分支改用流式：
   - `cfg.ContinuousPlayback = false`（实测 true 对多段文本失败）；
   - `io.Pipe` 连接合成端与播放端（合成端写、播放端读）；
   - 播放失败/结束时关闭 pipe，避免 goroutine 泄漏。

### 不做

- 不改 Edge 合成协议与 SSML 生成。
- 不为 Edge 加并行/首段独占（首包受服务端 TTFB 限制，并行无收益）。
- 不改豆包路径。

## 风险与处理

| 风险 | 处理 |
|---|---|
| 流中途断开导致播放挂起 | 合成端出错时关闭 pipe 写端，播放端收到 EOF/错误后返回 |
| 空音频 | `PlayMP3Stream` 解码零字节时返回明确错误 |
| 停止朗读（speechd SIGTERM） | 进程被杀，无需额外取消逻辑 |
| 多段文本（>4096） | 强制 `ContinuousPlayback=false`，实测正常 |

## 测试计划

1. 单元：`PlayMP3Stream` 用 `testdata/edge_test.mp3` 分块写 pipe，断言解码播放不挂起（真出声用 integration tag）。
2. 单元：`SynthesizeStream` 只透传 audio 块（可用真实服务 integration 测试）。
3. 集成（真实 Edge）：
   - 长文本 20 次首声采样（目标全部 ≤2s）；
   - 短文本回归；
   - 多段文本（>4096 字节）完整播放；
   - speechd 路径 5 次首声。
4. 回归：豆包首声 20 次、`go test ./...` 全绿。

## 验收标准

- Edge 长文本首声 20 次全部 ≤2s（当前为完整合成时间，740 字 2.45s+，长文本 20s+）。
- 短文本、多段文本、speechd 路径均正常出声。
- 豆包路径无回归。

## 实测结果（2026-11-02）

详细原始数据见 `docs/superpowers/2026-11-02-edge-streaming-test.md`。

| 场景 | 结果 |
|---|---|
| Edge 长文本（740 字）首声 20 次 | **20/20 ≤2s**（927-1762ms） |
| Edge 短文本首声 10 次 | 10/10 ≤2s（883-1086ms） |
| 多段文本（4201 字节 / 2 段，+100%） | 首声 1195ms；完整播放 2m42s 正常退出 |
| 多段完整性（网络测试） | 整段 MP3 2444400 == 各段之和 2444400 |
| speechd 路径 5 次 | 5/5 ≤2s（1174-1439ms） |
| 豆包回归 20 次 | 20/20 ≤2s（1014-1406ms） |
| `go build` / `go vet` / `go test ./...` / 集成测试 | 全绿 |

对照：改动前 Edge 740 字首声等于完整合成时间（约 2.45s）；多段文本只播第 1 段（已修复）。
