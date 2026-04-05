package matrix

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"

	_ "github.com/gen2brain/avif"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"
)

// 将过大图片等比例压缩至 1280x720 边界内，并统一返回 JPEG 字节流
func (c *Client) CompressImageTo720p(rawData []byte) (compressedData []byte, finalMimeType string, err error) {
	// 1. 将物理字节流解码为 image.Image 内存结构
	img, format, err := image.Decode(bytes.NewReader(rawData))
	if err != nil {
		// 如果遇到了无法解析的格式则打回
		return rawData, "", fmt.Errorf("decode image error: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	const maxWidth = 1280.0
	const maxHeight = 720.0

	// 2. 等比例缩放探测
	ratioX := maxWidth / float64(width)
	ratioY := maxHeight / float64(height)

	// 取长宽压缩比中更苛刻的那一个，确保长宽都绝对不会越界
	ratio := ratioX
	if ratioY < ratioX {
		ratio = ratioY
	}

	// 如果原图比 1280x720 还要小并且本身就是 jpeg 则打回
	if ratio >= 1.0 && format == "jpeg" {
		return rawData, "image/jpeg", nil
	}

	// 3. 计算压缩后的新像素尺寸
	newWidth := int(float64(width) * ratio)
	newHeight := int(float64(height) * ratio)

	// 如果只是因为格式不是 jpeg 而走到这里，保持原尺寸即可
	if ratio >= 1.0 {
		newWidth = width
		newHeight = height
	}

	// 4. 在内存中开辟一块新的画布
	dstImg := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// 5. 双线性插值算法，将原图平滑地映射到新画布上
	draw.BiLinear.Scale(dstImg, dstImg.Bounds(), img, bounds, draw.Src, nil)

	// 6. 将新画布通过高压缩比的 JPEG 引擎写入缓冲区
	var buf bytes.Buffer
	// Quality: 85 是画质与体积之间极其完美的黄金分割点
	err = jpeg.Encode(&buf, dstImg, &jpeg.Options{Quality: 85})
	if err != nil {
		return rawData, "", fmt.Errorf("encode jpeg error: %w", err)
	}

	return buf.Bytes(), "image/jpeg", nil
}
