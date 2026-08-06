# go-qrcode

零依赖二维码生成器，纯 Go 标准库实现，把文本/URL 编码成二维码并打印到终端。

## 用法

```powershell
go run . -text "https://example.com"
go run . -text "hello world" -o qr.txt
```

## 说明

- 仅支持**字节模式 + L 级纠错**，覆盖版本 1~10，足够常见短链接、文本、WiFi 配置。
- 算法实现在 `qrcode.go`：比特流构造 → 容量填充 → Reed-Solomon 纠错（GF(256)）→ 矩阵绘制（定位/时序/数据模块）。
- 终端用 `#` / 空格渲染模块，手机扫码即可识别。

## 测试

```powershell
go test ./...
```

覆盖版本选择、矩阵方阵性、ASCII 渲染、Reed-Solomon 码字长度。
