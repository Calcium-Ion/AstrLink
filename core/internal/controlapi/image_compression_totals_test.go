package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestImageCompressionTotalsAPI(t *testing.T) {
	ctx := context.Background()
	store, err := openControlTestStore(t, ctx, filepath.Join(t.TempDir(), "images.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: testControlToken})
	if err != nil {
		t.Fatal(err)
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		response := accessTokenRequest(t, handler, http.MethodGet, ImageCompressionTotalsPath, "", "")
		var body map[string]json.RawMessage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body) != 6 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return body
	}
	empty := read()
	var since time.Time
	if err := json.Unmarshal(empty["since"], &since); err != nil || since.IsZero() || since.After(time.Now()) {
		t.Fatalf("since = %s", empty["since"])
	}
	for _, key := range []string{"requests", "images", "compressed", "original_bytes", "sent_bytes"} {
		if string(empty[key]) != "0" {
			t.Fatalf("%s = %s before any request", key, empty[key])
		}
	}

	if err := store.AddImageCompression(ctx, storage.ImageCompressionCount{Images: 2, Compressed: 1, OriginalBytes: 9000, SentBytes: 6000}); err != nil {
		t.Fatal(err)
	}
	totals := read()
	for key, want := range map[string]string{"requests": "1", "images": "2", "compressed": "1", "original_bytes": "9000", "sent_bytes": "6000"} {
		if string(totals[key]) != want {
			t.Fatalf("%s = %s, want %s", key, totals[key], want)
		}
	}
	if string(totals["since"]) != string(empty["since"]) {
		t.Fatalf("since moved from %s to %s", empty["since"], totals["since"])
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, ImageCompressionTotalsPath, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", unauthorized.Code)
	}
	if response := accessTokenRequest(t, handler, http.MethodPost, ImageCompressionTotalsPath, "", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", response.Code)
	}
}
