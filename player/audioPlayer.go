/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:audioPlayer.go
 * 修改日期:2024/05/24 17:12:37
 * 作者:kerojiang
 */

package player

import (
	"bytes"
	"os"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
)

// PlayFile
//
//	@Description: 播放音频文件
//	@param filePath
//	@return error
func PlayFile(filePath string) error {
	f, err := os.ReadFile(filePath)

	if err != nil {
		return err
	}

	return PlayStream(f)
}

// PlayStream
//
//	@Description: 播放音频数据流
//	@param audioBytes
//	@return error
func PlayStream(audioBytes []byte) error {
	reader := bytes.NewReader(audioBytes)
	dec, err := mp3.NewDecoder(reader)
	if err != nil {
		return err
	}

	op := &oto.NewContextOptions{
		SampleRate:   dec.SampleRate(),
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	}

	otoCtx, readyChan, err := oto.NewContext(op)
	if err != nil {
		return err
	}

	<-readyChan

	player := otoCtx.NewPlayer(dec)

	player.Play()

	for player.IsPlaying() {
		time.Sleep(time.Millisecond)
	}

	err = player.Close()
	if err != nil {
		return err
	}
	return nil
}
