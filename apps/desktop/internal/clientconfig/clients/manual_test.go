package clients

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestAConfigPastedFromTheManualSetupNamesItsToken(t *testing.T) {
	home := scratchHome(t)
	url := origin + "/"
	state := func(client cc.Client, url string, tokens cc.Tokens) (cc.State, *string, cc.CurrentModels) {
		t.Helper()
		statuses, err := cc.StatusOf(home, []cc.Client{client}, url, tokens)
		if err != nil {
			t.Fatal(err)
		}
		return statuses[0].State, statuses[0].TokenID, statuses[0].Models
	}
	for _, tc := range []struct {
		client cc.Client
		file   string
		models []cc.Field
	}{
		{Claude, ".claude/settings.json", nil},
		{Codex, ".codex/config.toml", codexModels()},
	} {
		path := filepath.Join(home.Dir(), tc.file)
		paste := func(token string) {
			t.Helper()
			connection := connection(tc.models)
			connection.Token = token
			files, err := cc.Snippet(tc.client, home, connection, cc.Catalog{})
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Fatalf("%s: %d snippet files", tc.client, len(files))
			}
			if err := os.WriteFile(path, []byte(files[0].Contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		paste(token)
		found, tokenID, current := state(tc.client, url, tokens())
		if found != cc.Manual || tokenID == nil || *tokenID != "token_01" {
			t.Fatalf("%s: %s %v", tc.client, found, tokenID)
		}
		text, _ := readFile(t, path)
		if want := cc.CurrentModelsOf(tc.client, texts(text)); !reflect.DeepEqual(current, want) {
			t.Fatalf("%s: models %+v, want %+v", tc.client, current, want)
		}
		// Writing over it is a plain write: nothing to confirm.
		if asked := writeAt(t, home, tc.client, connection(tc.models), false); asked != nil {
			t.Fatalf("%s: asked %+v", tc.client, asked)
		}
		if found, _, _ := state(tc.client, url, tokens()); found != cc.Configured {
			t.Fatalf("%s: %s after the write", tc.client, found)
		}
		// Removing it brings back what the user pasted.
		removeAt(t, home, tc.client)
		if found, _, _ := state(tc.client, url, tokens()); found != cc.Manual {
			t.Fatalf("%s: %s after the removal", tc.client, found)
		}

		// A token whose hint no gateway token has, or a hint two tokens
		// share, names none.
		paste(otherToken)
		if found, tokenID, current := state(tc.client, url, tokens()); found != cc.NotConfigured || tokenID != nil || !reflect.DeepEqual(current, cc.CurrentModels{}) {
			t.Fatalf("%s: %s %v %+v with another token", tc.client, found, tokenID, current)
		}
		paste(token)
		twice := cc.Tokens{"token_01": cc.TokenHint(token), "token_02": cc.TokenHint(token)}
		if found, _, _ := state(tc.client, url, twice); found != cc.NotConfigured {
			t.Fatalf("%s: %s with a shared hint", tc.client, found)
		}
		// Another port is another gateway's.
		if found, _, _ := state(tc.client, "http://127.0.0.1:9000/", tokens()); found != cc.NotConfigured {
			t.Fatalf("%s: %s on another port", tc.client, found)
		}
		// A changed connection key is not what the manual setup shows.
		text, _ = readFile(t, path)
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(text, token, token+"x")), 0o600); err != nil {
			t.Fatal(err)
		}
		if found, _, _ := state(tc.client, url, tokens()); found != cc.NotConfigured {
			t.Fatalf("%s: %s with a changed token", tc.client, found)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTokensMatchOnlyOneHint(t *testing.T) {
	tokens := cc.Tokens{"token_01": cc.TokenHint(token), "token_02": cc.TokenHint(otherToken)}
	if id, ok := tokens.Matching(otherToken); !ok || id != "token_02" {
		t.Fatalf("matching %q %v", id, ok)
	}
	if _, ok := (cc.Tokens{"a": cc.TokenHint(token), "b": cc.TokenHint(token)}).Matching(token); ok {
		t.Fatal("a shared hint matched")
	}
	if _, ok := tokens.Matching("abc"); ok {
		t.Fatal("a short token matched")
	}
}

func TestAConfirmationShowsEachReplacedValueBeforeAndAfter(t *testing.T) {
	home := scratchHome(t)
	settings := `{"env": {
		"ANTHROPIC_BASE_URL": "https://me:pw@relay.example/v1?key=abc",
		"ANTHROPIC_AUTH_TOKEN": "sk-relay-0123456789",
		"ANTHROPIC_MODEL": "relay-opus",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "relay-haiku",
		"ANTHROPIC_CUSTOM_HEADERS": "{\"x\": 1}"
	}}`
	if err := os.WriteFile(filepath.Join(home.Dir(), ".claude/settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	confirmation := writeAt(t, home, Claude, connection(models("model", "main-route")), false)
	if confirmation == nil {
		t.Fatal("expected a confirmation")
	}
	type row struct {
		key           string
		current, next string
		removed       bool
	}
	text := func(value *string) string {
		if value == nil {
			return "<nil>"
		}
		return *value
	}
	var shown []row
	for _, key := range confirmation.Keys {
		shown = append(shown, row{key.Key, text(key.Current), text(key.Next), key.Removed})
	}
	masked := token[:4] + "…" + token[len(token)-3:]
	want := []row{
		// An address drops what may carry a credential.
		{"env.ANTHROPIC_BASE_URL", "https://relay.example/v1", Claude.BaseURL(origin), false},
		{"env.ANTHROPIC_AUTH_TOKEN", "sk-r…789", masked, false},
		{"env.ANTHROPIC_MODEL", "relay-opus", "main-route", false},
		// A tier the write leaves empty goes; an object shows as nothing.
		{"env.ANTHROPIC_DEFAULT_HAIKU_MODEL", "relay-haiku", "<nil>", true},
		{"env.ANTHROPIC_CUSTOM_HEADERS", "<nil>", "<nil>", true},
	}
	if !reflect.DeepEqual(shown, want) {
		t.Fatalf("shown %+v\nwant  %+v", shown, want)
	}
	all := fmt.Sprintf("%+v", shown)
	for _, secret := range []string{"sk-relay-0123456789", token, "pw", "abc"} {
		if strings.Contains(all, secret) {
			t.Fatalf("%q shown: %s", secret, all)
		}
	}
}
