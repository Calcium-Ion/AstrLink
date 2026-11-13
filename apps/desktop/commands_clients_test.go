package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
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
