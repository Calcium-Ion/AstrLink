package clients

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

// chatboxFirst is what Chatbox makes on its first start, in short.
const chatboxFirst = "{\n\t\"configVersion\": 15,\n\t\"configs\": {\n\t\t\"uuid\": \"f1999b34\"\n\t},\n\t\"settings\": {\n\t\t\"showModelName\": true,\n\t\t\"providers\": {\n\t\t\t\"chatbox-ai\": {}\n\t\t},\n\t\t\"__version\": 9\n\t}\n}"

func chatboxPath(home agents.Home) string {
	return filepath.Join(Chatbox.Dir(home, ""), "config.json")
}

func TestChatboxAddsAProviderPerProtocolToTheFileChatboxMade(t *testing.T) {
	home := scratchHome(t)
	chosen := listed(models("model", "claude-sonnet-5"), "claude-sonnet-5", "deepseek-v4")
	chosen.Protocols = map[string]string{"claude-sonnet-5": "anthropic.messages"}
	if _, _, failed := cc.WriteWith(home, place(Chatbox), chosen, cc.Catalog{}, tokens(), false); failed == nil {
		t.Fatal("wrote before Chatbox made its file")
	}
	writeFile(t, chatboxPath(home), chatboxFirst)
	details := cc.Catalog{"claude-sonnet-5": {Reasoning: []string{"low", "high"}, HasReasoning: true, ContextWindow: cc.Some(uint64(200_000)), MaxTokens: cc.Some(uint64(64_000))}}
	if asked := writePlace(t, home, place(Chatbox), chosen, details, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written := readJSON(t, chatboxPath(home))
	if got := at(written, "settings", "customProviders"); !reflect.DeepEqual(got, decode(t, `[
	  {"id": "local-chat", "name": "AstrLink", "type": "openai", "isCustom": true},
	  {"id": "local-anthropic", "name": "AstrLink Anthropic", "type": "claude", "isCustom": true}]`)) {
		t.Fatalf("%v", got)
	}
	if got := at(written, "settings", "providers", "local-anthropic"); !reflect.DeepEqual(got, decode(t, `{
	  "apiHost": "`+origin+`/v1", "apiKey": "`+token+`",
	  "models": [{"modelId": "claude-sonnet-5", "type": "chat", "capabilities": ["tool_use", "reasoning"], "contextWindow": 200000, "maxOutput": 64000}]}`)) {
		t.Fatalf("%v", got)
	}
	if got := at(written, "settings", "defaultChatModel"); !reflect.DeepEqual(got, decode(t, `{"provider": "local-anthropic", "model": "claude-sonnet-5"}`)) {
		t.Fatalf("%v", got)
	}
	if got := at(written, "settings", "__version"); !reflect.DeepEqual(got, decode(t, "9")) {
		t.Fatal(got)
	}
	if text, _ := readFile(t, chatboxPath(home)); !strings.Contains(text, "\n\t\t\"customProviders\": [\n") {
		t.Fatal(text)
	}
	status := statusOf(t, home, Chatbox, origin)
	if status.State != cc.Configured || *status.Models.Model != "claude-sonnet-5" || len(status.Notes) != 0 {
		t.Fatalf("%+v", status)
	}

	syncAt(t, home, "http://127.0.0.1:9000/")
	if got := at(readJSON(t, chatboxPath(home)), "settings", "providers", "local-chat", "apiHost"); got != "http://127.0.0.1:9000/v1" {
		t.Fatal(got)
	}

	removeAt(t, home, Chatbox)
	if got := readJSON(t, chatboxPath(home)); !reflect.DeepEqual(got, decode(t, chatboxFirst)) {
		text, _ := readFile(t, chatboxPath(home))
		t.Fatalf("%s", text)
	}
}

func TestChatboxWaitsWhileChatboxIsOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Chromium locks with a file on Windows")
	}
	home := scratchHome(t)
	writeFile(t, chatboxPath(home), chatboxFirst)
	lock := filepath.Join(Chatbox.Dir(home, ""), "SingletonLock")
	if err := os.Symlink("host-"+strconv.Itoa(os.Getpid()), lock); err != nil {
		t.Fatal(err)
	}
	chosen := connection(models("model", "deepseek-v4"))
	_, _, failed := cc.WriteWith(home, place(Chatbox), chosen, cc.Catalog{}, tokens(), false)
	if failed == nil || failed.Message != "Chatbox is open; quit it and try again" {
		t.Fatalf("%+v", failed)
	}
	sameStrings(t, statusOf(t, home, Chatbox, origin).Notes, "running")
	if text, _ := readFile(t, chatboxPath(home)); text != chatboxFirst {
		t.Fatal(text)
	}

	// A lock a crash left behind names no live process.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("host-999999", lock); err != nil {
		t.Fatal(err)
	}
	writeAt(t, home, Chatbox, chosen, false)
	if got := at(readJSON(t, chatboxPath(home)), "settings", "defaultChatModel", "provider"); got != "local-chat" {
		t.Fatal(got)
	}
}
