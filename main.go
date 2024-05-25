/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:main.go
 * 修改日期:2024/05/24 13:59:50
 * 作者:kerojiang
 */

package main

import (
	"fmt"
	"os"

	"edgetts-speechd/player"
	"edgetts-speechd/tts"
)

func main() {

	// lang := flag.String("$LANG", "", "语言")
	// voice := flag.String("$VOICE", "", "语音")
	args := os.Args[1:]

	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println("直接输入要播报的文字,默认不生成语音文件\n -f 生成语音文件不播报\n -p 生成语音文件并播报")
	} else {
		text := args[len(args)-1]

		if len(args) > 1 {

			if args[0] == "-f" {
				f, err := tts.GetTTSAudioFile(text)
				if err != nil {
					fmt.Println(err)
				}
				fmt.Println(f)
			}
			if args[0] == "-p" {
				f, err := tts.GetTTSAudioFile(text)
				if err != nil {
					fmt.Println(err)
				}
				fmt.Println(f)
				err = player.PlayFile(f)
				if err != nil {
					fmt.Println(err)
				}
			}

		} else {
			s, err := tts.GetTTSAudioStream(text)
			if err != nil {
				fmt.Println(err)
			}
			err = player.PlayStream(s)
			if err != nil {
				fmt.Println(err)
			}
		}
	}

}
