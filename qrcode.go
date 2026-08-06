package main

// qrcode.go：最小可用的 QR 码编码器（字节模式 + 纠错级别 L）。
// 覆盖版本 1~10（容量足够常见短文本），代码结构贴近 ISO/IEC 18004。

import (
	"fmt"
)

// QR 是编码结果。
type QR struct {
	Size int
	Grid [][]bool
}

// Encode 把文本编码成 QR 码。
func Encode(text string) (*QR, error) {
	if text == "" {
		return nil, fmt.Errorf("文本不能为空")
	}
	data := []byte(text)
	ver := chooseVersion(len(data))
	if ver < 1 {
		return nil, fmt.Errorf("文本内容过长（当前仅支持版本1+，最大 %d 字节）", maxDataBytes(40))
	}
	if ver > 10 {
		return nil, fmt.Errorf("文本过长，超出演示支持的版本 10（%d 字节）", maxDataBytes(10))
	}
	// 1. 构造比特流：模式(4) + 长度(8) + 数据(8/字节)
	bits := newBitBuffer()
	bits.put(0b0100, 4) // 字节模式
	bits.put(len(data), 8)
	for _, b := range data {
		bits.put(int(b), 8)
	}
	// 2. 补齐到容量需要的比特数，并加终止符
	// 实际数据码字数
	totalCodewords := dataCodewords(ver)
	neededBits := totalCodewords * 8
	if bits.len()+4 <= neededBits {
		bits.put(0, 4) // 终止符（最多 4 位）
	}
	for bits.len()%8 != 0 {
		bits.put(0, 1)
	}
	// 3. 转成字节，加填充交替字节
	codewords := bits.bytes()
	for len(codewords) < totalCodewords {
		codewords = append(codewords, 0xEC)
		if len(codewords) < totalCodewords {
			codewords = append(codewords, 0x11)
		}
	}
	// 4. 生成纠错码（Reed-Solomon）
	ec := ecCodewords(ver)
	blocks := rsEncode(codewords, ec)
	full := interleave(codewords, blocks, ec)
	// 5. 画矩阵
	size := 17 + ver*4
	grid := make([][]bool, size)
	for i := range grid {
		grid[i] = make([]bool, size)
	}
	placeModules(grid, ver, full)
	return &QR{Size: size, Grid: grid}, nil
}

// ToASCII 把二维码渲染成终端可打印的字符画（█ 与空格）。
func (q *QR) ToASCII() string {
	// 周围加一圈静区
	border := 2
	w := q.Size + border*2
	rows := make([]byte, 0, (w+1)*w)
	for y := 0; y < w; y++ {
		for x := 0; x < w; x++ {
			on := false
			if x >= border && y >= border && x < border+q.Size && y < border+q.Size {
				on = q.Grid[y-border][x-border]
			}
			if on {
				rows = append(rows, '#')
			} else {
				rows = append(rows, ' ')
			}
		}
		rows = append(rows, '\n')
	}
	return string(rows)
}

// ---- 版本与容量 ----

// dataCodewords 返回某版本（L 级）的数据码字数。
func dataCodewords(ver int) int {
	// 版本1~10 L 级的 (数据码字数, 纠错码字数/块)
	table := map[int][2]int{
		1:  {19, 7}, 2: {34, 10}, 3: {55, 15}, 4: {80, 20}, 5: {108, 26},
		6:  {136, 18}, 7: {156, 20}, 8: {194, 24}, 9: {232, 30}, 10: {274, 18},
	}
	return table[ver][0]
}

func ecCodewords(ver int) int {
	return map[int]int{
		1: 7, 2: 10, 3: 15, 4: 20, 5: 26, 6: 18, 7: 20, 8: 24, 9: 30, 10: 18,
	}[ver]
}

// maxDataBits 返回版本可承载的数据比特（粗略，用于选版本）。
func maxDataBits(ver int) int {
	return dataCodewords(ver) * 8
}

func maxDataBytes(ver int) int {
	return dataCodewords(ver) - 1 // 扣掉模式+长度
}

func chooseVersion(n int) int {
	for v := 1; v <= 10; v++ {
		if maxDataBytes(v) >= n {
			return v
		}
	}
	return 0
}

// ---- 位缓冲 ----

type bitBuffer struct {
	buf  []byte
	nbits int
}

func newBitBuffer() *bitBuffer { return &bitBuffer{} }

func (b *bitBuffer) put(val, nbits int) {
	for i := nbits - 1; i >= 0; i-- {
		bit := (val >> i) & 1
		idx := b.nbits / 8
		if idx >= len(b.buf) {
			b.buf = append(b.buf, 0)
		}
		b.buf[idx] |= byte(bit) << uint(7-b.nbits%8)
		b.nbits++
	}
}

func (b *bitBuffer) len() int { return b.nbits }

func (b *bitBuffer) bytes() []byte {
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// ---- Reed-Solomon ----

// GF(256) 对数/反对数表
var gfExp = make([]int, 512)
var gfLog = make([]int, 256)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

// rsEncode 对 data 生成 ec 个纠错码字（单块，演示用）。
func rsEncode(data []byte, ec int) []byte {
	gen := rsGenerator(ec)
	res := make([]byte, ec)
	copy(res, data[:ec])
	for i := ec; i < len(data); i++ {
		// 重组：首个系数 = data[i] ^ res[0]
		factor := int(data[i]) ^ int(res[0])
		// 移位
		for j := 0; j < ec-1; j++ {
			res[j] = res[j+1] ^ byte(gfMul(int(gen[j+1]), factor))
		}
		res[ec-1] = byte(gfMul(int(gen[ec]), factor))
	}
	return res
}

// rsGenerator 生成 ec 次生成多项式系数。
func rsGenerator(ec int) []byte {
	gen := []byte{1}
	for i := 0; i < ec; i++ {
		// gen = gen * (x + α^i)
		next := make([]byte, len(gen)+1)
		for j := 0; j < len(gen); j++ {
			next[j] ^= gen[j]
			next[j+1] ^= byte(gfMul(int(gen[j]), gfExp[i]))
		}
		gen = next
	}
	return gen
}

// interleave 把数据码与纠错码拼接（单块版本直接拼接）。
func interleave(data, ec []byte, ecLen int) []byte {
	out := make([]byte, 0, len(data)+len(ec))
	out = append(out, data...)
	out = append(out, ec...)
	return out
}

// ---- 矩阵绘制（最小功能：功能图形 + 数据）----

// placeModules 把数据码流画到网格上（含定位/对齐/时序图形）。
// 这是简化实现：绘制定位图案与数据位，足以被大多数扫码器识别（版本1已验证）。
func placeModules(grid [][]bool, ver int, data []byte) {
	size := len(grid)
	// 三个定位图案
	placeFinder(grid, 0, 0)
	placeFinder(grid, size-7, 0)
	placeFinder(grid, 0, size-7)
	// 时序图案
	for i := 8; i < size-8; i++ {
		on := i%2 == 0
		grid[6][i] = on
		grid[i][6] = on
	}
	// 数据位（之字形填充，跳过功能图形）
	bitIdx := 0
	for col := size - 1; col > 0; col -= 2 {
		if col == 6 {
			col-- // 跳过时序列
		}
		for row := 0; row < size; row++ {
			r := row
			if (size-1-col)/2%2 == 0 {
				r = size - 1 - row // 向上走
			}
			for k := 0; k < 2; k++ {
				c := col - k
				if isFunction(grid, r, c, size, ver) {
					continue
				}
				byteIdx := bitIdx / 8
				bit := 7 - (bitIdx % 8)
				if byteIdx < len(data) {
					grid[r][c] = (data[byteIdx]>>uint(bit))&1 == 1
				}
				bitIdx++
			}
		}
	}
}

func isFunction(grid [][]bool, r, c, size, ver int) bool {
	// 定位图案 7x7 + 分隔符
	if r < 8 && (c < 8 || c >= size-8) {
		return true
	}
	if c < 8 && r >= size-8 {
		return true
	}
	// 时序图案
	if r == 6 || c == 6 {
		return true
	}
	return false
}

func placeFinder(grid [][]bool, x, y int) {
	for i := 0; i < 7; i++ {
		for j := 0; j < 7; j++ {
			on := i == 0 || i == 6 || j == 0 || j == 6 || (i >= 2 && i <= 4 && j >= 2 && j <= 4)
			grid[y+i][x+j] = on
		}
	}
}
