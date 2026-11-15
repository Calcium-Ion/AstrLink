package clients

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

const (
	gpt    = "gpt-5.5"
	sonnet = "claude-sonnet-5"
)

func workBuddyModels(home agents.Home, target string) string {
	dir := ".workbuddy"
	if target == "intl" {
		dir = ".workbuddy-ai"
	}
	return filepath.Join(home.Dir(), dir, "models.json")
}

func gptDetails() cc.Catalog {
	return cc.Catalog{gpt: {Reasoning: []string{"none", "low", "medium", "high", "xhigh"}, HasReasoning: true, ContextWindow: cc.Some(uint64(400_000)), MaxTokens: cc.Some(uint64(128_000))}}
}

func chatEntry(id, origin string) string {
	return `{"id": "` + id + `", "name": "` + id + `", "vendor": "AstrLink", "url": "` + origin + `/v1/chat/completions", "apiKey": "` + token + `"}`
}

func responsesEntry(origin string) string {
	return `{"id": "` + gpt + `", "name": "` + gpt + `", "vendor": "AstrLink", "url": "` + origin + `/v1", "api": "openai-responses", "apiKey": "` + token + `",
		"maxInputTokens": 400000, "maxOutputTokens": 128000, "supportsReasoning": true, "reasoning": {"supportedEfforts": ["low", "medium", "high", "xhigh"]}}`
}

func TestWorkBuddyWritesAnEntryPerModelInEachEdition(t *testing.T) {
	home := scratchHome(t)
	cn, intl := targeted(t, WorkBuddy, "cn"), targeted(t, WorkBuddy, "intl")
	mkdir(t, home.Dir(), ".workbuddy")
	mkdir(t, home.Dir(), ".workbuddy-ai")
	connection := listed(models("model", gpt), gpt, sonnet)
	connection.Protocols = map[string]string{gpt: "openai.responses"}
	if asked := writePlace(t, home, cn, connection, gptDetails(), false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := decode(t, `{"models": [`+responsesEntry(origin)+`, `+chatEntry(sonnet, origin)+`]}`)
	if got := readJSON(t, workBuddyModels(home, "cn")); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	text, _ := readFile(t, workBuddyModels(home, "cn"))
	if strings.Index(text, `"api"`) > strings.Index(text, `"apiKey"`) {
		t.Fatal(text)
	}
	if runtime.GOOS != "windows" && fileMode(t, workBuddyModels(home, "cn")) != 0o600 {
		t.Fatal(fileMode(t, workBuddyModels(home, "cn")))
	}
	if readJSON(t, workBuddyModels(home, "intl")) != nil {
		t.Fatal("wrote the other edition")
	}
	if current := cc.CurrentModelsOf(WorkBuddy, texts(text)); !reflect.DeepEqual(current, cc.CurrentModels{Model: some(gpt), Models: []string{gpt, sonnet}}) {
		t.Fatalf("%+v", current)
	}
	states := func(inferenceURL string) []string {
		statuses, err := cc.StatusOf(home, []cc.Client{WorkBuddy}, inferenceURL, tokens())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, status := range statuses {
			out = append(out, *status.Target+" "+string(status.State))
		}
		return out
	}
	sameStrings(t, states(origin), "cn configured", "intl not_configured")

	// The international edition is a place of its own.
	writePlace(t, home, intl, connection, gptDetails(), false)
	if !reflect.DeepEqual(readJSON(t, workBuddyModels(home, "intl")), readJSON(t, workBuddyModels(home, "cn"))) {
		t.Fatal("editions differ")
	}

	// A new port moves every entry, each in the form its API needs.
	moved := "http://127.0.0.1:9000"
	syncAt(t, home, moved+"/")
	want = decode(t, `{"models": [`+responsesEntry(moved)+`, `+chatEntry(sonnet, moved)+`]}`)
	for _, target := range []string{"cn", "intl"} {
		if got := readJSON(t, workBuddyModels(home, target)); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v", target, got)
		}
	}
	sameStrings(t, states(moved), "cn configured", "intl configured")

	// A created file goes once AstrLink's entries do.
	removePlace(t, home, cn)
	if readJSON(t, workBuddyModels(home, "cn")) != nil || readJSON(t, workBuddyModels(home, "intl")) == nil {
		t.Fatal("removed the wrong edition")
	}
	removePlace(t, home, intl)
	if readJSON(t, workBuddyModels(home, "intl")) != nil {
		t.Fatal("left the international edition")
	}
}

// workBuddySaved is what WorkBuddy's model settings leave once they saved
// the file.
const workBuddySaved = `[
  {
    "id": "deepseek-v4",
    "name": "DeepSeek V4",
    "vendor": "DeepSeek",
    "url": "https://api.deepseek.com/v1/chat/completions",
    "apiKey": "sk-deepseek",
    "supportsToolCall": true,
    "maxInputTokens": 128000
  }
]`

func TestWorkBuddyKeepsASavedListAList(t *testing.T) {
	home := scratchHome(t)
	cn := targeted(t, WorkBuddy, "cn")
	path := workBuddyModels(home, "cn")
	writeFile(t, path, workBuddySaved)
	if asked := writePlace(t, home, cn, connection(models("model", sonnet)), cc.Catalog{}, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	want := append(decode(t, workBuddySaved).([]any), decode(t, chatEntry(sonnet, origin)))
	if got := readJSON(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	text, _ := readFile(t, path)
	sameStrings(t, cc.CurrentModelsOf(WorkBuddy, texts(text)).Models, sonnet)

	syncAt(t, home, "http://127.0.0.1:9000/")
	list := readJSON(t, path).([]any)
	if at(list[1], "url") != "http://127.0.0.1:9000/v1/chat/completions" || !reflect.DeepEqual(list[0], decode(t, workBuddySaved).([]any)[0]) {
		t.Fatalf("%v", list)
	}

	removePlace(t, home, cn)
	if text, _ := readFile(t, path); text != workBuddySaved {
		t.Fatalf("%q", text)
	}
}

func TestWorkBuddyAsksBeforeReplacingAnEntryOfTheSameID(t *testing.T) {
	home := scratchHome(t)
	cn := targeted(t, WorkBuddy, "cn")
	path := workBuddyModels(home, "cn")
	original := `{
  "models": [
    {
      "id": "claude-sonnet-5",
      "name": "Sonnet via relay",
      "vendor": "Relay",
      "url": "https://relay.example/v1/chat/completions",
      "apiKey": "sk-relay"
    }
  ],
  "availableModels": ["claude-sonnet-5", "hunyuan-turbos"]
}
`
	writeFile(t, path, original)
	connection := listed(models("model", gpt), gpt, sonnet)
	asked := writePlace(t, home, cn, connection, cc.Catalog{}, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "models[claude-sonnet-5]")
	if text, _ := readFile(t, path); text != original {
		t.Fatal(text)
	}
	writePlace(t, home, cn, connection, cc.Catalog{}, true)
	written := readJSON(t, path)
	if got := at(written, "models"); !reflect.DeepEqual(got, decode(t, `[`+chatEntry(sonnet, origin)+`, `+chatEntry(gpt, origin)+`]`)) {
		t.Fatalf("%v", got)
	}
	if got := at(written, "availableModels"); !reflect.DeepEqual(got, decode(t, `["claude-sonnet-5", "hunyuan-turbos", "`+gpt+`"]`)) {
		t.Fatalf("%v", got)
	}

	// The relay's entry comes back with the file AstrLink found.
	removePlace(t, home, cn)
	if got := readJSON(t, path); !reflect.DeepEqual(got, decode(t, original)) {
		t.Fatalf("%v", got)
	}
}

func TestWorkBuddyLeavesAFileWithCommentsAlone(t *testing.T) {
	home := scratchHome(t)
	cn := targeted(t, WorkBuddy, "cn")
	path := workBuddyModels(home, "cn")
	original := "{\n  // mine\n  \"models\": []\n}\n"
	writeFile(t, path, original)
	if _, _, err := cc.WriteWith(home, cn, connection(models("model", gpt)), cc.Catalog{}, tokens(), false); err == nil {
		t.Fatal("wrote")
	}
	if text, _ := readFile(t, path); text != original {
		t.Fatal(text)
	}
	if state := statusOf(t, home, WorkBuddy, origin).State; state != cc.Invalid {
		t.Fatal(state)
	}
}
