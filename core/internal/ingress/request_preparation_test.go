package ingress

import (
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassifiedBodyReplaysWithInboundCapture(t *testing.T) {
	body := []byte(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`)
	request := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	if _, err := classify(request, 0); err != nil {
		t.Fatal(err)
	}
	if request.GetBody == nil {
		t.Fatal("complete classified body must have a replay factory")
	}
	tee := &captureTee{ReadCloser: request.Body, buffer: captureBuffer{enabled: true, maxBytes: len(body)}}
	request.Body = tee
	source, err := captureRequestBody(request, true)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for i := 0; i < 2; i++ {
		attempt, ok, err := source.Next(request.Context())
		if err != nil || !ok {
			t.Fatalf("attempt %d: %v", i, err)
		}
		got, err := io.ReadAll(attempt.Body)
		_ = attempt.Body.Close()
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("attempt %d lost original bytes: %v", i, err)
		}
	}
	if captured := tee.snapshot(); !captured.complete || !bytes.Equal(captured.bytes, body) {
		t.Fatal("replay factory bypassed the inbound audit capture")
	}
}

func BenchmarkRequestPreparation(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		body := []byte(`{"model":"test","stream":true,"messages":[{"role":"user","content":"` + strings.Repeat("a", size) + `"}]}`)
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				request := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
				if _, err := classify(request, 0); err != nil {
					b.Fatal(err)
				}
				source, err := captureRequestBody(request, true)
				if err != nil {
					b.Fatal(err)
				}
				source.Close()
				_ = request.Body.Close()
			}
		})
	}
}
