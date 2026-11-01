/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edge-tts
 * 文件名称:core.go
 * 修改日期:2026/02/21 19:38:07
 * 作者:kerojiang
 */

package tts

import (
	"os"
	"strings"
)

// COre defines the core TTS interface
// Note: There was a spelling error in the original interface name
type Core interface {
	// GetAudioFile converts text to audio and saves to a file
	// Returns the file path and any error encountered
	GetAudioFile(text string) (string, error)

	// GetAudioFileTo converts text to audio and saves to the specified file
	// outputPath: the file path to save the audio
	// Returns the actual file path and any error encountered
	GetAudioFileTo(text string, outputPath string) (string, error)

	// GetAudioBytes converts text to audio and returns as bytes
	// Returns the audio data as bytes and any error encountered
	GetAudioBytes(text string) ([]byte, error)
}

// Default constants
var (
	DEFAULT_FILE   = strings.Join([]string{os.TempDir(), "ttsfile.mp3"}, string(os.PathSeparator))
	DEFAULT_VOICE  = "zh-CN-XiaoxiaoNeural"
	DEFAULT_RATE   = "+0%"
	DEFAULT_PITCH  = "+0Hz"
	DEFAULT_VOLUME = "+0%"
)

// Ensure EdgeTTS implements the Core interface
var _ Core = (*EdgeTTS)(nil)