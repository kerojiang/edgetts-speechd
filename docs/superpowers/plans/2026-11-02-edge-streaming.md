# Edge TTS 流式播放 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Edge 引擎从「完整合成后播放」改为「边收边播」，长文本首声稳定在 ~1s（当前 740 字 2.45s+，5220 字节 20.4s）。

**Architecture:** 先修 `EdgeTTS.Stream` 标准模式的多段早退缺陷，使多段文本的 MP3 能完整拼成一条流；随后 `SynthesizeStream` 把音频块写入 `io.Writer`，`main.go` 用 `io.Pipe` 连接合成与 `player.PlayMP3Stream`，go-mp3 边读边解码喂给 oto。

**Tech Stack:** Go 1.26、`github.com/hajimehoshi/go-mp3`、`github.com/ebitengine/oto/v3`、`gorilla/websocket`（已有）；无 cgo、无外部播放器。

**Spec:** `docs/superpowers/specs/2026-11-02-edge-streaming-design.md`

## Global Constraints

- 只用 Go：不得引入 ffplay/ffmpeg/aplay 或任何外部播放器。
- 正常流程不输出任何提示（stdout/stderr 均静默）；只有错误才输出。
- 诊断日志只能走 `timing.Logf`，默认（未设 `EDGETTS_TIMING=1`）完全静默。
- 不得改变 `-o <路径>` 的行为：仍然整段合成写文件、不播放、stdout 打印路径。
- 不得改动豆包路径（`tts/doubao.go`、`streamPlayDoubao`）。
- Edge 播放必须固定 `ContinuousPlayback=false`：实测 `true` + >4096 字节文本报 `websocket: close 1007 SSML is invalid`。
- **Edge 多段文本必须完整合成/播放**：实测旧实现只返回第 1 段（Task 1 修复）。
- 所有结论必须有实测依据；测试命令与预期输出写进步骤。

### 实施前实测结论（决定方案的关键依据）

| 实测项 | 结果 |
|---|---|
| Edge 首个音频包（740 字单段，false） | 1.09s |
| Edge 完整合成（740 字单段，false） | 2.45s |
| 多段文本（6039 字节 → 2 段，false） | **旧实现只返回第 1 段**：整段 MP3 1665648 字节 == 第 1 段 1665648 字节，第 2 段 874800 字节丢失 |
| 早退根因 | `Stream` 标准模式内层 select 在 `streamSingle` 正常关闭 `errChan` 时读到 `nil`，直接 `return`，未继续下一段 |
| 各段单独合成后手动拼接 MP3 | go-mp3 连续解码成功：PCM 40647168 字节，第 1 段 26650368 字节（比例 1.53，两段都在） |
| `ContinuousPlayback=true` + >4096 字节 | `websocket: close 1007 SSML is invalid` |

结论：**先修 `Stream` 早退**；修复后多段拼接是合法连续 MP3，流式方案成立。

### 与已批准设计的两处签名修正（有依据）

1. 设计写的是 `(e *EdgeTTS) SynthesizeStream(ctx, out chan<- []byte)`。但 `NewEdge()` 的 `Texts` 为空，真正切分发生在 `newEdgeWithConfig(text, e.Config)`（`tts/edge.go:228`）；`GetAudioFile` 也是先构造新实例再 `Stream`（`tts/edge.go:287-293`）。因此方法必须接收 `text`：`SynthesizeStream(ctx context.Context, text string, w io.Writer) error`。
2. 用 `io.Writer` 而非 `chan []byte`：修复后 Edge 产出的是**一条连续 MP3 流**，`io.Writer` 可直接对接 `io.Pipe`，背压天然、少一层转发 goroutine。

## Review Focus

1. **`-o` 文件输出**：`edgetts -engine edge -o /tmp/x.mp3 "文本"` 仍须得到完整、可播放的 MP3，且不发声。
2. **服务端零音频**：Edge 返回 0 字节音频且无错误时，必须返回明确错误，不能“成功退出”。
3. **流中途报错**：服务端发 `SSML is invalid` / 连接断开时，播放端必须返回错误而不是永久阻塞。
4. **多段文本不丢段**：>4096 字节文本必须完整合成与播放（Task 1 的 bug 会静默丢段）。
5. **与豆包共用 oto context**：Edge（24000Hz/2ch）与豆包（24000Hz/2ch 上混）必须能先后播放，不出现“采样率已固定”错误。

---

### Task 1: 修复 `Stream` 标准模式多段早退（阻塞性缺陷）

**Files:**
- Modify: `tts/edge.go:429-511`（`Stream` 内层循环改为调用 `relayEdgeStream`）
- Test: `tts/edge_relay_test.go`（新建）

**Interfaces:**
- Consumes: `(*TTSChunk)`、`streamSingle` 返回的 `(<-chan *TTSChunk, <-chan error)`
- Produces: `relayEdgeStream(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, out chan<- *TTSChunk) error`

- [ ] **Step 1: 写失败测试（可确定复现旧 bug）**

```go
// TestRelayEdgeStreamContinuesAfterErrChanClosed：errChan 正常关闭后必须继续转发 chunk。
func TestRelayEdgeStreamContinuesAfterErrChanClosed(t *testing.T) {
    chunks := make(chan *TTSChunk, 1)
    errs := make(chan error)
    close(errs) // 模拟 streamSingle 正常结束：无错误、直接关闭
    out := make(chan *TTSChunk, 1)

    done := make(chan error, 1)
    go func() { done <- relayEdgeStream(context.Background(), chunks, errs, out) }()

    // 旧实现会在 errs 关闭时立即 return，这里之后发送的 chunk 会丢失
    time.Sleep(50 * time.Millisecond)
    chunks <- &TTSChunk{Type: "audio", Data: []byte("A")}
    close(chunks)

    select {
    case c := <-out:
        if string(c.Data) != "A" {
            t.Fatalf("转发内容 = %q, 期望 \"A\"", c.Data)
        }
    case <-time.After(time.Second):
        t.Fatal("errChan 关闭后丢弃了后续音频块（多段早退 bug）")
    }
    if err := <-done; err != nil {
        t.Fatalf("relayEdgeStream 返回错误: %v", err)
    }
}

// TestRelayEdgeStreamPropagatesError：真实错误必须原样返回。
func TestRelayEdgeStreamPropagatesError(t *testing.T) {
    var chunks chan *TTSChunk // nil channel：确保 select 只可能命中 errs，测试无竞态
    errs := make(chan error, 1)
    errs <- errors.New("websocket: close 1007 SSML is invalid")
    close(errs)

    err := relayEdgeStream(context.Background(), chunks, errs, make(chan *TTSChunk, 1))
    if err == nil || !strings.Contains(err.Error(), "SSML is invalid") {
        t.Fatalf("期望透出原始错误，实际: %v", err)
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./tts/ -run TestRelayEdgeStream -count=1`
Expected: 编译失败 `undefined: relayEdgeStream`

- [ ] **Step 3: 实现 `relayEdgeStream` 并替换 `Stream` 内两段重复循环**

```go
// relayEdgeStream 把单个 streamSingle 的 chunk/err 转发到 out。
// 关键：errChan 正常关闭（无错误）时必须置 nil 并继续读 chunkChan，
// 否则会在多段合成时提前返回、静默丢弃后续段（历史 bug）。
func relayEdgeStream(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, out chan<- *TTSChunk) error {
    for {
        select {
        case chunk, ok := <-chunks:
            if !ok {
                return nil
            }
            select {
            case out <- chunk:
            case <-ctx.Done():
                return ctx.Err()
            }
        case err, ok := <-errs:
            if !ok {
                errs = nil // 正常结束：不再监听，继续读直到 chunks 关闭
                continue
            }
            if err != nil {
                return err
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

`Stream` 的 goroutine 改为：

```go
if e.Config.ContinuousPlayback {
    if len(e.OriginalTexts) == 0 {
        errChan <- fmt.Errorf("no text segments available for continuous playback")
        return
    }
    e.State.PartialText = e.OriginalTexts[0]
    chunks, errs := e.streamSingle(ctx)
    if err := relayEdgeStream(ctx, chunks, errs, chunkChan); err != nil {
        errChan <- err
    }
} else {
    for _, partialText := range e.Texts {
        e.State.PartialText = partialText
        chunks, errs := e.streamSingle(ctx)
        if err := relayEdgeStream(ctx, chunks, errs, chunkChan); err != nil {
            errChan <- err
            return
        }
    }
}
```

- [ ] **Step 4: 运行确认通过 + 全量测试**

Run: `go test ./tts/ -run TestRelayEdgeStream -count=1 -v && go test ./...`
Expected: 2 个测试 PASS；其余包全部 `ok`

- [ ] **Step 5: 写真实网络回归测试（env gate，先写测试再验证）**

```go
// TestEdgeMultiSegmentReal：真实 Edge 下，多段整段 MP3 字节数 == 各段单独合成之和。
// 默认跳过，EDGETTS_NET_TEST=1 才联网。
func TestEdgeMultiSegmentReal(t *testing.T) {
    if os.Getenv("EDGETTS_NET_TEST") != "1" {
        t.Skip("设置 EDGETTS_NET_TEST=1 才执行真实网络测试")
    }
    text := strings.Repeat("这是用于验证分段拼接完整性的中文测试句子，需要足够长以触发分段。", 61)
    cfg := *NewEdge().Config
    cfg.ContinuousPlayback = false
    whole, err := newEdgeWithConfig(text, &cfg)
    if err != nil {
        t.Fatal(err)
    }
    if len(whole.Texts) < 2 {
        t.Fatalf("文本未切成多段：%d", len(whole.Texts))
    }
    collect := func(inst *EdgeTTS) []byte {
        ch, ech := inst.Stream(context.Background())
        var out []byte
        for {
            select {
            case c, ok := <-ch:
                if !ok {
                    return out
                }
                if c.Type == "audio" {
                    out = append(out, c.Data...)
                }
            case err, ok := <-ech:
                if ok && err != nil {
                    t.Fatalf("stream err: %v", err)
                }
            }
        }
    }
    wholeBytes := collect(whole)
    sum := 0
    for i, seg := range whole.Texts {
        one, err := newEdgeWithConfig(string(seg), &cfg)
        if err != nil {
            t.Fatal(err)
        }
        n := len(collect(one))
        sum += n
        t.Logf("第 %d 段 MP3 = %d 字节", i+1, n)
    }
    t.Logf("整段 MP3 = %d 字节，各段之和 = %d 字节", len(wholeBytes), sum)
    if len(wholeBytes) != sum {
        t.Fatalf("多段音频不完整：%d != %d", len(wholeBytes), sum)
    }
}
```

- [ ] **Step 6: 真实网络验证多段完整性（约 9s）**

Run: `EDGETTS_NET_TEST=1 go test ./tts/ -run TestEdgeMultiSegmentReal -count=1 -v`
Expected: PASS，日志 `整段 MP3 = 各段之和`（修复前该测试失败：整段 == 第 1 段）

- [ ] **Step 7: 提交**

```bash
git add tts/edge.go tts/edge_relay_test.go
git commit -m "fix(edge): 修复 Stream 标准模式多段早退（只播第一段）

errChan 正常关闭时读到 nil 直接 return，导致后续段被静默丢弃。
改为 errs=nil 后继续读 chunkChan，并抽出 relayEdgeStream 消除重复。
新增可确定复现的单元测试与真实网络完整性测试（EDGETTS_NET_TEST=1）。"
```

---

### Task 2: player 层 MP3 流式解码器

**Files:**
- Modify: `player/audioPlayer.go`（`decodeMP3` 之后）
- Test: `player/mp3_stream_test.go`（新建）

**Interfaces:**
- Consumes: `github.com/hajimehoshi/go-mp3`、现有 `readFixture`/`decodeMP3`
- Produces: `newMP3StreamDecoder(r io.Reader) (*mp3.Decoder, error)`

- [ ] **Step 1: 写失败测试**

```go
// TestNewMP3StreamDecoderFromPipe：分块写入 io.Pipe 的 MP3 能被流式解码。
func TestNewMP3StreamDecoderFromPipe(t *testing.T) {
    data := readFixture(t, "edge_test.mp3")
    pr, pw := io.Pipe()
    go func() {
        const chunk = 1024
        for i := 0; i < len(data); i += chunk {
            end := i + chunk
            if end > len(data) {
                end = len(data)
            }
            if _, err := pw.Write(data[i:end]); err != nil {
                return
            }
            time.Sleep(time.Millisecond)
        }
        pw.Close()
    }()

    dec, err := newMP3StreamDecoder(pr)
    if err != nil {
        t.Fatalf("newMP3StreamDecoder 失败: %v", err)
    }
    if got := dec.SampleRate(); got != 24000 {
        t.Errorf("采样率 = %d, 期望 24000", got)
    }
    total := 0
    buf := make([]byte, 8192)
    for {
        n, rerr := dec.Read(buf)
        total += n
        if rerr == io.EOF {
            break
        }
        if rerr != nil {
            t.Fatalf("流式解码失败: %v", rerr)
        }
    }
    want, err := decodeMP3(data)
    if err != nil {
        t.Fatalf("decodeMP3 基准失败: %v", err)
    }
    if total != len(want.pcm) {
        t.Errorf("流式 PCM = %d 字节, 期望 %d（与整段解码一致）", total, len(want.pcm))
    }
}

// TestNewMP3StreamDecoderEmpty：空流失效，必须报错。
func TestNewMP3StreamDecoderEmpty(t *testing.T) {
    pr, pw := io.Pipe()
    pw.Close()
    if _, err := newMP3StreamDecoder(pr); err == nil {
        t.Fatal("空流应返回错误")
    }
}

// TestNewMP3StreamDecoderPipeError：写端带错误关闭时，解码读取必须透出该错误。
func TestNewMP3StreamDecoderPipeError(t *testing.T) {
    pr, pw := io.Pipe()
    go func() {
        pw.Write(readFixture(t, "edge_test.mp3")[:512])
        pw.CloseWithError(errors.New("合成中断"))
    }()
    dec, err := newMP3StreamDecoder(pr)
    if err != nil {
        return // 头部阶段就失败也算透出错误
    }
    buf := make([]byte, 8192)
    for {
        _, rerr := dec.Read(buf)
        if rerr != nil {
            if !strings.Contains(rerr.Error(), "合成中断") {
                t.Fatalf("错误未透出原文: %v", rerr)
            }
            return
        }
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./player/ -run TestNewMP3StreamDecoder -count=1`
Expected: 编译失败 `undefined: newMP3StreamDecoder`

- [ ] **Step 3: 实现**

```go
// newMP3StreamDecoder 用 go-mp3 创建流式解码器：只读取帧头即返回，
// 后续 Read 持续从 r 拉取（r 可为 io.Pipe 读端，无数据时自然阻塞形成背压）。
func newMP3StreamDecoder(r io.Reader) (*mp3.Decoder, error) {
    dec, err := mp3.NewDecoder(r)
    if err != nil {
        return nil, fmt.Errorf("MP3 流式解码器创建失败: %w", err)
    }
    return dec, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./player/ -run TestNewMP3StreamDecoder -count=1 -v`
Expected: 3 个测试全部 PASS

- [ ] **Step 5: 提交**

```bash
git add player/audioPlayer.go player/mp3_stream_test.go
git commit -m "feat(player): 新增 MP3 流式解码器构造（io.Reader 输入）"
```

---

### Task 3: player 层 `PlayMP3Stream` 流式播放

**Files:**
- Modify: `player/audioPlayer.go`（`newMP3StreamDecoder` 之后）
- Test: `player/mp3_play_integration_test.go`（新建，`//go:build integration`）

**Interfaces:**
- Consumes: `newMP3StreamDecoder(r)`（Task 2）、`ensureAudioContext(sr, ch)`、`timing.Logf`
- Produces: `PlayMP3Stream(r io.Reader) error`（阻塞到播完或出错）

- [ ] **Step 1: 写集成测试（真出声，`integration` tag）**

```go
//go:build integration

// TestPlayMP3Stream 分块喂入真实 Edge MP3，验证边收边播且不挂起。
func TestPlayMP3Stream(t *testing.T) {
    data, err := os.ReadFile(filepath.Join("..", "testdata", "edge_test.mp3"))
    if err != nil {
        t.Fatalf("读取 fixture 失败: %v", err)
    }
    pr, pw := io.Pipe()
    go func() {
        const chunk = 4096
        for i := 0; i < len(data); i += chunk {
            end := i + chunk
            if end > len(data) {
                end = len(data)
            }
            if _, werr := pw.Write(data[i:end]); werr != nil {
                return
            }
            time.Sleep(5 * time.Millisecond)
        }
        pw.Close()
    }()

    start := time.Now()
    if err := PlayMP3Stream(pr); err != nil {
        t.Fatalf("PlayMP3Stream 失败: %v", err)
    }
    elapsed := time.Since(start)
    // fixture 约 4.6s；超过 20s 视为挂起
    if elapsed < 3*time.Second || elapsed > 20*time.Second {
        t.Fatalf("播放时长异常: %v（期望约 4.6s）", elapsed)
    }
    t.Logf("PlayMP3Stream 完成，耗时 %v", elapsed)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -tags integration ./player/ -run TestPlayMP3Stream -count=1 -v`
Expected: 编译失败 `undefined: PlayMP3Stream`

- [ ] **Step 3: 实现**

```go
// pcmFirstReadLogger 包装解码器，在首次取到 PCM 时打点（首声时刻）。
type pcmFirstReadLogger struct {
    r     io.Reader
    first bool
}

func (l *pcmFirstReadLogger) Read(p []byte) (int, error) {
    n, err := l.r.Read(p)
    if n > 0 && l.first {
        l.first = false
        timing.Logf("player: 首个 PCM 块被取走（首声时刻，%d 字节）", n)
    }
    return n, err
}

// PlayMP3Stream 流式解码并播放 MP3，阻塞到流结束或出错。
// 数据未到达时 Read 阻塞（背压），不会提前返回。
func PlayMP3Stream(r io.Reader) error {
    timing.Logf("player: 等待 Edge 首个音频块")
    dec, err := newMP3StreamDecoder(r)
    if err != nil {
        return err
    }
    // go-mp3 恒输出 16bit 双声道（v0.3.4 无 Channels 方法，见编译报错实测）
    ctx, err := ensureAudioContext(dec.SampleRate(), 2)
    if err != nil {
        return fmt.Errorf("音频设备初始化失败: %w", err)
    }
    timing.Logf("player: MP3 解码器就绪 sr=%d ch=2", dec.SampleRate())
    p := ctx.NewPlayer(&pcmFirstReadLogger{r: dec, first: true})
    p.Play()
    timing.Logf("player: 播放器已启动（首声应在此后立即出现）")

    // 流式时长未知：用较大超时兜底（长文本可达数分钟），防设备卡死。
    deadline := time.Now().Add(30 * time.Minute)
    for p.IsPlaying() {
        if time.Now().After(deadline) {
            p.Close()
            return fmt.Errorf("播放超时（超过 30 分钟）")
        }
        time.Sleep(50 * time.Millisecond)
    }
    if err := p.Err(); err != nil {
        p.Close()
        return fmt.Errorf("播放失败: %w", err)
    }
    p.Close()
    return nil
}
```

- [ ] **Step 4: 运行确认通过（会真出声约 4.6s）**

Run: `go test -tags integration ./player/ -run TestPlayMP3Stream -count=1 -v`
Expected: PASS，耗时约 4.6s（3s-20s 区间）

- [ ] **Step 5: 提交**

```bash
git add player/audioPlayer.go player/mp3_play_integration_test.go
git commit -m "feat(player): 新增 PlayMP3Stream 边收边播（io.Pipe + go-mp3 + oto）"
```

---

### Task 4: tts 层 Edge 音频转发

**Files:**
- Modify: `tts/edge.go`（`Stream` 之后新增）
- Test: `tts/edge_stream_test.go`（新建）

**Interfaces:**
- Consumes: `(*EdgeTTS).Stream(ctx)`（Task 1 修复后）、`newEdgeWithConfig(text, cfg)`、`TTSChunk{Type,Data}`
- Produces:
  - `forwardEdgeAudio(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, w io.Writer) error`
  - `(e *EdgeTTS) SynthesizeStream(ctx context.Context, text string, w io.Writer) error`

- [ ] **Step 1: 写失败测试**

```go
// TestForwardEdgeAudioWritesOnlyAudio：只透传 audio 块，顺序不变。
func TestForwardEdgeAudioWritesOnlyAudio(t *testing.T) {
    chunks := make(chan *TTSChunk, 5)
    errs := make(chan error, 1)
    chunks <- &TTSChunk{Type: "WordBoundary"}
    chunks <- &TTSChunk{Type: "audio", Data: []byte("AAA")}
    chunks <- &TTSChunk{Type: "SentenceBoundary"}
    chunks <- &TTSChunk{Type: "audio", Data: []byte("BB")}
    chunks <- &TTSChunk{Type: "audio", Data: []byte("CCC")}
    close(chunks)
    close(errs)

    var buf bytes.Buffer
    if err := forwardEdgeAudio(context.Background(), chunks, errs, &buf); err != nil {
        t.Fatalf("forwardEdgeAudio 失败: %v", err)
    }
    if got := buf.String(); got != "AAABBCCC" {
        t.Errorf("输出 = %q, 期望 %q", got, "AAABBCCC")
    }
}

// TestForwardEdgeAudioPropagatesError：流中途错误必须返回，且不挂起。
func TestForwardEdgeAudioPropagatesError(t *testing.T) {
    chunks := make(chan *TTSChunk, 1)
    errs := make(chan error, 1)
    errs <- errors.New("websocket: close 1007 SSML is invalid")
    close(errs)
    close(chunks)

    var buf bytes.Buffer
    err := forwardEdgeAudio(context.Background(), chunks, errs, &buf)
    if err == nil || !strings.Contains(err.Error(), "SSML is invalid") {
        t.Fatalf("期望透出原始错误，实际: %v", err)
    }
}

// TestForwardEdgeAudioRejectsEmpty：零音频必须报错（不能“成功静默”）。
func TestForwardEdgeAudioRejectsEmpty(t *testing.T) {
    chunks := make(chan *TTSChunk)
    errs := make(chan error)
    close(chunks)
    close(errs)
    if err := forwardEdgeAudio(context.Background(), chunks, errs, io.Discard); err == nil {
        t.Fatal("零音频应返回错误")
    }
}

// TestSynthesizeStreamRejectsBlankText：空白文本在联网前就报错。
func TestSynthesizeStreamRejectsBlankText(t *testing.T) {
    e := NewEdge()
    if err := e.SynthesizeStream(context.Background(), "   ", io.Discard); err == nil {
        t.Fatal("空白文本应返回错误")
    }
}

// TestEdgeSynthesizeStreamMultiSegmentReal：真实网络下 SynthesizeStream 输出包含全部段。
// 默认跳过，EDGETTS_NET_TEST=1 才联网。
func TestEdgeSynthesizeStreamMultiSegmentReal(t *testing.T) {
    if os.Getenv("EDGETTS_NET_TEST") != "1" {
        t.Skip("设置 EDGETTS_NET_TEST=1 才执行真实网络测试")
    }
    text := strings.Repeat("这是用于验证流式输出完整性的中文测试句子，需要足够长以触发分段。", 61)
    cfg := *NewEdge().Config
    cfg.ContinuousPlayback = false
    inst, err := newEdgeWithConfig(text, &cfg)
    if err != nil {
        t.Fatal(err)
    }
    if len(inst.Texts) < 2 {
        t.Fatalf("文本未切成多段：%d", len(inst.Texts))
    }

    var whole bytes.Buffer
    if err := inst.SynthesizeStream(context.Background(), text, &whole); err != nil {
        t.Fatalf("SynthesizeStream 失败: %v", err)
    }

    var first bytes.Buffer
    if err := inst.SynthesizeStream(context.Background(), string(inst.Texts[0]), &first); err != nil {
        t.Fatalf("单段 SynthesizeStream 失败: %v", err)
    }
    t.Logf("整段 MP3 = %d 字节，仅第 1 段 = %d 字节", whole.Len(), first.Len())
    if whole.Len() <= first.Len() {
        t.Fatalf("流式输出只包含第 1 段：%d <= %d", whole.Len(), first.Len())
    }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./tts/ -run "TestForwardEdgeAudio|TestSynthesizeStreamRejects" -count=1`
Expected: 编译失败 `undefined: forwardEdgeAudio`、`SynthesizeStream` 不存在

- [ ] **Step 3: 实现**

先在 `tts/edge.go` 的 import 块加入 `"io"`（当前没有该 import）。然后实现：

```go
// forwardEdgeAudio 把 Edge 音频块按序写入 w；零音频或流错误时返回错误。
func forwardEdgeAudio(ctx context.Context, chunks <-chan *TTSChunk, errs <-chan error, w io.Writer) error {
    written := 0
    for {
        select {
        case chunk, ok := <-chunks:
            if !ok {
                select {
                case err := <-errs:
                    if err != nil {
                        return err
                    }
                default:
                }
                if written == 0 {
                    return fmt.Errorf("Edge 未返回任何音频数据")
                }
                return nil
            }
            if chunk == nil || chunk.Type != "audio" || len(chunk.Data) == 0 {
                continue
            }
            n, err := w.Write(chunk.Data)
            if err != nil {
                return fmt.Errorf("写入音频流失败: %w", err)
            }
            if written == 0 {
                timing.Logf("tts: Edge 首个音频块（%d 字节）", n)
            }
            written += n
        case err, ok := <-errs:
            if !ok {
                errs = nil // 正常关闭：继续读 chunks
                continue
            }
            if err != nil {
                return err
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// SynthesizeStream 流式合成 Edge 文本：把音频块直接写入 w。
// 播放模式固定标准分段（ContinuousPlayback=false）：实测 true 对 >4096 字节
// 文本只发第一段并报 websocket close 1007 SSML is invalid。
func (e *EdgeTTS) SynthesizeStream(ctx context.Context, text string, w io.Writer) error {
    if strings.TrimSpace(text) == "" {
        return fmt.Errorf("待合成文本为空")
    }
    cfg := *e.Config
    cfg.ContinuousPlayback = false
    inst, err := newEdgeWithConfig(text, &cfg)
    if err != nil {
        return fmt.Errorf("构造 Edge 合成实例失败: %w", err)
    }
    timing.Logf("tts: Edge 切分为 %d 段", len(inst.Texts))
    chunks, errs := inst.Stream(ctx)
    return forwardEdgeAudio(ctx, chunks, errs, w)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./tts/ -run "TestForwardEdgeAudio|TestSynthesizeStreamRejects" -count=1 -v`
Expected: 4 个测试全部 PASS

- [ ] **Step 5: 真实网络验证（约 10s）**

Run: `EDGETTS_NET_TEST=1 go test ./tts/ -run TestEdgeSynthesizeStreamMultiSegmentReal -count=1 -v`
Expected: PASS，日志 `整段 MP3 > 仅第 1 段`

- [ ] **Step 6: 确认设计文档与实现签名一致**

Run: `grep -n "SynthesizeStream" docs/superpowers/specs/2026-11-02-edge-streaming-design.md tts/edge.go`
Expected: 两处签名均为 `SynthesizeStream(ctx context.Context, text string, w io.Writer)`

- [ ] **Step 7: 提交**

```bash
git add tts/edge.go tts/edge_stream_test.go docs/superpowers/specs/2026-11-02-edge-streaming-design.md
git commit -m "feat(edge): 新增 SynthesizeStream，把 Edge 音频块写入 io.Writer"
```

---

### Task 5: main.go 接线（io.Pipe 边收边播）

**Files:**
- Modify: `main.go:342-360`（播放分支）、新增 `streamPlayEdge`

**Interfaces:**
- Consumes: `(*tts.EdgeTTS).SynthesizeStream(ctx, text, w)`、`player.PlayMP3Stream(r)`
- Produces: `streamPlayEdge(et *tts.EdgeTTS, text string) error`

- [ ] **Step 1: 改播放分支**

```go
// 播放模式：豆包与 Edge 都走流式（边合成边播）
switch e := ttsEngine.(type) {
case *tts.DoubaoTTS:
    if err := streamPlayDoubao(e, text); err != nil {
        failf("播放失败：%v", err)
    }
    return
case *tts.EdgeTTS:
    if err := streamPlayEdge(e, text); err != nil {
        failf("播放失败：%v", err)
    }
    return
}
```

`GetAudioFile` 默认分支保留（供未来新增引擎使用）。

- [ ] **Step 2: 实现 `streamPlayEdge`（放在 `streamPlayDoubao` 之后）**

```go
// streamPlayEdge 流式播放 Edge TTS：合成方把音频块写入 pipe，播放方边读边解码。
// 合成出错时用 CloseWithError 让播放端立刻收到错误，避免永久阻塞。
func streamPlayEdge(et *tts.EdgeTTS, text string) error {
    timing.Logf("main: Edge 流式合成（文本 %d 字节）", len(text))
    pr, pw := io.Pipe()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    synthErr := make(chan error, 1)
    go func() {
        err := et.SynthesizeStream(ctx, text, pw)
        if err != nil {
            _ = pw.CloseWithError(err)
        } else {
            _ = pw.Close()
        }
        synthErr <- err
    }()

    playErr := player.PlayMP3Stream(pr)
    cancel()
    _ = pr.CloseWithError(playErr) // 播放提前结束时解除合成方 Write 阻塞
    sErr := <-synthErr
    if playErr != nil {
        if sErr != nil {
            return sErr
        }
        return playErr
    }
    return sErr
}
```

新增 import：`"context"`（`io` 已存在）。

- [ ] **Step 3: 编译 + 单元测试**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全部通过（无 FAIL）

- [ ] **Step 4: 手工冒烟：短文本（真出声，约 1-2s）**

Run: `go build -o edgetts . && EDGETTS_TIMING=1 timeout 30 ./edgetts -engine edge "流式冒烟测试。"`
Expected: stderr 出现 `tts: Edge 首个音频块`、`player: 首个 PCM 块被取走（首声时刻`，无错误输出；`echo $?` 为 0

- [ ] **Step 5: 手工验证 `-o` 回归（Review Focus 1）**

Run: `go build -o edgetts . && timeout 60 ./edgetts -engine edge -o /tmp/opencode/edge_o.mp3 "输出文件回归测试。" && ls -l /tmp/opencode/edge_o.mp3 && go test ./player/ -run TestDecodeAudio_mp3 -count=1`
Expected: stdout 打印路径；文件 > 10KB；decoder 测试通过

- [ ] **Step 6: 提交**

```bash
git add main.go
git commit -m "feat(main): Edge 播放改为 io.Pipe 边收边播"
```

---

### Task 6: 清理探针 + 详细实测 + 文档

**Files:**
- Delete: `tts/edge_probe_test.go`、`tts/edge_split_probe_test.go`、`player/mp3_stream_probe_test.go`
- Modify: `docs/superpowers/specs/2026-11-02-edge-streaming-design.md`
- Create: `docs/superpowers/2026-11-02-edge-streaming-test.md`
- Modify: `go.mod`（`go mod tidy`）

**Interfaces:**
- Consumes: Task 1-5 的全部产物
- Produces: 可复现的测试结论

- [ ] **Step 1: 删除临时探针并跑全量测试**

```bash
rm -f tts/edge_probe_test.go tts/edge_split_probe_test.go player/mp3_stream_probe_test.go
gofmt -l main.go tts/ player/ timing/ | grep -v secMsGecV2 || echo "(已格式化)"
go build ./... && go vet ./... && go test ./...
go test -tags integration ./player/ -count=1
EDGETTS_NET_TEST=1 go test ./tts/ -run "TestEdgeMultiSegmentReal|TestEdgeSynthesizeStreamMultiSegmentReal" -count=1
```

Expected: 无格式问题；build/vet 退出码 0；所有包 `ok`；集成与网络测试 PASS

- [ ] **Step 2: Edge 长文本首声 20 次（目标全部 ≤2s）**

准备约 740 字（~2200 字节）测试文本（不存在则生成同量级文本）：

```bash
test -f /tmp/opencode/page_text.txt || python3 -c 'print("这是一段用于测量长文本首声延迟的中文测试文字，内容需要足够长。" * 74)' > /tmp/opencode/page_text.txt
text=$(cat /tmp/opencode/page_text.txt)
for i in $(seq 1 20); do
  out=$(EDGETTS_TIMING=1 timeout 20 ./edgetts -engine edge "$text" 2>&1 | grep "首个 PCM" | head -1)
  echo "第${i}次: $(echo "$out" | grep -oE '[0-9]+ms' | head -1)"
done
```

Expected: 20 行全部 ≤2000ms（实测基线：首包 1.09s）。

- [ ] **Step 3: 短文本 10 次**

```bash
for i in $(seq 1 10); do
  EDGETTS_TIMING=1 timeout 20 ./edgetts -engine edge "短文本首声测试。" 2>&1 | grep "首个 PCM"
done
```

Expected: 10 次均有首声日志，耗时不高于长文本档。

- [ ] **Step 4: 多段文本（>4096 字节）完整播放（Review Focus 4）**

构造约 6100 字节中文，用 `-r +100%` 缩短播放时长：

```bash
python3 - <<'PY' > /tmp/opencode/edge_multi.txt
print("这是用于验证多段文本完整播放的中文句子，需要足够长以触发 4096 字节切分。" * 68)
PY
wc -c /tmp/opencode/edge_multi.txt   # 期望 > 4096
set -o pipefail
time EDGETTS_TIMING=1 timeout 300 ./edgetts -engine edge -r +100% "$(cat /tmp/opencode/edge_multi.txt)" 2>&1 | grep -E "切分为|首个音频块|首个 PCM"
echo "exit=${PIPESTATUS[0]}"
```

Expected: `切分为 N 段`（N≥2）；首个 PCM ≤2000ms；`exit=0`；无 `SSML is invalid`、无解码错误。另用 Task 4 的网络测试证明整段字节数 == 各段之和（不丢段）。

- [ ] **Step 5: speechd 路径 5 次（Okular 同链路）**

确认 `~/.config/speech-dispatcher/modules/edgetts.conf` 指向 `~/.local/bin/edgetts`，然后：

```bash
cp edgetts /home/jie/.local/bin/edgetts
systemctl --user restart speech-dispatcher && sleep 2
python3 - <<'PY'
import subprocess, time, signal, re
def playing():
    out = subprocess.run(["pactl","list","sink-inputs"], capture_output=True, text=True).stdout
    ids = re.findall(r'Sink Input #(\d+)', out)
    corked = re.findall(r'Corked: (yes|no)', out)
    return {i for i,c in zip(ids,corked) if c=="no"}
text = open("/tmp/opencode/page_text.txt").read()
for n in range(5):
    before = playing(); t0 = time.monotonic()
    p = subprocess.Popen(["spd-say", text], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    first = None
    for _ in range(600):
        if playing() - before:
            first = (time.monotonic()-t0)*1000; break
        time.sleep(0.02)
    print(f"speechd 第{n+1}次首声 @ {first:.0f}ms" if first else f"第{n+1}次未检测")
    subprocess.run(["spd-say","-S"], capture_output=True)
    p.send_signal(signal.SIGTERM); p.wait(); time.sleep(0.4)
PY
```

Expected: 5 次全部 ≤2000ms（豆包同链路基线 1112-1181ms）。

- [ ] **Step 6: 豆包回归 20 次（Review Focus 5）**

```bash
text=$(cat /tmp/opencode/page_text.txt)
for i in $(seq 1 20); do
  out=$(EDGETTS_TIMING=1 timeout 20 ./edgetts -engine doubao "$text" 2>&1 | grep "首个 PCM" | head -1)
  echo "第${i}次: $(echo "$out" | grep -oE '[0-9]+ms' | head -1)"
done
```

Expected: 20 次全部 ≤2000ms（基线 931-1301ms），无 `采样率已固定` 错误。

- [ ] **Step 7: `go mod tidy`**

```bash
go mod tidy && git diff --stat go.mod go.sum && go build ./... && go test ./...
```

Expected: oto/go-mp3/pion/opus 不再带 `// indirect`；构建与测试仍通过。

- [ ] **Step 8: 写实测记录并更新设计文档**

新建 `docs/superpowers/2026-11-02-edge-streaming-test.md`，按 Step 1-6 的**原始输出**填表（场景 / 次数 / 首声分布 / 是否 ≤2s / 错误原文），并在 spec 末尾追加「## 实测结果（2026-11-02）」小结（含 Task 1 缺陷修复前后对比）。

- [ ] **Step 9: 提交**

```bash
git add -A tts/ player/ main.go go.mod go.sum docs/superpowers/
git commit -m "test(edge): 流式播放详细实测记录，清理临时探针"
```

---

## 验收标准

- Edge 长文本（740 字）20 次首声全部 ≤2s。
- 多段文本（>4096 字节）完整合成（整段字节 == 各段之和）且正常播放。
- 短文本、speechd 路径均正常出声。
- 豆包 20 次首声无回归，且与 Edge 共用 oto context 不报采样率错误。
- `-o` 行为不变。
- `go build` / `go vet` / `go test ./...` / `go test -tags integration ./player/` 全绿。
