# Edge TTS 流式播放实测记录（2026-11-02）

## 目的

验证 Edge 引擎改为纯 Go 流式播放后：

1. 长文本首声是否稳定 ≤2s；
2. 短文本是否正常；
3. >4096 字节多段文本是否完整播放（不丢段）；
4. speechd 路径（Okular 同链路）是否同样达标；
5. 豆包路径是否回归。

## 环境

- Linux，Go 1.26，`CGO_ENABLED=0`（纯 Go 播放栈）
- 二进制：`/home/jie/Data/Projects/edgetts-speechd/edgetts`
- 部署路径：`/home/jie/.local/bin/edgetts`（speechd 模块指向此处）
- 诊断：`EDGETTS_TIMING=1`（默认关闭）
- 测试文本：`/tmp/opencode/page_text.txt`（2220 字节 / 约 740 字）

## 实施中修复的阻塞性缺陷

多段文本（6039 字节 → 2 段）旧实现**只返回第 1 段**且无任何报错：

| 项目 | 实测值 |
|---|---|
| 整段 MP3（旧实现） | 1665648 字节 |
| 第 1 段单独合成 | 1665648 字节（完全相等） |
| 第 2 段单独合成 | 874800 字节（被丢弃） |

根因：`Stream` 标准模式内层 select 在 `streamSingle` 正常关闭 `errChan` 时读到 `nil` 直接 `return`。修复后：`relayEdgeStream` 在 errs 关闭时置 nil 并继续读 chunkChan。

修复后真实网络回归（`TestEdgeMultiSegmentReal`）：

```
第 1 段 MP3 = 1682784 字节
第 2 段 MP3 = 761616 字节
整段 MP3 = 2444400 字节，各段之和 = 2444400 字节
```

## 结果

### 1. Edge 长文本首声（740 字，20 次）

每次首声后 6s 强制停止（避免等待完整播放）。

| 次数 | 首声 | 次数 | 首声 |
|---|---|---|---|
| 1 | 1167ms | 11 | 1044ms |
| 2 | 1103ms | 12 | 959ms |
| 3 | 1188ms | 13 | 1201ms |
| 4 | 1043ms | 14 | 1032ms |
| 5 | 1095ms | 15 | 1068ms |
| 6 | 927ms | 16 | 1208ms |
| 7 | 1078ms | 17 | 1041ms |
| 8 | 1014ms | 18 | 1762ms |
| 9 | 1200ms | 19 | 1013ms |
| 10 | 1093ms | 20 | 1048ms |

**20/20 全部 ≤2000ms**（范围 927-1762ms）。

### 2. Edge 短文本首声（10 次）

| 次数 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 |
|---|---|---|---|---|---|---|---|---|---|---|
| 首声 | 1086ms | 909ms | 927ms | 1025ms | 941ms | 967ms | 907ms | 985ms | 883ms | 967ms |

**10/10 ≤2000ms**（范围 883-1086ms）。

### 3. 多段文本完整播放（4201 字节 → 2 段，`-r +100%`）

```
[timing      0ms] tts: Edge 切分为 2 段
[timing   1195ms] player: 首个 PCM 块被取走（首声时刻，2304 字节）
exit=0
real 2m42.555s
```

首声 1195ms；完整播放 2 分 42 秒后正常退出，无 `SSML is invalid`、无解码错误。配合上面的网络回归（整段 == 各段之和），确认未丢段。

### 4. speechd 路径（5 次）

测量方法：`spd-say` 播放 740 字文本，轮询 `pactl list sink-inputs` 中新增的 `Corked: no` 流。

| 次数 | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|
| 首声 | 1288ms | 1174ms | 1272ms | 1439ms | 1266ms |

**5/5 ≤2000ms**（范围 1174-1439ms）。

### 5. 豆包回归（20 次）

| 次数 | 首声 | 次数 | 首声 |
|---|---|---|---|
| 1 | 1406ms | 11 | 1176ms |
| 2 | 1050ms | 12 | 1153ms |
| 3 | 1356ms | 13 | 1020ms |
| 4 | 1103ms | 14 | 1014ms |
| 5 | 1243ms | 15 | 1058ms |
| 6 | 1249ms | 16 | 1136ms |
| 7 | 1214ms | 17 | 1084ms |
| 8 | 1169ms | 18 | 1177ms |
| 9 | 1299ms | 19 | 1043ms |
| 10 | 1278ms | 20 | 1194ms |

**20/20 ≤2000ms**（范围 1014-1406ms），无采样率冲突。

### 6. 构建与测试

- `go build ./...` / `go vet ./...`：退出码 0
- `go test ./...`：全部 `ok`
- `go test -tags integration ./player/`：PASS（含真出声流式播放，26.4s）
- `EDGETTS_NET_TEST=1 go test ./tts/ -run "TestEdgeMultiSegmentReal|TestEdgeSynthesizeStreamMultiSegmentReal"`：PASS（24.3s）

## 结论

- Edge 长文本首声从「完整合成时间」降为 **0.93-1.76s**，20/20 达标。
- 多段文本不再丢段；完整播放正常结束。
- speechd 实测路径 1.17-1.44s，与直跑一致。
- 豆包无回归。

## 复测命令

```bash
# 长文本首声 20 次
text=$(cat /tmp/opencode/page_text.txt)
for i in $(seq 1 20); do
  EDGETTS_TIMING=1 timeout 6 ./edgetts -engine edge "$text" 2>&1 | grep "首个 PCM"
done

# 多段完整性（网络回归）
EDGETTS_NET_TEST=1 go test ./tts/ -run TestEdgeMultiSegmentReal -count=1 -v

# 流式输出完整性
EDGETTS_NET_TEST=1 go test ./tts/ -run TestEdgeSynthesizeStreamMultiSegmentReal -count=1 -v

# 播放栈集成（真出声）
go test -tags integration ./player/ -count=1 -run TestPlayMP3Stream -v
```

## 局限

1. 首声数据来自本机同一网络环境，未覆盖跨地域/弱网。
2. 多段完整播放只测了 2 段（4201 字节）；更多段数未逐档测试（多段逻辑与段数无关）。
3. `-o` 多段场景仍受旧默认 `--continuous` 影响（本次未改变 `-o` 行为），可用 `--no-continuous` 规避。

## 最终审查后的修复与复测（同日）

独立审查发现 1 个 Critical、6 个 Important，均已修复并补测试：

| 问题 | 根因 | 修复 | 回归测试 |
|---|---|---|---|
| Critical：真实流错误被静默丢弃 | `relayEdgeStream` 在 `chunks` 关闭时直接 `return nil`，`select` 随机命中的情况下已缓冲的 `errs` 错误被丢弃（音频截断却报成功） | `chunks` 关闭时非阻塞取走 `errs` 缓冲值，有错误则返回 | `TestRelayEdgeStreamReportsBufferedErrorWhenChunksClose`（循环 500 次，旧实现第 1 次即失败） |
| `GetAudioFile`/`GetAudioFileTo`/`GetAudioBytes` 零音频成功退出、错误可能丢失 | 三个方法各自重复实现 channel 循环 | 统一走 `synthesizeTo` → `forwardEdgeAudio`（错误优先、零音频报错） | `TestForwardEdgeAudioPropagatesError` / `TestForwardEdgeAudioRejectsEmpty` |
| `-o` 失败会破坏已有文件 | 先 `os.Create` 截断目标，再合成 | `writeFileAtomic`：同目录临时文件 → 成功后 `rename`；保留原权限（新文件 0644） | `TestWriteFileAtomicSuccess` / `TestWriteFileAtomicPreservesTargetOnError` / `TestWriteFileAtomicPreservesMode` |
| 无音频后端时“静默成功” | `oto.NewContext` 是异步初始化，未等 `ready` 就当作可用 | `ensureAudioContext` 等 `ready` 关闭后检查 `Context.Err()`（上限 10s） | `TestEnsureAudioContextNoBackend`（子进程 + `PULSE_SERVER`/`ALSA_CONFIG_PATH` 指向无效后端） |
| WebSocket 读超时被当作正常结束 | `audioWasReceived` 为真时超时直接 `return` | 超时一律上报 `WebSocket 读取超时（音频可能不完整）`；正常结束只走 `turn.end` | `TestClassifyReadError` |
| 播放错误被 `context.Canceled`/pipe 副作用遮盖 | 先 `cancel()` 再 `CloseWithError`，合成端先返回取消错误 | 先 `CloseWithError(playErr)` 再 `cancel()`；`preferPlayErr` 播放错误优先 | `TestPreferPlayErr` |

修复后复测（同一环境、同一文本 2220 字节 / 约 740 字）：

| 场景 | 次数 | 首声范围 | 是否 ≤2s |
|---|---|---|---|
| Edge 直跑 | 20 | 957-1746ms | 20/20 |
| 豆包直跑 | 20 | 1074-1339ms | 20/20 |
| speechd 路径 | 5 | 1146-1329ms | 5/5 |

`-o` 复测：

- 短文本：exit=0，14832 字节，权限 0644，stdout 打印路径，无发声。
- 多段文本 + `--no-continuous`：exit=0，3275568 字节。
- 多段文本（默认 `--continuous`）：exit=1，报 `websocket: close 1007 ... SSML is invalid`，且目标文件原内容 `ORIGINAL` 保持不变（旧实现会被截断为 0 字节）。

测试：`go build ./...`、`go vet ./...`、`go test ./...`、`go test -tags integration ./player/`、`EDGETTS_NET_TEST=1 go test ./tts/ -run "TestEdgeMultiSegmentReal|TestEdgeSynthesizeStreamMultiSegmentReal|TestSynthesizeStreamFirstSegmentAlone"` 全部通过。
