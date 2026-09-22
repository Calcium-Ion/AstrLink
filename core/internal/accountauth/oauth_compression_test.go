package accountauth

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestTokenClientHandlesCompressedSuccessAndInvalidGrant(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		body := `{"access_token":"access-test","refresh_token":"refresh-test","expires_in":3600}`
		if invalid {
			body = `{"error":"invalid_grant"}`
		}
		var encoded bytes.Buffer
		compressor := gzip.NewWriter(&encoded)
		_, _ = compressor.Write([]byte(body))
		_ = compressor.Close()
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("originator") != DefaultCodexOriginator || request.UserAgent() != CodexUserAgent("") || request.Header.Get("version") != "" {
				t.Error("token request must use the Codex auth identity without an inference version header")
			}
			if request.Header.Get("Accept-Encoding") != transport.SupportedResponseEncodings {
				t.Errorf("Accept-Encoding = %q", request.Header.Get("Accept-Encoding"))
			}
			writer.Header().Set("Content-Encoding", "gzip")
			if invalid {
				writer.WriteHeader(http.StatusBadRequest)
			}
			_, _ = writer.Write(encoded.Bytes())
		}))
		tokens, err := NewTokenClient(OAuthConfig{Issuer: upstream.URL, HTTPClient: upstream.Client()}).Refresh(context.Background(), "refresh-test")
		upstream.Close()
		if invalid {
			if !errors.Is(err, ErrInvalidGrant) {
				t.Fatalf("compressed invalid_grant error = %v", err)
			}
		} else if err != nil || tokens.AccessToken != "access-test" {
			t.Fatalf("compressed token response error = %v", err)
		}
	}
}
