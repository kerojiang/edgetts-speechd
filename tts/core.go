/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:core.go
 * 修改日期:2024/05/24 16:57:59
 * 作者:kerojiang
 */

package tts

import (
	"fmt"
	"os"

	"edgetts-speechd/tts/edge"
)

func GetTTSAudioStream(text string) ([]byte, error) {
	commu, err := edge.NewCommunicate(text)
	if err != nil {
		return nil, err
	}
	defer commu.CloseOutput()

	streamMap, err := commu.Stream()
	if err != nil {
		return nil, err
	}

	dataCount := 0
	audioData := make([][][]byte, commu.AudioDataIndex)

	for i := range streamMap {
		if _, ok := i["end"]; ok {
			dataCount++
			if dataCount == commu.AudioDataIndex {
				break
			}
		}
		t, ok := i["type"]
		if ok && t == "audio" {
			data := i["data"].(edge.AudioData)
			audioData[data.Index] = append(audioData[data.Index], data.Data)
		}
		e, ok := i["error"]
		if ok {
			return nil, fmt.Errorf("error:%q", e)
		}
	}

	// 写入音频流到本地
	var audioBytes []byte
	for _, v := range audioData {
		for _, data := range v {
			audioBytes = append(audioBytes, data...)
		}
	}

	return audioBytes, nil
}

// GetTTSAudioFile
//
//	@Description: 获取tts音频文件
//	@param text
//	@return string
//	@return error
func GetTTSAudioFile(text string) (string, error) {

	audioBytes, err := GetTTSAudioStream(text)

	fileName, err := WriteTmpFile(audioBytes)
	if err != nil {

		return "", err
	}
	return fileName, nil
}

func WriteTmpFile(audioBytes []byte) (string, error) {

	f, err := os.CreateTemp("", "tts*.mp3")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = f.Write(audioBytes)
	if err != nil {
		return "", err
	}

	return f.Name(), nil
}
