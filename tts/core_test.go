/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:core_test.go
 * 修改日期:2024/05/24 16:59:05
 * 作者:kerojiang
 */

package tts

import (
	"fmt"
	"testing"

	"edgetts-speechd/player"
)

func TestGetTTSAudioFile(t *testing.T) {
	text := "你好啊,今天天气怎么样"
	f, err := GetTTSAudioFile(text)
	if err != nil {
		t.Error(err)
	}
	fmt.Println(f)

	err = player.PlayFile(f)
	if err != nil {
		t.Error(err)
	}
}

func TestGetTTSAudioStream(t *testing.T) {
	text := "你好啊,今天天气怎么样"
	s, err := GetTTSAudioStream(text)
	if err != nil {
		t.Error(err)
	}
	// fmt.Println(f)

	err = player.PlayStream(s)
	if err != nil {
		t.Error(err)
	}
}
