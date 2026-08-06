package main

// qrcode.go：最小可用的 QR 码编码器（字节模式 + 纠错级别 L）。
// 覆盖版本 1~10（容量足够常见短文本），代码结构贴近 ISO/IEC 18004。

import "fmt"

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
		return nil, fmt.Errorf("文本内容过长（当前仅支持版本1+，最大 %d 字节）", maxDataBytes(10))
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
	// 4. 按块结构做 Reed-Solomon 纠错并交错（版本5+ 有多块）
	ec := ecPerBlock(ver)
	nb := numBlocks(ver)
	perBlock := totalCodewords / nb
	dataBlocks := make([][]byte, nb)
	for i := 0; i < nb; i++ {
		dataBlocks[i] = codewords[i*perBlock : (i+1)*perBlock]
	}
	ecBlocks := make([][]byte, nb)
	for i := 0; i < nb; i++ {
		ecBlocks[i] = rsEncode(dataBlocks[i], ec)
	}
	full := interleaveBlocks(dataBlocks, ecBlocks)
	// 5. 画矩阵（含掩码选择 + 格式信息）
	size := 17 + ver*4
	grid := buildMatrix(ver, full)
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

// versionInfo 是某版本 L 级纠错下的块结构（依据 ISO/IEC 18004）。
type versionInfo struct {
	dataCW     int // 总数据码字数
	ecPerBlock int // 每块纠错码字数
	blocks     int // 块数
}

// versionTable 覆盖版本 1~10（L 级）。下标 0 不用。
var versionTable = []versionInfo{
	{},
	{19, 7, 1},
	{34, 10, 1},
	{55, 15, 1},
	{80, 20, 1},
	{108, 26, 2},
	{136, 18, 2},
	{156, 20, 2},
	{194, 16, 2},
	{232, 18, 2},
	{274, 16, 2},
}

// dataCodewords 返回某版本（L 级）的总数据码字数。
func dataCodewords(ver int) int { return versionTable[ver].dataCW }

// ecPerBlock 返回每块纠错码字数。
func ecPerBlock(ver int) int { return versionTable[ver].ecPerBlock }

// numBlocks 返回块数。
func numBlocks(ver int) int { return versionTable[ver].blocks }

// countBits 返回字节模式下"字符计数指示符"的位数：版本 1~9 为 8 位，10~40 为 16 位。
func countBits(ver int) int {
	if ver <= 9 {
		return 8
	}
	return 16
}

// maxDataBits 返回版本可承载的"数据比特"（扣掉模式 4 位 + 字符计数位）。
func maxDataBits(ver int) int {
	return dataCodewords(ver)*8 - 4 - countBits(ver)
}

func maxDataBytes(ver int) int {
	return maxDataBits(ver) / 8
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
	buf   []byte
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

// rsEncode 对 data 生成 ec 个纠错码字（基于多项式取模，等价于标准 RS 编码）。
func rsEncode(data []byte, ec int) []byte {
	gen := rsGenerator(ec) // 生成多项式，长度 ec+1，首系数为 1
	// 被除多项式：data 左移 ec 位（高位补 0），即 data 后接 ec 个 0。
	num := make([]byte, len(data)+ec)
	copy(num, data)
	// 多项式长除法：对 i 从 0 到 len(data)-1，逐次消去最高项。
	for i := 0; i < len(data); i++ {
		coef := int(num[i])
		if coef == 0 {
			continue
		}
		for j := 0; j < len(gen); j++ {
			num[i+j] ^= byte(gfMul(int(gen[j]), coef))
		}
	}
	// 余数即最后 ec 个字节。
	out := make([]byte, ec)
	copy(out, num[len(data):])
	return out
}

// interleaveBlocks 按 ISO 18004 做块间交错：
// 先按列交错所有块的数据码，再按列交错所有块的纠错码。
// 本实现支持的版本每块数据码字数相等、每块纠错码字数也相等，故按列遍历即可。
func interleaveBlocks(dataBlocks, ecBlocks [][]byte) []byte {
	out := make([]byte, 0)
	// 数据码交错
	for col := 0; ; col++ {
		emitted := false
		for b := 0; b < len(dataBlocks); b++ {
			if col < len(dataBlocks[b]) {
				out = append(out, dataBlocks[b][col])
				emitted = true
			}
		}
		if !emitted {
			break
		}
	}
	// 纠错码交错
	for col := 0; ; col++ {
		emitted := false
		for b := 0; b < len(ecBlocks); b++ {
			if col < len(ecBlocks[b]) {
				out = append(out, ecBlocks[b][col])
				emitted = true
			}
		}
		if !emitted {
			break
		}
	}
	return out
}

// ---- 矩阵绘制（功能图形 + 数据 + 掩码 + 格式信息）----

// isFunction 判断 (r,c) 是否为"功能图形"区域（定位/分隔/时序/对齐/格式/版本），
// 这些区域不参与数据放置。跳过集合严格对齐 ISO 18004 与各参考实现的绘制区域：
// 每个定位图案占据 7x7，外围 8x8（含分隔符白边）整块视为功能区。
func isFunction(r, c, size, ver int) bool {
	// 三个定位图案 + 分隔符（各占 8x8 块）
	if r < 8 && c < 8 {
		return true
	}
	if r < 8 && c >= size-8 {
		return true
	}
	if r >= size-8 && c < 8 {
		return true
	}
	// 时序图案（整行/整列）
	if r == 6 || c == 6 {
		return true
	}
	// 格式信息：列 8 的上半 (行 0~8) 与下半 (行 size-8~size-1)；行 8 的左右两部分
	if c == 8 && (r <= 8 || r >= size-8) {
		return true
	}
	if r == 8 && (c <= 8 || c >= size-8) {
		return true
	}
	// 版本 >= 7 的版本信息区
	if ver >= 7 {
		if (r >= size-11 && r < size-7 && c < 6) || (c >= size-11 && c < size-7 && r < 6) {
			return true
		}
	}
	// 对齐图案（5x5）
	for _, p := range alignmentPositions(ver) {
		ar, ac := p[0], p[1]
		if ar-2 <= r && r <= ar+2 && ac-2 <= c && c <= ac+2 {
			return true
		}
	}
	return false
}

// alignmentPositions 返回版本 ver 的对齐图案中心坐标（去掉与定位图案重叠的点）。
func alignmentPositions(ver int) [][2]int {
	pos := alignmentCoords(ver)
	var res [][2]int
	for _, r := range pos {
		for _, c := range pos {
			// 与三个定位图案区域重叠的中心要跳过：
			// 左上(6,6)、右上(6,size-7)、左下(size-7,6)
			if (r == 6 && c == 6) || (r == 6 && c == pos[len(pos)-1]) || (r == pos[len(pos)-1] && c == 6) {
				continue
			}
			res = append(res, [2]int{r, c})
		}
	}
	return res
}

// alignmentCoords 返回版本 ver 的对齐图案中心行/列坐标集合。
func alignmentCoords(ver int) []int {
	// ISO 18004 表 1 的对齐图案坐标（按版本）。版本 1 无对齐图案。
	coords := [][]int{
		{},          // 0
		{},          // 1
		{6, 18},     // 2
		{6, 22},     // 3
		{6, 26},     // 4
		{6, 30},     // 5  (注：坐标值为 6,30，对应 37x37 模块；旧误写为 34)
		{6, 34},     // 6
		{6, 22, 38}, // 7
		{6, 24, 42}, // 8
		{6, 26, 46}, // 9
		{6, 30, 54}, // 10
	}
	return coords[ver]
}

// maskCondition 判断在 (row,col) 处是否应翻转模块（掩码公式 0~7）。
func maskCondition(maskID, row, col int) bool {
	switch maskID {
	case 0:
		return (row+col)%2 == 0
	case 1:
		return row%2 == 0
	case 2:
		return col%3 == 0
	case 3:
		return (row+col)%3 == 0
	case 4:
		return (row/2+col/3)%2 == 0
	case 5:
		return (row*col)%2+(row*col)%3 == 0
	case 6:
		return ((row*col)%2+(row*col)%3)%2 == 0
	case 7:
		return ((row+col)%2+(row*col)%3)%2 == 0
	}
	return false
}

// placeFunctionPatterns 绘制所有功能图形（定位/分隔/时序/对齐）。
// 注意：格式信息在选好掩码后再画。
func placeFunctionPatterns(grid [][]bool, ver int) {
	size := len(grid)
	// 三个定位图案 + 分隔符
	placeFinder(grid, 0, 0)
	placeFinder(grid, size-7, 0)
	placeFinder(grid, 0, size-7)
	// 分隔符（定位图案与数据之间的白边）已在 placeFinder 外由 isFunction 跳过，
	// 这里显式把分隔符置 0，保证是白模块。
	for i := 0; i < 8; i++ {
		if i != 6 {
			grid[7][i] = false
			grid[i][7] = false
		}
	}
	for i := 0; i < 8; i++ {
		grid[7][size-1-i] = false
		grid[size-1-i][7] = false
	}
	// 时序图案
	for i := 8; i < size-8; i++ {
		on := i%2 == 0
		grid[6][i] = on
		grid[i][6] = on
	}
	// 对齐图案
	for _, p := range alignmentPositions(ver) {
		placeAlignment(grid, p[0], p[1])
	}
}

func placeFinder(grid [][]bool, x, y int) {
	for i := 0; i < 7; i++ {
		for j := 0; j < 7; j++ {
			on := i == 0 || i == 6 || j == 0 || j == 6 || (i >= 2 && i <= 4 && j >= 2 && j <= 4)
			grid[y+i][x+j] = on
		}
	}
}

func placeAlignment(grid [][]bool, ar, ac int) {
	for i := -2; i <= 2; i++ {
		for j := -2; j <= 2; j++ {
			on := i == -2 || i == 2 || j == -2 || j == 2 || (i == 0 && j == 0)
			grid[ar+i][ac+j] = on
		}
	}
}

// placeData 把数据码流以之字形填入（跳过功能图形），并就地应用掩码 maskID。
// 标准 ISO 18004 放置规则：从右下角开始，每次取两列组成一对，交替向上/向下走；
// 每对里先填右列再填左列；每个模块按字节高位(MSB)优先取数据位，再按掩码条件翻转。
func placeData(grid [][]bool, ver int, data []byte, maskID int) {
	size := len(grid)
	bit := 0
	byteIdx := 0
	gotBit := func() int {
		if byteIdx >= len(data) {
			return 0
		}
		// bit 为当前字节内的位序号：0=最高位(MSB) … 7=最低位(LSB)
		b := int((data[byteIdx] >> uint(7-bit)) & 1)
		bit++
		if bit == 8 {
			bit = 0
			byteIdx++
		}
		return b
	}
	up := true // 最右列对从底行向上走（与参考实现 inc=-1、row 起始底部一致）
	// 列对从右往左，步长 2（与 Python 参考的 range(count-1,0,-2) 一致）。
	// 当时序列 col<=6 时，把这一对整体左移到 (col-1, col-2)，但不改变下一对的步长。
	for col := size - 1; col > 0; col -= 2 {
		rc := col
		if rc <= 6 {
			rc-- // 跳过第 6 列（时序图案），本列对改为 (rc-1, rc-2)
		}
		// 方向在每一对列之间翻转（到达上下边缘后才翻，等效于逐对交替）。
		for i := 0; i < size; i++ {
			row := i
			if up {
				row = size - 1 - i
			}
			for k := 0; k < 2; k++ {
				c := rc - k
				if isFunction(row, c, size, ver) {
					continue
				}
				v := gotBit()
				if maskID >= 0 && maskCondition(maskID, row, c) {
					v ^= 1
				}
				grid[row][c] = v == 1
			}
		}
		up = !up
	}
}

// formatBits 计算 15 位格式信息（EC 级别 + 掩码编号，含 BCH 纠错）。
// EC 级别 L 的 2 位指示符为 01。
func formatBits(maskID int) int {
	ec := 0b01                          // L
	data := (ec << 3) | (maskID & 0x07) // 5 位
	// BCH(15,5)：生成多项式 0x537
	rem := data << 10
	gen := 0x537
	for i := 14; i >= 10; i-- {
		if (rem>>uint(i))&1 != 0 {
			rem ^= gen << uint(i-10)
		}
	}
	res := (data << 10) | (rem & 0x3FF)
	res ^= 0x5412 // 固定掩码
	return res & 0x7FFF
}

// placeFormat 绘制格式信息（两种副本：围绕左上定位图案、以及右上与左下），
// 坐标严格对齐 ISO 18004 与各参考实现的 setup_type_info。bit 索引 i 采用 LSB 优先
// （与参考库 (bits>>i)&1 一致），i=0 为最低位。
func placeFormat(grid [][]bool, ver int, maskID int) {
	size := len(grid)
	bits := formatBits(maskID)
	// 竖向副本（列 8）：行 0~5、行 7~8、以及底部行 size-7~size-1
	for i := 0; i < 15; i++ {
		v := (bits>>uint(i))&1 == 1
		if i < 6 {
			grid[i][8] = v
		} else if i < 8 {
			grid[i+1][8] = v // i=6 -> 行7, i=7 -> 行8
		} else {
			grid[size-15+i][8] = v // i=8 -> 行 size-7 ... i=14 -> 行 size-1
		}
	}
	// 横向副本（行 8）：右侧 size-1..size-8、列 7、左侧 6..0
	for i := 0; i < 15; i++ {
		v := (bits>>uint(i))&1 == 1
		if i < 8 {
			grid[8][size-1-i] = v // 右侧
		} else if i == 8 {
			grid[8][7] = v // 列 7 的特殊位
		} else {
			grid[8][14-i] = v // 左侧：i=9->列5 ... i=14->列0
		}
	}
	// 固定黑模块（总是深色）
	grid[size-8][8] = true
}

// penalty 计算掩码惩罚分（ISO 18004 规则1~4），分数越低越好。
func penalty(grid [][]bool, ver int) int {
	size := len(grid)
	score := 0
	// 规则1：相邻同色成行/列的段长度惩罚
	for r := 0; r < size; r++ {
		run := 1
		for c := 1; c < size; c++ {
			if grid[r][c] == grid[r][c-1] {
				run++
			} else {
				score += pen(run)
				run = 1
			}
		}
		score += pen(run)
	}
	for c := 0; c < size; c++ {
		run := 1
		for r := 1; r < size; r++ {
			if grid[r][c] == grid[r-1][c] {
				run++
			} else {
				score += pen(run)
				run = 1
			}
		}
		score += pen(run)
	}
	// 规则2：2x2 同色块
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			b := grid[r][c]
			if b == grid[r][c+1] && b == grid[r+1][c] && b == grid[r+1][c+1] {
				score += 3
			}
		}
	}
	// 规则3：类似定位图案的出现（1:1:3:1:1 暗亮暗亮暗，前后 >=4 个浅）
	pat := func(seq []bool) bool {
		// 寻找模式 暗亮暗暗暗亮亮暗（10111010000）或其反向 00001011101（定位图案变体）。
		target := []int{1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0}
		target2 := []int{0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1}
		if len(seq) < 11 {
			return false
		}
		for off := 0; off <= len(seq)-11; off++ {
			ok1, ok2 := true, true
			for i := 0; i < 11; i++ {
				if (seq[off+i] && 1 != target[i]) || (!seq[off+i] && target[i] == 1) {
					ok1 = false
				}
				if (seq[off+i] && 1 != target2[i]) || (!seq[off+i] && target2[i] == 1) {
					ok2 = false
				}
			}
			if ok1 || ok2 {
				return true
			}
		}
		return false
	}
	for r := 0; r < size; r++ {
		seq := make([]bool, size)
		for c := 0; c < size; c++ {
			seq[c] = grid[r][c]
		}
		if pat(seq) {
			score += 40
		}
	}
	for c := 0; c < size; c++ {
		seq := make([]bool, size)
		for r := 0; r < size; r++ {
			seq[r] = grid[r][c]
		}
		if pat(seq) {
			score += 40
		}
	}
	// 规则4：黑白比例偏离 50% 的惩罚
	dark := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if grid[r][c] {
				dark++
			}
		}
	}
	total := size * size
	percent := dark * 100 / total
	// 与参考实现一致：直接截断（向零取整），而非四舍五入到最近的 5% 整数倍。
	rating := absInt(percent-50) / 5
	score += rating * 10
	return score
}

func pen(run int) int {
	if run >= 5 {
		return 3 + run - 5
	}
	return 0
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// buildMatrix 绘制功能图形、尝试所有 8 种掩码、选惩罚最低者，并写回格式信息。
func buildMatrix(ver int, data []byte) [][]bool {
	size := 17 + ver*4
	best := make([][]bool, size)
	bestScore := 1 << 60
	for mask := 0; mask < 8; mask++ {
		grid := make([][]bool, size)
		for i := range grid {
			grid[i] = make([]bool, size)
		}
		placeFunctionPatterns(grid, ver)
		placeData(grid, ver, data, mask)
		placeFormat(grid, ver, mask)
		s := penalty(grid, ver)
		if s < bestScore {
			bestScore = s
			best = grid
		}
	}
	return best
}
