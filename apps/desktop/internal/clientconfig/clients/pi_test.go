package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// route is details for Pi's `pi-route` entry.
func route(details cc.ModelDetails) cc.Catalog { return cc.Catalog{"pi-route": details} }

func reasoning(levels ...string) cc.ModelDetails {
	if levels == nil {
		levels = []string{}
	}
	return cc.ModelDetails{Reasoning: levels, HasReasoning: true}
}

// piCurrent is what Pi's config holds once AstrLink wrote model alone.
func piCurrent(model string) cc.CurrentModels {
	return cc.CurrentModels{Model: some(model), Models: []string{model}}
}

func planWriteWith(t *testing.T, client cc.Client, existing []*string, connection *cc.Connection, details cc.Catalog, previous *cc.Record) *cc.WritePlan {
	t.Helper()
	plan, err := cc.PlanWrite(client, existing, connection, details, previous)
	if err != nil {
		t.Fatalf("plan: %+v", err)
	}
	return plan
}

func piList(t *testing.T, text string) any {
	t.Helper()
	return at(decode(t, text), "providers", "astrlink", "models")
}

func TestPiEndpointsAndStartupModelAreWrittenToTwoFiles(t *testing.T) {
	plan := planWrite(t, Pi, texts(missing, missing), connection(piRoute()), nil)
	sameStrings(t, plan.Conflicts)
	sameJSON(t, plan.Contents[0], `{"providers": {"astrlink": {"name": "AstrLink", "baseUrl": "http://127.0.0.1:18317/v1", "api": "openai-responses", "apiKey": "`+token+`", "models": [{"id": "pi-route"}]}}}`)
	sameJSON(t, plan.Contents[1], `{"defaultProvider": "astrlink", "defaultModel": "pi-route"}`)
	if strings.Contains(plan.Contents[1], token) || !plan.Record.CreatedFile || plan.Record.BaseURL != "http://127.0.0.1:18317/v1" {
		t.Fatalf("%+v", plan.Record)
	}
	existing := texts(plan.Contents[0], plan.Contents[1])
	if state := inspect(Pi, existing, plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	// Both created files go.
	sameRemovals(t, planRemove(t, Pi, existing, plan.Record), deleted, deleted)
}

func TestPiFilesKeepCommentsOtherProvidersAndPreferences(t *testing.T) {
	models := "{\n  // local servers\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\", \"apiKey\": \"sk-other\"},\n  },\n}\n"
	settings := "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"ollama\",\n  \"defaultModel\": \"qwen\"\n}\n"
	plan := planWrite(t, Pi, texts(models, settings), connection(piRoute()), nil)
	sameStrings(t, plan.Conflicts, "defaultProvider", "defaultModel")
	if !strings.HasPrefix(plan.Contents[0], "{\n  // local servers\n") || !strings.Contains(plan.Contents[0], `"apiKey": "sk-other"`) {
		t.Fatal(plan.Contents[0])
	}
	sameJSON(t, plan.Contents[1], `{"theme": "dark", "defaultProvider": "astrlink", "defaultModel": "pi-route"}`)
	if plan.Record.CreatedFile {
		t.Fatal("created")
	}
	removals := planRemove(t, Pi, texts(plan.Contents[0], plan.Contents[1]), plan.Record)
	if len(removals) != 2 || removals[0].Kind != cc.RemovalWrite || removals[1].Kind != cc.RemovalWrite {
		t.Fatalf("%+v", removals)
	}
	left := removals[0].Contents
	if !strings.Contains(left, "// local servers") || !strings.Contains(left, "ollama") || strings.Contains(left, "astrlink") {
		t.Fatal(left)
	}
	// The replaced settings come back.
	sameJSON(t, removals[1].Contents, settings)
}

func TestPiModelChoicesAndEditedModelListsBelongToTheUser(t *testing.T) {
	plan := planWrite(t, Pi, texts(missing, missing), connection(piRoute()), nil)
	// Saving another model in Pi's picker is not a connection change.
	picked := strings.ReplaceAll(plan.Contents[1], "astrlink", "anthropic")
	edited := strings.Replace(plan.Contents[0], `"id": "pi-route"`, `"id": "pi-route", "reasoning": true`, 1)
	if edited == plan.Contents[0] {
		t.Fatal(plan.Contents[0])
	}
	existing := texts(edited, picked)
	if state := inspect(Pi, existing, plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	synced, record := planSync(t, Pi, existing, plan.Record, "http://127.0.0.1:9000")
	sameStrings(t, synced, strings.ReplaceAll(edited, "127.0.0.1:18317", "127.0.0.1:9000"), picked)
	if record.BaseURL != "http://127.0.0.1:9000/v1" {
		t.Fatal(record.BaseURL)
	}
	rewrite := planWrite(t, Pi, texts(synced...), connection(piRoute()), record)
	sameStrings(t, rewrite.Conflicts, "providers.astrlink.models", "defaultProvider")
	removals := planRemove(t, Pi, texts(synced...), record)
	if len(removals) != 2 || removals[0].Kind != cc.RemovalWrite || removals[1].Kind != cc.RemovalWrite {
		t.Fatalf("%+v", removals)
	}
	sameJSON(t, removals[0].Contents, `{"providers": {"astrlink": {"models": [{"id": "pi-route", "reasoning": true}]}}}`)
	sameJSON(t, removals[1].Contents, `{"defaultProvider": "anthropic"}`)
}

func TestPiEntriesNameTheOfficialThinkingLevels(t *testing.T) {
	gpt := reasoning("none", "low", "medium", "high", "xhigh", "max")
	plan := planWriteWith(t, Pi, texts(missing, missing), connection(piRoute()), route(gpt), nil)
	sameStrings(t, plan.Conflicts)
	want := decode(t, `[{"id": "pi-route", "reasoning": true, "thinkingLevelMap": {"off": "none", "minimal": null, "low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max"}}]`)
	if got := piList(t, plan.Contents[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	// Written in Pi's order, recorded with sorted keys.
	text := plan.Contents[0]
	if !(strings.Index(text, `"reasoning"`) < strings.Index(text, `"thinkingLevelMap"`) && strings.Index(text, `"off"`) < strings.Index(text, `"max"`)) {
		t.Fatal(text)
	}
	if plan.Record.Keys[piModelsKey] != cc.SHA256Hex(`{"id":"pi-route","reasoning":true,"thinkingLevelMap":{"high":"high","low":"low","max":"max","medium":"medium","minimal":null,"off":"none","xhigh":"xhigh"}}`) {
		t.Fatal("fingerprint")
	}
	existing := texts(plan.Contents[0], plan.Contents[1])
	if state := inspect(Pi, existing, plan.Record, origin); state != cc.Configured {
		t.Fatal(state)
	}
	if current := cc.CurrentModelsOf(Pi, []*string{existing[0], nil}); !reflect.DeepEqual(current, piCurrent("pi-route")) {
		t.Fatalf("%+v", current)
	}
	if removal := planRemove(t, Pi, existing, plan.Record)[0]; removal != deleted {
		t.Fatalf("%+v", removal)
	}

	// A model that cannot answer without reasoning has no off level.
	opus := reasoning("low", "medium", "high", "xhigh", "max")
	plan = planWriteWith(t, Pi, texts(missing, missing), connection(piRoute()), route(opus), nil)
	levels := piList(t, plan.Contents[0]).([]any)[0].(map[string]any)["thinkingLevelMap"]
	if !reflect.DeepEqual(levels, decode(t, `{"off": null, "minimal": null, "low": "low", "medium": "medium", "high": "high", "xhigh": "xhigh", "max": "max"}`)) {
		t.Fatalf("%v", levels)
	}
	// One that reasons with no strength to choose keeps Pi's range.
	plan = planWriteWith(t, Pi, texts(missing, missing), connection(piRoute()), route(reasoning()), nil)
	if got := piList(t, plan.Contents[0]); !reflect.DeepEqual(got, decode(t, `[{"id": "pi-route", "reasoning": true}]`)) {
		t.Fatalf("%v", got)
	}
}

func TestPiThinkingLevelsReachReleasedConfigsAndEditsStayTheUsers(t *testing.T) {
	released := planWrite(t, Pi, texts(missing, missing), connection(piRoute()), nil)
	gpt := reasoning("none", "low", "medium", "high", "xhigh", "max")
	plan := planWriteWith(t, Pi, texts(released.Contents...), connection(piRoute()), route(gpt), released.Record)
	sameStrings(t, plan.Conflicts)
	if !strings.Contains(plan.Contents[0], `"thinkingLevelMap"`) {
		t.Fatal(plan.Contents[0])
	}
	// Reformatting the entry keeps it AstrLink's.
	compact := format.Encode(decode(t, plan.Contents[0]))
	if removal := planRemove(t, Pi, texts(compact, plan.Contents[1]), plan.Record)[0]; removal != deleted {
		t.Fatalf("%+v", removal)
	}
	// A level the user changed is theirs.
	edited := strings.Replace(plan.Contents[0], `"max": "max"`, `"max": null`, 1)
	if edited == plan.Contents[0] {
		t.Fatal(plan.Contents[0])
	}
	existing := texts(edited, plan.Contents[1])
	rewrite := planWriteWith(t, Pi, existing, connection(piRoute()), route(gpt), plan.Record)
	sameStrings(t, rewrite.Conflicts, "providers.astrlink.models")
	if current := cc.CurrentModelsOf(Pi, []*string{existing[0], nil}); !reflect.DeepEqual(current, piCurrent("pi-route")) {
		t.Fatalf("%+v", current)
	}
	removals := planRemove(t, Pi, existing, plan.Record)
	if removals[0].Kind != cc.RemovalWrite {
		t.Fatal("expected the edited list to stay")
	}
	entry := piList(t, removals[0].Contents).([]any)[0].(map[string]any)
	if levels := entry["thinkingLevelMap"].(map[string]any); levels["max"] != nil {
		t.Fatal(levels)
	}
}

func TestPiEntriesCarryTheLimitsTheUserSet(t *testing.T) {
	details := route(cc.ModelDetails{Reasoning: []string{"none", "high"}, HasReasoning: true, ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(64_000))})
	plan := planWriteWith(t, Pi, texts(missing, missing), connection(piRoute()), details, nil)
	entry := piList(t, plan.Contents[0]).([]any)[0].(map[string]any)
	if entry["contextWindow"] != format.Number("400000") || entry["maxTokens"] != format.Number("64000") {
		t.Fatal(entry)
	}
	existing := texts(plan.Contents...)
	want := piCurrent("pi-route")
	want.ContextWindow, want.MaxTokens = cc.Some(uint64(400_000)), cc.Some(uint64(64_000))
	if current := cc.CurrentModelsOf(Pi, existing); !reflect.DeepEqual(current, want) {
		t.Fatalf("%+v", current)
	}
	// Writing the same entry again changes nothing and asks nothing.
	again := planWriteWith(t, Pi, existing, connection(piRoute()), details, plan.Record)
	sameStrings(t, again.Conflicts)
	sameStrings(t, again.Contents, plan.Contents...)
	// Clearing the limits gives Pi its defaults back.
	cleared := planWriteWith(t, Pi, existing, connection(piRoute()), route(cc.ModelDetails{}), plan.Record)
	sameStrings(t, cleared.Conflicts)
	if got := piList(t, cleared.Contents[0]); !reflect.DeepEqual(got, decode(t, `[{"id": "pi-route"}]`)) {
		t.Fatalf("%v", got)
	}
	if removal := planRemove(t, Pi, existing, plan.Record)[0]; removal != deleted {
		t.Fatalf("%+v", removal)
	}
	limits := func(contextWindow, maxTokens *uint64) *cc.Models {
		return &cc.Models{Model: some("m"), ContextWindow: contextWindow, MaxTokens: maxTokens}
	}
	if selection, err := cc.Select(limits(cc.Some(uint64(1)), cc.Some(uint64(cc.MaxPiLimit))), Pi); err != nil ||
		*selection.Limits.ContextWindow != 1 || *selection.Limits.MaxTokens != cc.MaxPiLimit {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		models *cc.Models
		client cc.Client
	}{
		{limits(cc.Some(uint64(0)), nil), Pi},
		{limits(nil, cc.Some(uint64(cc.MaxPiLimit+1))), Pi},
		{limits(cc.Some(uint64(1)), nil), Codex},
	} {
		if _, err := cc.Select(tc.models, tc.client); err == nil {
			t.Errorf("%s took %+v", tc.client, tc.models)
		}
	}
}

func TestPiFilesPiCannotReadAreLeftAlone(t *testing.T) {
	for _, tc := range []struct {
		models, settings string
		file             int
	}{
		{`{"providers": []}`, "{}", 0},
		{"{providers: {}}", "{}", 0},
		{"[]", "{}", 0},
		{"{}", "{\n  // note\n}", 1},
		{"{}", `{"a": 1,}`, 1},
	} {
		existing := texts(tc.models, tc.settings)
		_, err := cc.PlanWrite(Pi, existing, connection(piRoute()), cc.Catalog{}, nil)
		if err == nil || err.File != tc.file || strings.Contains(err.Message, token) {
			t.Fatalf("%+v: %+v", tc, err)
		}
		if state := inspect(Pi, existing, nil, origin); state != cc.Invalid {
			t.Fatal(state)
		}
	}
	plan := planWrite(t, Pi, texts(`{"providers": {"astrlink": "broken"}}`, missing), connection(piRoute()), nil)
	sameStrings(t, plan.Conflicts, "providers.astrlink")
	if !strings.Contains(plan.Contents[0], token) {
		t.Fatal(plan.Contents[0])
	}
}

func TestPiApplyWritesBothFilesAndRemovesOnlyWhatItWrote(t *testing.T) {
	home := scratchHome(t)
	agent := mkdir(t, home.Dir(), ".pi", "agent")
	settings := filepath.Join(agent, "settings.json")
	writeFile(t, settings, "{\n  \"theme\": \"dark\"\n}\n")
	if confirmation := writeAt(t, home, Pi, connection(piRoute()), false); confirmation != nil {
		t.Fatalf("%+v", confirmation)
	}
	models := filepath.Join(agent, "models.json")
	if text, _ := readFile(t, models); !strings.Contains(text, token) {
		t.Fatal(text)
	}
	if text, _ := readFile(t, settings); !strings.Contains(text, `"defaultModel": "pi-route"`) {
		t.Fatal(text)
	}
	status := statusOf(t, home, Pi, "http://127.0.0.1:9000/")
	if status.State != cc.Outdated || !reflect.DeepEqual(status.Paths, []string{models, settings}) {
		t.Fatalf("%+v", status)
	}
	syncAt(t, home, "http://127.0.0.1:9000/")
	if text, _ := readFile(t, models); !strings.Contains(text, "http://127.0.0.1:9000/v1") {
		t.Fatal(text)
	}
	removeAt(t, home, Pi)
	if _, exists := readFile(t, models); exists {
		t.Fatal("models left")
	}
	text, _ := readFile(t, settings)
	sameJSON(t, text, `{"theme": "dark"}`)
	if _, exists := readFile(t, cc.RecordsPath(home)); exists {
		t.Fatal("records left")
	}
}

func TestAFailedPiWritePutsBackTheFilesAlreadyWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic links")
	}
	home := scratchHome(t)
	agent := mkdir(t, home.Dir(), ".pi", "agent")
	// A dangling link reads as missing but cannot be written.
	if err := os.Symlink(filepath.Join(home.Dir(), "missing", "settings.json"), filepath.Join(agent, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cc.WriteWith(home, place(Pi), connection(piRoute()), cc.Catalog{}, tokens(), false); err == nil {
		t.Fatal("wrote")
	}
	for _, path := range []string{filepath.Join(agent, "models.json"), cc.RecordsPath(home)} {
		if _, exists := readFile(t, path); exists {
			t.Fatal(path)
		}
	}
}
