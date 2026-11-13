package plugin

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"hash/crc32"
	"io"
	"strconv"

	"github.com/andybalholm/brotli"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// maxInflated bounds what one decompression produces, against archives
// that expand without end.
const maxInflated = 256 << 20

// errInflatedTooLarge is a decompression past its bound.
var errInflatedTooLarge = errors.New("too large")

// zlibFunctions are the host's side of node:zlib: whole buffers in, whole
// buffers out. node/zlib.js gathers what a stream receives and runs the
// codec when it ends.
func (bound *instance) zlibFunctions() map[string]any {
	return map[string]any{
		"process": bound.function("process", 4, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			op, err := stringArg(realm, args, 0, "operation")
			if err != nil {
				return moejs.Undefined(), err
			}
			data, ok := bytesOf(moejs.Arg(args, 1))
			if !ok {
				return moejs.Undefined(), realm.TypeError("the data must be bytes")
			}
			level := -1
			if value := moejs.Arg(args, 2); !value.IsUndefined() && !value.IsNull() {
				number, err := realm.ToNumber(value)
				if err != nil {
					return moejs.Undefined(), err
				}
				level = int(number)
			}
			limit := maxInflated
			if value := moejs.Arg(args, 3); !value.IsUndefined() && !value.IsNull() {
				number, err := realm.ToNumber(value)
				if err != nil {
					return moejs.Undefined(), err
				}
				limit = min(int(number), maxInflated)
			}
			input := bytes.Clone(data)
			var output []byte
			bound.blocking(func() { output, err = runZlib(op, input, level, limit) })
			if err != nil {
				return moejs.Undefined(), zlibError(realm, err, limit)
			}
			return bytesValue(realm, output)
		}),
		"crc32": bound.function("crc32", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			data, ok := bytesOf(moejs.Arg(args, 0))
			if !ok {
				return moejs.Undefined(), realm.TypeError("the data must be bytes")
			}
			initial, err := realm.ToUint32(moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Number(float64(crc32.Update(initial, crc32.IEEETable, data))), nil
		}),
	}
}

// runZlib runs the codec op of node:zlib over data.
func runZlib(op string, data []byte, level, limit int) ([]byte, error) {
	var output bytes.Buffer
	compress := func(writer io.WriteCloser, err error) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return output.Bytes(), nil
	}
	inflate := func(reader io.Reader, err error) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(&output, io.LimitReader(reader, int64(limit)+1))
		if err != nil {
			return nil, err
		}
		if n > int64(limit) {
			return nil, errInflatedTooLarge
		}
		return output.Bytes(), nil
	}
	if level < -1 || level > 9 {
		level = -1
	}
	switch op {
	case "gzip":
		return compress(gzip.NewWriterLevel(&output, level))
	case "deflate":
		return compress(zlib.NewWriterLevel(&output, level))
	case "deflateRaw":
		return compress(flate.NewWriter(&output, level))
	case "gunzip":
		return inflate(gzipReader(data))
	case "inflate":
		return inflate(zlib.NewReader(bytes.NewReader(data)))
	case "inflateRaw":
		return inflate(flate.NewReader(bytes.NewReader(data)), nil)
	case "unzip":
		if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			return inflate(gzipReader(data))
		}
		return inflate(zlib.NewReader(bytes.NewReader(data)))
	case "brotliCompress":
		quality := level
		if quality < 0 || quality > 11 {
			quality = brotli.DefaultCompression
		}
		return compress(brotli.NewWriterLevel(&output, quality), nil)
	case "brotliDecompress":
		return inflate(brotli.NewReader(bytes.NewReader(data)), nil)
	}
	return nil, errors.New("unknown operation " + op)
}

// gzipReader reads data as gzip, members after the first included, as Node
// does.
func gzipReader(data []byte) (io.Reader, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	reader.Multistream(true)
	return reader, nil
}

// zlibError throws the error node:zlib reports for what a codec failed
// with: its message, errno and code.
func zlibError(realm *moejs.Realm, err error, limit int) error {
	message, code, errno := err.Error(), "Z_DATA_ERROR", -3
	var corrupt flate.CorruptInputError
	switch {
	case errors.Is(err, errInflatedTooLarge):
		return cryptoError(realm, "ERR_BUFFER_TOO_LARGE", "Cannot create a Buffer larger than "+strconv.Itoa(limit)+" bytes")
	case errors.Is(err, gzip.ErrHeader), errors.Is(err, zlib.ErrHeader):
		message = "incorrect header check"
	case errors.Is(err, gzip.ErrChecksum), errors.Is(err, zlib.ErrChecksum):
		message = "incorrect data check"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		message, code, errno = "unexpected end of file", "Z_BUF_ERROR", -5
	case errors.As(err, &corrupt):
		message = "invalid stored block lengths"
	}
	object := realm.NewError(engine.KindError, "%s", message)
	_, _ = object.CreateDataProperty(realm, realm.KeyFromGoString("errno"), moejs.Int(int64(errno)))
	_, _ = object.CreateDataProperty(realm, realm.KeyFromGoString("code"), moejs.String(code))
	return realm.Throw(engine.ObjectValue(object))
}
