/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubaoConfig.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import (
	"fmt"
	"strings"
)

// 豆包 VoiceGenie TTS 协议常量。
const (
	// DoubaoDefaultVoice 是默认的豆包说话人。
	DoubaoDefaultVoice = "zh_female_wenroutaozi_uranus_bigtts"
	// DoubaoAppKey 是豆包 Web 端使用的应用标识。
	DoubaoAppKey = "GOqQpfo1fO7slHv8"
	// DoubaoNamespace 是语音服务的命名空间。
	DoubaoNamespace = "VoiceGenie"
	// DoubaoAID 是豆包 Web 的应用 ID。
	DoubaoAID = "586864"
	// DoubaoWSSEndpoint 是新版语音合成 WebSocket 地址。
	DoubaoWSSEndpoint = "wss://frontier-audio-web-ws.doubao.com/api/v2/sami/voicegenie"
	// DoubaoOrigin 是必须伪造的插件来源。
	DoubaoOrigin = "chrome-extension://capohkkfagimodmlpnahjoijgoocdjhd"

	// 豆包语音服务状态码
	DoubaoStatusCodeSuccess = 20000000
	DoubaoStatusCodeAuth    = 40200001

	// doubaoMaxTextBytes 是单次合成文本的字节上限。
	doubaoMaxTextBytes = 4096
)

// DoubaoConfig 是豆包 TTS 的配置。
type DoubaoConfig struct {
	Voice  string  `json:"voice"`  // 说话人
	Rate   float64 `json:"rate"`   // 语速，0.7 ~ 2.0
	Pitch  float64 `json:"pitch"`  // 音调，协议要求整数
	Cookie string  `json:"cookie"` // 直接指定的 Cookie（优先级最高）
}

// NewDoubaoConfig 创建带默认值的豆包配置。
func NewDoubaoConfig(voice string) *DoubaoConfig {
	if strings.TrimSpace(voice) == "" {
		voice = DoubaoDefaultVoice
	}
	return &DoubaoConfig{
		Voice: voice,
		Rate:  1,
		Pitch: 0,
	}
}

// Validate 校验配置。
func (config *DoubaoConfig) Validate() error {
	if strings.TrimSpace(config.Voice) == "" {
		return fmt.Errorf("豆包说话人不能为空")
	}
	if config.Rate < 0.7 || config.Rate > 2.0 {
		return fmt.Errorf("豆包语速必须在 0.7 ~ 2.0 之间，当前为 %v", config.Rate)
	}
	return nil
}
