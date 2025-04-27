/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:core.go
 * 修改日期:2024/05/24 16:57:59
 * 作者:kerojiang
 */

package tts

import (
	"context"

	"github.com/difyz9/edge-tts-go/pkg/communicate"
)

// GetTTSAudioFile
//
//	@Description: 获取tts音频文件
//	@param text
//	@return string
//	@return error
func GetTTSAudioFile(text string, voice string, proxy string) (string, error) {

	if voice == "" {
		voice = "zh-CN-XiaoxiaoNeural"
	}

	comm, err := communicate.NewCommunicate(text, voice, "+0%", "+0%", "+0Hz", proxy, 10, 60)

	if err != nil {
		return "", err
	}

	filePath := "/tmp/tts.mp3"
	ctx := context.Background()

	err = comm.Save(ctx, filePath, "")

	if err != nil {
		return "", err
	}

	return filePath, nil

}
