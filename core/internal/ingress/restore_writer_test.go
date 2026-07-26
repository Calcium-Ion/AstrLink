package ingress

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

func TestRestoringWriterRecomputesContentLength(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Length", "12")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte(`{"t":"<PRIVATE_EMAIL>"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if body != `{"t":"alice@example.com"}` {
		t.Fatalf("body=%s", body)
	}
	if recorder.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatalf("content-length=%q body=%d", recorder.Header().Get("Content-Length"), len(body))
	}
}

func TestRestoringWriterDoesNotCommitBufferedResponseOnTransportFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	downstream := newCommitTrackingWriter(recorder)
	writer := newRestoringResponseWriter(downstream, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusCreated)
	writer.Flush()
	if downstream.Committed() {
		t.Fatal("non-streaming transport flush committed the buffered response")
	}
	if _, err := writer.Write([]byte(`{"t":"<PRIVATE_EMAIL>"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated ||
		recorder.Body.String() != `{"t":"alice@example.com"}` {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRestoringWriterFailsClosedOnGzip(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false)
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Encoding", "gzip")
	writer.WriteHeader(http.StatusOK)
	if _, err := writer.Write([]byte("not-restored")); err == nil {
		t.Fatal("expected write to fail after encoding rejection")
	}
	if recorder.Code != http.StatusBadGateway ||
		!strings.Contains(recorder.Body.String(), "upstream_content_encoding") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "alice@example.com") {
		t.Fatalf("plaintext leaked: %s", recorder.Body.String())
	}
}

func TestRestoringWriterStreamingCarryAndDropsContentLength(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, true)
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Content-Length", "999")
	writer.WriteHeader(http.StatusOK)
	if recorder.Header().Get("Content-Length") != "" {
		t.Fatalf("streaming must delete content-length, got %q", recorder.Header().Get("Content-Length"))
	}
	if _, err := writer.Write([]byte("data: <PRIV")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("ATE_EMAIL>\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Body.String(); got != "data: alice@example.com\n\n" {
		t.Fatalf("stream body=%q", got)
	}
}

func TestRestoringWriterPassthroughUnknownContentType(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: privacy.KindEmail, Value: "alice@example.com"},
	}, false)
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.WriteHeader(http.StatusOK)
	payload := []byte(`opaque <PRIVATE_EMAIL>`)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if recorder.Body.String() != string(payload) {
		t.Fatalf("passthrough body=%s", recorder.Body.String())
	}
}
