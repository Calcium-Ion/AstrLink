package clients

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

const dshFactory = "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n[]\n"

func yamlValue(t *testing.T, text string) any {
	t.Helper()
	value, err := format.ReadYAML(text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

func yamlFile(t *testing.T, path string) any {
	t.Helper()
	text, _ := readFile(t, path)
	return yamlValue(t, text)
}

func dshPatchPath(home agents.Home, profile string) string {
	return filepath.Join(home.Dir(), ".dsh", "profiles", profile, "cordis.patch.yml")
}

func dshCredentialsPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".dsh", ".credentials.yaml")
}

func item(value any, index int) any {
	items, _ := value.([]any)
	if index >= len(items) {
		return nil
	}
	return items[index]
}

func dshDetails() cc.Catalog {
	return cc.Catalog{
		"claude-sonnet-5": {Reasoning: []string{"low", "medium", "high", "max"}, HasReasoning: true, ContextWindow: cc.Some(uint64(1_000_000)), MaxTokens: cc.Some(uint64(64_000))},
		"gpt-5.5":         {Reasoning: []string{"none", "low", "ultra"}, HasReasoning: true},
	}
}

func dshProtocols() map[string]string {
	return map[string]string{"claude-sonnet-5": "anthropic.messages", "gpt-5.5": "openai.responses"}
}

func TestDSHWritesARoutePerProtocolIntoTheFactoryPatch(t *testing.T) {
	home := scratchHome(t)
	web, desktop := targeted(t, DeepSeekHarness, "web"), targeted(t, DeepSeekHarness, "desktop")
	writeFile(t, dshPatchPath(home, "web"), dshFactory)
	fields := models("model", "deepseek-v4-pro")
	chosen := listed(fields, "deepseek-v4-pro", "claude-sonnet-5", "gpt-5.5")
	chosen.Target, chosen.Protocols = "web", dshProtocols()
	if asked := writePlace(t, home, web, chosen, dshDetails(), false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written, _ := readFile(t, dshPatchPath(home, "web"))
	if !strings.HasPrefix(written, "# Your patch layer") || !strings.Contains(written, `"off": none`) {
		t.Fatal(written)
	}
	want := `- id: llm-pi-ai
  config:
    providers:
      astrlink-chat:
        displayName: AstrLink (OpenAI Chat)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: openai-completions
        baseURL: ` + origin + `/v1
        models:
          - id: deepseek-v4-pro
      astrlink-anthropic:
        displayName: AstrLink (Anthropic)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: anthropic-messages
        baseURL: ` + origin + `
        models:
          - id: claude-sonnet-5
            contextWindow: 1000000
            maxTokens: 64000
            reasoningEfforts: {low: low, medium: medium, high: high, max: max}
      astrlink-responses:
        displayName: AstrLink (Responses)
        apiKeyEnv: ASTRLINK_WEB_API_KEY
        api: openai-responses
        baseURL: ` + origin + `/v1
        models:
          - id: gpt-5.5
            reasoningEfforts: {"off": none, low: low}
- id: agent-default-model
  config:
    provider: astrlink-chat
    model: deepseek-v4-pro
`
	if got := yamlValue(t, written); !reflect.DeepEqual(got, yamlValue(t, want)) {
		t.Fatalf("%s", written)
	}
	if got := yamlFile(t, dshCredentialsPath(home)); !reflect.DeepEqual(got, yamlValue(t, "version: 1\nrefs:\n  ASTRLINK_WEB_API_KEY: "+token+"\n")) {
		t.Fatalf("%v", got)
	}
	if runtime.GOOS != "windows" && fileMode(t, dshCredentialsPath(home)) != 0o600 {
		t.Fatal(fileMode(t, dshCredentialsPath(home)))
	}
	statuses, err := cc.StatusOf(home, []cc.Client{DeepSeekHarness}, origin, tokens())
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != cc.Configured || !reflect.DeepEqual(statuses[0].Models, cc.CurrentModels{Model: some("deepseek-v4-pro"), Models: []string{"deepseek-v4-pro", "claude-sonnet-5", "gpt-5.5"}}) || statuses[1].Detected {
		t.Fatalf("%+v", statuses)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	providers := at(item(yamlFile(t, dshPatchPath(home, "web")), 0), "config", "providers")
	for route, url := range map[string]string{"astrlink-chat": "http://127.0.0.1:9000/v1", "astrlink-anthropic": "http://127.0.0.1:9000", "astrlink-responses": "http://127.0.0.1:9000/v1"} {
		if got := at(providers, route, "baseURL"); got != url {
			t.Errorf("%s: %v", route, got)
		}
	}

	// The desktop profile names a token of its own in the same file.
	mkdir(t, home.Dir(), ".dsh", "profiles", "desktop")
	other := connection(fields)
	other.Target = "desktop"
	writePlace(t, home, desktop, other, cc.Catalog{}, false)
	shared := yamlFile(t, dshCredentialsPath(home))
	if at(shared, "refs", "ASTRLINK_DESKTOP_API_KEY") != token || at(shared, "refs", "ASTRLINK_WEB_API_KEY") != token {
		t.Fatalf("%v", shared)
	}

	removePlace(t, home, web)
	if text, _ := readFile(t, dshPatchPath(home, "web")); text != dshFactory {
		t.Fatalf("%q", text)
	}
	shared = yamlFile(t, dshCredentialsPath(home))
	if refs := at(shared, "refs").(map[string]any); refs["ASTRLINK_WEB_API_KEY"] != nil || refs["ASTRLINK_DESKTOP_API_KEY"] != token {
		t.Fatalf("%v", shared)
	}
	// The web place created the shared file; the desktop one found it.
	removePlace(t, home, desktop)
	if _, exists := readFile(t, dshPatchPath(home, "desktop")); exists {
		t.Fatal("desktop patch left")
	}
	if got := yamlFile(t, dshCredentialsPath(home)); !reflect.DeepEqual(got, yamlValue(t, "version: 1")) {
		t.Fatalf("%v", got)
	}
}

const dshUserPatch = "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n\n# 自建网关（手写）\n- id: llm-pi-ai\n  config:\n    providers:\n      gw:                              # 路由 id 创建后不可改名\n        displayName: Gateway\n        apiKeyEnv: GW_API_KEY\n        api: openai-completions\n        baseURL: http://127.0.0.1:9/v1\n        compat: {supportsDeveloperRole: false, maxTokensField: max_tokens}\n        models:\n          - id: deepseek-v4-pro\n            name: DeepSeek V4 Pro\n            input: [text]\n            reasoningEfforts: {\"off\": null, high: high, max: max}\n- id: agent-default-model\n  config:\n    provider: gw\n    model: deepseek-v4-pro\n- id: tools\n  config:\n    mode: !!js process.env.DSH_TOOLS_MODE\n"

const dshUserCredentials = "version: 1\n# my keys\nrefs:\n    GW_API_KEY: sk-gw\nrecords:\n    client-connection/browser-session:\n        kind: grant\n"

var dshKept = []string{"# 自建网关（手写）", "# 路由 id 创建后不可改名", "!!js process.env.DSH_TOOLS_MODE"}

func gwRoute(t *testing.T) any {
	return at(item(yamlValue(t, dshUserPatch), 0), "config", "providers", "gw")
}

func TestDSHJoinsAHandWrittenPatchAndPutsItBack(t *testing.T) {
	home := scratchHome(t)
	web := targeted(t, DeepSeekHarness, "web")
	writeFile(t, dshPatchPath(home, "web"), dshUserPatch)
	writeFile(t, dshCredentialsPath(home), dshUserCredentials)
	chosen := connection(models("model", "claude-opus-5"))
	chosen.Target, chosen.Protocols = "web", map[string]string{"claude-opus-5": "anthropic.messages"}
	asked := writePlace(t, home, web, chosen, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "agent-default-model.config")
	writePlace(t, home, web, chosen, cc.Catalog{}, true)
	written, _ := readFile(t, dshPatchPath(home, "web"))
	for _, kept := range dshKept {
		if !strings.Contains(written, kept) {
			t.Fatalf("%s lacks %s", written, kept)
		}
	}
	value := yamlValue(t, written)
	providers := at(item(value, 0), "config", "providers")
	if len(value.([]any)) != 3 || !reflect.DeepEqual(at(providers, "gw"), gwRoute(t)) || at(providers, "astrlink-anthropic", "baseURL") != origin {
		t.Fatalf("%s", written)
	}
	if got := at(item(value, 1), "config"); !reflect.DeepEqual(got, yamlValue(t, "{provider: astrlink-anthropic, model: claude-opus-5}")) {
		t.Fatalf("%v", got)
	}
	shared, _ := readFile(t, dshCredentialsPath(home))
	if !strings.Contains(shared, "# my keys") || at(yamlValue(t, shared), "refs", "GW_API_KEY") != "sk-gw" || at(yamlValue(t, shared), "refs", "ASTRLINK_WEB_API_KEY") != token {
		t.Fatal(shared)
	}
	status := statusOf(t, home, DeepSeekHarness, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-opus-5" {
		t.Fatalf("%+v", status)
	}

	removePlace(t, home, web)
	if text, _ := readFile(t, dshPatchPath(home, "web")); text != dshUserPatch {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, dshCredentialsPath(home)); text != dshUserCredentials {
		t.Fatalf("%q", text)
	}
}

// dshTuned is what a user adds to AstrLink's chat route by hand.
const dshTuned = "        # 网关不认 developer 角色\n        compat:\n          supportsDeveloperRole: false\n        timeoutMs: 600000\n"

func chatRoute(t *testing.T, text string) any {
	return at(item(yamlValue(t, text), 0), "config", "providers", "astrlink-chat")
}

func tunedKept(t *testing.T, text string) {
	t.Helper()
	route := chatRoute(t, text)
	if !strings.Contains(text, dshTuned) || !reflect.DeepEqual(at(route, "compat"), yamlValue(t, "{supportsDeveloperRole: false}")) || at(route, "timeoutMs") != format.Number("600000") {
		t.Fatalf("%s", text)
	}
}

func TestDSHKeepsWhatTheUserAddedToAnAstrLinkRoute(t *testing.T) {
	home := scratchHome(t)
	web := targeted(t, DeepSeekHarness, "web")
	path := dshPatchPath(home, "web")
	writeFile(t, path, dshUserPatch)
	writeFile(t, dshCredentialsPath(home), dshUserCredentials)
	both := cc.Tokens{"token_01": cc.TokenHint(token), "token_02": cc.TokenHint(otherToken)}
	first := listed(models("model", "deepseek-v4-pro"), "deepseek-v4-pro", "claude-sonnet-5")
	first.Target, first.Protocols = "web", dshProtocols()
	writePlace(t, home, web, first, cc.Catalog{}, true)
	written, _ := readFile(t, path)
	tuned := strings.Replace(written, "      astrlink-chat:\n", "      astrlink-chat:\n"+dshTuned, 1)
	if tuned == written {
		t.Fatal(written)
	}
	writeFile(t, path, tuned)
	tunedKept(t, tuned)
	if state := statusOf(t, home, DeepSeekHarness, origin).State; state != cc.Configured {
		t.Fatal(state)
	}

	// The same setup again asks nothing and changes nothing.
	if asked := writePlace(t, home, web, first, cc.Catalog{}, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	if text, _ := readFile(t, path); text != tuned {
		t.Fatalf("%q", text)
	}

	// Another model list rewrites the models, and the route of a protocol
	// no longer used goes.
	switched := listed(models("model", "deepseek-v4-flash"), "deepseek-v4-flash", "deepseek-v4-pro")
	switched.Target, switched.Protocols = "web", dshProtocols()
	if asked := writePlace(t, home, web, switched, cc.Catalog{}, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written, _ = readFile(t, path)
	tunedKept(t, written)
	value := yamlValue(t, written)
	providers := at(item(value, 0), "config", "providers")
	if !reflect.DeepEqual(at(providers, "astrlink-chat", "models"), yamlValue(t, "[{id: deepseek-v4-flash}, {id: deepseek-v4-pro}]")) ||
		at(providers, "astrlink-anthropic") != nil || !reflect.DeepEqual(at(providers, "gw"), gwRoute(t)) {
		t.Fatalf("%s", written)
	}
	if got := at(item(value, 1), "config"); !reflect.DeepEqual(got, yamlValue(t, "{provider: astrlink-chat, model: deepseek-v4-flash}")) {
		t.Fatalf("%v", got)
	}
	for _, kept := range dshKept {
		if !strings.Contains(written, kept) {
			t.Fatalf("%s lacks %s", written, kept)
		}
	}

	// Another token goes to the credentials; the route names the same
	// credential.
	other := *switched
	other.TokenID, other.Token = "token_02", otherToken
	_, asked, failed := cc.WriteWith(home, web, &other, cc.Catalog{}, both, false)
	if failed != nil || asked == nil || len(asked.Keys) != 0 || asked.Gateway != nil {
		t.Fatalf("%+v %+v", asked, failed)
	}
	if _, _, failed := cc.WriteWith(home, web, &other, cc.Catalog{}, both, true); failed != nil {
		t.Fatal(failed)
	}
	if text, _ := readFile(t, path); text != written {
		t.Fatalf("%q", text)
	}
	if got := at(yamlFile(t, dshCredentialsPath(home)), "refs", "ASTRLINK_WEB_API_KEY"); got != otherToken {
		t.Fatal(got)
	}

	// A new gateway port moves the address alone.
	if err := cc.Sync(home, "http://127.0.0.1:9000/", both); err != nil {
		t.Fatal(err)
	}
	synced, _ := readFile(t, path)
	tunedKept(t, synced)
	if got := at(chatRoute(t, synced), "baseURL"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}
	statuses, err := cc.StatusOf(home, []cc.Client{DeepSeekHarness}, "http://127.0.0.1:9000", both)
	if err != nil || statuses[0].State != cc.Configured {
		t.Fatalf("%+v %v", statuses, err)
	}

	// Removal takes AstrLink's routes whole: dsh rejects a route without
	// models.
	removePlace(t, home, web)
	if text, _ := readFile(t, path); text != dshUserPatch {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, dshCredentialsPath(home)); text != dshUserCredentials {
		t.Fatalf("%q", text)
	}
}

// dshOwnChatRoute is a route the user wrote under the name AstrLink uses.
const dshOwnChatRoute = "- id: llm-pi-ai\n  config:\n    providers:\n      astrlink-chat:\n        displayName: Gateway\n        apiKeyEnv: GW_API_KEY\n        api: openai-completions\n        baseURL: http://127.0.0.1:9/v1\n        # 网关不认 developer 角色\n        compat:\n          supportsDeveloperRole: false\n        models:\n          - id: deepseek-v4-pro\n"

func TestDSHARouteTakenOverKeepsTheUsersProperties(t *testing.T) {
	home := scratchHome(t)
	web := targeted(t, DeepSeekHarness, "web")
	path := dshPatchPath(home, "web")
	writeFile(t, path, dshOwnChatRoute)
	chosen := connection(models("model", "deepseek-v4-flash"))
	chosen.Target = "web"
	asked := writePlace(t, home, web, chosen, cc.Catalog{}, false)
	if asked == nil {
		t.Fatal("asked nothing")
	}
	sameStrings(t, askedKeys(asked), "llm-pi-ai.providers.astrlink-chat")
	writePlace(t, home, web, chosen, cc.Catalog{}, true)
	written, _ := readFile(t, path)
	route := chatRoute(t, written)
	if !strings.Contains(written, "# 网关不认 developer 角色") || !reflect.DeepEqual(at(route, "compat"), yamlValue(t, "{supportsDeveloperRole: false}")) ||
		at(route, "apiKeyEnv") != "ASTRLINK_WEB_API_KEY" || at(route, "baseURL") != origin+"/v1" ||
		!reflect.DeepEqual(at(route, "models"), yamlValue(t, "[{id: deepseek-v4-flash}]")) {
		t.Fatalf("%s", written)
	}

	// Removal puts the user's values back around their own property.
	removePlace(t, home, web)
	if text, _ := readFile(t, path); text != dshOwnChatRoute {
		t.Fatalf("%q", text)
	}
}

func TestDSHAPatchDSHCannotReadIsLeftAlone(t *testing.T) {
	home := scratchHome(t)
	web := targeted(t, DeepSeekHarness, "web")
	path := dshPatchPath(home, "web")
	for _, original := range []string{"providers: {}\n", "- id: llm-pi-ai\n  config: !!js x\n"} {
		writeFile(t, path, original)
		if _, _, failed := cc.WriteWith(home, web, connection(models("model", "deepseek-v4-pro")), cc.Catalog{}, tokens(), false); failed == nil {
			t.Fatalf("%q: wrote", original)
		}
		if text, _ := readFile(t, path); text != original {
			t.Fatalf("%q", text)
		}
	}
	// A patch in `~/.dsh` itself overrides every profile.
	writeFile(t, path, dshFactory)
	writeFile(t, filepath.Join(home.Dir(), ".dsh", "cordis.patch.yml"), "- id: agent-default-model\n  config: {provider: gw, model: x}\n")
	sameStrings(t, statusOf(t, home, DeepSeekHarness, origin).Notes, "overridden")
}
