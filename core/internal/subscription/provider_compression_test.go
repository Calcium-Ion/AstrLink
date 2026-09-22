package subscription

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/accountauth"
)

func TestProviderReadsCompressedCatalogAndNormalizesResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		body := `{"id":"response-test","output":[]}`
		if request.URL.Path == "/models" {
			body = `{"models":[{"slug":"test-model","visibility":"list"}]}`
		}
		var encoded bytes.Buffer
		compressor := gzip.NewWriter(&encoded)
		_, _ = compressor.Write([]byte(body))
		_ = compressor.Close()
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", strconv.Itoa(encoded.Len()))
		_, _ = writer.Write(encoded.Bytes())
	}))
	defer upstream.Close()
	client := upstream.Client()
	configured := http.DefaultTransport.(*http.Transport).Clone()
	configured.DisableCompression = true
	defer configured.CloseIdleConnections()
	client.Transport = configured
	provider := NewCodexProvider(accountauth.OAuthConfig{APIBaseURL: upstream.URL, HTTPClient: client})
	tokens := accountauth.AccountTokens{AccessToken: "test-token"}
	models, err := provider.ListModels(context.Background(), tokens)
	if err != nil || len(models.Data) != 1 || models.Data[0].ID != "test-model" {
		t.Fatalf("models = %#v, err = %v", models, err)
	}
	body, status, headers, err := provider.CreateResponse(context.Background(), tokens, []byte(`{"input":"test"}`))
	if err != nil || status != http.StatusOK || string(body) != `{"id":"response-test","output":[]}` {
		t.Fatalf("response = %d %s, err = %v", status, body, err)
	}
	if headers.Get("Content-Encoding") != "" || headers.Get("Content-Length") != "" {
		t.Fatalf("compressed headers remain on decoded response: %#v", headers)
	}
}
