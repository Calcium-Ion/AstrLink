package accountauth

import (
	"net/http"
	"testing"
)

func TestCodexClientVersion(t *testing.T) {
	for _, test := range []struct {
		name, version, userAgent, want string
	}{
		{"explicit version wins", "0.100.0", "codex_cli_rs/0.156.0", "0.100.0"},
		{"explicit version without Codex user agent", " 0.156.0 ", "OpenAI/Python 1.0", "0.156.0"},
		{"Rust CLI", "", "codex_cli_rs/0.156.0 (Mac OS 15.0; arm64) Terminal/1.0", "0.156.0"},
		{"older Rust CLI", "", "codex_cli_rs/0.99.0", "0.99.0"},
		{"Codex CLI", "", "codex-cli/0.156.0", "0.156.0"},
		{"prerelease", "", "codex_cli_rs/0.156.0-alpha.1+build.2 (Linux; x86_64)", "0.156.0-alpha.1+build.2"},
		{"blank explicit version", " \t", "codex_cli_rs/0.156.0", "0.156.0"},
		{"missing identity", "", "", DefaultCodexModelsClientVersion},
		{"unrelated user agent", "", "OpenAI/Python 1.0", DefaultCodexModelsClientVersion},
		{"unrelated product", "", "not_codex_cli_rs/0.156.0", DefaultCodexModelsClientVersion},
		{"empty product version", "", "codex_cli_rs/ (Mac OS; arm64)", DefaultCodexModelsClientVersion},
		{"malformed product version", "", "codex_cli_rs/0.156.0/other", DefaultCodexModelsClientVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("version", test.version)
			headers.Set("User-Agent", test.userAgent)
			if got := CodexClientVersion(headers); got != test.want {
				t.Fatalf("version = %q, want %q", got, test.want)
			}
		})
	}
	if got := CodexClientVersion(nil); got != DefaultCodexModelsClientVersion {
		t.Fatalf("nil headers version = %q", got)
	}
}

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
