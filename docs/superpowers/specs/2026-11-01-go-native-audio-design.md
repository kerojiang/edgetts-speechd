# edgetts Go 原生播放组件设计（已实现）

- 日期：2026-11-01
- 状态：**已实现并验证**

## 意图（Intent）

edgetts 播放原依赖系统外部命令 `ffplay`（回退 `ffmpeg`/`aplay`）。这带来两个问题：

1. **音频竞争**：`speechd.conf` 的 `AudioOutputMethod pulse` 让 `sd_generic` 模块进程持有 PulseAudio 连接（`/proc/<sd_generic>/fd` 可见 `/memfd:pulseaudio`），而 ffplay 作为独立外部进程又连 PipeWire 播放，两者抢同一音频设备，导致 ffplay 的 `SDLPwAudioPlug` 线程卡在 `ep_poll`，**播放挂起 60+ 秒**才退出（edgetts 等它 → speechd 等 edgetts → 用户听到第一声 6 秒以上）。
2. **外部依赖**：需要 ffmpeg 全家桶，且 headless 下行为不可控。

目标：**在 Go 进程内直接播放**，不依赖任何外部播放器，消除音频竞争。

## 方案（Approach，全部实测验证）

采用**纯 Go 组合**（均无需 C 编译器，本机 `CGO_ENABLED=0`、无 gcc）：

| 用途 | 库 | 版本 | 说明 |
|---|---|---|---|
| 音频输出 | `github.com/ebitengine/oto/v3` | v3.5.1 | **Linux 纯 Go**：`driver_unix.go` 先连 PulseAudio（`jfreymuth/pulse`），失败回退 `driver_alsa_unix.go`（`purego` 动态加载 `libasound.so.2`），均无 cgo |
| MP3 解码 | `github.com/hajimehoshi/go-mp3` | v0.3.4 | 纯 Go，恒输出 16bit 双声道 |
| Ogg Opus 解码 | `github.com/pion/opus` | v0.1.0 | 纯 Go，含 `pkg/oggreader` 解析 Ogg 容器 |

> **关键实测纠正**：豆包引擎输出是 **Ogg Opus（24000Hz mono）**，**不是 Ogg Vorbis**（researcher 曾误判 Vorbis，`file` 实测确认 `Ogg data, Opus audio, 24000 Hz mono`）。`jfreymuth/oggvorbis` 无法解豆包，必须用 `pion/opus`。

## 实测验证（项目内，有据可查）

1. **临时探针**（/tmp/opencode/dectest，非项目内）：
   - oto v3.5.1 在 `CGO_ENABLED=0` 下编译 + 打开 ALSA 设备 + 播放 + 关闭全部成功。
   - pion/opus 解码豆包真实文件：202 包 → 96480 样本 → 4.02s（24000Hz mono），`oggreader.NewWith` 成功。
   - pion/opus 解码 + oto 播放：4.02s 出声无错。
   - go-mp3 解码 Edge 真实文件：4.63s（24000Hz 2ch）+ oto 播放成功。
2. **TDD**：`player/audioPlayer_test.go` 的 `TestDetectFormat_*` / `TestDecodeAudio_*` 先 RED（undefined: detectFormat/decodeAudio）后 GREEN，全部 PASS。
3. **集成测试**（`player/play_integration_test.go`，`integration` tag）：`TestPlayReal_mp3` 4.78s、`TestPlayReal_opus` 4.07s 真出声且不挂起；`TestPlayReal_noExternalPlayer` 确认无 ffplay/ffmpeg/aplay 进程；`TestPlayReal_fileNotExist` 报错。
4. **完整链路**（仓库 `./edgetts`）：Edge 播放 exit=0 耗时 5.4s；豆包播放 exit=0 耗时 4.3s，输出 `/tmp/ttsfile.ogg` 确认 `Ogg data, Opus audio, mono, 24000 Hz`；均无 ffplay/ffmpeg/aplay 残留。
5. **speechd 环境抗竞争**：speechd 活跃（sd_generic 持 pulse 连接）时，新 edgetts 播放 **3.7s** 完成，**不再卡**（此前 ffplay 卡 60s+）。
6. **成功静默**：播放模式成功时 stdout+stderr 为空；`-o` 模式只输出路径。

## 改动（Changes，已完成）

### 1. `go.mod`
新增 3 个依赖：`ebitengine/oto/v3 v3.5.1`、`hajimehoshi/go-mp3 v0.3.4`、`pion/opus v0.1.0`。

### 2. `player/audioPlayer.go` 重构（Go 原生播放）
- `PlayFile(filePath)`：读文件字节 → `PlayStream(data)`。
- `PlayStream(data []byte)`：`detectFormat` 识别 → `decodeAudio` → `playPCM`。
- `detectFormat`：`ID3`/`0xFFEx` → MP3；`OggS` → Opus；否则 unknown。
- `decodeMP3`：go-mp3 解码，输出 16bit 双声道。
- `decodeOpus`：pion/opus 解码，**跳过 OpusHead/OpusTags 头包**（否则首包报 `malformed packet`），mono 上混为立体声。
- `playPCM`：**进程内单例 oto context**（`sync.Once`，oto 只允许一个 context），`NewPlayer` 流式播放，轮询 `IsPlaying()` + 超时（`estDur*2+10s`）防卡死。
- 删除所有 `exec.Command("ffplay"/"ffmpeg"/"aplay")`。

### 3. `tts/doubao.go`（移除 ffmpeg 转码）
- 豆包**始终输出 Ogg Opus**（`DOUBAO_DEFAULT_FILE = /tmp/ttsfile.ogg`），`GetAudioFileTo` 直接写原始字节，不再 `transcodeToMP3`（ffmpeg）。
- 删除 `transcodeToMP3`、`replaceExt`、`isOggOutput` 及 `os/exec` import。

### 4. `tts/core.go`
新增 `DOUBAO_DEFAULT_FILE` 常量（`.ogg`）。

### 5. `main.go`
- 删除成功提示输出：`[INFO] 使用 Edge/豆包 TTS 引擎`、`音频文件已生成：...`、`音频播放完成`（保留 `-o` 路径输出给调用方）。

### 6. 测试
- `player/audioPlayer_test.go`（TDD：detectFormat/decodeAudio）。
- `player/play_integration_test.go`（`integration` tag：真实播放）。
- `tts/doubao_flow_test.go`：`TestDoubaoGetAudioFileToFallsBackToOgg` → `TestDoubaoGetAudioFileToWritesOgg`。
- `tts/doubao_test.go`：删除 `TestIsOggOutput`。

## 已知坑（已处理）

1. **oto 单 context 限制**：`oto.NewContext` 进程内只允许一个（二次调用报 `context is already created`）。用包级 `sync.Once` 单例复用；首次采样率/声道固定，后续不一致则报错（当前 Edge/豆包均 24000Hz，复用安全）。
2. **豆包 Opus 头包**：Ogg 首两包是 `OpusHead`/`OpusTags`，需跳过，否则 `DecodeToInt16` 报 `malformed packet: packet duration exceeds 120 ms`。
3. **mono 上混**：Opus mono 在 `decodeOpus` 内左右复制为 2ch，与 oto 双声道输出一致。

## 非目标 / 反目标

- **不改** `speechd.conf` 的 `AudioOutputMethod`（保持 pulse）。
- **不引入** 任何 cgo 依赖（本机无 gcc，`CGO_ENABLED=0`）。
- **不新增** 外部命令依赖（ffplay/ffmpeg/aplay 全部移除）。
- **不实现** 重采样（当前两个引擎均 24000Hz，单例 context 天然匹配）。

## 验证结果（全部通过）

```
go build ./...   ✓
go vet ./...     ✓
go test ./...    ✓（player/tts/主包全绿）
go test -tags integration ./player/ -run TestPlayReal   ✓（真出声不挂起，无外部播放器）
./edgetts 播放（Edge/豆包）   ✓ 快速出声、无外部播放器、成功静默
speechd 活跃环境播放   ✓ 3.7s（此前 ffplay 卡 60s+）
```

## 流式播放（2026-11-02 追加，解决长文本首声等待）

### 问题
用户长文本（一页书，约 740 字）朗读「等了半天」：`GetAudioBytes` 先逐句**串行**合成完整音频再播放，首声 = 完整合成时间。实测合成耗时 ∝ 文本长度（100字 2.9s / 400字 9.9s / 800字 18.8s / 1500字 29s）。

### 方案（流式边合成边播）
- `player.PlayOggChunks(chunks <-chan []byte)`：逐段解码 Ogg Opus（`pcmChunkReader`），流式喂给 oto 播放。
- `DoubaoTTS.SynthesizeStream(text, out)`：多段**并行**合成（`doubaoParallelLimit=6`），首段完成立即送入 `out`，按序保序。
- `main.go` 豆包播放模式改用 `streamPlayDoubao`（Edge 保持整段文件）。
- `doubaoParallelChunkBytes=100`（约 33 字/段），首段合成约 1.15s。

### 实测（740 字长文本首声）
| 方案 | 首声 |
|---|---|
| 串行合成（先全部再播放） | 约 17s |
| 并行合成（先全部再播放） | 约 5.8s |
| **流式播放（边合成边播）** | **约 1s**（首次冷启动 5.2s，后续稳定） |

### 关键验证
- 拼接多个独立 Ogg Opus 流（每段含 OpusHead）可被 `decodeOpus` 解码（中间 OpusHead 跳过），实测 8 段 → 23.56s PCM 正常。
- 豆包并行连接 8 并发无限流（实测）。
- 短文本流式正常（2.8s），无外部播放器残留。

