/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:main.go
 * 修改日期:2026/03/10
 * 作者:kerojiang
 */

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"edgetts-speechd/player"
	"edgetts-speechd/tts"
)

// cleanText 清理文本中的特殊符号
func cleanText(text string) string {
	// 移除控制字符和一些特殊符号，保留常用标点
	var cleaned []rune
	for _, r := range text {
		// 保留字母、数字、中文字符、标点符号和空白字符
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Scripts["Han"], r) ||
			unicode.IsPunct(r) ||
			strings.ContainsRune(" \t\n\r!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			cleaned = append(cleaned, r)
		}
	}

	// 规范化空白字符
	result := strings.Join(strings.Fields(string(cleaned)), " ")
	return result
}

// parseDoubaoRate 把 -r 参数解析为豆包 speech_rate。
// 支持百分比写法（+10% → 1.1）与直接倍数写法（1.1）。
func parseDoubaoRate(input string) (float64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 1, nil
	}

	if strings.HasSuffix(input, "%") {
		value, err := strconv.ParseFloat(strings.TrimSuffix(input, "%"), 64)
		if err != nil {
			return 0, fmt.Errorf("无效的语速参数：%s", input)
		}
		return 1 + value/100, nil
	}

	value, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的语速参数：%s", input)
	}
	return value, nil
}

// parseDoubaoPitch 把 -p 参数解析为豆包 pitch。
// 支持直接数值（-3）与带 Hz 单位（-50Hz）两种写法，单位只做剥离不做换算。
func parseDoubaoPitch(input string) (float64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, nil
	}

	input = strings.TrimSuffix(input, "Hz")
	input = strings.TrimSuffix(input, "hz")

	value, err := strconv.ParseFloat(input, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的音调参数：%s", input)
	}
	return value, nil
}

// programName 返回当前可执行文件名，用于帮助信息。
func programName() string {
	if len(os.Args) > 0 && os.Args[0] != "" {
		return filepath.Base(os.Args[0])
	}
	return "edgetts"
}

// listDeps 是列出语音所需的依赖，便于测试替换。
type listDeps struct {
	clearDoubaoCookie func() error
	listDoubaoVoices  func(string, []tts.DoubaoTab) ([]tts.DoubaoVoice, error)
	listEdgeVoices    func() ([]tts.Voice, error)
}

// defaultListDeps 返回真实依赖。
func defaultListDeps() listDeps {
	return listDeps{
		clearDoubaoCookie: tts.ClearDoubaoCookie,
		listDoubaoVoices:  tts.ListDoubaoVoicesWithCookie,
		listEdgeVoices:    tts.ListVoices,
	}
}

// runList 按引擎列出可用语音；返回错误时由调用方决定退出码。
func runList(stdout, stderr io.Writer, engine, tab, cookie string, refresh bool, deps listDeps) error {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "doubao":
		if refresh {
			if err := deps.clearDoubaoCookie(); err != nil {
				return fmt.Errorf("清空豆包 Cookie 缓存失败：%w", err)
			}
			fmt.Fprintln(stderr, "[INFO] 已清空本地豆包 Cookie 缓存")
		}

		tabs, err := tts.SelectDoubaoTabs(tab)
		if err != nil {
			return err
		}

		voices, err := deps.listDoubaoVoices(cookie, tabs)
		if err != nil {
			return err
		}

		fmt.Fprintln(stdout, "豆包可用的语音列表:")
		for _, v := range voices {
			fmt.Fprintf(stdout, "%s (%s) - %s\n", v.StyleID, strings.Join(v.Tabs, ","), v.Name)
		}

	case "edge", "":
		voices, err := deps.listEdgeVoices()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Edge TTS 可用的语音列表:")
		for _, v := range voices {
			fmt.Fprintf(stdout, "%s (%s) - %s\n", v.ShortName, v.Locale, v.FriendlyName)
		}

	default:
		return fmt.Errorf("不支持的引擎：%s（可选 edge 或 doubao）", engine)
	}

	return nil
}

// printHelp 打印帮助信息
func printHelp() {
	name := programName()
	fmt.Printf("用法：%s [选项] <文本>\n", name)
	fmt.Println("\n直接输入要播报的文字")
	fmt.Println("\n常用选项:")
	fmt.Println("  -engine ENGINE   选择 TTS 引擎：edge（默认）或 doubao")
	fmt.Println("  -v VOICE         指定语音 (Edge TTS)")
	fmt.Println("  -l               列出当前引擎所有可用语音")
	fmt.Println("  -r RATE          指定语速 (Edge: +0%/-5%；豆包: +10%/1.1)")
	fmt.Println("  -vol VOLUME      指定音量 (volume，格式:+0% 或 -10%，仅 Edge)")
	fmt.Println("  -p PITCH         指定音调 (Edge: +0Hz/-50Hz；豆包: -3)")
	fmt.Println("  -o FILE          只生成音频文件不播放，指定输出路径 (如: output.mp3)")
	fmt.Println("  --no-split       禁用句子分割功能")
	fmt.Println("  --continuous     启用连续播放模式 (默认启用)")
	fmt.Println("  --no-continuous  禁用连续播放模式")
	fmt.Println("\n豆包选项:")
	fmt.Println("  -dv VOICE        指定豆包说话人 (默认: " + tts.DoubaoDefaultVoice + ")")
	fmt.Println("  -doubao-cookie C 直接指定豆包登录 Cookie")
	fmt.Println("  -doubao-refresh  强制重新从 Edge 提取豆包 Cookie")
	fmt.Println("  -dv-tab TAB      列出豆包音色时限定分类 (rec/female/male/characters/accent)")
	fmt.Println("\n示例:")
	fmt.Printf("  %s \"你好，这是测试文本\"\n", name)
	fmt.Printf("  %s -v zh-CN-XiaoxiaoNeural \"你好\"\n", name)
	fmt.Printf("  %s -o output.mp3 \"你好，只生成音频文件不播放\"\n", name)
	fmt.Printf("  %s -engine doubao \"你好，这是豆包语音\"\n", name)
	fmt.Printf("  %s -l\n", name)
	fmt.Printf("  %s -engine doubao -l\n", name)
	fmt.Printf("  %s -engine doubao -l -dv-tab female\n", name)
}

func main() {
	// 定义命令行参数
	engine := flag.String("engine", "edge", "选择 TTS 引擎：edge 或 doubao")
	voice := flag.String("v", "", "指定语音 (Edge TTS)")
	rate := flag.String("r", "", "指定语速 (格式:+0% 或 -5%)")
	volume := flag.String("vol", "", "指定音量 (格式:+0% 或 -10%)")
	pitch := flag.String("p", "", "指定音调 (格式:+0Hz 或 -50Hz)")
	outputFlag := flag.String("o", "", "只生成音频文件不播放，指定输出路径")
	noSplit := flag.Bool("no-split", false, "禁用句子分割功能")
	continuous := flag.Bool("continuous", true, "启用连续播放模式")
	noContinuous := flag.Bool("no-continuous", false, "禁用连续播放模式")
	listVoices := flag.Bool("l", false, "列出当前引擎所有可用语音")
	doubaoVoice := flag.String("dv", "", "豆包说话人")
	doubaoCookie := flag.String("doubao-cookie", "", "直接指定豆包登录 Cookie")
	doubaoRefresh := flag.Bool("doubao-refresh", false, "强制重新从 Edge 提取豆包 Cookie")
	doubaoTab := flag.String("dv-tab", "", "列出豆包音色时限定分类：rec/female/male/characters/accent")
	showHelp := flag.Bool("h", false, "显示帮助信息")
	showHelpLong := flag.Bool("help", false, "显示帮助信息")

	// 禁用 flag 的默认错误输出，我们自己处理
	flag.Usage = func() {}

	// 解析参数
	flag.Parse()

	// 处理帮助
	if *showHelp || *showHelpLong {
		printHelp()
		return
	}

	// 处理 -l 参数（按引擎列出可用语音）
	if *listVoices {
		if err := runList(os.Stdout, os.Stderr, *engine, *doubaoTab, *doubaoCookie, *doubaoRefresh, defaultListDeps()); err != nil {
			fmt.Printf("获取语音列表失败：%v\n", err)
			os.Exit(1)
		}
		return
	}

	// 获取剩余的非 flag 参数作为文本
	args := flag.Args()
	text := cleanText(strings.Join(args, " "))

	// 如果没有文本内容，则显示帮助信息
	if text == "" {
		fmt.Println("请输入要播报的文字内容")
		printHelp()
		return
	}

	// 按引擎类型构造 TTS 实例
	var ttsEngine tts.Core
	var err error

	switch strings.ToLower(strings.TrimSpace(*engine)) {
	case "edge":
		fmt.Fprintln(os.Stderr, "[INFO] 使用 Edge TTS 引擎")
		edgeTTS := tts.NewEdge()

		// 设置各种参数（如果提供了的话）
		if *voice != "" {
			edgeTTS.Config.Voice = *voice
		}
		if *rate != "" {
			edgeTTS.Config.Rate = *rate
		}
		if *volume != "" {
			edgeTTS.Config.Volume = *volume
		}
		if *pitch != "" {
			edgeTTS.Config.Pitch = *pitch
		}
		edgeTTS.Config.SentenceSplit = !*noSplit
		edgeTTS.Config.ContinuousPlayback = *continuous && !*noContinuous

		// 验证配置参数
		if err := edgeTTS.Config.Validate(); err != nil {
			fmt.Printf("参数验证失败：%v\n", err)
			return
		}

		ttsEngine = edgeTTS

	case "doubao":
		fmt.Fprintln(os.Stderr, "[INFO] 使用豆包 TTS 引擎")
		doubaoTTS := tts.NewDoubao()

		if *doubaoVoice != "" {
			doubaoTTS.Config.Voice = *doubaoVoice
		}
		if *doubaoCookie != "" {
			doubaoTTS.Config.Cookie = *doubaoCookie
		}
		if *rate != "" {
			parsed, err := parseDoubaoRate(*rate)
			if err != nil {
				fmt.Printf("参数验证失败：%v\n", err)
				return
			}
			doubaoTTS.Config.Rate = parsed
		}
		if *pitch != "" {
			parsed, err := parseDoubaoPitch(*pitch)
			if err != nil {
				fmt.Printf("参数验证失败：%v\n", err)
				return
			}
			doubaoTTS.Config.Pitch = parsed
		}
		if *volume != "" {
			fmt.Fprintln(os.Stderr, "[WARN] 豆包引擎不支持音量参数，已忽略 -vol")
		}

		if *doubaoRefresh {
			if err := tts.ClearDoubaoCookie(); err != nil {
				fmt.Printf("清空豆包 Cookie 缓存失败：%v\n", err)
				return
			}
			fmt.Fprintln(os.Stderr, "[INFO] 已清空本地豆包 Cookie 缓存")
		}

		if err := doubaoTTS.Config.Validate(); err != nil {
			fmt.Printf("参数验证失败：%v\n", err)
			return
		}

		ttsEngine = doubaoTTS

	default:
		fmt.Printf("不支持的引擎：%s（可选 edge 或 doubao）\n", *engine)
		return
	}

	// 生成音频文件
	var audioFile string
	if *outputFlag != "" {
		// 使用指定输出路径，不播放
		audioFile, err = ttsEngine.GetAudioFileTo(text, *outputFlag)
		if err != nil {
			fmt.Printf("生成音频文件失败：%v\n", err)
			return
		}
		fmt.Println(audioFile)
		return
	}

	// 使用默认路径生成并播放
	audioFile, err = ttsEngine.GetAudioFile(text)
	if err != nil {
		fmt.Printf("生成音频文件失败：%v\n", err)
		return
	}
	fmt.Printf("音频文件已生成：%s\n", audioFile)

	// 播放音频文件
	err = player.PlayFile(audioFile)
	if err != nil {
		fmt.Printf("播放音频失败：%v\n", err)
		return
	}
	fmt.Println("音频播放完成")
}
