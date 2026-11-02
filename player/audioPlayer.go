/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:audioPlayer.go
 * 修改日期:2024/05/24 17:12:37
 * 作者:kerojiang
 *
 * Go 原生音频播放：不再依赖 ffplay / ffmpeg / aplay 外部命令。
 * - 播放后端：github.com/ebitengine/oto/v3（Linux 纯 Go，动态加载 ALSA / 连 PulseAudio）
 * - MP3 解码：github.com/hajimehoshi/go-mp3（纯 Go，恒输出 16bit 双声道）
 * - Ogg Opus 解码：github.com/pion/opus（纯 Go，含 Ogg 容器解析）
 */

package player

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
	"github.com/pion/opus"
	"github.com/pion/opus/pkg/oggreader"

	"edgetts-speechd/timing"
)

// audioFormat 识别出的音频容器格式。
type audioFormat int

const (
	formatUnknown audioFormat = iota
	formatMP3
	formatOpus
)

// decodedAudio 解码后的 PCM 数据（16bit 小端交错）。
type decodedAudio struct {
	pcm        []byte
	sampleRate int
	channels   int
}

// detectFormat 按文件头魔数识别音频格式。
func detectFormat(data []byte) audioFormat {
	// MP3：ID3 标签头，或 0xFFEx 帧同步
	if len(data) >= 3 && data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		return formatMP3
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0 {
		return formatMP3
	}
	// Ogg：本项目豆包引擎只产出 Opus 编码的 Ogg 容器
	if len(data) >= 4 && bytes.Equal(data[0:4], []byte("OggS")) {
		return formatOpus
	}
	return formatUnknown
}

// decodeAudio 把音频字节解码为 PCM。
func decodeAudio(data []byte) (*decodedAudio, error) {
	switch detectFormat(data) {
	case formatMP3:
		return decodeMP3(data)
	case formatOpus:
		return decodeOpus(data)
	default:
		return nil, fmt.Errorf("无法识别的音频格式（仅支持 MP3 或 Ogg Opus）")
	}
}

// decodeMP3 用 go-mp3 解码，输出恒为 16bit 双声道。
func decodeMP3(data []byte) (*decodedAudio, error) {
	dec, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("MP3 解码器创建失败: %w", err)
	}
	var pcm []byte
	buf := make([]byte, 8192)
	for {
		n, rerr := dec.Read(buf)
		if n > 0 {
			pcm = append(pcm, buf[:n]...)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("MP3 解码失败: %w", rerr)
		}
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("MP3 解码未产生音频数据")
	}
	return &decodedAudio{pcm: pcm, sampleRate: dec.SampleRate(), channels: 2}, nil
}

// decodeOpus 用 pion/opus 解码 Ogg Opus，输出 16bit 交错（单声道则上混为立体声）。
func decodeOpus(data []byte) (*decodedAudio, error) {
	or, header, err := oggreader.NewWith(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("Ogg 解析失败: %w", err)
	}
	sr := int(header.SampleRate)
	ch := int(header.Channels)
	dec, err := opus.NewDecoderWithOutput(sr, ch)
	if err != nil {
		return nil, fmt.Errorf("Opus 解码器创建失败: %w", err)
	}
	var pcm []byte
	for {
		packet, _, err := or.ParseNextPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Ogg 读取包失败: %w", err)
		}
		// 跳过 OpusHead / OpusTags 头包（非音频数据）
		if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
			continue
		}
		buf := make([]int16, 5760*ch)
		n, derr := dec.DecodeToInt16(packet, buf)
		if derr != nil {
			continue // 单个包解码失败则跳过（容错）
		}
		if ch == 1 {
			// 单声道上混为立体声（左右复制），与 oto 默认双声道输出一致
			for _, v := range buf[:n] {
				pcm = append(pcm, byte(v), byte(v>>8), byte(v), byte(v>>8))
			}
		} else {
			for _, v := range buf[:n] {
				pcm = append(pcm, byte(v), byte(v>>8))
			}
		}
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("Opus 解码未产生音频数据")
	}
	return &decodedAudio{pcm: pcm, sampleRate: sr, channels: 2}, nil
}

// oto 进程内只允许一个 context（二次调用报 "context is already created"），
// 故用包级单例复用。首次播放的采样率/声道固定，后续须一致。
var (
	audioCtxOnce sync.Once
	audioCtx     *oto.Context
	audioCtxErr  error
	audioCtxSR   int
	audioCtxCh   int
)

// ensureAudioContext 返回进程内唯一 oto context（懒初始化）。
func ensureAudioContext(sr, ch int) (*oto.Context, error) {
	audioCtxOnce.Do(func() {
		audioCtxSR, audioCtxCh = sr, ch
		op := &oto.NewContextOptions{
			SampleRate:   sr,
			ChannelCount: ch,
			Format:       oto.FormatSignedInt16LE,
		}
		audioCtx, _, audioCtxErr = oto.NewContext(op)
	})
	if audioCtxErr != nil {
		return nil, audioCtxErr
	}
	if audioCtxSR != sr || audioCtxCh != ch {
		return nil, fmt.Errorf("音频设备采样率已固定为 %dHz/%dch（当前请求 %dHz/%dch），无法复用",
			audioCtxSR, audioCtxCh, sr, ch)
	}
	return audioCtx, nil
}

// playPCM 用 oto 播放 PCM，阻塞直到播完（带超时保护）。
func playPCM(dec *decodedAudio) error {
	ctx, err := ensureAudioContext(dec.sampleRate, dec.channels)
	if err != nil {
		return fmt.Errorf("音频设备初始化失败: %w", err)
	}
	p := ctx.NewPlayer(bytes.NewReader(dec.pcm))
	p.Play()

	// 估算音频时长，作为超时上限（双倍 + 10s 保护，防止设备卡死）
	estDur := time.Duration(float64(len(dec.pcm))/(float64(dec.sampleRate*dec.channels)*2)*1000) * time.Millisecond
	deadline := time.Now().Add(estDur*2 + 10*time.Second)
	for p.IsPlaying() {
		if time.Now().After(deadline) {
			p.Close()
			return fmt.Errorf("播放超时（超过 %v）", estDur*2+10*time.Second)
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

// PlayFile 播放音频文件（Go 原生，不依赖外部播放器）。
func PlayFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取音频文件失败：%s: %w", filePath, err)
	}
	return PlayStream(data)
}

// PlayStream 播放音频数据流（MP3 或 Ogg Opus），阻塞直到播完。
func PlayStream(audioBytes []byte) error {
	dec, err := decodeAudio(audioBytes)
	if err != nil {
		return err
	}
	return playPCM(dec)
}

// pcmChunkReader 从 channel 逐段读取 Ogg Opus 字节，解码为 PCM 流供 oto 播放。
// 每段是独立 Ogg Opus 流（含 OpusHead），decodeOpus 会跳过头包。
type pcmChunkReader struct {
	chunks  <-chan []byte
	current []byte
	first   bool // 首次 Read 时打点，标记首声时刻
}

// Read 实现 io.Reader：从 channel 取段解码，持续提供 PCM 字节；channel 关闭后返回 io.EOF。
func (r *pcmChunkReader) Read(p []byte) (int, error) {
	for len(r.current) == 0 {
		ogg, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		dec, err := decodeOpus(ogg)
		if err != nil {
			return 0, fmt.Errorf("流式解码失败: %w", err)
		}
		r.current = dec.pcm
	}
	n := copy(p, r.current)
	r.current = r.current[n:]
	if r.first && n > 0 {
		r.first = false
		timing.Logf("player: 首个 PCM 块被取走（首声时刻，%d 字节）", n)
	}
	return n, nil
}

// PlayOggChunks 流式播放多个 Ogg Opus 段（每段独立 Ogg 流）。
// 阻塞直到所有段播完；channel 关闭即结束（合成方负责 close）。
// 首声 ≈ 第一段合成完成（合成方并行合成，首段完成立即送入 channel）。
func PlayOggChunks(chunks <-chan []byte) error {
	timing.Logf("player: 等待首段音频")
	first, ok := <-chunks
	if !ok {
		return fmt.Errorf("无音频数据（合成方未产生任何段）")
	}
	timing.Logf("player: 收到首段（%d 字节），开始解码", len(first))
	dec, err := decodeOpus(first)
	if err != nil {
		return fmt.Errorf("解码首段失败: %w", err)
	}
	ctx, err := ensureAudioContext(dec.sampleRate, dec.channels)
	if err != nil {
		return fmt.Errorf("音频设备初始化失败: %w", err)
	}
	timing.Logf("player: 首段解码完成 sr=%d ch=%d pcm=%d 字节", dec.sampleRate, dec.channels, len(dec.pcm))
	r := &pcmChunkReader{chunks: chunks, current: dec.pcm, first: true}
	p := ctx.NewPlayer(r)
	p.Play()
	timing.Logf("player: 播放器已启动（首声应在此后立即出现）")

	// 流式时长未知：用较大超时兜底（长文本音频可达数分钟），防设备卡死。
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
