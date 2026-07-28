package ingress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedactHeadersPrecedence(t *testing.T) {
	cases := []struct {
		name         string
		header       string
		value        string
		wantValue    string
		wantRedacted bool
	}{
		{
			name:      "allowlist beats token heuristic",
			header:    "X-Ratelimit-Remaining-Tokens",
			value:     "4999",
			wantValue: "4999",
		},
		{
			name:      "anthropic ratelimit prefix survives",
			header:    "Anthropic-Ratelimit-Requests-Remaining",
			value:     "42",
			wantValue: "42",
		},
		{
			name:      "request id survives",
			header:    "X-Request-Id",
			value:     "req_abc123",
			wantValue: "req_abc123",
		},
		{
			name:         "authorization masked with scheme",
			header:       "Authorization",
			value:        "Bearer " + strings.Repeat("k", 51),
			wantValue:    "Bearer <redacted:51 chars>",
			wantRedacted: true,
		},
		{
			name:         "api key masked without scheme",
			header:       "X-Api-Key",
			value:        "sk-1234567890",
			wantValue:    "<redacted:13 chars>",
			wantRedacted: true,
		},
		{
			name:         "unknown custom secret header masked by heuristic",
			header:       "X-Acme-Secret",
			value:        "hunter2",
			wantValue:    "<redacted:7 chars>",
			wantRedacted: true,
		},
		{
			name:         "empty sensitive value masked distinctly",
			header:       "Cookie",
			value:        "",
			wantValue:    "<redacted:empty>",
			wantRedacted: true,
		},
		{
			name:      "plain header untouched",
			header:    "Content-Type",
			value:     "application/json",
			wantValue: "application/json",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set(testCase.header, testCase.value)
			captured := redactHeaders(headers)
			if len(captured) != 1 {
				t.Fatalf("captured %d headers, want 1: %#v", len(captured), captured)
			}
			entry := captured[0]
			if entry.Name != strings.ToLower(testCase.header) {
				t.Fatalf("name=%q, want lowercase %q", entry.Name, testCase.header)
			}
			if entry.Value != testCase.wantValue {
				t.Fatalf("value=%q, want %q", entry.Value, testCase.wantValue)
			}
			if entry.Redacted != testCase.wantRedacted {
				t.Fatalf("redacted=%v, want %v", entry.Redacted, testCase.wantRedacted)
			}
		})
	}
}

func TestRedactHeadersNeverKeepsSensitiveBytes(t *testing.T) {
	const credential = "sk-super-secret-value-000"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+credential)
	headers.Set("X-Goog-Api-Key", credential)
	headers.Add("Cookie", "session="+credential)
	for _, entry := range redactHeaders(headers) {
		if strings.Contains(entry.Value, credential) {
			t.Fatalf("captured value leaks credential: %q", entry.Value)
		}
	}
}

func TestRedactHeadersBounds(t *testing.T) {
	headers := http.Header{}
	for index := 0; index < maxCapturedHeaders+10; index++ {
		headers.Set("X-Filler-"+strings.Repeat("a", index%5)+string(rune('a'+index%26))+"-"+strings.Repeat("b", index/26), "v")
	}
	captured := redactHeaders(headers)
	if len(captured) > maxCapturedHeaders+1 {
		t.Fatalf("captured %d entries, want at most %d + overflow marker", len(captured), maxCapturedHeaders)
	}
	last := captured[len(captured)-1]
	if last.Name != "…" || !strings.Contains(last.Value, "more headers omitted") {
		t.Fatalf("missing overflow marker, last=%#v", last)
	}

	long := http.Header{}
	long.Set("X-Long", strings.Repeat("v", maxCapturedHeaderValue+100))
	entry := redactHeaders(long)[0]
	if len(entry.Value) > maxCapturedHeaderValue+len("…") {
		t.Fatalf("value not truncated: %d bytes", len(entry.Value))
	}
	if !strings.HasSuffix(entry.Value, "…") {
		t.Fatalf("truncated value missing marker: %q", entry.Value[len(entry.Value)-8:])
	}
}

func TestRedactRequestMetaURL(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:generateContent?key=AIzaSecret123&stream=true&alt=sse",
		nil,
	)
	meta := RedactRequestMeta(request)
	if meta.Method != http.MethodPost {
		t.Fatalf("method=%q", meta.Method)
	}
	if meta.HTTPVersion != "HTTP/1.1" {
		t.Fatalf("http_version=%q", meta.HTTPVersion)
	}
	if strings.Contains(meta.URL, "AIzaSecret123") {
		t.Fatalf("url leaks credential: %q", meta.URL)
	}
	if !strings.Contains(meta.URL, "key=<redacted>") {
		t.Fatalf("url missing masked key parameter: %q", meta.URL)
	}
	if !strings.Contains(meta.URL, "stream=true") || !strings.Contains(meta.URL, "alt=sse") {
		t.Fatalf("url dropped benign parameters: %q", meta.URL)
	}
}

func TestRedactRequestMetaStripsUserinfo(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://user:pass@localhost/v1/models", nil)
	meta := RedactRequestMeta(request)
	if strings.Contains(meta.URL, "user") || strings.Contains(meta.URL, "pass") {
		t.Fatalf("url keeps userinfo: %q", meta.URL)
	}
}

func TestRedactRequestMetaNil(t *testing.T) {
	meta := RedactRequestMeta(nil)
	if meta.RequestHeaders == nil || meta.ResponseHeaders == nil {
		t.Fatal("nil request must still yield empty header slices")
	}
}
