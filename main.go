// go-qrcode 是一个零依赖的二维码生成器，把文本/URL 编码成 QR 码并打印到终端。
// 仅实现字节模式 + L 级纠错，足以应付短链接、文本、WiFi 等常见场景。
// 参考 ISO/IEC 18004 的 Reed-Solomon 与位流规范，纯标准库实现。
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	text := flag.String("text", "", "要编码的文本或 URL")
	file := flag.String("o", "", "可选：把 ASCII 二维码写到该文件（不指定则打印到终端）")
	help := flag.Bool("h", false, "显示帮助")
	flag.Parse()

	if *help || *text == "" {
		fmt.Print(`go-qrcode 二维码生成器（零依赖，纯标准库）

用法:
  go-qrcode -text "https://example.com"
  go-qrcode -text "hello" -o qr.txt

说明: 仅支持字节模式 + L 级纠错，适合短文本/链接/WiFi。
`)
		return
	}

	qr, err := Encode(*text)
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
	art := qr.ToASCII()
	if *file != "" {
		if err := os.WriteFile(*file, []byte(art), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写文件失败:", err)
			os.Exit(1)
		}
		fmt.Printf("已写入 %s（%dx%d 模块）\n", *file, qr.Size, qr.Size)
		return
	}
	fmt.Println(art)
}
