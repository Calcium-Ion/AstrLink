package clients

import (
	"path/filepath"
	"reflect"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func codeBuddyEntry(id, origin string) string { return chatEntry(id, origin) }

func TestCodeBuddyWritesChatEntriesAndTheDefaultModel(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".codebuddy")
	connection := listed(models("model", sonnet), sonnet, gpt)
	// The IDE posts every model to Chat, whatever was chosen.
	connection.Protocols = map[string]string{gpt: "openai.responses"}
	if asked := writeAt(t, home, CodeBuddy, connection, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	if got := readJSON(t, filepath.Join(dir, "models.json")); !reflect.DeepEqual(got, decode(t, `{"models": [`+codeBuddyEntry(sonnet, origin)+`, `+codeBuddyEntry(gpt, origin)+`]}`)) {
		t.Fatalf("%v", got)
	}
	if got := readJSON(t, filepath.Join(dir, "settings.json")); !reflect.DeepEqual(got, decode(t, `{"model": "custom-local:claude-sonnet-5"}`)) {
		t.Fatalf("%v", got)
	}
	status := statusOf(t, home, CodeBuddy, origin)
	if status.State != cc.Configured || !reflect.DeepEqual(status.Models, cc.CurrentModels{Model: some(sonnet), Models: []string{sonnet, gpt}}) {
		t.Fatalf("%+v", status)
	}

	// `/model` in the CLI picks another of the entries.
	writeFile(t, filepath.Join(dir, "settings.json"), "{\"model\": \"custom-local:gpt-5.5\"}\n")
	status = statusOf(t, home, CodeBuddy, origin)
	if status.State != cc.Configured || *status.Models.Model != gpt || !reflect.DeepEqual(status.Models.Models, []string{gpt, sonnet}) {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if got := readJSON(t, filepath.Join(dir, "models.json")); !reflect.DeepEqual(got, decode(t, `{"models": [`+codeBuddyEntry(sonnet, "http://127.0.0.1:9000")+`, `+codeBuddyEntry(gpt, "http://127.0.0.1:9000")+`]}`)) {
		t.Fatalf("%v", got)
	}

	removeAt(t, home, CodeBuddy)
	if readJSON(t, filepath.Join(dir, "models.json")) != nil {
		t.Fatal("models left")
	}
	if got := readJSON(t, filepath.Join(dir, "settings.json")); !reflect.DeepEqual(got, decode(t, `{"model": "custom-local:gpt-5.5"}`)) {
		t.Fatalf("%v", got)
	}
}

func TestCodeBuddyJoinsTheModelsAndAllowlistAlreadyThere(t *testing.T) {
	home := scratchHome(t)
	dir := mkdir(t, home.Dir(), ".codebuddy")
	modelsJSON := `{
  "models": [
    {
      "id": "deepseek-v4",
      "name": "DeepSeek V4",
      "vendor": "DeepSeek",
      "url": "https://api.deepseek.com/v1/chat/completions",
      "apiKey": "${DEEPSEEK_API_KEY}",
      "maxInputTokens": 128000,
      "maxOutputTokens": 8192
    }
  ],
  "availableModels": ["deepseek-v4", "gpt-5.4"]
}
`
	settingsJSON := `{"theme":"dark","model":"deepseek-v4"}`
	writeFile(t, filepath.Join(dir, "models.json"), modelsJSON)
	writeFile(t, filepath.Join(dir, "settings.json"), settingsJSON)
	connection := listed(models("model", sonnet), sonnet, gpt)
	asked := writeAt(t, home, CodeBuddy, connection, false)
	if asked == nil || asked.Gateway != nil {
		t.Fatalf("%+v", asked)
	}
	sameStrings(t, askedKeys(asked), "model")
	writeAt(t, home, CodeBuddy, connection, true)
	written := readJSON(t, filepath.Join(dir, "models.json"))
	list := at(written, "models").([]any)
	if !reflect.DeepEqual(list[0], at(decode(t, modelsJSON), "models").([]any)[0]) ||
		!reflect.DeepEqual(list[1:], decode(t, `[`+codeBuddyEntry(sonnet, origin)+`, `+codeBuddyEntry(gpt, origin)+`]`)) {
		t.Fatalf("%v", list)
	}
	if got := at(written, "availableModels"); !reflect.DeepEqual(got, decode(t, `["deepseek-v4", "gpt-5.4", "`+sonnet+`", "`+gpt+`"]`)) {
		t.Fatalf("%v", got)
	}
	if got := readJSON(t, filepath.Join(dir, "settings.json")); !reflect.DeepEqual(got, decode(t, `{"theme": "dark", "model": "custom-local:claude-sonnet-5"}`)) {
		t.Fatalf("%v", got)
	}

	removeAt(t, home, CodeBuddy)
	if text, _ := readFile(t, filepath.Join(dir, "models.json")); text != modelsJSON {
		t.Fatalf("%q", text)
	}
	if text, _ := readFile(t, filepath.Join(dir, "settings.json")); text != settingsJSON {
		t.Fatalf("%q", text)
	}
}
