package ingress

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/core/contract"
)

func TestClassifyAlphaProtocolRoutes(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		protocol  contract.ProtocolID
		model     string
		streaming bool
	}{
		{name: "responses", method: http.MethodPost, path: "/v1/responses", body: `{"model":"gpt-5","stream":true}`, protocol: contract.ProtocolOpenAIResponses, model: "gpt-5", streaming: true},
		{name: "compact", method: http.MethodPost, path: "/v1/responses/compact", body: `{"model":"gpt-5","stream":true}`, protocol: contract.ProtocolOpenAIResponsesCompact, model: "gpt-5"},
		{name: "messages", method: http.MethodPost, path: "/v1/messages", body: `{"model":"claude-test","stream":true}`, protocol: contract.ProtocolAnthropicMessages, model: "claude-test", streaming: true},
		{name: "chat", method: http.MethodPost, path: "/v1/chat/completions", body: `{"model":"gpt-4o"}`, protocol: contract.ProtocolOpenAIChat, model: "gpt-4o"},
		{name: "completions", method: http.MethodPost, path: "/v1/completions", body: `{"model":"legacy"}`, protocol: contract.ProtocolOpenAICompletions, model: "legacy"},
		{name: "openai models", method: http.MethodGet, path: "/v1/models", protocol: contract.ProtocolOpenAIModels},
		{name: "gemini generate", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:generateContent", body: `{}`, protocol: contract.ProtocolGoogleGenerateContent, model: "gemini-2.5-pro"},
		{name: "gemini auto", method: http.MethodPost, path: "/v1beta/models/astrlink/auto:generateContent", body: `{"contents":[{"parts":[{"text":"hello"}]}]}`, protocol: contract.ProtocolGoogleGenerateContent, model: contract.AstrLinkAutoModelID},
		{name: "gemini stream", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse", body: `{}`, protocol: contract.ProtocolGoogleGenerateContent, model: "gemini-2.5-flash", streaming: true},
		{name: "google models", method: http.MethodGet, path: "/v1beta/models?pageSize=20", protocol: contract.ProtocolGoogleModels},
		{name: "codex image", method: http.MethodPost, path: "/v1/images/generations", body: `{"model":"gpt-image-2","prompt":"fox"}`, protocol: contract.ProtocolOpenAIImages, model: "gpt-image-2"},
		{name: "codex image edit", method: http.MethodPost, path: "/v1/images/edits", body: `{"model":"gpt-image-2","prompt":"fox"}`, protocol: contract.ProtocolOpenAIImages, model: "gpt-image-2"},
		{name: "codex search", method: http.MethodPost, path: "/v1/alpha/search", body: `{"id":"session","model":"gpt-5","commands":{}}`, protocol: contract.ProtocolOpenAISearch, model: "gpt-5"},
		{name: "embeddings", method: http.MethodPost, path: "/v1/embeddings", body: `{"model":"text-embedding-3-small","input":["a","b"]}`, protocol: contract.ProtocolOpenAIEmbeddings, model: "text-embedding-3-small"},
		{name: "embeddings ignore stream", method: http.MethodPost, path: "/v1/embeddings", body: `{"model":"bge-m3","input":"a","stream":true}`, protocol: contract.ProtocolOpenAIEmbeddings, model: "bge-m3"},
		{name: "rerank", method: http.MethodPost, path: "/v1/rerank", body: `{"model":"bge-reranker-v2-m3","query":"q","documents":["a"]}`, protocol: contract.ProtocolRerank, model: "bge-reranker-v2-m3"},
		{name: "gemini embed", method: http.MethodPost, path: "/v1beta/models/gemini-embedding-001:embedContent", body: `{"content":{"parts":[{"text":"a"}]}}`, protocol: contract.ProtocolGoogleEmbeddings, model: "gemini-embedding-001"},
		{name: "gemini batch embed", method: http.MethodPost, path: "/v1beta/models/gemini-embedding-001:batchEmbedContents", body: `{"requests":[{"model":"models/gemini-embedding-001","content":{"parts":[{"text":"a"}]}}]}`, protocol: contract.ProtocolGoogleEmbeddings, model: "gemini-embedding-001"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			got, err := classify(request, 0)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if got.Protocol != test.protocol || got.Model != test.model || got.Streaming != test.streaming {
				t.Fatalf("classification = %#v", got)
			}
			preserved, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(preserved) != test.body {
				t.Fatalf("body = %q, want exact %q", preserved, test.body)
			}
		})
	}
}

func TestClassifyFromPathDoesNotReadTheBody(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gpt-5","stream":true}`),
	)
	got, ok := classifyFromPath(request)
	if !ok || got.Protocol != contract.ProtocolOpenAIResponses || got.Model != "" || got.Streaming {
		t.Fatalf("classifyFromPath = %#v ok=%t", got, ok)
	}
	preserved, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != `{"model":"gpt-5","stream":true}` {
		t.Fatalf("body was consumed: %q", preserved)
	}
	if _, ok := classifyFromPath(httptest.NewRequest(http.MethodPost, "/v1/moderations", nil)); ok {
		t.Fatal("unknown path should not classify")
	}
}

func TestClassifyRejectsUnknownPathsAndWrongMethods(t *testing.T) {
	for _, path := range []string{
		"/v1/moderations",
		"/v1beta/models/:generateContent",
		"/v1beta/models/:embedContent",
		"/v1beta/models/publisher/model:generateContent",
		"/v1beta/models/gemini:countTokens",
	} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		if _, err := classify(request, 0); err != errProtocolPathNotFound {
			t.Fatalf("classify(%q) error = %v", path, err)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	_, err := classify(request, 0)
	methodErr, ok := err.(methodNotAllowedError)
	if !ok || methodErr.allow != http.MethodPost {
		t.Fatalf("wrong method error = %#v", err)
	}
}

func TestClassifyDoesNotPreviewRetrievalInput(t *testing.T) {
	// A preview without a conversation is stored as a gateway label, outside
	// privacy masking, so retrieval text must not become one.
	for _, test := range []struct{ path, body string }{
		{path: "/v1/embeddings", body: `{"model":"m","input":"secret text"}`},
		{path: "/v1/rerank", body: `{"model":"m","query":"secret text","documents":["a"]}`},
		{path: "/v1beta/models/m:embedContent", body: `{"content":{"parts":[{"text":"secret text"}]}}`},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
		got, err := classify(request, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.InputPreview != "" || got.Conversation.LastUserText != "" {
			t.Fatalf("%s preview = %q last user = %q", test.path, got.InputPreview, got.Conversation.LastUserText)
		}
	}
}

func TestClassifyRejectsMalformedMetadataAndPreservesItsBody(t *testing.T) {
	for _, body := range []string{
		`{"stream":true`,
		`null`,
		`[]`,
		`{"stream":false} {"stream":true}`,
		`{"model":null}`,
		`{"stream":null}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		if _, err := classify(request, 0); err != errInvalidMetadata {
			t.Fatalf("classify(%q) error = %v", body, err)
		}
		preserved, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(preserved) != body {
			t.Fatalf("body = %q, want %q", preserved, body)
		}
	}
}

func TestClassifyUsesExactMetadataKeys(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		model     string
		streaming bool
	}{
		{
			name:  "case variants cannot override canonical keys",
			body:  `{"model":"canonical","MODEL":"override","stream":true,"STREAM":false}`,
			model: "canonical", streaming: true,
		},
		{
			name: "case variants alone are not routing metadata",
			body: `{"MODEL":"ignored","STREAM":true}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(test.body))
			got, err := classify(request, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != test.model || got.Streaming != test.streaming {
				t.Fatalf("classification = %#v", got)
			}
			preserved, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(preserved) != test.body {
				t.Fatalf("body = %q, want %q", preserved, test.body)
			}
		})
	}
}

func TestClassifyBoundsModelSelector(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "JSON model", path: "/v1/responses", body: `{"model":"` + strings.Repeat("m", maxModelRunes+1) + `"}`},
		{name: "path model", path: "/v1beta/models/" + strings.Repeat("m", maxModelRunes+1) + ":generateContent", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			if _, err := classify(request, 0); err != errInvalidMetadata {
				t.Fatalf("classify error = %v", err)
			}
		})
	}
}

func TestClassifyUsesGeminiActionMetadataWithoutInspectingOpaqueBody(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		body      string
		streaming bool
	}{
		{name: "generate action remains nonstreaming", path: "/v1beta/models/gemini-test:generateContent", body: `{"stream":true}`, streaming: false},
		{name: "stream action accepts opaque encoding", path: "/v1beta/models/gemini-test:streamGenerateContent", body: "compressed bytes", streaming: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Encoding", "gzip")
			got, err := classify(request, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != "gemini-test" || got.Streaming != test.streaming {
				t.Fatalf("classification = %#v", got)
			}
			preserved, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(preserved) != test.body {
				t.Fatalf("body = %q, want %q", preserved, test.body)
			}
		})
	}
}

func TestClassifyRejectsEncodedBodyWithoutGuessingStreamingCapability(t *testing.T) {
	const body = "compressed bytes"
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Encoding", "gzip")
	if _, err := classify(request, 0); err != errUnsupportedContentEncoding {
		t.Fatalf("classify error = %v", err)
	}
	preserved, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(preserved) != body {
		t.Fatalf("body = %q, want %q", preserved, body)
	}
}

func TestClassifyInspectsIdentityEncodedBody(t *testing.T) {
	const body = `{"model":"gpt-5","stream":true}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Encoding", "identity")
	got, err := classify(request, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5" || !got.Streaming {
		t.Fatalf("classification = %#v", got)
	}
}

func TestClassifyUsesConfiguredBodyLimit(t *testing.T) {
	body := `{"input":"` + strings.Repeat("x", 8<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	if _, err := classify(request, 8<<20); err != errMetadataTooLarge {
		t.Fatalf("classify error = %v", err)
	}
}

// Counterexample for the sanitized rebuild guard: when the sanitized body
// yields no extractable user text, the rebuild must still drop every
// pre-redaction text view instead of keeping the raw copies for the routing
// evaluator and classifier.
func TestClassifiedWithSanitizedTextDropsPreRedactionViewsWhenExtractionIsEmpty(t *testing.T) {
	classified := Request{
		Protocol:             contract.ProtocolOpenAIResponses,
		routingUserText:      "write go code for alice@example.com",
		routingUserTextRunes: 35,
		routingUserTextKnown: true,
		lastUserText:         "write go code for alice@example.com",
		InputPreview:         "write go code for alice@example.com",
		Conversation:         convo.RequestSummary{LastUserText: "write go code for alice@example.com"},
	}
	rebuilt := classifiedWithSanitizedText(classified, []byte(`{"input":""}`))
	if rebuilt.lastUserText != "" || rebuilt.Conversation.LastUserText != "" || rebuilt.InputPreview != "" || rebuilt.routingUserText != "" {
		t.Fatalf("pre-redaction text survived the rebuild: lastUserText=%q conversation=%q preview=%q routing=%q",
			rebuilt.lastUserText, rebuilt.Conversation.LastUserText, rebuilt.InputPreview, rebuilt.routingUserText)
	}
	// The sanitized body was read, and its user text is empty: that is a
	// known zero, unlike text the policy withheld.
	if !rebuilt.routingUserTextKnown || rebuilt.routingUserTextRunes != 0 {
		t.Fatalf("empty sanitized text: known=%v runes=%d", rebuilt.routingUserTextKnown, rebuilt.routingUserTextRunes)
	}
	withheld := classifiedWithSanitizedText(classified, nil)
	if withheld.routingUserTextKnown || withheld.routingUserText != "" || withheld.lastUserText != "" {
		t.Fatalf("withheld text must leave nothing to read: %+v", withheld)
	}
}

func TestClassifiedWithSanitizedTextRebuildsViewsFromSanitizedUserText(t *testing.T) {
	classified := Request{
		Protocol:     contract.ProtocolOpenAIResponses,
		lastUserText: "write go code for alice@example.com",
		InputPreview: "write go code for alice@example.com",
		Conversation: convo.RequestSummary{LastUserText: "write go code for alice@example.com"},
	}
	rebuilt := classifiedWithSanitizedText(classified, []byte(`{"input":"write go code for <PRIVATE_EMAIL_1>"}`))
	if strings.Contains(rebuilt.lastUserText, "alice@example.com") ||
		strings.Contains(rebuilt.Conversation.LastUserText, "alice@example.com") ||
		strings.Contains(rebuilt.InputPreview, "alice@example.com") {
		t.Fatalf("pre-redaction text survived the rebuild: lastUserText=%q conversation=%q preview=%q",
			rebuilt.lastUserText, rebuilt.Conversation.LastUserText, rebuilt.InputPreview)
	}
	if rebuilt.lastUserText == "" || rebuilt.Conversation.LastUserText == "" {
		t.Fatalf("sanitized user text was dropped: lastUserText=%q conversation=%q",
			rebuilt.lastUserText, rebuilt.Conversation.LastUserText)
	}
}

func TestReplayReadCloserSupportsConcurrentReadAndClose(t *testing.T) {
	original := &coordinatedReadCloser{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	body := &replayReadCloser{reader: original, closer: original}
	readDone := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		readDone <- err
	}()

	<-original.started
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	err := <-readDone
	if !errors.Is(err, http.ErrBodyReadAfterClose) {
		t.Fatalf("concurrent Read error = %v", err)
	}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, http.ErrBodyReadAfterClose) {
		t.Fatalf("Read after Close error = %v", err)
	}
}

type coordinatedReadCloser struct {
	started chan struct{}
	closed  chan struct{}
}

func (body *coordinatedReadCloser) Read([]byte) (int, error) {
	close(body.started)
	<-body.closed
	return 0, http.ErrBodyReadAfterClose
}

func (body *coordinatedReadCloser) Close() error {
	close(body.closed)
	return nil
}
