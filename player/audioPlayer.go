/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:audioPlayer.go
 * 修改日期:2024/05/24 17:12:37
 * 作者:kerojiang
 */

package player

import (
	"fmt"
	"os"
	"os/exec"
)

// PlayFile 播放音频文件（使用 ffmpeg 支持 MP3 和 AAC）
func PlayFile(filePath string) error {
	// 检查文件是否存在
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("文件不存在：%s", filePath)
	}

	// 使用 ffplay 播放音频（支持 MP3、AAC 等多种格式）
	// -nodisp: 不显示视频
	// -autoexit: 播放完成后自动退出
	// -loglevel quiet: 不显示日志信息
	cmd := exec.Command("ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet", filePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// 如果 ffplay 不可用，尝试使用 ffmpeg 播放到音频设备
		cmd = exec.Command("ffmpeg", "-i", filePath, "-f", "alsa", "default", "-loglevel", "quiet")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		
		if err := cmd.Run(); err != nil {
			// 如果 ffmpeg 也不可用，尝试使用 aplay（仅支持 WAV/PCM）
			cmd = exec.Command("aplay", filePath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("所有播放器都失败：%w", err)
			}
		}
	}

	return nil
}

// PlayStream 播放音频数据流（写入临时文件后播放）
func PlayStream(audioBytes []byte) error {
	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "tts_audio_*.mp3")
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// 写入音频数据
	if _, err := tmpFile.Write(audioBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("写入音频数据失败：%w", err)
	}
	tmpFile.Close()

	// 播放临时文件
	return PlayFile(tmpPath)
}
