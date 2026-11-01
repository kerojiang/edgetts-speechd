package tts

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestPBRequestEncoding 验证请求字段编号与 wire type 与服务端协议一致。
// 若字段编号被改动，编码字节序列会变化，本测试失败。
func TestPBRequestEncoding(t *testing.T) {
	got := encodePBRequest(pbRequest{
		Appkey:    "k",
		Namespace: "VoiceGenie",
		Event:     "StartTask",
	})

	want, err := hex.DecodeString("12016b1a0a566f69636547656e69652a0953746172745461736b")
	if err != nil {
		t.Fatalf("测试常量非法: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("编码结果不匹配\n got: % x\nwant: % x", got, want)
	}
}

// TestPBRequestSeqIDAndData 验证 seq_id 使用 varint、data 使用 bytes 字段。
func TestPBRequestSeqIDAndData(t *testing.T) {
	seq := uint64(1)
	got := encodePBRequest(pbRequest{Data: []byte("OggS"), SeqID: &seq})

	want, err := hex.DecodeString("3a044f6767534801")
	if err != nil {
		t.Fatalf("测试常量非法: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("编码结果不匹配\n got: % x\nwant: % x", got, want)
	}
}

// TestDecodePBResponse 验证响应解码：字符串、int32 状态码、二进制音频。
func TestDecodePBResponse(t *testing.T) {
	raw, err := hex.DecodeString(
		"0a027431" + // task_id = "t1"
			"220b5461736b53746172746564" + // event = "TaskStarted"
			"2880dac409" + // status_code = 20000000
			"42" + "044f676753", // data = "OggS"
	)
	if err != nil {
		t.Fatalf("测试常量非法: %v", err)
	}

	resp, err := decodePBResponse(raw)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if resp.TaskID != "t1" {
		t.Errorf("TaskID = %q, 期望 %q", resp.TaskID, "t1")
	}
	if resp.Event != "TaskStarted" {
		t.Errorf("Event = %q, 期望 %q", resp.Event, "TaskStarted")
	}
	if resp.StatusCode != 20000000 {
		t.Errorf("StatusCode = %d, 期望 20000000", resp.StatusCode)
	}
	if !bytes.Equal(resp.Data, []byte("OggS")) {
		t.Errorf("Data = %q, 期望 %q", resp.Data, "OggS")
	}
}

// TestDecodePBResponseSkipsUnknown 验证未知字段可被跳过，不影响已知字段解析。
func TestDecodePBResponseSkipsUnknown(t *testing.T) {
	raw, err := hex.DecodeString(
		"6203" + "616263" + // field 12, bytes "abc"（未知字段）
			"220454545445", // event = "TTTE"
	)
	if err != nil {
		t.Fatalf("测试常量非法: %v", err)
	}

	resp, err := decodePBResponse(raw)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if resp.Event != "TTTE" {
		t.Errorf("Event = %q, 期望 %q", resp.Event, "TTTE")
	}
}

// TestDecodePBResponseTruncated 验证长度越界时返回错误而不是 panic。
func TestDecodePBResponseTruncated(t *testing.T) {
	raw, err := hex.DecodeString("0a0561")
	if err != nil {
		t.Fatalf("测试常量非法: %v", err)
	}

	if _, err := decodePBResponse(raw); err == nil {
		t.Fatal("期望返回错误，实际返回 nil")
	}
}
