package relaykitbridge

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
)

func TestMediaResolverRejectsMIMESizeAndHonorsCancel(t *testing.T) {
	_, _, err := decodeBase64FileData("data:application/javascript;base64," + base64.StdEncoding.EncodeToString([]byte("alert(1)")))
	if err == nil || !strings.Contains(err.Error(), "MIME") {
		t.Fatalf("javascript MIME error = %v", err)
	}

	huge := base64.StdEncoding.EncodeToString(make([]byte, maxMediaBytes+1))
	if _, _, err := decodeBase64FileData(huge); err == nil || !strings.Contains(err.Error(), "20 MiB") {
		t.Fatalf("oversize error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = resolveMediaURL(ctx, types.NewURLFileSource("https://example.com/image.png"))
	if err == nil {
		t.Fatal("expected cancellation failure")
	}
}

func TestMediaResolverRevalidatesRedirects(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "image/png")
		_, _ = writer.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	}))
	defer final.Close()

	redirects := 0
	hop := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirects++
		http.Redirect(writer, request, "http://127.0.0.1/private.png", http.StatusFound)
	}))
	defer hop.Close()

	// checkedMediaURL requires https; exercise redirect policy helper directly.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return io.EOF
			}
			_, err := checkedMediaURL(req.Context(), req.URL.String())
			return err
		},
	}
	response, err := client.Get(hop.URL)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("redirect to loopback should fail")
	}
	if redirects == 0 {
		t.Fatal("expected redirect attempt")
	}
}

func TestDecodeBase64AcceptsImageAudioPDF(t *testing.T) {
	for _, mimeType := range []string{"image/png", "audio/mpeg", "application/pdf"} {
		payload := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString([]byte("hello-media"))
		gotMIME, err := func() (string, error) {
			_, mime, err := decodeBase64FileData(payload)
			return mime, err
		}()
		if err != nil || gotMIME != mimeType {
			t.Fatalf("mime=%s got=%q err=%v", mimeType, gotMIME, err)
		}
	}
}
