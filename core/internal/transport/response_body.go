package transport

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// SupportedResponseEncodings is for callers that consume and decode upstream
// bodies locally. Transparent proxies must preserve the client's negotiation.
const SupportedResponseEncodings = "gzip, deflate"

// ErrResponseBodyTooLarge covers both wire and decoded size limits.
var ErrResponseBodyTooLarge = errors.New("HTTP response body exceeds limit")

// ErrUnsupportedContentEncoding marks a coding DecodeBody cannot decode.
var ErrUnsupportedContentEncoding = errors.New("unsupported HTTP content encoding")

// maxZstdWindow caps the history a zstd frame may ask the decoder to keep.
// Every standard level, long-distance matching included, stays within it.
const maxZstdWindow = 128 << 20

// ReadResponseBody reads a bounded, decoded HTTP entity. The caller still owns
// response.Body and must close it. Decoded responses lose their wire encoding
// and length, matching net/http's transparent gzip handling.
func ReadResponseBody(response *http.Response, limit int64) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errors.New("HTTP response body is missing")
	}
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified ||
		(response.Request != nil && response.Request.Method == http.MethodHead) {
		return nil, nil
	}
	body, err := readBoundedBody(response.Body, limit)
	if err != nil {
		return nil, err
	}
	encoding := strings.Join(response.Header.Values("Content-Encoding"), ",")
	body, err = DecodeBody(body, encoding, limit)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoding) != "" {
		response.Header.Del("Content-Encoding")
		response.Header.Del("Content-Length")
		response.ContentLength = -1
		response.Uncompressed = true
	}
	return body, nil
}

// DecodeBody decodes an already buffered HTTP entity, a response or a request.
// Wire bytes and every decoded layer share the same bound, including stacked
// content codings.
func DecodeBody(body []byte, contentEncoding string, limit int64) ([]byte, error) {
	if limit < 0 || limit == 1<<63-1 {
		return nil, errors.New("invalid HTTP response body limit")
	}
	if int64(len(body)) > limit {
		return nil, ErrResponseBodyTooLarge
	}
	if strings.TrimSpace(contentEncoding) == "" {
		return body, nil
	}
	encodings, err := contentCodings(contentEncoding)
	if err != nil {
		return nil, err
	}
	// Content-Encoding lists codings in the order they were applied.
	for index := len(encodings) - 1; index >= 0; index-- {
		if encodings[index] == "identity" {
			continue
		}
		reader, err := codingReader(encodings[index], body)
		if err != nil {
			return nil, fmt.Errorf("decode HTTP %s body: %w", encodings[index], err)
		}
		body, err = readBoundedBody(reader, limit)
		_ = reader.Close()
		if err != nil {
			return nil, fmt.Errorf("decode HTTP %s body: %w", encodings[index], err)
		}
	}
	return body, nil
}

// DecodableContentEncoding reports whether DecodeBody can decode every coding
// that contentEncoding lists.
func DecodableContentEncoding(contentEncoding string) bool {
	if strings.TrimSpace(contentEncoding) == "" {
		return true
	}
	_, err := contentCodings(contentEncoding)
	return err == nil
}

func contentCodings(contentEncoding string) ([]string, error) {
	encodings := strings.Split(strings.ToLower(contentEncoding), ",")
	if len(encodings) > 4 {
		return nil, errors.New("too many HTTP content encodings")
	}
	for index, encoding := range encodings {
		encodings[index] = strings.TrimSpace(encoding)
		switch encodings[index] {
		case "identity", "gzip", "x-gzip", "deflate", "br", "zstd":
		default:
			return nil, ErrUnsupportedContentEncoding
		}
	}
	return encodings, nil
}

func codingReader(encoding string, body []byte) (io.ReadCloser, error) {
	switch encoding {
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		return reader, nil
	case "deflate":
		return zlib.NewReader(bytes.NewReader(body))
	case "br":
		return io.NopCloser(brotli.NewReader(bytes.NewReader(body))), nil
	case "zstd":
		decoder, err := zstd.NewReader(
			bytes.NewReader(body),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(maxZstdWindow),
		)
		if err != nil {
			return nil, err
		}
		return decoder.IOReadCloser(), nil
	}
	return nil, ErrUnsupportedContentEncoding
}

func readBoundedBody(reader io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit == 1<<63-1 {
		return nil, errors.New("invalid HTTP response body limit")
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, ErrResponseBodyTooLarge
	}
	return body, nil
}
