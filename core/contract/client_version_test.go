package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompareClientVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"2.1.258", "2.1.258", 0},
		{"2.1.300", "2.1.258", 1},
		{"2.1.99", "2.1.258", -1},
		{"3.0.0", "2.99.99", 1},
		{"0.155.1", "0.155.0", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1},
		{"1.0.0-rc.1", "1.0.0-beta.11", 1},
		{"1.0.0+build.5", "1.0.0", 0},
	} {
		order, ok := CompareClientVersions(test.left, test.right)
		if !ok || order != test.want {
			t.Errorf("CompareClientVersions(%q, %q) = %d, %t; want %d", test.left, test.right, order, ok, test.want)
		}
		if reverse, _ := CompareClientVersions(test.right, test.left); reverse != -test.want {
			t.Errorf("CompareClientVersions(%q, %q) = %d; want %d", test.right, test.left, reverse, -test.want)
		}
	}
	for _, invalid := range []string{"", "2.1", "v2.1.258", "2.1.258 ", "2.1.258/x", "99999999999.0.0", "1.0.0-" + strings.Repeat("a", 64)} {
		if _, ok := CompareClientVersions(invalid, "2.1.258"); ok {
			t.Errorf("compared invalid version %q", invalid)
		}
		if ValidClientVersion(invalid) {
			t.Errorf("accepted invalid version %q", invalid)
		}
	}
}

func TestRoutingSettingsIdentityDefaultsAndValidation(t *testing.T) {
	defaults := DefaultRoutingSettings()
	if !defaults.OfficialClientPassthrough ||
		!defaults.ClaudeIdentityAutoLearn || !defaults.CodexIdentityAutoLearn ||
		defaults.ClaudeIdentityVersion != "" || defaults.CodexIdentityVersion != "" {
		t.Fatalf("identity defaults = %+v", defaults)
	}
	encoded, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "identity_version") {
		t.Fatalf("empty version overrides were serialized: %s", encoded)
	}

	// A document saved before these fields existed keeps learning on.
	legacy := DefaultRoutingSettings()
	if err := json.Unmarshal([]byte(`{"official_client_passthrough":false}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.ClaudeIdentityAutoLearn || !legacy.CodexIdentityAutoLearn {
		t.Fatalf("legacy document disabled learning: %+v", legacy)
	}

	for _, test := range []struct {
		claude, codex string
		valid         bool
	}{
		{"2.1.300", "0.160.0", true},
		{"1.0.0", "0.144.0", true},
		{"2.2.0-beta.1", "0.157.0-alpha.2", true},
		{"2.1", "", false},
		{"v2.1.300", "", false},
		{"2.1.300 (external, cli)", "", false},
		{"", "0.143.9", false},
		{"", "0.155", false},
		{"", "codex-tui/0.155.1", false},
	} {
		settings := DefaultRoutingSettings()
		settings.ClaudeIdentityVersion, settings.CodexIdentityVersion = test.claude, test.codex
		if err := settings.Validate(); (err == nil) != test.valid {
			t.Errorf("Validate(claude=%q, codex=%q) = %v, want valid %t", test.claude, test.codex, err, test.valid)
		}
	}
}
