package imagecompress

import (
	"bytes"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"

	"github.com/deepteams/webp"
	xdraw "golang.org/x/image/draw"
	xwebp "golang.org/x/image/webp"
)

// Bounds of an image a plugin has re-encoded: larger ones are left as they
// are.
const (
	maxReencodeBytes  = 20 << 20
	maxReencodePixels = 40_000_000
)

// Reencode re-encodes an image as a request processing plugin asks: format
// auto (JPEG, or PNG for an image with transparency), jpeg at quality,
// png, or webp, which is lossless; maxSide, when above zero, scales it down
// so neither side exceeds it. It returns the new image and its media type,
// or false to keep the image: one it cannot read, an animated GIF, one too
// large, or a result that is not smaller.
func Reencode(data []byte, format string, quality, maxSide int) ([]byte, string, bool) {
	if len(data) > maxReencodeBytes {
		return nil, "", false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxReencodePixels {
		return nil, "", false
	}
	var img image.Image
	switch {
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		all, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil || len(all.Image) != 1 {
			return nil, "", false
		}
		img = all.Image[0]
	case len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		img, err = xwebp.Decode(bytes.NewReader(data))
	default:
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	if err != nil || img == nil {
		return nil, "", false
	}
	bounds := img.Bounds()
	if longest := max(bounds.Dx(), bounds.Dy()); maxSide > 0 && longest > maxSide {
		width := max(1, bounds.Dx()*maxSide/longest)
		height := max(1, bounds.Dy()*maxSide/longest)
		scaled := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, bounds, draw.Src, nil)
		img = scaled
	}
	if format == "auto" {
		format = "jpeg"
		if transparent(img) {
			format = "png"
		}
	}
	var out bytes.Buffer
	mime := ""
	switch format {
	case "jpeg":
		if quality <= 0 {
			quality = 85
		}
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: min(quality, 100)}); err != nil {
			return nil, "", false
		}
		mime = "image/jpeg"
	case "png":
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&out, img); err != nil {
			return nil, "", false
		}
		mime = "image/png"
	case "webp":
		options := webp.DefaultOptions()
		options.Lossless, options.Exact = true, true
		if err := webp.Encode(&out, img, options); err != nil {
			return nil, "", false
		}
		mime = "image/webp"
	default:
		return nil, "", false
	}
	if out.Len() >= len(data) {
		return nil, "", false
	}
	return out.Bytes(), mime, true
}

// Size reads an image's width and height without decoding it.
func Size(data []byte) (int, int, bool) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			if webpConfig, err := xwebp.DecodeConfig(bytes.NewReader(data)); err == nil {
				return webpConfig.Width, webpConfig.Height, true
			}
		}
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// transparent reports whether an image has a pixel that is not opaque.
func transparent(img image.Image) bool {
	if opaque, ok := img.(interface{ Opaque() bool }); ok {
		return !opaque.Opaque()
	}
	return true
}
