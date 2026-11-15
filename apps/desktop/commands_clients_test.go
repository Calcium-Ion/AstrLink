package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/instance"
)

// snippetGateway is a ready gateway with one access token.
type snippetGateway struct{ inferenceURL string }

func (g snippetGateway) Session() (string, string, bool) { return g.inferenceURL, "session", true }

func (snippetGateway) ListAccessTokens(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"items": [{"id": "tok_a", "name": "Laptop", "hint": "sk-...cal"}]}`), nil
}

func (snippetGateway) RevealAccessToken(context.Context, string) (string, error) {
	return "sk-local", nil
}

func (snippetGateway) ModelMetadata(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}

func TestCopyClientConfigSnippetIsFalseWhenTheClipboardRefuses(t *testing.T) {
	t.Setenv("ASTRLINK_AGENT_HOME", t.TempDir())
	client, ok := cc.Find("claude")
	if !ok {
		t.Fatal("claude is not a registered client")
	}
	const inferenceURL = "http://127.0.0.1:18317"
	var written []string
	refused := errors.New("clipboard busy")
	clients := Clients{
		gateway: snippetGateway{inferenceURL: inferenceURL},
		writeClipboard: func(text string) error {
			written = append(written, text)
			return refused
		},
	}
	copied, err := clients.CopyClientConfigSnippet(t.Context(), "tok_a", client, cc.Models{}, inferenceURL, 0)
	if err != nil || copied {
		t.Fatalf("refused clipboard = %v, %v; want false, nil", copied, err)
	}
	if len(written) != 1 || !strings.Contains(written[0], "sk-local") {
		t.Fatalf("written %q, want the revealed snippet", written)
	}

	refused = nil
	copied, err = clients.CopyClientConfigSnippet(t.Context(), "tok_a", client, cc.Models{}, inferenceURL, 0)
	if err != nil || !copied {
		t.Fatalf("accepted clipboard = %v, %v; want true, nil", copied, err)
	}
}

// Only the installed instance repoints client configs at startup; the
// development instance shares them and changes one only when it is set up
// from its window.
func TestOnlyTheInstalledInstanceSyncsClientConfigsAtStartup(t *testing.T) {
	if !syncsClientConfigsAtStartup(instance.Current(false)) {
		t.Fatal("the installed instance must keep its client configs on its address")
	}
	if syncsClientConfigsAtStartup(instance.Current(true)) {
		t.Fatal("the development instance must not repoint client configs at startup")
	}
}
