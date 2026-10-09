package accountauth

import (
	"net/http"
	"testing"
)

func TestRecognizedCodexToolClientNeedsAPairedOriginator(t *testing.T) {
	for _, test := range []struct {
		userAgent, originator string
		want                  bool
	}{
		{"codex_cli_rs/0.162.0 (Mac OS 27.0.0; arm64) dumb", "codex_cli_rs", true},
		{"codex_exec/0.162.0 (Mac OS 27.0.0; arm64) dumb", "codex_exec", true},
		{"codex_cli_rs/0.162.0 (Mac OS 27.0.0; arm64) dumb", "codex_exec", false},
		{"codex_cli_rs/0.99.0", "codex_cli_rs", false},
		{"curl/8.0", "codex_cli_rs", false},
		{"", "", false},
	} {
		header := http.Header{}
		header.Set("User-Agent", test.userAgent)
		header.Set("originator", test.originator)
		if got := RecognizedCodexToolClient(header); got != test.want {
			t.Errorf("%q / %q = %v, want %v", test.userAgent, test.originator, got, test.want)
		}
		// The tool requests carry no session header, which a turn needs.
		if RecognizedCodexOfficialClient(header) {
			t.Errorf("%q was taken for a conversation turn without a session", test.userAgent)
		}
	}
}
