/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:edgeConfig.go
 * 修改日期:2026/03/10
 * 作者:kerojiang
 */

package tts

import (
	"fmt"
	"regexp"
	"strings"
)

// TTSConfig represents the TTS configuration for Edge TTS
type TTSConfig struct {
	Voice              string `json:"voice"`
	Rate               string `json:"rate"`
	Volume             string `json:"volume"`
	Pitch              string `json:"pitch"`
	Boundary           string `json:"boundary"` // "WordBoundary" or "SentenceBoundary"
	SentenceSplit      bool   `json:"sentence_split"`
	ContinuousPlayback bool   `json:"continuous_playback"`
}

// Validate validates the TTSConfig
func (config *TTSConfig) Validate() error {
	if config.Voice == "" {
		return fmt.Errorf("voice must be specified")
	}

	// Validate voice format
	match, err := regexp.MatchString(`^([a-z]{2,})-([A-Z]{2,})-(.+Neural)$`, config.Voice)
	if err != nil {
		return fmt.Errorf("error validating voice format: %w", err)
	}

	if match {
		re := regexp.MustCompile(`^([a-z]{2,})-([A-Z]{2,})-(.+Neural)$`)
		parts := re.FindStringSubmatch(config.Voice)
		if len(parts) >= 4 {
			lang := parts[1]
			region := parts[2]
			name := parts[3]

			if strings.Contains(name, "-") {
				dashIndex := strings.Index(name, "-")
				region = fmt.Sprintf("%s-%s", region, name[:dashIndex])
				name = name[dashIndex+1:]
			}

			config.Voice = fmt.Sprintf("Microsoft Server Speech Text to Speech Voice (%s-%s, %s)",
				lang, region, name)
		}
	}

	// Validate rate, volume, pitch formats
	if err := config.validateStringParam("rate", config.Rate, `^[+-]\d+%$`); err != nil {
		return err
	}
	if err := config.validateStringParam("volume", config.Volume, `^[+-]\d+%$`); err != nil {
		return err
	}
	if err := config.validateStringParam("pitch", config.Pitch, `^[+-]\d+Hz$`); err != nil {
		return err
	}

	// Validate boundary
	if config.Boundary != "WordBoundary" && config.Boundary != "SentenceBoundary" {
		return fmt.Errorf("boundary must be 'WordBoundary' or 'SentenceBoundary'")
	}

	return nil
}

func (config *TTSConfig) validateStringParam(paramName, paramValue, pattern string) error {
	if paramValue == "" {
		return fmt.Errorf("%s must be specified", paramName)
	}

	matched, err := regexp.MatchString(pattern, paramValue)
	if err != nil {
		return fmt.Errorf("error validating %s: %w", paramName, err)
	}

	if !matched {
		return fmt.Errorf("invalid %s '%s'", paramName, paramValue)
	}

	return nil
}

// NewTTSConfig creates a new TTSConfig with default values
func NewTTSConfig(voice string) *TTSConfig {
	return &TTSConfig{
		Voice:              voice,
		Rate:               "+0%",
		Volume:             "+0%",
		Pitch:              "+0Hz",
		Boundary:           "SentenceBoundary",
		SentenceSplit:      true,
		ContinuousPlayback: true,
	}
}
