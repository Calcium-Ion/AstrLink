package accountauth

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestClaudeForwardHeadersPairSDKFingerprintWithIdentity(t *testing.T) {
	const clientUA = "claude-cli/2.1.100 (external, cli)"
	for _, test := range []struct {
		name, userAgent string
		enforce         bool
		wantDefault     bool
	}{
		{"enforced replaces a recognized client", clientUA, true, true},
		{"enforced replaces another SDK", "Anthropic/Python 0.60.0", true, true},
		{"disabled keeps a recognized client", clientUA, false, false},
		{"disabled unknown client uses the default", "Anthropic/Python 0.60.0", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := make(http.Header)
			client.Set("User-Agent", test.userAgent)
			client.Set("X-Stainless-Lang", "python")
			client.Set("X-Stainless-Async", "async:asyncio")
			headers := make(http.Header)
			ApplyClaudeForwardHeaders(headers, AccountTokens{AccessToken: "token"}, client, ClientIdentity{}, test.enforce)
			if !test.wantDefault {
				if headers.Get("User-Agent") != clientUA || len(headers.Values("X-Stainless-Lang")) != 0 {
					t.Fatalf("recognized client lost its own fingerprint: %v", headers)
				}
				if _, overlaid := headers["X-Stainless-Async"]; overlaid {
					t.Fatalf("recognized client header was overlaid: %v", headers)
				}
				return
			}
			if headers.Get("User-Agent") != DefaultClaudeUserAgent {
				t.Fatalf("User-Agent = %q", headers.Get("User-Agent"))
			}
			for name, want := range DefaultClaudeIdentity().Headers {
				if got := headers.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if values, overlaid := headers["X-Stainless-Async"]; !overlaid || values != nil {
				t.Fatalf("another SDK's header is not deleted: %v", headers)
			}
		})
	}
}

func TestClaudeCodeClientHeadersMapHostPlatform(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "MacOS", "windows": "Windows", "linux": "Linux", "freebsd": "FreeBSD", "plan9": "Linux"} {
		if got := stainlessOS(goos); got != want {
			t.Errorf("stainlessOS(%s) = %s, want %s", goos, got, want)
		}
	}
	for goarch, want := range map[string]string{"arm64": "arm64", "amd64": "x64", "arm": "arm", "386": "x32"} {
		if got := stainlessArch(goarch); got != want {
			t.Errorf("stainlessArch(%s) = %s, want %s", goarch, got, want)
		}
	}
}

func TestCodexDefaultIdentityDropsStainlessHeaders(t *testing.T) {
	for _, test := range []struct {
		name, userAgent string
		disabled, keep  bool
	}{
		{"enforced", "codex_cli_rs/0.156.0", false, false},
		{"disabled unknown client", "OpenAI/JS 5.0.0", true, false},
		{"disabled recognized client", "codex_cli_rs/0.156.0", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := make(http.Header)
			client.Set("User-Agent", test.userAgent)
			client.Set("X-Stainless-Lang", "js")
			headers := make(http.Header)
			ApplyCodexForwardHeaders(headers, AccountTokens{AccessToken: "token"}, client, CodexIdentityPolicy{DisableEnforcement: test.disabled})
			values, overlaid := headers["X-Stainless-Lang"]
			if test.keep == (overlaid && values == nil) {
				t.Fatalf("keep=%t overlay=%v", test.keep, headers)
			}
		})
	}
}

// DefaultClaudeUserAgent must be a release that sends the JSON user_id and the
// session header, or ClaudeMetadataUserID would contradict the identity.
func TestDefaultClaudeUserAgentUsesJSONUserID(t *testing.T) {
	match := regexp.MustCompile(`^claude-cli/(\d+)\.(\d+)\.(\d+) `).FindStringSubmatch(DefaultClaudeUserAgent)
	if match == nil {
		t.Fatalf("unexpected default UA %q", DefaultClaudeUserAgent)
	}
	var version [3]int
	for index := range version {
		version[index], _ = strconv.Atoi(match[index+1])
	}
	if version[0] < 2 || (version[0] == 2 && (version[1] < 1 || (version[1] == 1 && version[2] < 87))) {
		t.Fatalf("default UA %q predates the JSON user_id and session header", DefaultClaudeUserAgent)
	}
}

func TestScopedSessionIDIsStablePerAccount(t *testing.T) {
	first := ScopedSessionID("service_a", "session-1")
	if first != ScopedSessionID("service_a", "session-1") {
		t.Fatal("mapping is not stable")
	}
	if first == ScopedSessionID("service_b", "session-1") || first == ScopedSessionID("service_a", "session-2") {
		t.Fatal("mapping links accounts or sessions")
	}
	if !uuidPattern.MatchString(first) || first[14] != '4' || !strings.ContainsRune("89ab", rune(first[19])) {
		t.Fatalf("%q is not a version 4 UUID", first)
	}
}

func TestClaudeMetadataUserID(t *testing.T) {
	const account = "4f3c2b1a-0000-4000-8000-000000000001"
	encoded := ClaudeMetadataUserID("service_a", account, "session-1", false)
	var payload map[string]string
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil || len(payload) != 3 {
		t.Fatalf("user_id = %s", encoded)
	}
	if payload["account_uuid"] != account || payload["session_id"] != "session-1" || len(payload["device_id"]) != 64 {
		t.Fatalf("user_id = %s", encoded)
	}
	other := ClaudeMetadataUserID("service_b", account, "session-1", false)
	if json.Unmarshal([]byte(other), &payload) != nil || payload["device_id"] == "" || strings.Contains(encoded, payload["device_id"]) {
		t.Fatal("accounts share a device")
	}
	if !strings.Contains(ClaudeMetadataUserID("service_a", "not-a-uuid", "s", false), `"account_uuid":""`) {
		t.Fatal("non-UUID account id was forwarded")
	}
	legacy := ClaudeMetadataUserID("service_a", account, "session-1", true)
	if legacy != "user_"+payloadDevice(t, encoded)+"_account_"+account+"_session_session-1" ||
		ClaudeUserIDSession(legacy) != "session-1" {
		t.Fatalf("legacy user_id = %s", legacy)
	}
	if strings.Contains(strings.ToLower(encoded), "astrlink") {
		t.Fatal("user_id carries gateway branding")
	}
}

func payloadDevice(t *testing.T, encoded string) string {
	t.Helper()
	var payload struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.DeviceID
}

func TestClaudeUserIDSession(t *testing.T) {
	for userID, want := range map[string]string{
		`{"device_id":"d","account_uuid":"","session_id":"s-1"}`:                   "s-1",
		"user_" + strings.Repeat("a", 64) + "_account__session_7a8b":               "7a8b",
		"user_" + strings.Repeat("a", 64) + "_account_4f3c2b1a-0000_session_ c-2 ": "c-2",
		"plain-id":   "",
		`{"broken":`: "",
		"":           "",
	} {
		if got := ClaudeUserIDSession(userID); got != want {
			t.Errorf("ClaudeUserIDSession(%q) = %q, want %q", userID, got, want)
		}
		if legacy := ClaudeLegacyUserID(userID); legacy != strings.HasPrefix(userID, "user_") {
			t.Errorf("ClaudeLegacyUserID(%q) = %t", userID, legacy)
		}
	}
}
