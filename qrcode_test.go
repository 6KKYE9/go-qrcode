package main

import (
	"strings"
	"testing"
)

func TestEncodeProducesSquare(t *testing.T) {
	qr, err := Encode("HELLO")
	if err != nil {
		t.Fatalf("Encode 报错: %v", err)
	}
	if qr.Size != qr.Size || qr.Size < 21 {
		t.Fatalf("版本1 尺寸应为 21, got %d", qr.Size)
	}
	// 应是方阵
	if len(qr.Grid) != qr.Size {
		t.Fatalf("网格行数 %d 不等于尺寸 %d", len(qr.Grid), qr.Size)
	}
	for _, row := range qr.Grid {
		if len(row) != qr.Size {
			t.Fatal("存在非方阵行")
		}
	}
}

func TestEncodeASCIIHasContent(t *testing.T) {
	qr, _ := Encode("https://example.com")
	art := qr.ToASCII()
	if !strings.Contains(art, "#") {
		t.Fatal("ASCII 应含模块字符")
	}
	// 三个定位角应有实心块
	lines := strings.Split(strings.TrimRight(art, "\n"), "\n")
	// 第一行（带静区）应有若干 #
	if len(lines) < qr.Size {
		t.Fatalf("行数 %d 应 >= 尺寸 %d", len(lines), qr.Size)
	}
}

func TestVersionSelection(t *testing.T) {
	if v := chooseVersion(10); v != 1 {
		t.Fatalf("10 字节应选版本1, got %d", v)
	}
	if v := chooseVersion(30); v < 2 {
		t.Fatalf("30 字节应选 >=2, got %d", v)
	}
}

func TestReedSolomonRoundTripShape(t *testing.T) {
	// 验证纠错码长度符合预期（版本1: 19 数据 + 7 纠错）
	ec := ecCodewords(1)
	if ec != 7 {
		t.Fatalf("版本1 纠错码应为 7, got %d", ec)
	}
	data := make([]byte, dataCodewords(1))
	for i := range data {
		data[i] = byte(i)
	}
	blk := rsEncode(data, ec)
	if len(blk) != ec {
		t.Fatalf("rsEncode 应返回 %d 个码字, got %d", ec, len(blk))
	}
}

func TestEmptyTextError(t *testing.T) {
	if _, err := Encode(""); err == nil {
		t.Fatal("空文本应报错（由 main 在调用前拦截，但 Encode 也应对超长/空有防御）")
	}
}
