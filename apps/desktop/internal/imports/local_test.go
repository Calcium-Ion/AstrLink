package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

// Every home here is a temporary directory; nothing reads the real one,
// and the environment is a map.

const (
	claudeKey = "fake-claude-key-0c7d"
	relayKey  = "fake-relay-key-k2Qe"
	openAIKey = "fake-openai-key-T3bA"
	piKeyText = "fake-pi-key-x81P"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func homesOf(host string) *agents.Homes { return &agents.Homes{Host: host} }

func envOf(vars map[string]string) lookupEnv {
	return func(name string) (string, bool) {
		value, ok := vars[name]
		return value, ok
	}
}

func noEnv(string) (string, bool) { return "", false }

func sourcesOf(host string, only ...Tool) []source {
	var picked []source
	for _, src := range locate(homesOf(host)) {
		if slices.Contains(only, src.view.Tool) {
			picked = append(picked, src)
		}
	}
	return picked
}

func readAll(host string, env lookupEnv, ports ...uint16) collected {
	return collect(sourcesOf(host, tools...), readFile, env, ports)
}

func viewOf(read collected) ReadResult {
	result := ReadResult{Candidates: []Candidate{}, Skipped: read.skipped}
	for _, p := range read.found {
		result.Candidates = append(result.Candidates, p.view())
	}
	return result
}

func providerAt(t *testing.T, address string, k key) *provider {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	return &provider{id: "c0", address: address, origin: origin(u), normalized: normalized(u), key: k}
}

func shown(parts ...string) string {
	separator := "/"
	if runtime.GOOS == "windows" {
		separator = `\`
	}
	return "~" + separator + strings.Join(parts, separator)
}

func text(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func fixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env": {
		"ANTHROPIC_BASE_URL": "https://open.bigmodel.cn/api/anthropic/",
		"ANTHROPIC_AUTH_TOKEN": "`+claudeKey+`",
		"ANTHROPIC_MODEL": "glm-5.3[1m]",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3"
	}}`)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model = "relay-large"
model_provider = "relay"

[profiles.fast]
model = "relay-small"

[profiles.openai]
model = "gpt-5.5"
model_provider = "openai"

[model_providers.relay]
name = "Relay Hub"
base_url = "https://api.relayhub.example/v1"
experimental_bearer_token = "`+relayKey+`"

[model_providers.astrlink]
name = "AstrLink"
base_url = "http://localhost:8317/v1"
`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{"OPENAI_API_KEY": "`+openAIKey+`", "tokens": null}`)
	writeFile(t, filepath.Join(home, ".pi", "agent", "models.json"), `{
	  // Pi takes comments and trailing commas.
	  "providers": {
	    "relay": {
	      "baseUrl": "https://api.relayhub.example",
	      "api": "openai-completions",
	      "apiKey": "`+relayKey+`",
	      "models": [{"id": "relay-large"}, {"id": "relay-chat"}],
	    },
	    "deepseek": {
	      "baseUrl": "https://api.deepseek.com/v1",
	      "api": "openai-completions",
	      "apiKey": "$DEEPSEEK_API_KEY",
	      "models": [{"id": "deepseek-chat"}, {"id": "deepseek-reasoner"}],
	    },
	    "kimi": {
	      "baseUrl": "https://api.kimi.com/coding",
	      "api": "anthropic-messages",
	      "apiKey": "`+piKeyText+`",
	    },
	    "astrlink": {
	      "baseUrl": "http://127.0.0.1:8317/v1",
	      "apiKey": "astr_fake_token_value",
	    },
	  },
	}`)
	return home
}

func TestLocateListsEachToolWithHomeRelativePaths(t *testing.T) {
	opened, err := NewLocalImports().Open(homesOf(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id    string
		tool  Tool
		paths []string
	}
	want := []row{
		{"s0", ToolClaude, []string{shown(".claude", "settings.json")}},
		{"s1", ToolCodex, []string{shown(".codex", "config.toml"), shown(".codex", "auth.json")}},
		{"s2", ToolPi, []string{shown(".pi", "agent", "models.json")}},
	}
	if len(opened.Sources) != len(want) {
		t.Fatal(opened.Sources)
	}
	for i, source := range opened.Sources {
		if source.ID != want[i].id || source.Tool != want[i].tool || !slices.Equal(source.Paths, want[i].paths) || source.WSL != nil {
			t.Errorf("%d: %+v", i, source)
		}
	}
	if len(opened.Session) != 32 {
		t.Fatal(opened.Session)
	}
}

func TestReadsOnlyTheCheckedTools(t *testing.T) {
	home := fixtureHome(t)
	imports := NewLocalImports()
	opened, err := imports.Open(homesOf(home))
	if err != nil {
		t.Fatal(err)
	}
	var codex string
	for _, source := range opened.Sources {
		if source.Tool == ToolCodex {
			codex = source.ID
		}
	}
	if _, err := imports.chosen(opened.Session, nil); err == nil {
		t.Fatal("nothing chosen was accepted")
	}
	if _, err := imports.chosen(opened.Session, []string{"s9"}); err == nil || err.Error() != "unknown source s9" {
		t.Fatal(err)
	}
	chosen, err := imports.chosen(opened.Session, []string{codex})
	if err != nil {
		t.Fatal(err)
	}
	var opens []string
	read := collect(chosen, func(path string) (*string, string) {
		opens = append(opens, path)
		return readFile(path)
	}, noEnv, nil)
	codexDir := filepath.Join(home, ".codex")
	if !slices.Equal(opens, []string{filepath.Join(codexDir, "config.toml"), filepath.Join(codexDir, "auth.json")}) {
		t.Fatal(opens)
	}
	for _, p := range read.found {
		if len(p.sources) != 1 || p.sources[0].Tool != ToolCodex || p.sources[0].WSL != nil {
			t.Fatal(p.sources)
		}
	}
}

func TestExtractsEveryToolAndMergesTheSameProvider(t *testing.T) {
	read := readAll(fixtureHome(t), envOf(map[string]string{"DEEPSEEK_API_KEY": "fake-deepseek-key-2041"}), 8317)
	result := viewOf(read)
	hint := func(secret string) KeyView { return KeyView{Status: KeyFound, Hint: KeyHint(secret)} }
	type row struct {
		name      string
		address   string
		protocols []string
		models    []string
		key       KeyView
		tools     []Tool
	}
	want := []row{
		{"<nil>", "https://open.bigmodel.cn/api/anthropic", []string{"anthropic.messages"}, []string{"glm-5.3", "glm-5.3-flash"}, hint(claudeKey), []Tool{ToolClaude}},
		{"Relay Hub", "https://api.relayhub.example/v1", []string{"openai.responses", "openai.chat"}, []string{"relay-large", "relay-small", "relay-chat"}, hint(relayKey), []Tool{ToolCodex, ToolPi}},
		{"OpenAI", officialOpenAI, []string{"openai.responses"}, []string{"gpt-5.5"}, hint(openAIKey), []Tool{ToolCodex}},
		{"deepseek", "https://api.deepseek.com/v1", []string{"openai.chat"}, []string{"deepseek-chat", "deepseek-reasoner"}, hint("fake-deepseek-key-2041"), []Tool{ToolPi}},
		{"kimi", "https://api.kimi.com/coding", []string{"anthropic.messages"}, []string{}, hint(piKeyText), []Tool{ToolPi}},
	}
	if len(result.Candidates) != len(want) {
		t.Fatalf("%+v", result.Candidates)
	}
	for i, candidate := range result.Candidates {
		var sourceTools []Tool
		for _, source := range candidate.Sources {
			sourceTools = append(sourceTools, source.Tool)
		}
		w := want[i]
		if text(candidate.Name) != w.name || candidate.Address != w.address || !slices.Equal(candidate.Protocols, w.protocols) ||
			!slices.Equal(candidate.Models, w.models) || candidate.Key.Status != w.key.Status || text(candidate.Key.Hint) != text(w.key.Hint) ||
			!slices.Equal(sourceTools, w.tools) || candidate.ID != fmt.Sprintf("c%d", i) {
			t.Errorf("%d: %+v", i, candidate)
		}
	}
	var skipped []string
	for _, skip := range result.Skipped {
		skipped = append(skipped, fmt.Sprintf("%s %s %s", skip.Reason, skip.Tool, text(skip.Name)))
	}
	if !slices.Equal(skipped, []string{"astrlink codex astrlink", "astrlink pi astrlink"}) {
		t.Fatal(skipped)
	}

	// Nothing the dialog receives carries a key.
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{claudeKey, relayKey, openAIKey, piKeyText, "fake-deepseek-key-2041", "astr_fake"} {
		if strings.Contains(string(serialized), secret) {
			t.Error(secret)
		}
	}
}

func TestSourceFilesStayByteIdentical(t *testing.T) {
	home := fixtureHome(t)
	files := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".codex", "auth.json"),
		filepath.Join(home, ".pi", "agent", "models.json"),
	}
	type state struct {
		bytes    string
		modified time.Time
	}
	snapshot := func() []state {
		var states []state
		for _, file := range files {
			bytes, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			states = append(states, state{string(bytes), info.ModTime()})
		}
		return states
	}
	before := snapshot()
	readAll(home, noEnv)
	if after := snapshot(); !slices.Equal(before, after) {
		t.Fatal("a source file changed")
	}
	entries, err := os.ReadDir(filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"auth.json", "config.toml"}) {
		t.Fatal(names)
	}
}

func TestKeysBehindVariablesAndCommandsAreNeverRunOrGuessed(t *testing.T) {
	home := t.TempDir()
	ran := filepath.Join(home, "ran")
	command := "touch " + ran
	settings, err := json.Marshal(map[string]any{
		"apiKeyHelper": command,
		"env":          map[string]any{"ANTHROPIC_BASE_URL": "https://relay.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), string(settings))
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `[model_providers.env]
base_url = "https://env.example.com/v1"
env_key = "RELAY_KEY"

[model_providers.set]
base_url = "https://set.example.com/v1"
env_key = "SET_KEY"

[model_providers.command]
base_url = "https://command.example.com/v1"
auth = { command = "touch", args = ["`+strings.ReplaceAll(ran, `\`, `\\`)+`"] }

[model_providers.bare]
base_url = "https://bare.example.com/v1"
`)
	models, err := json.Marshal(map[string]any{"providers": map[string]any{
		"a-command":  map[string]any{"baseUrl": "https://a.example.com", "apiKey": "!" + command},
		"b-template": map[string]any{"baseUrl": "https://b.example.com", "apiKey": "sk-${PART_ONE}-$PART_TWO"},
		"c-missing":  map[string]any{"baseUrl": "https://c.example.com", "apiKey": "${NOT_SET}"},
		"d-bare":     map[string]any{"baseUrl": "https://d.example.com", "apiKey": "OLD_STYLE_KEY"},
		"e-escaped":  map[string]any{"baseUrl": "https://e.example.com", "apiKey": "$$literal$!key-1234"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".pi", "agent", "models.json"), string(models))
	read := readAll(home, envOf(map[string]string{
		"SET_KEY":  "fake-set-key-9999",
		"PART_ONE": "first",
		"PART_TWO": "second-5678",
	}))
	if _, err := os.Stat(ran); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a key command ran")
	}
	type row struct {
		address string
		view    string
	}
	viewText := func(k KeyView) string { return fmt.Sprintf("%s %s %s", k.Status, text(k.Hint), k.Name) }
	want := []row{
		{"https://relay.example.com", "command <nil> "},
		{"https://env.example.com/v1", "env <nil> RELAY_KEY"},
		{"https://set.example.com/v1", "found …9999 "},
		{"https://command.example.com/v1", "command <nil> "},
		{"https://bare.example.com/v1", "missing <nil> "},
		{"https://a.example.com", "command <nil> "},
		{"https://b.example.com", "found …5678 "},
		{"https://c.example.com", "env <nil> NOT_SET"},
		{"https://d.example.com", "env <nil> OLD_STYLE_KEY"},
		{"https://e.example.com", "found …1234 "},
	}
	if len(read.found) != len(want) {
		t.Fatal(len(read.found))
	}
	for i, p := range read.found {
		if p.address != want[i].address || viewText(p.key.view()) != want[i].view {
			t.Errorf("%d: %s %s", i, p.address, viewText(p.key.view()))
		}
	}
	if read.found[6].key != foundKey("sk-first-second-5678") || read.found[9].key != foundKey("$literal!key-1234") {
		t.Fatal(read.found[6].key, read.found[9].key)
	}
}

func TestSkipsAstrLinkItselfAndChatGPTSignIn(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"env": {"ANTHROPIC_BASE_URL": "http://localhost:8317", "ANTHROPIC_AUTH_TOKEN": "astr_fake_token_value"}}`)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `[model_providers.gateway]
base_url = "http://127.0.0.1:8317/v1"
experimental_bearer_token = "fake-not-astr-key-1"

[model_providers.local]
base_url = "http://127.0.0.1:3425/v1"
experimental_bearer_token = "fake-local-key-2222"
`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"),
		`{"OPENAI_API_KEY": "`+openAIKey+`", "tokens": {"id_token": "fake", "access_token": "fake"}}`)
	read := readAll(home, noEnv, 8317)
	if len(read.found) != 1 || read.found[0].address != "http://127.0.0.1:3425/v1" {
		t.Fatal(read.found)
	}
	var skipped []string
	for _, skip := range read.skipped {
		skipped = append(skipped, fmt.Sprintf("%s %s %s", skip.Reason, skip.Tool, text(skip.Name)))
	}
	if !slices.Equal(skipped, []string{
		"astrlink claude localhost:8317",
		"chatgpt_login codex <nil>",
		"astrlink codex gateway",
		"missing pi <nil>",
	}) {
		t.Fatal(skipped)
	}
	// The ChatGPT sign-in's API key is not offered as an OpenAI key.
	serialized, err := json.Marshal(viewOf(read))
	if err != nil || strings.Contains(string(serialized), openAIKey) {
		t.Fatal(string(serialized), err)
	}
}

func TestReportsMissingUnreadableAndInvalidFiles(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"env": `)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "model = [")
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), "\xff\xfe\x00")
	writeFile(t, filepath.Join(home, ".pi", "agent", "models.json"), `{"providers": {
		"a-bad": {"baseUrl": "https://user:`+piKeyText+`@relay.example.com", "apiKey": "x"},
		"b-vertex": {"baseUrl": "https://v.example.com", "api": "google-vertex"},
		"c-radius": {"baseUrl": "https://r.example.com", "oauth": "radius"},
	}}`)
	read := readAll(home, noEnv)
	if len(read.found) != 0 {
		t.Fatal(read.found)
	}
	row := func(skip Skipped) string {
		return fmt.Sprintf("%s|%s|%s|%s", skip.Reason, text(skip.Name), text(skip.Path), text(skip.Detail))
	}
	var got []string
	for _, skip := range read.skipped {
		got = append(got, row(skip))
	}
	if want := []string{
		"invalid|<nil>|" + shown(".claude", "settings.json") + "|is not valid JSON (line 1, column 8)",
		"invalid|<nil>|" + shown(".codex", "config.toml") + "|is not valid TOML",
		"unreadable|<nil>|" + shown(".codex", "auth.json") + "|not_text",
		"unsupported|b-vertex|<nil>|google-vertex",
		"sign_in|c-radius|<nil>|<nil>",
		"invalid_address|a-bad|<nil>|<nil>",
	}; !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	serialized, err := json.Marshal(read.skipped)
	if err != nil || strings.Contains(string(serialized), piKeyText) {
		t.Fatal(string(serialized), err)
	}

	missing := readAll(t.TempDir(), noEnv)
	got = nil
	for _, skip := range missing.skipped {
		got = append(got, fmt.Sprintf("%s|%s", skip.Reason, text(skip.Path)))
	}
	if want := []string{
		"missing|" + shown(".claude", "settings.json"),
		"missing|" + shown(".codex", "config.toml"),
		"missing|" + shown(".pi", "agent", "models.json"),
	}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
}

func TestReadsCodexAuthLikeSerde(t *testing.T) {
	for _, tc := range []struct {
		text, err string
		apiKey    string
		chatgpt   bool
	}{
		{`{"OPENAI_API_KEY": "sk-1", "tokens": null}`, "", "sk-1", false},
		{`{"tokens": {"id_token": "x"}}`, "", "<nil>", true},
		{`{"OPENAI_API_KEY": null}`, "", "<nil>", false},
		{"", "is not valid JSON (line 1, column 0)", "", false},
		{" \n  ", "is not valid JSON (line 2, column 2)", "", false},
		{`{"OPENAI_API_KEY": 1}`, "is not valid JSON", "", false},
		{`[]`, "is not a JSON object", "", false},
	} {
		auth, err := readCodexAuth(tc.text)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%q: %v", tc.text, err)
			}
			continue
		}
		if err != nil || text(auth.apiKey) != tc.apiKey || auth.chatgpt != tc.chatgpt {
			t.Errorf("%q: %+v, %v", tc.text, auth, err)
		}
	}
}

func TestMergesOnlyTheSameAddressAndKey(t *testing.T) {
	var found []*provider
	claude := SourceRef{Tool: ToolClaude}
	distribution := "Ubuntu"
	inWSL := SourceRef{Tool: ToolCodex, WSL: &distribution}
	for _, tc := range []struct {
		address string
		key     key
		place   SourceRef
	}{
		{"https://Relay.example.com", foundKey("fake-key-aaaa"), claude},
		{"https://relay.example.com/v1/", foundKey("fake-key-aaaa"), inWSL},
		{"https://relay.example.com/v1", foundKey("fake-key-bbbb"), inWSL},
		{"https://relay.example.com:8443/v1", foundKey("fake-key-aaaa"), inWSL},
		{"http://relay.example.com/v1", foundKey("fake-key-aaaa"), inWSL},
		{"https://relay.example.com/claude", foundKey("fake-key-aaaa"), inWSL},
	} {
		u, err := url.Parse(tc.address)
		if err != nil {
			t.Fatal(err)
		}
		found = merge(found, entry{address: strings.TrimRight(tc.address, "/"), key: tc.key}, u, tc.place)
	}
	var got []string
	for _, p := range found {
		got = append(got, fmt.Sprintf("%s %d", p.normalized, len(p.sources)))
	}
	if !slices.Equal(got, []string{
		"https://relay.example.com 2",
		"https://relay.example.com 1",
		"https://relay.example.com:8443 1",
		"http://relay.example.com 1",
		"https://relay.example.com/claude 1",
	}) {
		t.Fatal(got)
	}
}

// edit decodes a service input and changes it.
func edit(t *testing.T, input string, change func(map[string]any)) json.RawMessage {
	t.Helper()
	value, err := format.DecodeJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	change(value.(map[string]any))
	return json.RawMessage(format.Encode(value))
}

func httpOf(object map[string]any) map[string]any { return object["http"].(map[string]any) }

func TestAttachesTheKeyOnlyToTheSameOrigin(t *testing.T) {
	provider := providerAt(t, "https://open.bigmodel.cn/api/anthropic", foundKey(claudeKey))
	input := `{"name": "GLM", "kind": "glm_coding", "enabled": true, "models": ["glm-5.3"],
		"http": {"base_url": "https://open.bigmodel.cn/api/coding/paas/v4", "auth": {"scheme": "bearer"}},
		"capabilities": []}`
	create := []string{"name", "kind", "enabled", "models", "http", "capabilities"}
	authorized, secret, err := authorizeLocal(provider, json.RawMessage(input), create)
	if err != nil || secret != claudeKey {
		t.Fatal(secret, err)
	}
	if got := at(decoded(t, authorized), "http", "credential", "secret"); got != claudeKey {
		t.Fatal(got)
	}

	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"another host", func(o map[string]any) { httpOf(o)["base_url"] = "https://evil.example.com/api" }},
		{"plain http", func(o map[string]any) { httpOf(o)["base_url"] = "http://open.bigmodel.cn/api" }},
		{"another port", func(o map[string]any) { httpOf(o)["base_url"] = "https://open.bigmodel.cn:444/api" }},
		{"a user", func(o map[string]any) { httpOf(o)["base_url"] = "https://user@open.bigmodel.cn/api" }},
		{"no sign-in", func(o map[string]any) { httpOf(o)["auth"] = map[string]any{"scheme": "none"} }},
		{"a custom header", func(o map[string]any) {
			httpOf(o)["auth"] = map[string]any{"scheme": "custom_header", "header_name": "X-Key"}
		}},
		{"a subscription", func(o map[string]any) { o["kind"] = "codex_subscription" }},
		{"vertex", func(o map[string]any) { o["kind"] = "vertex" }},
		{"another key", func(o map[string]any) { httpOf(o)["credential"] = map[string]any{"secret": "other"} }},
		{"headers", func(o map[string]any) { httpOf(o)["headers"] = map[string]any{"X-Echo": "1"} }},
		{"a proxy", func(o map[string]any) { o["proxy"] = map[string]any{"mode": "none"} }},
	} {
		_, _, err := authorizeLocal(provider, edit(t, input, tc.change), create)
		if err == nil || strings.Contains(err.Error(), claudeKey) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}

	// A provider whose key AstrLink cannot read takes the pasted one.
	pending := providerAt(t, "https://api.deepseek.com/v1", key{keyEnv, "DEEPSEEK_API_KEY"})
	pasted := func(credential any) json.RawMessage {
		return edit(t, input, func(o map[string]any) {
			o["kind"] = "deepseek"
			httpOf(o)["base_url"] = "https://api.deepseek.com/v1"
			if credential != nil {
				httpOf(o)["credential"] = credential
			}
		})
	}
	if _, _, err := authorizeLocal(pending, pasted(nil), create); err == nil || err.Error() != "paste the key first" {
		t.Fatal(err)
	}
	if _, secret, err := authorizeLocal(pending, pasted(map[string]any{"secret": "fake-pasted-key-7777"}), create); err != nil || secret != "fake-pasted-key-7777" {
		t.Fatal(secret, err)
	}
	if _, _, err := authorizeLocal(pending, pasted(map[string]any{"secret": "a\nb"}), create); err == nil {
		t.Fatal("a key with a line break was accepted")
	}

	// The model-list request takes no other fields.
	probe := `{"kind": "glm_coding", "protocol": "openai.models",
		"http": {"base_url": "https://open.bigmodel.cn/api/coding/paas/v4", "auth": {"scheme": "bearer"}}}`
	probing := []string{"kind", "http", "protocol"}
	if _, _, err := authorizeLocal(provider, json.RawMessage(probe), probing); err != nil {
		t.Fatal(err)
	}
	withService := edit(t, probe, func(o map[string]any) { o["service_id"] = "svc_other" })
	if _, _, err := authorizeLocal(provider, withService, probing); err == nil {
		t.Fatal("a service id was accepted")
	}
}

func TestRedactsAKeyCoreEchoes(t *testing.T) {
	secret := relayKey
	if got := redact("upstream said: bad key "+relayKey, &secret); got != "unable to add the service" {
		t.Fatal(got)
	}
}

func sessionWith(t *testing.T, imports *LocalImports, p *provider) string {
	t.Helper()
	opened, err := imports.open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := imports.store(opened.Session, collected{found: []*provider{p}, skipped: []Skipped{}}); err != nil {
		t.Fatal(err)
	}
	return opened.Session
}

const (
	relayInput = `{"name": "Relay", "kind": "newapi", "models": ["relay-large"],
		"http": {"base_url": "https://api.relayhub.example/v1", "auth": {"scheme": "bearer"}}, "capabilities": []}`
	relayProbe = `{"kind": "newapi", "protocol": "openai.models",
		"http": {"base_url": "https://api.relayhub.example/v1", "auth": {"scheme": "bearer"}}}`
)

func TestCreatesEachProviderOnceAndProbesItOnce(t *testing.T) {
	imports := NewLocalImports()
	session := sessionWith(t, imports, providerAt(t, "https://api.relayhub.example/v1", foundKey(relayKey)))
	if _, _, err := imports.beginProbe(session, "c0", json.RawMessage(relayProbe)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := imports.beginProbe(session, "c0", json.RawMessage(relayProbe)); err == nil || err.Error() != "the model list was already requested" {
		t.Fatal(err)
	}
	if _, _, err := imports.beginConfirm(session, "c9", json.RawMessage(relayInput)); err == nil {
		t.Fatal("an unknown provider was confirmed")
	}
	if _, _, err := imports.beginConfirm(session, "c0", json.RawMessage(relayInput)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := imports.beginConfirm(session, "c0", json.RawMessage(relayInput)); err == nil || err.Error() != "this provider is being imported" {
		t.Fatal(err)
	}
	imports.finishConfirm(session, "c0", false)
	if _, _, err := imports.beginConfirm(session, "c0", json.RawMessage(relayInput)); err != nil {
		t.Fatal(err)
	}
	imports.finishConfirm(session, "c0", true)
	if _, _, err := imports.beginConfirm(session, "c0", json.RawMessage(relayInput)); err == nil || err.Error() != "this provider was already imported" {
		t.Fatal(err)
	}
	if imports.session.found[0].key.kind != keyMissing {
		t.Fatal("an imported provider kept its key")
	}
}

func TestDismissingOrIdlingDropsTheKeys(t *testing.T) {
	imports := NewLocalImports()
	session := sessionWith(t, imports, providerAt(t, "https://api.relayhub.example/v1", foundKey(relayKey)))
	imports.Dismiss("someone-else")
	if imports.session == nil {
		t.Fatal("another session's dismissal closed this one")
	}
	imports.Dismiss(session)
	if imports.session != nil {
		t.Fatal("the session stayed")
	}
	if _, err := imports.chosen(session, []string{"s0"}); err == nil || err.Error() != closedImport {
		t.Fatal(err)
	}

	// Before a read the session holds no keys and never times out.
	idle := newLocalImports(0)
	opened, err := idle.open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idle.chosen(opened.Session, nil); err == nil || err.Error() == closedImport {
		t.Fatal(err)
	}
	if _, err := idle.store(opened.Session, collected{found: []*provider{providerAt(t, "https://a.example.com", foundKey(relayKey))}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := idle.beginProbe(opened.Session, "c0", json.RawMessage(`{}`)); err == nil || err.Error() != closedImport {
		t.Fatal(err)
	}
	if idle.session != nil {
		t.Fatal("an idle session kept its keys")
	}

	// A newer dialog replaces the older one and its keys.
	replaced := sessionWith(t, imports, providerAt(t, "https://a.example.com", foundKey(relayKey)))
	if _, err := imports.open(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := imports.beginProbe(replaced, "c0", json.RawMessage(`{}`)); err == nil || err.Error() != closedImport {
		t.Fatal(err)
	}
}

func TestAWatchedReadDropsItsKeysOnceIdle(t *testing.T) {
	imports := newLocalImports(time.Hour)
	var waits []time.Duration
	var checks []func()
	imports.schedule = func(wait time.Duration, check func()) {
		waits = append(waits, wait)
		checks = append(checks, check)
	}
	home := fixtureHome(t)
	opened, err := imports.Open(homesOf(home))
	if err != nil {
		t.Fatal(err)
	}
	result, err := imports.Read(opened.Session, []string{"s0"}, nil)
	if err != nil || len(result.Candidates) != 1 {
		t.Fatal(result, err)
	}
	// A second read keeps the one watch.
	if _, err := imports.Read(opened.Session, []string{"s0"}, nil); err != nil || len(checks) != 1 || waits[0] != time.Hour {
		t.Fatal(waits, err)
	}
	// A check before the hour is up waits for the rest of it.
	checks[0]()
	if imports.session == nil || len(checks) != 2 || waits[1] <= 0 || waits[1] > time.Hour {
		t.Fatal(waits)
	}
	imports.session.touched = time.Now().Add(-time.Hour)
	checks[1]()
	if imports.session != nil || len(checks) != 2 {
		t.Fatal("the idle session stayed")
	}
	// A check for a closed session does nothing.
	checks[0]()
	if len(checks) != 2 {
		t.Fatal("a closed session was watched")
	}
}

type localCore struct {
	created []json.RawMessage
	probed  []json.RawMessage
	err     error
}

func (c *localCore) CreateService(_ context.Context, input json.RawMessage) (control.ServiceRecord, error) {
	c.created = append(c.created, input)
	if c.err != nil {
		return control.ServiceRecord{}, c.err
	}
	return control.ServiceRecord{Service: json.RawMessage(`{"id":"svc_relay"}`), ETag: "1"}, nil
}

func (c *localCore) ProbeDraftServiceModels(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	c.probed = append(c.probed, input)
	if c.err != nil {
		return nil, c.err
	}
	return json.RawMessage(`{"models":[]}`), nil
}

func TestProbeAndConfirmSendTheKeyToCoreAndKeepItOutOfErrors(t *testing.T) {
	imports := NewLocalImports()
	session := sessionWith(t, imports, providerAt(t, "https://api.relayhub.example/v1", foundKey(relayKey)))
	core := &localCore{err: errors.New("upstream said: bad key " + relayKey)}
	if _, err := imports.Probe(context.Background(), core, session, "c0", json.RawMessage(relayProbe)); err == nil || err.Error() != "unable to add the service" {
		t.Fatal(err)
	}
	if len(core.probed) != 1 || at(decoded(t, core.probed[0]), "http", "credential", "secret") != relayKey {
		t.Fatal(core.probed)
	}
	// A failed create leaves the provider ready for another try.
	if _, err := imports.Confirm(context.Background(), core, session, "c0", json.RawMessage(relayInput)); err == nil || strings.Contains(err.Error(), relayKey) {
		t.Fatal(err)
	}
	core.err = nil
	record, err := imports.Confirm(context.Background(), core, session, "c0", json.RawMessage(relayInput))
	if err != nil || record.ETag != "1" || len(core.created) != 2 || at(decoded(t, core.created[1]), "http", "credential", "secret") != relayKey {
		t.Fatal(record, err)
	}
	if _, err := imports.Confirm(context.Background(), core, session, "c0", json.RawMessage(relayInput)); err == nil || err.Error() != "this provider was already imported" {
		t.Fatal(err)
	}
}

func TestTheDialogReceivesExactlyItsFields(t *testing.T) {
	distribution := "Ubuntu"
	hint := "…1234"
	result := ReadResult{
		Candidates: []Candidate{{ID: "c0", Address: "https://a.example.com", Protocols: []string{}, Models: []string{},
			Key: KeyView{Status: KeyFound}, Sources: []SourceRef{{Tool: ToolCodex, WSL: &distribution}}}},
		Skipped: []Skipped{{Reason: SkipMissing, Tool: ToolPi}},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"candidates":[{"id":"c0","name":null,"address":"https://a.example.com","protocols":[],"models":[],`+
		`"key":{"status":"found","hint":null},"sources":[{"tool":"codex","wsl":"Ubuntu"}]}],`+
		`"skipped":[{"reason":"missing","tool":"pi","wsl":null,"name":null,"path":null,"detail":null}]}` {
		t.Fatal(string(encoded))
	}
	for _, tc := range []struct {
		key  KeyView
		want string
	}{
		{KeyView{Status: KeyFound, Hint: &hint}, `{"status":"found","hint":"…1234"}`},
		{KeyView{Status: KeyEnv, Name: "RELAY_KEY"}, `{"status":"env","name":"RELAY_KEY"}`},
		{KeyView{Status: KeyCommand}, `{"status":"command"}`},
		{KeyView{Status: KeyMissing}, `{"status":"missing"}`},
	} {
		if encoded, err := json.Marshal(tc.key); err != nil || string(encoded) != tc.want {
			t.Errorf("%s, %v", encoded, err)
		}
	}
}

func TestGatewayPortsComeFromTheReadyAnnouncement(t *testing.T) {
	if got := GatewayPorts("http://127.0.0.1:18318", "http://localhost:18318", "not a url\x7f"); !slices.Equal(got, []uint16{18318, 18318}) {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		address string
		local   bool
	}{
		{"http://localhost:1", true},
		{"http://app.localhost:1", true},
		{"http://127.1.2.3:1", true},
		{"http://[::1]:1", true},
		{"http://0.0.0.0:1", true},
		{"http://[::ffff:127.0.0.1]:1", true},
		{"http://LOCALHOST:1", true},
		{"http://192.168.1.2:1", false},
		{"https://localhost.example.com", false},
	} {
		u, err := url.Parse(tc.address)
		if err != nil {
			t.Fatal(err)
		}
		if loopback(u) != tc.local {
			t.Errorf("%s", tc.address)
		}
	}
	for _, tc := range []struct{ address, origin, authority string }{
		{"https://Relay.Example.com:443/v1", "https://relay.example.com", "relay.example.com"},
		{"http://[::1]:8317/v1", "http://[::1]:8317", "[::1]:8317"},
		{"http://relay.example.com:80", "http://relay.example.com", "relay.example.com"},
	} {
		u, err := url.Parse(tc.address)
		if err != nil {
			t.Fatal(err)
		}
		if origin(u) != tc.origin || authority(u) != tc.authority {
			t.Errorf("%s: %s %s", tc.address, origin(u), authority(u))
		}
	}
}
