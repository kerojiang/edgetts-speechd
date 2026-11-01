package tts

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// 微软 Edge Read Aloud 自 2026-10 起要求 Sec-MS-GEC v2 令牌。
// 令牌格式为 "<16位十六进制>-<64位十六进制>"，
// 由自定义 SHA-256（含 57 字节盐）与 CS64 校验算法生成。
// 本文件为 rany2/edge-tts 官方实现的 Go 移植：
// https://github.com/rany2/edge-tts/blob/master/src/edge_tts/drm.py

const (
	u32Mask = 0xFFFFFFFF

	sha256Adjustment     = 0xC9DC06A7
	sigma0Adjustment     = 0x059C0B80
	sigma1Adjustment     = 0x000B92C0
	cs64Factor           = 0x82893933
	cs64Adjustment0      = 0xF2F90B1A
	cs64Adjustment1      = 0x0B1A0000
	cs64StateMultiplier0 = 0xCF98B111
	cs64StateMultiplier1 = 0x87085B9F
	cs64StateMultiplier2 = 0x12CEB96D
	cs64StateMultiplier3 = 0x257E1D83
)

var sha256Initial = [8]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
	0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

var sha256State = [8]uint32{
	0xFD9FBEA2, 0x91243CE7, 0xBE499094, 0xCD2E1E0A,
	0x6C006F49, 0x04BD35AB, 0xFEB0A7E4, 0x03EFFBD2,
}

var sha256Schedule = [14]uint32{
	0xEB24BF9E, 0x1558691F, 0xA0C09EE2, 0x0CFA32BA,
	0xA622EE8C, 0xD1DD9491, 0xC0DD25A0, 0xF9E70AB6,
	0xF0C31573, 0x7058CE4E, 0x7188E606, 0x00E5AB33,
	0xD258B060, 0x72766564,
}

var sha256RoundConstants = [64]uint32{
	0x428A2F98, 0x71374491, 0xB5C0FBCF, 0xE9B5DBA5,
	0x3956C25B, 0x59F111F1, 0x923F82A4, 0xAB1C5ED5,
	0xD807AA98, 0x12835B01, 0x243185BE, 0x550C7DC3,
	0x72BE5D74, 0x80DEB1FE, 0x9BDC06A7, 0xC19BF174,
	0xE49B69C1, 0xEFBE4786, 0x0FC19DC6, 0x240CA1CC,
	0x2DE92C6F, 0x4A7484AA, 0x5CB0A9DC, 0x76F988DA,
	0x983E5152, 0xA831C66D, 0xB00327C8, 0xBF597FC7,
	0xC6E00BF3, 0xD5A79147, 0x06CA6351, 0x14292967,
	0x27B70A85, 0x2E1B2138, 0x4D2C6DFC, 0x53380D13,
	0x650A7354, 0x766A0ABB, 0x81C2C92E, 0x92722C85,
	0xA2BFE8A1, 0xA81A664B, 0xC24B8B70, 0xC76C51A3,
	0xD192E819, 0xD6990624, 0xF40E3585, 0x106AA070,
	0x19A4C116, 0x1E376C08, 0x2748774C, 0x34B0BCB5,
	0x391C0CB3, 0x4ED8AA4A, 0x5B9CCA4F, 0x682E6FF3,
	0x748F82EE, 0x78A5636F, 0x84C87814, 0x8CC70208,
	0x90BEFFFA, 0xA4506CEB, 0xBEF9A3F7, 0xC67178F2,
}

// sha256BlockConstants 将第 15 个常量替换为调整值。
var sha256BlockConstants = func() [64]uint32 {
	c := sha256RoundConstants
	c[14] = sha256Adjustment
	return c
}()

var cs64Coefficients = [14][2]uint32{
	{0xE82E91B1, 0xB32700D0},
	{0x44DE24C6, 0x88DFF51D},
	{0x3BEB65BC, 0x29514038},
	{0xC89C5933, 0xB328CBDF},
	{0x8D1EC211, 0xD405B4D5},
	{0x314EF5D5, 0x26E55F3C},
	{0xE92491B1, 0x607801C6},
	{0xF0A5B9D5, 0x82B2C1FB},
	{0x06064CF3, 0xFA51A3DA},
	{0xDA79FA60, 0x207CC464},
	{0xBED20560, 0x3C3C74F3},
	{0x89F9C9F8, 0xD2D14B61},
	{0x35FA38C6, 0xBB04A2C1},
	{0xCB49E3C6, 0xC5626167},
}

// rol16 将 32 位值循环左移 16 位。
func rol16(v uint32) uint32 {
	return v<<16 | v>>16
}

// sigma0 对应 SHA-256 的小写 sigma0，仅做一次截断。
func sigma0(v uint32) uint32 {
	return (v >> 7) ^ (v << 25) ^ (v >> 18) ^ (v << 14) ^ (v >> 3)
}

// sigma1 对应 SHA-256 的小写 sigma1，仅做一次截断。
func sigma1(v uint32) uint32 {
	return (v >> 17) ^ (v << 15) ^ (v >> 19) ^ (v << 13) ^ (v >> 10)
}

// sha256Rounds 执行 SHA-256 轮函数，可从首块内部恢复执行。
func sha256Rounds(state [8]uint32, words []uint32, firstRound int) [8]uint32 {
	a, b, c, d := state[0], state[1], state[2], state[3]
	e, f, g, h := state[4], state[5], state[6], state[7]

	constants := &sha256RoundConstants
	if firstRound == 14 {
		constants = &sha256BlockConstants
	}

	aligned := ((firstRound + 7) / 8) * 8
	if aligned > 64 {
		aligned = 64
	}

	for index := firstRound; index < aligned; index++ {
		rotE := uint64(e) * 0x100000001
		rotA := uint64(a) * 0x100000001
		t1 := uint64(h) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(g^(e&(f^g))) + uint64(constants[index]) + uint64(words[index])
		t2 := uint64((rotA >> 2) ^ (rotA >> 13) ^ (rotA >> 22)) +
			uint64((a&b)|(c&(a|b)))
		a, b, c, d, e, f, g, h = uint32(t1+t2), a, b, c, d+uint32(t1), e, f, g
	}

	for index := aligned; index < 64; index += 8 {
		rotE := uint64(e) * 0x100000001
		rotA := uint64(a) * 0x100000001
		t1 := uint64(h) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(g^(e&(f^g))) + uint64(constants[index]) + uint64(words[index])
		d = (d + uint32(t1)) & u32Mask
		h = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((a&b)|(c&(a|b))))
		// 以下每次更新两个寄存器，8 轮后工作寄存器名称回到原始顺序。
		rotE = uint64(d) * 0x100000001
		rotA = uint64(h) * 0x100000001
		t1 = uint64(g) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(f^(d&(e^f))) + uint64(constants[index+1]) + uint64(words[index+1])
		c = (c + uint32(t1)) & u32Mask
		g = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((h&a)|(b&(h|a))))
		rotE = uint64(c) * 0x100000001
		rotA = uint64(g) * 0x100000001
		t1 = uint64(f) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(e^(c&(d^e))) + uint64(constants[index+2]) + uint64(words[index+2])
		b = (b + uint32(t1)) & u32Mask
		f = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((g&h)|(a&(g|h))))
		rotE = uint64(b) * 0x100000001
		rotA = uint64(f) * 0x100000001
		t1 = uint64(e) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(d^(b&(c^d))) + uint64(constants[index+3]) + uint64(words[index+3])
		a = (a + uint32(t1)) & u32Mask
		e = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((f&g)|(h&(f|g))))
		rotE = uint64(a) * 0x100000001
		rotA = uint64(e) * 0x100000001
		t1 = uint64(d) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(c^(a&(b^c))) + uint64(constants[index+4]) + uint64(words[index+4])
		h = (h + uint32(t1)) & u32Mask
		d = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((e&f)|(g&(e|f))))
		rotE = uint64(h) * 0x100000001
		rotA = uint64(d) * 0x100000001
		t1 = uint64(c) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(b^(h&(a^b))) + uint64(constants[index+5]) + uint64(words[index+5])
		g = (g + uint32(t1)) & u32Mask
		c = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((d&e)|(f&(d|e))))
		rotE = uint64(g) * 0x100000001
		rotA = uint64(c) * 0x100000001
		t1 = uint64(b) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(a^(g&(h^a))) + uint64(constants[index+6]) + uint64(words[index+6])
		f = (f + uint32(t1)) & u32Mask
		b = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((c&d)|(e&(c|d))))
		rotE = uint64(f) * 0x100000001
		rotA = uint64(b) * 0x100000001
		t1 = uint64(a) + ((rotE >> 6) ^ (rotE >> 11) ^ (rotE >> 25)) +
			uint64(h^(f&(g^h))) + uint64(constants[index+7]) + uint64(words[index+7])
		e = (e + uint32(t1)) & u32Mask
		a = uint32(t1 + uint64((rotA>>2)^(rotA>>13)^(rotA>>22)) +
			uint64((b&c)|(d&(b|c))))
	}

	return [8]uint32{a, b, c, d, e, f, g, h}
}

// expandSHA256Schedule 展开 SHA-256 消息调度表。
func expandSHA256Schedule(words []uint32, firstWord int) {
	for index := firstWord; index < 64; index++ {
		x, y := words[index-2], words[index-15]
		s1 := (x >> 17) ^ (x << 15) ^ (x >> 19) ^ (x << 13) ^ (x >> 10)
		s0 := (y >> 7) ^ (y << 25) ^ (y >> 18) ^ (y << 14) ^ (y >> 3)
		words[index] = s1 + words[index-7] + s0 + words[index-16]
	}
}

// sha256Init 计算令牌数据的自定义 SHA-256 初始状态。
func sha256Init(data []byte) [8]uint32 {
	words := make([]uint32, 64)
	words[14] = uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
	words[15] = binary.BigEndian.Uint32(data[3:7])

	for index := 16; index < 30; index++ {
		value := sha256Schedule[index-16]
		if index-2 >= 14 {
			if index == 16 {
				value += sigma1(words[index-2]) ^ sigma1Adjustment
			} else {
				value += sigma1(words[index-2])
			}
		}
		if index-7 >= 14 {
			value += words[index-7]
		}
		if index-15 >= 14 {
			if index == 29 {
				value += sigma0(words[index-15]) ^ sigma0Adjustment
			} else {
				value += sigma0(words[index-15])
			}
		}
		words[index] = value
	}

	words[30] = sigma1(words[28]) + words[23] + sigma0(words[15]) + words[14] +
		sha256Adjustment - sha256RoundConstants[14]

	expandSHA256Schedule(words, 31)
	working := sha256Rounds(sha256State, words, 14)
	out := [8]uint32{}
	for i := range out {
		out[i] = sha256Initial[i] + working[i]
	}
	return out
}

// sha256Digest 计算令牌数据的自定义 SHA-256 摘要。
func sha256Digest(data []byte) []byte {
	length := 57 + len(data)
	padLen := ((55 - length) % 64 + 64) % 64

	padded := make([]byte, 0, len(data)-7+1+padLen+8)
	padded = append(padded, data[7:]...)
	padded = append(padded, 0x80)
	padded = append(padded, make([]byte, padLen)...)
	lenBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(lenBytes, uint64(length)*8)
	padded = append(padded, lenBytes...)

	state := sha256Init(data[:7])
	for offset := 0; offset < len(padded); offset += 64 {
		var words [64]uint32
		for i := 0; i < 16; i++ {
			words[i] = binary.BigEndian.Uint32(padded[offset+i*4 : offset+i*4+4])
		}
		expandSHA256Schedule(words[:], 16)
		working := sha256Rounds(state, words[:], 0)
		for i := range state {
			state[i] += working[i]
		}
	}

	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		binary.BigEndian.PutUint32(out[i*4:], state[i])
	}
	return out
}

// wordsLE4Even 按小端切分 32 位字，并补零到偶数个。
func wordsLE4Even(data []byte) []uint32 {
	n := (len(data) + 3) / 4
	padded := make([]byte, n*4)
	copy(padded, data)
	words := make([]uint32, n)
	for i := 0; i < n; i++ {
		words[i] = binary.LittleEndian.Uint32(padded[i*4:])
	}
	if len(words)%2 == 1 {
		words = append(words, 0)
	}
	return words
}

// cs64Digest 计算令牌数据的 CS64 摘要。
func cs64Digest(data, digest []byte) (uint32, uint32) {
	keyA := binary.LittleEndian.Uint32(digest[0:4]) | 1
	keyB := binary.LittleEndian.Uint32(digest[4:8]) | 1
	factors := [2]uint32{keyA * cs64Factor, keyB * cs64Factor}

	words := wordsLE4Even(append([]byte{0x00}, data...))
	total := len(cs64Coefficients) + len(words)
	terms := make([]uint32, 0, total)
	offsets := make([]uint32, 0, total)

	for index, coef := range cs64Coefficients {
		terms = append(terms, coef[0]*factors[index%2])
		offsets = append(offsets, coef[1])
	}

	for index, word := range words {
		term := word * keyA
		offset := rol16(word) * 0xEF0569FB
		if index%2 == 1 {
			term = word * keyB
			offset = rol16(word) * 0xC31713DB
		}
		if index == 0 {
			term += cs64Adjustment0 * factors[0]
			offset += cs64Adjustment1
		}
		terms = append(terms, term)
		offsets = append(offsets, offset)
	}

	var swapAcc, swapLast, state, acc uint32
	for index := 0; index < len(terms); index += 2 {
		x, y := terms[index], terms[index+1]

		t := rol16(offsets[index] + x)
		q := t * 0x76F50F34
		swapAcc += rol16(q)*0xC3EFEA97 + q
		t = rol16(offsets[index+1] + y)
		q = t * 0x1881CE3C
		swapLast = rol16(q)*0x35BD1EC9 + q
		swapAcc += swapLast

		state = state*keyA + x
		state = rol16(state) * cs64StateMultiplier0
		state = rol16(state) * cs64StateMultiplier1
		state = rol16(state) * cs64StateMultiplier2
		state = rol16(state) * cs64StateMultiplier3
		acc += state

		t = state*keyB + y
		t = rol16(t) * 0xA27416F5
		t = rol16(t) * 0xD38396FF
		state = rol16(rol16(t)*0x7C932B89) * 0xBFA49F69
		acc += state
	}

	return swapLast ^ state, swapAcc ^ acc
}

// generateSecMSGECV2 生成 v2 格式的 Sec-MS-GEC 令牌。
// unixTimestamp 为 Unix 秒；传入 0 表示使用当前时间。
func generateSecMSGECV2(unixTimestamp float64) string {
	if unixTimestamp == 0 {
		unixTimestamp = GetUnixTimestamp()
	}

	ticks := unixTimestamp + WinEpoch
	ticks -= math.Mod(ticks, 300)
	ticks *= SToNS / 100

	strToHash := []byte(fmt.Sprintf("%.0f%s", ticks, TrustedClientToken))
	digest := sha256Digest(strToHash)
	hi, lo := cs64Digest(strToHash, digest)

	cs64 := make([]byte, 8)
	binary.LittleEndian.PutUint32(cs64[0:], hi)
	binary.LittleEndian.PutUint32(cs64[4:], lo)

	return strings.ToUpper(fmt.Sprintf("%x", cs64)) + "-" + strings.ToUpper(fmt.Sprintf("%x", digest))
}
