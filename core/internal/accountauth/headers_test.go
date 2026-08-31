package accountauth

import (
	"net/http"
	"testing"
)

func TestApplyCodexAPIHeadersSetsObservedBackendContract(t *testing.T) {
	headers := make(http.Header)
	ApplyCodexAPIHeaders(headers, AccountTokens{
		AccessToken: "access",
		AccountID:   "acct_1",
	}, "", "")
	if got := headers.Get("Authorization"); got != "Bearer access" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("ChatGPT-Account-ID"); got != "acct_1" {
		t.Fatalf("ChatGPT-Account-ID = %q", got)
	}
	if got := headers.Get("User-Agent"); got != "codex-cli/"+DefaultCodexModelsClientVersion {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := headers.Get("originator"); got != "astrlink" {
		t.Fatalf("originator = %q", got)
	}
	if got := headers.Get("version"); got != DefaultCodexModelsClientVersion {
		t.Fatalf("version = %q", got)
	}
	if got := headers.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
}

func TestApplyCodexAPIHeadersOmitsEmptyAccountID(t *testing.T) {
	headers := make(http.Header)
	ApplyCodexAPIHeaders(headers, AccountTokens{AccessToken: "access"}, "astrlink", "0.99.0")
	if _, present := headers["Chatgpt-Account-Id"]; present {
		t.Fatal("empty account ID was sent")
	}
	if got := headers.Get("User-Agent"); got != "codex-cli/0.99.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}
