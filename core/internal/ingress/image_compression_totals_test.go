package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// missingCredentials has no API key for any provider, so a request is
// rejected after its images were compressed but before it leaves.
type missingCredentials struct{ codingPlanCredentials }

func (missingCredentials) Get(context.Context, secretstore.Ref) ([]byte, error) {
	return nil, errors.New("no credential")
}

// grainyJPEG is a low-quality JPEG of noise: a WebP of it at quality 100
// comes out larger, so compression sends it as it is.
func grainyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 160, 120))
	random := rand.New(rand.NewSource(7))
	for index := range img.Pix {
		img.Pix[index] = uint8(random.Intn(256))
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 10}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestImageCompressionTotalsCountEachForwardedRequestOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "images.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings, err := store.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ImageCompression = contract.ImageCompression{Enabled: true, Mode: contract.ImageCompressionLossy, Quality: 100}
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	start, err := store.GetImageCompressionTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(credentials secretstore.SecretStore, candidates []endpoint.Resolved, body string) int {
		t.Helper()
		handler := NewWithDependencies(Dependencies{
			Resolver:         candidateResolver{candidates: candidates},
			Authorizer:       endpoint.NewServiceAuthorizer(credentials, codingPlanCredentials{}).WithRoutingSettings(store),
			RequestRecords:   store,
			ConversionEngine: relaykitbridge.NewEngine(),
		})
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	totals := func() storage.ImageCompressionTotals {
		t.Helper()
		got, err := store.GetImageCompressionTotals(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	png := testPNG(t, testScreenshot(320, 200))
	grainy := grainyJPEG(t)
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	jpegURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(grainy)
	body := `{"model":"model-test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + pngURL +
		`"}},{"type":"image_url","image_url":{"url":"` + jpegURL + `"}}]}]}`

	// One request, sent three times: a retry, then a failover.
	_, _, response := nativeProviderExchange(contract.ProtocolOpenAIChat, false)
	first := newSequenceUpstream(t, "application/json", response, http.StatusInternalServerError, http.StatusInternalServerError)
	second := newSequenceUpstream(t, "application/json", response)
	candidates := []endpoint.Resolved{
		apiCandidate("service_a", contract.ServiceKindMagpie, first.url, contract.ProtocolOpenAIChat),
		apiCandidate("service_b", contract.ServiceKindMagpie, second.url, contract.ProtocolOpenAIChat),
	}
	candidates[0].Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: 6}
	if code := serve(codingPlanCredentials{}, candidates, body); code != http.StatusOK {
		t.Fatalf("response = %d", code)
	}
	attempts := append(first.requests(), second.requests()...)
	if len(first.requests()) < 2 || len(second.requests()) != 1 {
		t.Fatalf("expected a retry and a failover, got %d and %d attempts", len(first.requests()), len(second.requests()))
	}
	sent := ojson.Get(attempts[0], "messages.0.content.0.image_url.url").Str
	webp, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sent, "data:image/webp;base64,"))
	if err != nil || !strings.HasPrefix(sent, "data:image/webp;base64,") || len(webp) >= len(png) {
		t.Fatalf("the PNG went as %.60s", sent)
	}
	if ojson.Get(attempts[0], "messages.0.content.1.image_url.url").Str != jpegURL {
		t.Fatal("the JPEG whose WebP is larger was not sent as it was")
	}
	want := storage.ImageCompressionTotals{
		Since: start.Since, Requests: 1, Images: 2, Compressed: 1,
		OriginalBytes: int64(len(png) + len(grainy)), SentBytes: int64(len(webp) + len(grainy)),
	}
	if got := totals(); got != want {
		t.Fatalf("after %d attempts totals = %+v, want %+v", len(attempts), got, want)
	}

	// Compressed, then rejected for a missing key: it never left.
	unsent := newSequenceUpstream(t, "application/json", response)
	rejected := []endpoint.Resolved{apiCandidate("service_c", contract.ServiceKindMagpie, unsent.url, contract.ProtocolOpenAIChat)}
	if code := serve(missingCredentials{}, rejected, body); code == http.StatusOK || len(unsent.requests()) != 0 {
		t.Fatalf("response = %d after %d upstream requests", code, len(unsent.requests()))
	}
	// No upstream listens: the request was sent but nothing received it.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	unreachable := apiCandidate("service_e", contract.ServiceKindMagpie, closed.URL, contract.ProtocolOpenAIChat)
	if code := serve(codingPlanCredentials{}, []endpoint.Resolved{unreachable}, body); code == http.StatusOK {
		t.Fatalf("unreachable response = %d", code)
	}
	// No inline image, nothing to count.
	text := `{"model":"model-test","messages":[{"role":"user","content":"hello"}]}`
	plain := newSequenceUpstream(t, "application/json", response)
	if code := serve(codingPlanCredentials{}, []endpoint.Resolved{apiCandidate("service_d", contract.ServiceKindMagpie, plain.url, contract.ProtocolOpenAIChat)}, text); code != http.StatusOK {
		t.Fatalf("text response = %d", code)
	}
	if got := totals(); got != want {
		t.Fatalf("unsent and image-free requests changed the totals to %+v, want %+v", got, want)
	}

	// Unreachable first, then a failover that lands: counted once.
	reached := newSequenceUpstream(t, "application/json", response)
	failover := []endpoint.Resolved{unreachable, apiCandidate("service_f", contract.ServiceKindMagpie, reached.url, contract.ProtocolOpenAIChat)}
	failover[0].Failover = &contract.FailoverPolicy{Enabled: true, Strategy: contract.RetryFirst, MaxAttempts: 6}
	if code := serve(codingPlanCredentials{}, failover, body); code != http.StatusOK || len(reached.requests()) != 1 {
		t.Fatalf("failover response = %d after %d requests", code, len(reached.requests()))
	}
	want.Requests, want.Images, want.Compressed = 2, 4, 2
	want.OriginalBytes, want.SentBytes = 2*want.OriginalBytes, 2*want.SentBytes
	if got := totals(); got != want {
		t.Fatalf("after the failover totals = %+v, want %+v", got, want)
	}
}
