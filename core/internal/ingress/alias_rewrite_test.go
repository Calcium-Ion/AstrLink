package ingress

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestRewriteRequestModelJSONProtocolsPreserveUnrelatedBytes(t *testing.T) {
	protocols := []contract.ProtocolID{
		contract.ProtocolOpenAIResponses,
		contract.ProtocolOpenAIResponsesCompact,
		contract.ProtocolOpenAIChat,
		contract.ProtocolOpenAICompletions,
		contract.ProtocolAnthropicMessages,
	}
	tests := []struct {
		name          string
		body          string
		upstreamModel string
		wantModel     string
		wantErr       string
	}{
		{
			name:          "model first key",
			body:          `{"model":"public-alias","input":"keep me","nested":{"model":"nested-must-stay"}}`,
			upstreamModel: "provider/real",
			wantModel:     "provider/real",
		},
		{
			name:          "model after other members",
			body:          `{"input":"keep me","nested":{"model":"nested-must-stay"},"model":"public-alias","stream":false}`,
			upstreamModel: "provider/real",
			wantModel:     "provider/real",
		},
		{
			name:          "whitespace and key order preserved",
			body:          " {\n \"input\" : \"x\" , \"model\" : \"public-alias\" , \"arr\":[{\"model\":\"inside\"}] }\n",
			upstreamModel: "real",
			wantModel:     "real",
		},
		{
			name:          "escaped characters in new model",
			body:          `{"model":"public-alias","note":"ok"}`,
			upstreamModel: "prov\"ider/real\nmodel",
			wantModel:     "prov\"ider/real\nmodel",
		},
		{
			name:          "missing top-level model",
			body:          `{"input":"x","nested":{"model":"nested"}}`,
			upstreamModel: "real",
			wantErr:       "request has no top-level model member to rewrite",
		},
		{
			name:          "non-string top-level model",
			body:          `{"model":123,"input":"x"}`,
			upstreamModel: "real",
			wantErr:       "top-level model member must be a string",
		},
	}

	for _, protocol := range protocols {
		for _, test := range tests {
			t.Run(string(protocol)+"/"+test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(test.body))
				request.ContentLength = int64(len(test.body))
				request.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(test.body)), nil
				}
				classified := Request{Protocol: protocol}
				got, err := rewriteRequestModel(request, classified, test.upstreamModel, true)
				if test.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), test.wantErr) {
						t.Fatalf("error = %v, want %q", err, test.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("rewriteRequestModel: %v", err)
				}
				rewritten, err := io.ReadAll(got.Body)
				if err != nil {
					t.Fatalf("read rewritten body: %v", err)
				}
				if got.ContentLength != int64(len(rewritten)) {
					t.Fatalf("ContentLength = %d, want %d", got.ContentLength, len(rewritten))
				}
				assertJSONModelSpliced(t, test.body, string(rewritten), test.wantModel)
			})
		}
	}
}

func TestRewriteRequestModelRequiresBufferedBody(t *testing.T) {
	body := `{"model":"public-alias"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	_, err := rewriteRequestModel(
		request,
		Request{Protocol: contract.ProtocolOpenAIChat},
		"real",
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "alias rewrite requires a fully buffered request body") {
		t.Fatalf("error = %v", err)
	}
}

func TestRewriteRequestModelGeminiPath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		rawQuery      string
		upstreamModel string
		wantPath      string
	}{
		{
			name:          "space escaped in path segment",
			path:          "/v1beta/models/gemini-pro:generateContent",
			upstreamModel: "real model",
			wantPath:      "/v1beta/models/real%20model:generateContent",
		},
		{
			name:          "stream action and query preserved",
			path:          "/v1beta/models/gemini-pro:streamGenerateContent",
			rawQuery:      "alt=sse",
			upstreamModel: "upstream-flash",
			wantPath:      "/v1beta/models/upstream-flash:streamGenerateContent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(`{"contents":[]}`),
			)
			request.URL.RawQuery = test.rawQuery
			originalBody, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			request.Body = io.NopCloser(strings.NewReader(string(originalBody)))
			got, err := rewriteRequestModel(
				request,
				Request{Protocol: contract.ProtocolGoogleGenerateContent},
				test.upstreamModel,
				true,
			)
			if err != nil {
				t.Fatalf("rewriteRequestModel: %v", err)
			}
			if got.URL.Path != test.wantPath {
				t.Fatalf("Path = %q, want %q", got.URL.Path, test.wantPath)
			}
			if got.URL.RawQuery != test.rawQuery {
				t.Fatalf("RawQuery = %q, want %q", got.URL.RawQuery, test.rawQuery)
			}
			body, err := io.ReadAll(got.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(originalBody) {
				t.Fatalf("gemini body changed: %q", body)
			}
		})
	}
}

func TestModelMemberSpansAndSplice(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		memberNames   map[string]bool
		expectedValue string
		replacement   string
		wantSpans     int
		wantBody      string
	}{
		{
			name:          "nested anthropic message_start model",
			body:          `{"type":"message_start","message":{"id":"msg_1","model":"claude-real","role":"assistant"}}`,
			memberNames:   map[string]bool{"model": true},
			expectedValue: "claude-real",
			replacement:   "public-alias",
			wantSpans:     1,
			wantBody:      `{"type":"message_start","message":{"id":"msg_1","model":"public-alias","role":"assistant"}}`,
		},
		{
			name:          "different model value untouched",
			body:          `{"model":"other-model","text":"claude-real"}`,
			memberNames:   map[string]bool{"model": true},
			expectedValue: "claude-real",
			replacement:   "public-alias",
			wantSpans:     0,
			wantBody:      `{"model":"other-model","text":"claude-real"}`,
		},
		{
			name:          "array element string named model untouched",
			body:          `{"models":["claude-real"],"model":"claude-real"}`,
			memberNames:   map[string]bool{"model": true},
			expectedValue: "claude-real",
			replacement:   "public-alias",
			wantSpans:     1,
			wantBody:      `{"models":["claude-real"],"model":"public-alias"}`,
		},
		{
			name:          "gemini modelVersion matched",
			body:          `{"modelVersion":"gemini-real","model":"gemini-real","text":"gemini-real"}`,
			memberNames:   map[string]bool{"modelVersion": true, "model": true},
			expectedValue: "gemini-real",
			replacement:   "public-gemini",
			wantSpans:     2,
			wantBody:      `{"modelVersion":"public-gemini","model":"public-gemini","text":"gemini-real"}`,
		},
		{
			name:          "multiple spans in one document",
			body:          `{"model":"real","nested":{"model":"real"},"keep":"real"}`,
			memberNames:   map[string]bool{"model": true},
			expectedValue: "real",
			replacement:   "alias",
			wantSpans:     2,
			wantBody:      `{"model":"alias","nested":{"model":"alias"},"keep":"real"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spans, err := modelMemberSpans([]byte(test.body), test.memberNames, test.expectedValue)
			if err != nil {
				t.Fatalf("modelMemberSpans: %v", err)
			}
			if len(spans) != test.wantSpans {
				t.Fatalf("spans = %v, want %d", spans, test.wantSpans)
			}
			for index := 1; index < len(spans); index++ {
				if spans[index][0] < spans[index-1][0] {
					t.Fatalf("spans not ascending: %v", spans)
				}
			}
			replacement, err := json.Marshal(test.replacement)
			if err != nil {
				t.Fatal(err)
			}
			got := string(spliceSpans([]byte(test.body), spans, replacement))
			if got != test.wantBody {
				t.Fatalf("spliced = %q, want %q", got, test.wantBody)
			}
		})
	}
}

func assertJSONModelSpliced(t *testing.T, original, rewritten, wantModel string) {
	t.Helper()
	valueStart, valueEnd, err := topLevelModelValueSpan([]byte(original))
	if err != nil {
		t.Fatalf("locate original model: %v", err)
	}
	prefix := original[:valueStart]
	suffix := original[valueEnd:]
	if !strings.HasPrefix(rewritten, prefix) {
		t.Fatalf("prefix changed\noriginal prefix=%q\nrewritten=%q", prefix, rewritten)
	}
	if !strings.HasSuffix(rewritten, suffix) {
		t.Fatalf("suffix changed\noriginal suffix=%q\nrewritten=%q", suffix, rewritten)
	}
	gotValue := rewritten[len(prefix) : len(rewritten)-len(suffix)]
	encoded, err := json.Marshal(wantModel)
	if err != nil {
		t.Fatal(err)
	}
	if gotValue != string(encoded) {
		t.Fatalf("spliced model value = %s, want %s", gotValue, encoded)
	}
	if strings.Contains(original, `"nested":{"model":"nested-must-stay"}`) &&
		!strings.Contains(rewritten, `"nested":{"model":"nested-must-stay"}`) {
		t.Fatalf("nested model was rewritten: %s", rewritten)
	}
	if strings.Contains(original, `[{"model":"inside"}]`) &&
		!strings.Contains(rewritten, `[{"model":"inside"}]`) {
		t.Fatalf("array nested model was rewritten: %s", rewritten)
	}
}
