package transport

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func compressedBody(t *testing.T, body []byte, encoding string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var writer io.WriteCloser
	if encoding == "gzip" {
		writer = gzip.NewWriter(&buffer)
	} else {
		writer = zlib.NewWriter(&buffer)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestDecodeBodyContentEncodings(t *testing.T) {
	plain := []byte(`{"data":[{"id":"test-model"}]}`)
	gzipped := compressedBody(t, plain, "gzip")
	for _, test := range []struct {
		encoding string
		body     []byte
	}{
		{"", plain},
		{"identity", plain},
		{"gzip", gzipped},
		{" GZip ", gzipped},
		{"deflate", compressedBody(t, plain, "deflate")},
		{"gzip, identity, deflate", compressedBody(t, gzipped, "deflate")},
	} {
		t.Run(test.encoding, func(t *testing.T) {
			body, err := DecodeBody(test.body, test.encoding, 1024)
			if err != nil || !bytes.Equal(body, plain) {
				t.Fatalf("decoded = %q, err = %v", body, err)
			}
		})
	}
}

func TestDecodeBodyRejectsInvalidAndOversizedEntities(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 2048)
	gzipped := compressedBody(t, plain, "gzip")
	corrupt := bytes.Clone(gzipped)
	corrupt[len(corrupt)-8] ^= 1
	for _, test := range []struct {
		name, encoding string
		body           []byte
		limit          int64
		wantErr        error
	}{
		{"wire limit", "identity", plain, 1024, ErrResponseBodyTooLarge},
		{"gzip expansion", "gzip", gzipped, 1024, ErrResponseBodyTooLarge},
		{"deflate expansion", "deflate", compressedBody(t, plain, "deflate"), 1024, ErrResponseBodyTooLarge},
		{"intermediate limit", "gzip, deflate", compressedBody(t, bytes.Repeat([]byte("x"), 1025), "deflate"), 1024, ErrResponseBodyTooLarge},
		{"unknown encoding", "br", plain, 4096, nil},
		{"malformed chain", "gzip,", gzipped, 4096, nil},
		{"too many layers", "gzip,gzip,gzip,gzip,gzip", gzipped, 4096, nil},
		{"invalid gzip header", "gzip", plain, 4096, nil},
		{"invalid deflate header", "deflate", plain, 4096, nil},
		{"truncated gzip", "gzip", gzipped[:len(gzipped)-1], 4096, io.ErrUnexpectedEOF},
		{"gzip checksum", "gzip", corrupt, 4096, gzip.ErrChecksum},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := DecodeBody(test.body, test.encoding, test.limit)
			if err == nil || body != nil || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Fatalf("decoded %d bytes, err = %v, want %v", len(body), err, test.wantErr)
			}
		})
	}
	if _, err := DecodeBody(plain, "", int64(len(plain))); err != nil {
		t.Fatalf("exact bound should succeed: %v", err)
	}
	if _, err := DecodeBody(gzipped, "gzip", int64(len(plain))); err != nil {
		t.Fatalf("exact decoded bound should succeed: %v", err)
	}
}

func TestReadResponseBodyNormalizesDecodedHeaders(t *testing.T) {
	plain := []byte(`{"ok":true}`)
	encoded := compressedBody(t, compressedBody(t, plain, "gzip"), "deflate")
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Encoding": {"gzip", "deflate"},
			"Content-Length":   {strconv.Itoa(len(encoded))},
			"Content-Type":     {"application/json"},
		},
		ContentLength: int64(len(encoded)),
		Body:          io.NopCloser(bytes.NewReader(encoded)),
	}
	defer response.Body.Close()
	body, err := ReadResponseBody(response, 1024)
	if err != nil || !bytes.Equal(body, plain) {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	if response.Header.Get("Content-Encoding") != "" || response.Header.Get("Content-Length") != "" ||
		response.Header.Get("Content-Type") != "application/json" || response.ContentLength != -1 || !response.Uncompressed {
		t.Fatalf("decoded metadata = %#v", response)
	}
}

func TestReadResponseBodyBoundsReadsAndPreservesPlainHeaders(t *testing.T) {
	for _, size := range []int{1024, 1025, 4096} {
		reader := strings.NewReader(strings.Repeat("x", size))
		response := &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Length": {strconv.Itoa(size)}},
			ContentLength: int64(size),
			Body:          io.NopCloser(reader),
		}
		body, err := ReadResponseBody(response, 1024)
		_ = response.Body.Close()
		if size == 1024 && (err != nil || len(body) != size) {
			t.Fatalf("exact bound = %d bytes, err = %v", len(body), err)
		}
		if size > 1024 && !errors.Is(err, ErrResponseBodyTooLarge) {
			t.Fatalf("overflow err = %v", err)
		}
		if size-reader.Len() > 1025 {
			t.Fatal("read past the wire byte limit")
		}
		if response.ContentLength != int64(size) || response.Uncompressed || response.Header.Get("Content-Length") != strconv.Itoa(size) {
			t.Fatal("plain response metadata changed")
		}
	}
}

func TestReadResponseBodyHandlesAutomaticGzipAndBodylessResponses(t *testing.T) {
	encoded := compressedBody(t, []byte(`{"ok":true}`), "gzip")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()
	response, err := upstream.Client().Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !response.Uncompressed {
		t.Fatal("expected net/http transparent gzip")
	}
	if body, err := ReadResponseBody(response, 1024); err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("automatically decoded body = %q, err = %v", body, err)
	}
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		response := &http.Response{StatusCode: status, Header: http.Header{"Content-Encoding": {"gzip"}}, Body: http.NoBody}
		if body, err := ReadResponseBody(response, 1024); err != nil || len(body) != 0 {
			t.Fatalf("bodyless status %d = %q, err = %v", status, body, err)
		}
	}
}

func TestForwardPreservesCompressedResponse(t *testing.T) {
	encoded := compressedBody(t, []byte(`{"ok":true}`), "gzip")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept-Encoding") != "gzip, br" {
			t.Errorf("client compression negotiation changed: %q", request.Header.Get("Accept-Encoding"))
		}
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Header.Set("Accept-Encoding", "gzip, br")
	response := httptest.NewRecorder()
	if err := New(nil).Forward(response, request, Target{BaseURL: mustParseURL(t, upstream.URL)}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response.Body.Bytes(), encoded) || response.Header().Get("Content-Encoding") != "gzip" ||
		response.Header().Get("Content-Length") != strconv.Itoa(len(encoded)) {
		t.Fatal("transparent forwarding changed compressed response")
	}
}
