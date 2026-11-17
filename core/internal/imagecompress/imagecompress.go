// Package imagecompress re-encodes request images as WebP: lossless with
// moego's encoder, lossy with deepteams/webp, since moego writes lossless
// WebP only.
package imagecompress

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"sync"

	"github.com/MoeForge/moego/webp"
	deepwebp "github.com/deepteams/webp"
)

const (
	// Smaller images save too little to be worth re-encoding.
	minImageBytes = 2 << 10
	// maxPixels bounds the memory one decode and encode can take.
	maxPixels = 25_000_000
	// The cache keeps recent results so the images a conversation resends
	// every turn are encoded once and stay byte for byte the same.
	maxCacheBytes   = 64 << 20
	maxCacheEntries = 4096
	cacheEntryBytes = 96
)

// Options chooses the encoding: lossless, or lossy at Quality (1-100).
type Options struct {
	Lossy   bool
	Quality int
}

type cacheKey struct {
	sum     [sha256.Size]byte
	lossy   bool
	quality int
}

type cacheEntry struct {
	key cacheKey
	// webp is nil when the image is kept as it is.
	webp []byte
}

// Compressor encodes images as WebP. The zero value is ready to use and safe
// for concurrent use.
type Compressor struct {
	once  sync.Once
	slots chan struct{}

	mu      sync.Mutex
	entries map[cacheKey]*list.Element
	order   list.List
	bytes   int
}

func (compressor *Compressor) init() {
	compressor.once.Do(func() {
		compressor.slots = make(chan struct{}, min(max(runtime.GOMAXPROCS(0)/2, 1), 4))
		compressor.entries = make(map[cacheKey]*list.Element)
	})
}

// WebP returns data re-encoded as WebP, or false to keep data as it is: it
// is not a PNG, single-frame GIF or, for lossy options, JPEG image; it is
// very small or very large; it carries a 16-bit depth a lossless WebP cannot
// hold, CMYK color or an EXIF rotation; or no WebP is smaller. A done ctx
// keeps data without waiting for an encoder.
func (compressor *Compressor) WebP(ctx context.Context, data []byte, options Options) ([]byte, bool) {
	if len(data) < minImageBytes {
		return nil, false
	}
	compressor.init()
	key := cacheKey{sum: sha256.Sum256(data), lossy: options.Lossy}
	if options.Lossy {
		key.quality = min(max(options.Quality, 1), 100)
	}
	if encoded, found := compressor.lookup(key); found {
		return encoded, encoded != nil
	}
	if ctx.Err() != nil {
		return nil, false
	}
	select {
	case compressor.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, false
	}
	encoded := encode(data, key.lossy, key.quality)
	<-compressor.slots
	compressor.store(key, encoded)
	return encoded, encoded != nil
}

func (compressor *Compressor) lookup(key cacheKey) ([]byte, bool) {
	compressor.mu.Lock()
	defer compressor.mu.Unlock()
	element, found := compressor.entries[key]
	if !found {
		return nil, false
	}
	compressor.order.MoveToFront(element)
	return element.Value.(*cacheEntry).webp, true
}

func (compressor *Compressor) store(key cacheKey, encoded []byte) {
	size := len(encoded) + cacheEntryBytes
	if size > maxCacheBytes {
		return
	}
	compressor.mu.Lock()
	defer compressor.mu.Unlock()
	if _, found := compressor.entries[key]; found {
		return
	}
	compressor.entries[key] = compressor.order.PushFront(&cacheEntry{key: key, webp: encoded})
	compressor.bytes += size
	for compressor.bytes > maxCacheBytes || len(compressor.entries) > maxCacheEntries {
		oldest := compressor.order.Back()
		entry := compressor.order.Remove(oldest).(*cacheEntry)
		delete(compressor.entries, entry.key)
		compressor.bytes -= len(entry.webp) + cacheEntryBytes
	}
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// encode returns the smallest WebP the options allow, nil when none is
// smaller than data. A lossy option also tries a lossless WebP of a source it
// holds exactly: flat screenshots and diagrams often come out smaller that way.
func encode(data []byte, lossy bool, quality int) []byte {
	img, exact, ok := decode(data, lossy)
	if !ok {
		return nil
	}
	var best []byte
	keep := func(encoded []byte) {
		if encoded != nil && len(encoded) < len(data) && (best == nil || len(encoded) < len(best)) {
			best = encoded
		}
	}
	if lossy {
		keep(lossyWebP(img, quality))
	}
	if exact {
		keep(losslessWebP(img))
	}
	return best
}

func lossyWebP(img image.Image, quality int) []byte {
	options := deepwebp.DefaultOptions()
	options.Quality = float32(quality)
	var encoded bytes.Buffer
	if deepwebp.Encode(&encoded, img, options) != nil {
		return nil
	}
	return encoded.Bytes()
}

// losslessWebP keeps the color under transparent pixels too, so an upstream
// that drops the alpha channel still sees the original image. The default
// level runs in the request path: BestCompression saves a few percent more
// at about three times the encode time.
func losslessWebP(img image.Image) []byte {
	var encoded bytes.Buffer
	if webp.Encode(&encoded, img) != nil {
		return nil
	}
	return encoded.Bytes()
}

// decode decodes an image the options may re-encode, and reports whether a
// lossless WebP holds it exactly.
func decode(data []byte, lossy bool) (image.Image, bool, bool) {
	reader := bytes.NewReader(data)
	switch {
	case bytes.HasPrefix(data, pngSignature):
		config, err := png.DecodeConfig(reader)
		exact := !sixteenBit(config.ColorModel)
		if err != nil || !withinBounds(config) || (!lossy && !exact) {
			return nil, false, false
		}
		img, err := png.Decode(bytes.NewReader(data))
		return img, exact, err == nil
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		config, err := gif.DecodeConfig(reader)
		if err != nil || !withinBounds(config) {
			return nil, false, false
		}
		all, err := gif.DecodeAll(bytes.NewReader(data))
		// A frame smaller than the canvas shows the GIF's background around it.
		if err != nil || len(all.Image) != 1 || all.Image[0].Bounds() != image.Rect(0, 0, config.Width, config.Height) {
			return nil, false, false
		}
		return all.Image[0], true, true
	case lossy && bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		config, err := jpeg.DecodeConfig(reader)
		if err != nil || !withinBounds(config) || config.ColorModel == color.CMYKModel || exifRotated(data) {
			return nil, false, false
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		return img, false, err == nil
	default:
		return nil, false, false
	}
}

func withinBounds(config image.Config) bool {
	return config.Width > 0 && config.Height > 0 && int64(config.Width)*int64(config.Height) <= maxPixels
}

func sixteenBit(model color.Model) bool {
	return model == color.RGBA64Model || model == color.NRGBA64Model || model == color.Gray16Model
}

// exifRotated reports a JPEG whose EXIF orientation turns or mirrors it.
// WebP output drops the EXIF block, so such a photo would change its
// orientation for an upstream that honors the tag.
func exifRotated(data []byte) bool {
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			return false
		}
		marker := data[offset+1]
		switch {
		case marker == 0xff:
			offset++
			continue
		case marker == 0xd9 || marker == 0xda:
			return false
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			offset += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(data[offset+2:]))
		if length < 2 || offset+2+length > len(data) {
			return false
		}
		segment := data[offset+4 : offset+2+length]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			orientation := exifOrientation(segment[6:])
			return orientation >= 2 && orientation <= 8
		}
		offset += 2 + length
	}
	return false
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	directory := int(order.Uint32(tiff[4:]))
	if directory < 8 || directory+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[directory:]))
	for entry := directory + 2; count > 0 && entry+12 <= len(tiff); entry, count = entry+12, count-1 {
		if order.Uint16(tiff[entry:]) == 0x0112 {
			return int(order.Uint16(tiff[entry+8:]))
		}
	}
	return 0
}
