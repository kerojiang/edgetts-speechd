/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:core_test.go
 * 修改日期:2024/05/24 11:32:09
 * 作者:kerojiang
 */

package tts

import "testing"

func TestGetTTSAudioFile(t *testing.T) {
	type args struct {
		text string
		tts  TTSType
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTTSAudioFile(tt.args.text, tt.args.tts)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTTSAudioFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetTTSAudioFile() got = %v, want %v", got, tt.want)
			}
		})
	}
}