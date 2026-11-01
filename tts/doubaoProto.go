/*
 * Copyright (c)  2026  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:doubaoProto.go
 * 修改日期:2026/11/01
 * 作者:kerojiang
 */

package tts

import "fmt"

// 豆包 VoiceGenie 的 WebSocket 消息使用 protobuf 编码。
// 这里手写 varint 编解码，避免引入 protobuf 运行时依赖。

// pbRequest 对应服务端 WebSocketRequest。
type pbRequest struct {
	Token     string  // 字段 1
	Appkey    string  // 字段 2
	Namespace string  // 字段 3
	Version   string  // 字段 4
	Event     string  // 字段 5
	Payload   string  // 字段 6
	Data      []byte  // 字段 7
	TaskID    string  // 字段 8
	SeqID     *uint64 // 字段 9
}

// pbResponse 对应服务端 WebSocketResponse。
type pbResponse struct {
	TaskID     string // 字段 1
	MessageID  string // 字段 2
	Namespace  string // 字段 3
	Event      string // 字段 4
	StatusCode int32  // 字段 5
	StatusText string // 字段 6
	Payload    string // 字段 7
	Data       []byte // 字段 8
	SeqID      uint64 // 字段 9
}

// 编码相关 wire type
const (
	pbWireVarint = 0
	pbWire64Bit  = 1
	pbWireBytes  = 2
	pbWire32Bit  = 5
)

// appendPBVarint 追加一个 base-128 varint。
func appendPBVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// appendPBTag 追加字段 tag（字段编号 + wire type）。
func appendPBTag(b []byte, field, wire int) []byte {
	return appendPBVarint(b, uint64(field<<3|wire))
}

// appendPBString 追加 string 字段；空字符串按 protobuf 惯例省略。
func appendPBString(b []byte, field int, v string) []byte {
	if v == "" {
		return b
	}
	b = appendPBTag(b, field, pbWireBytes)
	b = appendPBVarint(b, uint64(len(v)))
	return append(b, v...)
}

// appendPBBytes 追加 bytes 字段；空数据省略（与缺省语义等价）。
func appendPBBytes(b []byte, field int, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	b = appendPBTag(b, field, pbWireBytes)
	b = appendPBVarint(b, uint64(len(v)))
	return append(b, v...)
}

// appendPBUint 追加 varint 字段。
func appendPBUint(b []byte, field int, v uint64) []byte {
	b = appendPBTag(b, field, pbWireVarint)
	return appendPBVarint(b, v)
}

// encodePBRequest 把 pbRequest 编码为 protobuf 字节。
func encodePBRequest(r pbRequest) []byte {
	var b []byte
	b = appendPBString(b, 1, r.Token)
	b = appendPBString(b, 2, r.Appkey)
	b = appendPBString(b, 3, r.Namespace)
	b = appendPBString(b, 4, r.Version)
	b = appendPBString(b, 5, r.Event)
	b = appendPBString(b, 6, r.Payload)
	b = appendPBBytes(b, 7, r.Data)
	b = appendPBString(b, 8, r.TaskID)
	if r.SeqID != nil {
		b = appendPBUint(b, 9, *r.SeqID)
	}
	return b
}

// readPBVarint 从 data[offset] 开始读取一个 varint。
func readPBVarint(data []byte, offset int) (uint64, int, error) {
	var value uint64
	var shift uint
	for offset < len(data) {
		c := data[offset]
		offset++
		if shift >= 64 {
			return 0, offset, fmt.Errorf("protobuf varint 溢出")
		}
		value |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return value, offset, nil
		}
		shift += 7
	}
	return 0, offset, fmt.Errorf("protobuf varint 不完整")
}

// decodePBResponse 解析服务端返回的 protobuf 消息。
// 未知字段按 wire type 跳过，长度越界或非法 wire type 返回错误。
func decodePBResponse(data []byte) (pbResponse, error) {
	var resp pbResponse

	offset := 0
	for offset < len(data) {
		tag, next, err := readPBVarint(data, offset)
		if err != nil {
			return resp, err
		}
		offset = next

		field := int(tag >> 3)
		wire := int(tag & 0x7)

		switch wire {
		case pbWireVarint:
			value, next, err := readPBVarint(data, offset)
			if err != nil {
				return resp, err
			}
			offset = next
			switch field {
			case 5:
				resp.StatusCode = int32(value)
			case 9:
				resp.SeqID = value
			}

		case pbWire64Bit:
			if len(data)-offset < 8 {
				return resp, fmt.Errorf("protobuf 字段 %d 的 64 位数据越界", field)
			}
			offset += 8

		case pbWire32Bit:
			if len(data)-offset < 4 {
				return resp, fmt.Errorf("protobuf 字段 %d 的 32 位数据越界", field)
			}
			offset += 4

		case pbWireBytes:
			length, next, err := readPBVarint(data, offset)
			if err != nil {
				return resp, err
			}
			offset = next
			if length > uint64(len(data)-offset) {
				return resp, fmt.Errorf("protobuf 字段 %d 长度越界：声明 %d，剩余 %d",
					field, length, len(data)-offset)
			}
			chunk := data[offset : offset+int(length)]
			offset += int(length)

			switch field {
			case 1:
				resp.TaskID = string(chunk)
			case 2:
				resp.MessageID = string(chunk)
			case 3:
				resp.Namespace = string(chunk)
			case 4:
				resp.Event = string(chunk)
			case 6:
				resp.StatusText = string(chunk)
			case 7:
				resp.Payload = string(chunk)
			case 8:
				// 复制一份，避免与底层读缓冲共享内存
				resp.Data = append([]byte(nil), chunk...)
			}

		default:
			return resp, fmt.Errorf("不支持的 protobuf wire type %d（字段 %d）", wire, field)
		}
	}

	return resp, nil
}
