/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:constant.go
 * 修改日期:2024/05/24 08:38:04
 * 作者:kerojiang
 */

package edge

type MessageType int

const (
	TrustedClientToken = "6A5AA1D4EAFF4E9FB37E23D68491D6F4"
	WssURL             = "wss://speech.platform.bing.com/consumer/speech/synthesize/" + "readaloud/edge/v1?TrustedClientToken=" + TrustedClientToken

	defaultVoice = "zh-CN-XiaoxiaoNeural"
)
