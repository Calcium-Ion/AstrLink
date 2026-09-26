package accountauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

type memoryIdentityStore struct {
	mu        sync.Mutex
	documents map[contract.SubscriptionProvider][]byte
	puts      int
	putErr    error
}

func (store *memoryIdentityStore) ListLearnedIdentities(context.Context) (map[contract.SubscriptionProvider][]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	documents := make(map[contract.SubscriptionProvider][]byte, len(store.documents))
	for provider, document := range store.documents {
		documents[provider] = append([]byte(nil), document...)
	}
	return documents, nil
}

func (store *memoryIdentityStore) PutLearnedIdentity(_ context.Context, provider contract.SubscriptionProvider, document []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.puts++
	if store.putErr != nil {
		return store.putErr
	}
	if store.documents == nil {
		store.documents = make(map[contract.SubscriptionProvider][]byte)
	}
	store.documents[provider] = append([]byte(nil), document...)
	return nil
}

type fixedSettings struct {
	settings contract.RoutingSettings
	err      error
}

func (source fixedSettings) GetRoutingSettings(context.Context) (contract.RoutingSettings, error) {
	return source.settings, source.err
}

func claudeClientHeaders(version string) http.Header {
	return http.Header{
		"User-Agent":                  {"claude-cli/" + version + " (external, sdk-cli)"},
		"X-App":                       {"cli"},
		"X-Claude-Code-Session-Id":    {"0f8c1d3e-3a4b-4c5d-8e9f-0a1b2c3d4e5f"},
		"Anthropic-Beta":              {"claude-code-20250219,oauth-2025-04-20"},
		"X-Stainless-Lang":            {"js"},
		"X-Stainless-Package-Version": {"0.95.1"},
		"X-Stainless-Os":              {"Linux"},
		"X-Stainless-Arch":            {"arm64"},
		"X-Stainless-Runtime":         {"node"},
		"X-Stainless-Runtime-Version": {"v24.9.0"},
		"X-Stainless-Retry-Count":     {"2"},
		"X-Stainless-Timeout":         {"600"},
		"X-Stainless-Helper-Method":   {"stream"},
	}
}

func codexClientHeaders(version string) http.Header {
	return http.Header{
		"User-Agent": {"codex_cli_rs/" + version + " (Mac OS 26.0.0; arm64) iTerm.app/3.6.1"},
		"Originator": {"codex_cli_rs"},
		"Session-Id": {"0f8c1d3e-3a4b-4c5d-8e9f-0a1b2c3d4e5f"},
	}
}

func learnedSettings() contract.RoutingSettings {
	return contract.DefaultRoutingSettings()
}

func TestIdentityRegistryLearnsOnlyUpward(t *testing.T) {
	ctx := context.Background()
	registry := NewIdentityRegistry(nil, nil)
	for _, step := range []struct {
		version string
		changed bool
		want    string
	}{
		{"2.1.300", true, "2.1.300"},
		{"2.1.299", false, "2.1.300"},
		{"2.1.300-beta.1", false, "2.1.300"},
		{"2.1.301", true, "2.1.301"},
	} {
		changed, err := registry.LearnClaude(ctx, claudeClientHeaders(step.version))
		if err != nil || changed != step.changed {
			t.Fatalf("LearnClaude(%s) = %t, %v; want %t", step.version, changed, err, step.changed)
		}
		if got := registry.ClaudeIdentity(learnedSettings()); got.Version != step.want {
			t.Fatalf("after %s learned %+v, want %s", step.version, got, step.want)
		}
	}
	learned := registry.ClaudeIdentity(learnedSettings())
	want := map[string]string{
		"X-App":                       "cli",
		"X-Stainless-Lang":            "js",
		"X-Stainless-Package-Version": "0.95.1",
		"X-Stainless-Os":              "Linux",
		"X-Stainless-Arch":            "arm64",
		"X-Stainless-Runtime":         "node",
		"X-Stainless-Runtime-Version": "v24.9.0",
		"X-Stainless-Retry-Count":     "0",
		"X-Stainless-Timeout":         "600",
	}
	if learned.UserAgent != "claude-cli/2.1.301 (external, sdk-cli)" || !reflect.DeepEqual(learned.Headers, want) {
		t.Fatalf("learned Claude identity = %+v", learned)
	}

	for _, step := range []struct {
		version string
		changed bool
		want    string
	}{
		{"0.160.0", true, "0.160.0"},
		{"0.155.0", false, "0.160.0"},
		{"0.161.2", true, "0.161.2"},
	} {
		changed, err := registry.LearnCodex(ctx, codexClientHeaders(step.version))
		if err != nil || changed != step.changed {
			t.Fatalf("LearnCodex(%s) = %t, %v; want %t", step.version, changed, err, step.changed)
		}
		if got := registry.CodexIdentity(learnedSettings(), ""); got.Version != step.want {
			t.Fatalf("after %s learned %+v, want %s", step.version, got, step.want)
		}
	}
	codex := registry.CodexIdentity(learnedSettings(), "")
	if codex.UserAgent != "codex_cli_rs/0.161.2 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1" ||
		!reflect.DeepEqual(codex.Headers, map[string]string{"originator": "codex_cli_rs"}) {
		t.Fatalf("learned Codex identity = %+v", codex)
	}
}

func TestIdentityRegistryRejectsUnsafeIdentities(t *testing.T) {
	claude := func(version string, edit func(http.Header)) http.Header {
		header := claudeClientHeaders(version)
		if edit != nil {
			edit(header)
		}
		return header
	}
	codex := func(version string, edit func(http.Header)) http.Header {
		header := codexClientHeaders(version)
		if edit != nil {
			edit(header)
		}
		return header
	}
	manyHeaders := func(header http.Header) {
		for index := 0; index < maxLearnedHeaders; index++ {
			header.Set(fmt.Sprintf("X-Stainless-Extra-%d", index), "1")
		}
	}
	for _, test := range []struct {
		name    string
		codex   bool
		header  http.Header
		learned bool
	}{
		{"claude floor", false, claude(minLearnedClaudeVersion, nil), true},
		{"claude below floor", false, claude("2.1.86", nil), false},
		{"claude two majors ahead", false, claude("4.0.0", nil), true},
		{"claude three majors ahead", false, claude("5.0.0", nil), false},
		{"claude malformed version", false, claude("2.1", nil), false},
		{"claude other product", false, claude("2.1.300", func(header http.Header) { header.Set("User-Agent", "claude-code/2.1.300") }), false},
		{"claude oversized user agent", false, claude("2.1.300", func(header http.Header) {
			header.Set("User-Agent", "claude-cli/2.1.300 ("+strings.Repeat("x", maxLearnedUserAgent)+")")
		}), false},
		{"claude non-ASCII user agent", false, claude("2.1.300", func(header http.Header) { header.Set("User-Agent", "claude-cli/2.1.300 (é)") }), false},
		{"claude branded user agent", false, claude("2.1.300", func(header http.Header) { header.Set("User-Agent", "claude-cli/2.1.300 (via AstrLink)") }), false},
		{"claude branded header", false, claude("2.1.300", func(header http.Header) { header.Set("X-Stainless-Runtime", "astrlink") }), false},
		{"claude oversized header", false, claude("2.1.300", func(header http.Header) { header.Set("X-Stainless-Timeout", strings.Repeat("6", maxLearnedValue+1)) }), false},
		{"claude control character", false, claude("2.1.300", func(header http.Header) { header["X-Stainless-Os"] = []string{"Linux\x00"} }), false},
		{"claude repeated header", false, claude("2.1.300", func(header http.Header) { header.Add("X-Stainless-Os", "MacOS") }), false},
		{"claude empty header", false, claude("2.1.300", func(header http.Header) { header.Set("X-Stainless-Os", " ") }), false},
		{"claude without SDK version", false, claude("2.1.300", func(header http.Header) { header.Del("X-Stainless-Package-Version") }), false},
		{"claude too many headers", false, claude("2.1.300", manyHeaders), false},
		{"codex floor", true, codex(contract.MinCodexClientVersion, nil), true},
		{"codex below floor", true, codex("0.143.9", nil), false},
		{"codex two majors ahead", true, codex("2.0.0", nil), true},
		{"codex three majors ahead", true, codex("3.0.0", nil), false},
		{"codex malformed version", true, codex("0.160", nil), false},
		{"codex unknown product", true, codex("0.160.0", func(header http.Header) {
			header.Set("User-Agent", "codex_other/0.160.0")
			header.Set("Originator", "codex_other")
		}), false},
		{"codex originator mismatch", true, codex("0.160.0", func(header http.Header) { header.Set("Originator", "codex-tui") }), false},
		{"codex branded user agent", true, codex("0.160.0", func(header http.Header) {
			header.Set("User-Agent", "codex_cli_rs/0.160.0 (Mac OS 26.0.0; arm64) astrlink")
		}), false},
		{"codex oversized user agent", true, codex("0.160.0", func(header http.Header) {
			header.Set("User-Agent", "codex_cli_rs/0.160.0 "+strings.Repeat("x", maxLearnedUserAgent))
		}), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &memoryIdentityStore{}
			registry := NewIdentityRegistry(nil, store)
			learn, provider := registry.LearnClaude, contract.SubscriptionProviderClaudeCode
			if test.codex {
				learn, provider = registry.LearnCodex, contract.SubscriptionProviderOpenAICodex
			}
			changed, err := learn(context.Background(), test.header)
			if err != nil || changed != test.learned {
				t.Fatalf("learn = %t, %v; want %t", changed, err, test.learned)
			}
			if _, stored := store.documents[provider]; stored != test.learned {
				t.Fatalf("stored = %t, want %t", stored, test.learned)
			}
			if _, ok := registry.learnedIdentity(provider); ok != test.learned {
				t.Fatalf("learned = %t, want %t", ok, test.learned)
			}
		})
	}
}

func TestIdentityRegistryRefreshesSameVersionAtBoundedRate(t *testing.T) {
	ctx := context.Background()
	store := &memoryIdentityStore{}
	registry := NewIdentityRegistry(nil, store)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	registry.now = func() time.Time { return now }

	if changed, _ := registry.LearnClaude(ctx, claudeClientHeaders("2.1.300")); !changed {
		t.Fatal("first identity was not learned")
	}
	if changed, _ := registry.LearnClaude(ctx, claudeClientHeaders("2.1.300")); changed {
		t.Fatal("identical identity was learned again")
	}
	other := claudeClientHeaders("2.1.300")
	other.Set("X-Stainless-Os", "MacOS")
	if changed, _ := registry.LearnClaude(ctx, other); changed {
		t.Fatal("same version refreshed before the interval")
	}
	now = now.Add(sameVersionRefreshInterval)
	if changed, _ := registry.LearnClaude(ctx, other); !changed {
		t.Fatal("same version did not refresh after the interval")
	}
	if got := registry.ClaudeIdentity(learnedSettings()).Headers["X-Stainless-Os"]; got != "MacOS" {
		t.Fatalf("refreshed X-Stainless-Os = %q", got)
	}
	// A newer release is never held back by the refresh interval.
	if changed, _ := registry.LearnClaude(ctx, claudeClientHeaders("2.1.301")); !changed {
		t.Fatal("newer version was rate limited")
	}
	if store.puts != 3 {
		t.Fatalf("store writes = %d, want 3", store.puts)
	}
}

func TestIdentityRegistryResolutionLayers(t *testing.T) {
	ctx := context.Background()
	registry := NewIdentityRegistry(nil, nil)
	settings := func(autoLearn bool, claudeVersion, codexVersion string) contract.RoutingSettings {
		value := contract.DefaultRoutingSettings()
		value.ClaudeIdentityAutoLearn, value.CodexIdentityAutoLearn = autoLearn, autoLearn
		value.ClaudeIdentityVersion, value.CodexIdentityVersion = claudeVersion, codexVersion
		return value
	}

	// Baseline: nothing learned yet.
	if got := registry.ClaudeIdentity(settings(true, "", "")); !reflect.DeepEqual(got, DefaultClaudeIdentity()) {
		t.Fatalf("baseline Claude = %+v", got)
	}
	if got := registry.CodexIdentity(settings(true, "", ""), ""); !reflect.DeepEqual(got, DefaultCodexIdentity()) {
		t.Fatalf("baseline Codex = %+v", got)
	}
	if got := registry.CodexIdentity(settings(true, "", ""), "0.156.0"); !reflect.DeepEqual(got, codexIdentityAt("0.156.0")) {
		t.Fatalf("configured Codex baseline = %+v", got)
	}

	// Learned: a learned version below the baseline still wins, because it is
	// what a real client on this machine sends.
	if _, err := registry.LearnClaude(ctx, claudeClientHeaders("2.1.100")); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.LearnCodex(ctx, codexClientHeaders("0.150.0")); err != nil {
		t.Fatal(err)
	}
	learnedClaude := registry.ClaudeIdentity(settings(true, "", ""))
	if learnedClaude.Version != "2.1.100" || learnedClaude.UserAgent != "claude-cli/2.1.100 (external, sdk-cli)" {
		t.Fatalf("learned Claude = %+v", learnedClaude)
	}
	learnedCodex := registry.CodexIdentity(settings(true, "", ""), "")
	if learnedCodex.Version != "0.150.0" || learnedCodex.Headers["originator"] != "codex_cli_rs" {
		t.Fatalf("learned Codex = %+v", learnedCodex)
	}

	// Auto-learn off: the learned identity is ignored, not forgotten.
	if got := registry.ClaudeIdentity(settings(false, "", "")); !reflect.DeepEqual(got, DefaultClaudeIdentity()) {
		t.Fatalf("Claude with learning off = %+v", got)
	}
	if got := registry.CodexIdentity(settings(false, "", ""), ""); !reflect.DeepEqual(got, DefaultCodexIdentity()) {
		t.Fatalf("Codex with learning off = %+v", got)
	}

	// Override: a newer floor rewrites only the declared version.
	floored := registry.ClaudeIdentity(settings(true, "2.1.400", ""))
	if floored.Version != "2.1.400" || floored.UserAgent != "claude-cli/2.1.400 (external, sdk-cli)" ||
		!reflect.DeepEqual(floored.Headers, learnedClaude.Headers) {
		t.Fatalf("floored learned Claude = %+v", floored)
	}
	flooredCodex := registry.CodexIdentity(settings(true, "", "0.170.0"), "")
	if flooredCodex.Version != "0.170.0" || flooredCodex.UserAgent != "codex_cli_rs/0.170.0 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1" ||
		flooredCodex.Headers["originator"] != "codex_cli_rs" {
		t.Fatalf("floored learned Codex = %+v", flooredCodex)
	}
	header := http.Header{}
	ApplyCodexAPIHeaders(header, AccountTokens{AccessToken: "token"}, flooredCodex)
	if header.Get("User-Agent") != flooredCodex.UserAgent || header.Get("version") != "0.170.0" || header.Get("originator") != "codex_cli_rs" {
		t.Fatalf("floored Codex headers = %v", header)
	}
	flooredBaseline := registry.ClaudeIdentity(settings(false, "2.1.400", ""))
	if flooredBaseline.UserAgent != "claude-cli/2.1.400 (external, cli)" ||
		!reflect.DeepEqual(flooredBaseline.Headers, DefaultClaudeIdentity().Headers) {
		t.Fatalf("floored baseline Claude = %+v", flooredBaseline)
	}

	// A stale override below the base never downgrades it.
	if got := registry.ClaudeIdentity(settings(true, "2.1.99", "")); !reflect.DeepEqual(got, learnedClaude) {
		t.Fatalf("stale override changed learned Claude: %+v", got)
	}
	if got := registry.ClaudeIdentity(settings(false, "2.1.200", "")); !reflect.DeepEqual(got, DefaultClaudeIdentity()) {
		t.Fatalf("stale override downgraded baseline Claude: %+v", got)
	}
	if got := registry.CodexIdentity(settings(false, "", "0.150.0"), ""); !reflect.DeepEqual(got, DefaultCodexIdentity()) {
		t.Fatalf("stale override downgraded baseline Codex: %+v", got)
	}

	// Callers cannot mutate the learned identity through a resolved copy.
	learnedClaude.Headers["X-App"] = "mutated"
	if got := registry.ClaudeIdentity(settings(true, "", "")).Headers["X-App"]; got != "cli" {
		t.Fatalf("resolved copy aliased learned headers: %q", got)
	}
}

func TestNilIdentityRegistryResolvesBaselineWithFloor(t *testing.T) {
	var registry *IdentityRegistry
	settings := contract.DefaultRoutingSettings()
	if got := registry.ClaudeIdentity(settings); !reflect.DeepEqual(got, DefaultClaudeIdentity()) {
		t.Fatalf("nil registry Claude = %+v", got)
	}
	if got := registry.CodexIdentityFor(context.Background(), ""); !reflect.DeepEqual(got, DefaultCodexIdentity()) {
		t.Fatalf("nil registry Codex = %+v", got)
	}
	settings.ClaudeIdentityVersion = "2.1.400"
	if got := registry.ClaudeIdentity(settings); got.UserAgent != "claude-cli/2.1.400 (external, cli)" {
		t.Fatalf("nil registry floored Claude = %+v", got)
	}
	if changed, err := registry.LearnClaude(context.Background(), claudeClientHeaders("2.1.300")); changed || err != nil {
		t.Fatalf("nil registry learned: %t, %v", changed, err)
	}
	if err := registry.Hydrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityRegistryReadsPersistedSettings(t *testing.T) {
	settings := contract.DefaultRoutingSettings()
	settings.ClaudeIdentityVersion, settings.CodexIdentityVersion = "2.1.400", "0.170.0"
	registry := NewIdentityRegistry(fixedSettings{settings: settings}, nil)
	if got := registry.ClaudeIdentityFor(context.Background()); got.Version != "2.1.400" {
		t.Fatalf("ClaudeIdentityFor = %+v", got)
	}
	if got := registry.CodexIdentityFor(context.Background(), ""); got.Version != "0.170.0" {
		t.Fatalf("CodexIdentityFor = %+v", got)
	}
	failing := NewIdentityRegistry(fixedSettings{settings: settings, err: errors.New("unavailable")}, nil)
	if got := failing.ClaudeIdentityFor(context.Background()); !reflect.DeepEqual(got, DefaultClaudeIdentity()) {
		t.Fatalf("ClaudeIdentityFor after a failed read = %+v", got)
	}
}

func TestIdentityRegistryHydratesPersistedIdentities(t *testing.T) {
	ctx := context.Background()
	store := &memoryIdentityStore{}
	first := NewIdentityRegistry(nil, store)
	if _, err := first.LearnClaude(ctx, claudeClientHeaders("2.1.300")); err != nil {
		t.Fatal(err)
	}
	if _, err := first.LearnCodex(ctx, codexClientHeaders("0.160.0")); err != nil {
		t.Fatal(err)
	}

	restarted := NewIdentityRegistry(nil, store)
	if err := restarted.Hydrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings := contract.DefaultRoutingSettings()
	if got, want := restarted.ClaudeIdentity(settings), first.ClaudeIdentity(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("hydrated Claude = %+v, want %+v", got, want)
	}
	if got, want := restarted.CodexIdentity(settings, ""), first.CodexIdentity(settings, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("hydrated Codex = %+v, want %+v", got, want)
	}
	// Hydrated identities keep learning one-way.
	if changed, _ := restarted.LearnClaude(ctx, claudeClientHeaders("2.1.299")); changed {
		t.Fatal("hydrated identity was downgraded")
	}

	// Documents that no longer pass the guardrails are dropped.
	for name, document := range map[string]string{
		"not JSON":       `{`,
		"unknown field":  `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0","headers":{"originator":"codex_cli_rs"},"extra":1}`,
		"trailing data":  `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0","headers":{"originator":"codex_cli_rs"}} {}`,
		"below floor":    `{"user_agent":"codex_cli_rs/0.143.0","version":"0.143.0","headers":{"originator":"codex_cli_rs"}}`,
		"version skew":   `{"user_agent":"codex_cli_rs/0.160.0","version":"0.161.0","headers":{"originator":"codex_cli_rs"}}`,
		"branded":        `{"user_agent":"codex_cli_rs/0.160.0 AstrLink","version":"0.160.0","headers":{"originator":"codex_cli_rs"}}`,
		"extra header":   `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0","headers":{"originator":"codex_cli_rs","X-App":"cli"}}`,
		"oversized":      `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0","headers":{"originator":"codex_cli_rs"}}` + strings.Repeat(" ", maxLearnedDocument),
		"no originator":  `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0"}`,
		"canonical key":  `{"user_agent":"codex_cli_rs/0.160.0","version":"0.160.0","headers":{"Originator":"codex_cli_rs"}}`,
		"wrong provider": `{"user_agent":"claude-cli/2.1.300","version":"2.1.300","headers":{"X-Stainless-Package-Version":"0.95.1"}}`,
	} {
		store := &memoryIdentityStore{documents: map[contract.SubscriptionProvider][]byte{
			contract.SubscriptionProviderOpenAICodex: []byte(document),
			contract.SubscriptionProviderXAIGrok:     []byte(`{"user_agent":"xai-grok-workspace/0.3.0","version":"0.3.0"}`),
		}}
		registry := NewIdentityRegistry(nil, store)
		if err := registry.Hydrate(ctx); err != nil {
			t.Fatal(err)
		}
		if got := registry.CodexIdentity(settings, ""); !reflect.DeepEqual(got, DefaultCodexIdentity()) {
			t.Errorf("%s: hydrated %+v", name, got)
		}
		if _, ok := registry.learnedIdentity(contract.SubscriptionProviderXAIGrok); ok {
			t.Errorf("%s: hydrated a Grok identity", name)
		}
	}
}

func TestIdentityRegistryKeepsIdentityWhenPersistFails(t *testing.T) {
	store := &memoryIdentityStore{putErr: errors.New("disk full")}
	registry := NewIdentityRegistry(nil, store)
	changed, err := registry.LearnClaude(context.Background(), claudeClientHeaders("2.1.300"))
	if !changed || err == nil {
		t.Fatalf("LearnClaude = %t, %v; want a changed identity and the persist error", changed, err)
	}
	if got := registry.ClaudeIdentity(contract.DefaultRoutingSettings()); got.Version != "2.1.300" {
		t.Fatalf("identity after a failed persist = %+v", got)
	}
}

func TestIdentityRegistryConcurrentLearningKeepsNewest(t *testing.T) {
	store := &memoryIdentityStore{}
	registry := NewIdentityRegistry(nil, store)
	var group sync.WaitGroup
	for patch := 100; patch < 140; patch++ {
		group.Add(1)
		go func(version string) {
			defer group.Done()
			_, _ = registry.LearnClaude(context.Background(), claudeClientHeaders(version))
			_ = registry.ClaudeIdentity(contract.DefaultRoutingSettings())
		}(fmt.Sprintf("2.1.%d", patch))
	}
	group.Wait()
	if got := registry.ClaudeIdentity(contract.DefaultRoutingSettings()); got.Version != "2.1.139" {
		t.Fatalf("learned %s, want the newest 2.1.139", got.Version)
	}
	restarted := NewIdentityRegistry(nil, store)
	if err := restarted.Hydrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := restarted.ClaudeIdentity(contract.DefaultRoutingSettings()); got.Version != "2.1.139" {
		t.Fatalf("persisted %s, want the newest 2.1.139", got.Version)
	}
}

func TestClaudeForwardHeadersReplayResolvedIdentity(t *testing.T) {
	registry := NewIdentityRegistry(nil, nil)
	if _, err := registry.LearnClaude(context.Background(), claudeClientHeaders("2.1.300")); err != nil {
		t.Fatal(err)
	}
	identity := registry.ClaudeIdentity(contract.DefaultRoutingSettings())
	clientHeaders := http.Header{
		"User-Agent":                {"Anthropic/Python 0.60.0"},
		"X-Stainless-Lang":          {"python"},
		"X-Stainless-Helper-Method": {"stream"},
		"X-Stainless-Async":         {"async:asyncio"},
	}
	header := clientHeaders.Clone()
	ApplyClaudeForwardHeaders(header, AccountTokens{AccessToken: "token"}, clientHeaders, identity, true)
	if header.Get("User-Agent") != identity.UserAgent {
		t.Fatalf("User-Agent = %q, want %q", header.Get("User-Agent"), identity.UserAgent)
	}
	for name, value := range identity.Headers {
		if got := header.Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
	for _, name := range []string{"X-Stainless-Helper-Method", "X-Stainless-Async"} {
		if values, ok := header[name]; ok && values != nil {
			t.Fatalf("caller %s survived: %v", name, values)
		}
	}
}
