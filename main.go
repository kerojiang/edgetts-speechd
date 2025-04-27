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

	voice := ""
	proxy := ""

	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Println("直接输入要播报的文字\n -v 指定的生成语音\n -p 代理设置")
	} else {

		if len(args) > 0 {
			text := args[len(args)-1]

			if len(args) > 1 {

				for _, arg := range args {
					if arg == "-v" {

					}
				}
				for i := 0; i < len(args); i++ {
					if args[i] == "-v" {
						voice = args[i+1]
					}
					if args[i] == "-p" {
						proxy = args[i+1]
					}
				}

			}
			audioFile, err := tts.GetTTSAudioFile(text, voice, proxy)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println(audioFile)
			err = player.PlayFile(audioFile)
			if err != nil {
				fmt.Println(err)
			}
		}

	}

}
