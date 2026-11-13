package clients

import (
	"reflect"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// What the tests of every client share: a scratch home, a connection to
// write, and each registered client by name.

const (
	token      = "astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0"
	otherToken = "astr_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB4"
	origin     = "http://127.0.0.1:18317"
)

// scratchHome is a home in a temporary directory with Claude Code and
// Codex installed; tests never touch the real one.
func scratchHome(t *testing.T) agents.Home {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{".claude", ".codex"} {
		mkdir(t, dir, sub)
	}
	return agents.Local(dir)
}

// models is fields of Models, by name and value.
func models(pairs ...string) []cc.Field {
	var fields []cc.Field
	for i := 0; i+1 < len(pairs); i += 2 {
		fields = append(fields, cc.Field{Name: pairs[i], Value: pairs[i+1]})
	}
	return fields
}

// connection writes models with the test token; no protocol is chosen for
// any model, so each takes its client's first.
func connection(fields []cc.Field) *cc.Connection {
	return &cc.Connection{TokenID: "token_01", Token: token, Origin: origin, Models: fields, Protocols: map[string]string{}}
}

func listed(fields []cc.Field, list ...string) *cc.Connection {
	c := connection(fields)
	c.List = list
	return c
}

func codexModels() []cc.Field  { return models("model", "gpt-route") }
func piModels() []cc.Field     { return models("model", "pi-route") }
func geminiModels() []cc.Field { return models("model", "gemini-route") }

// tokens are the tokens of the gateway connection writes for.
func tokens() cc.Tokens { return cc.Tokens{"token_01": true} }

func some(text string) *string { return &text }

// texts is file texts; "\x00" stands for a missing file.
func texts(files ...string) []*string {
	out := make([]*string, len(files))
	for i, file := range files {
		if file != "\x00" {
			out[i] = some(file)
		}
	}
	return out
}

const missing = "\x00"

func planWrite(t *testing.T, client cc.Client, existing []*string, connection *cc.Connection, previous *cc.Record) *cc.WritePlan {
	t.Helper()
	plan, err := cc.PlanWrite(client, existing, connection, cc.Catalog{}, previous)
	if err != nil {
		t.Fatalf("plan: %+v", err)
	}
	return plan
}

func planRemove(t *testing.T, client cc.Client, existing []*string, record *cc.Record) []cc.Removal {
	t.Helper()
	removals, err := cc.PlanRemove(client, existing, record)
	if err != nil {
		t.Fatalf("remove: %+v", err)
	}
	return removals
}

func write(text string) cc.Removal { return cc.Removal{Kind: cc.RemovalWrite, Contents: text} }

var (
	unchanged = cc.Removal{Kind: cc.RemovalUnchanged}
	deleted   = cc.Removal{Kind: cc.RemovalDelete}
)

func sameRemovals(t *testing.T, got []cc.Removal, want ...cc.Removal) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removals %+v, want %+v", got, want)
	}
}

func decode(t *testing.T, text string) any {
	t.Helper()
	value, err := format.DecodeJSON(text)
	if err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return value
}

// sameJSON compares JSON texts as values.
func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	if !reflect.DeepEqual(decode(t, got), decode(t, want)) {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func recordKeys(record *cc.Record) []string {
	var keys []string
	for key := range record.Keys {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sameStrings(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func inspect(client cc.Client, existing []*string, record *cc.Record, origin string) cc.State {
	return cc.Inspect(client, existing, record, origin)
}

func planSync(t *testing.T, client cc.Client, existing []*string, record *cc.Record, origin string) ([]string, *cc.Record) {
	t.Helper()
	contents, next, err := cc.PlanSync(client, existing, record, origin)
	if err != nil {
		t.Fatalf("sync: %+v", err)
	}
	return contents, next
}

func place(client cc.Client) cc.Place { return cc.Place{Client: client} }

// writeAt writes connection to client in home, failing the test on an
// error; it returns what the write asks to confirm, nil once it wrote.
func writeAt(t *testing.T, home agents.Home, client cc.Client, connection *cc.Connection, replace bool) *cc.Confirmation {
	t.Helper()
	applied, confirmation, err := cc.WriteWith(home, place(client), connection, cc.Catalog{}, tokens(), replace)
	if err != nil {
		t.Fatalf("write %s: %+v", client, err)
	}
	if applied != (confirmation == nil) {
		t.Fatalf("write %s: applied %v with %+v", client, applied, confirmation)
	}
	return confirmation
}

// askedKeys is the keys a confirmation asks about.
func askedKeys(confirmation *cc.Confirmation) []string {
	var keys []string
	for _, key := range confirmation.Keys {
		keys = append(keys, key.Key)
	}
	return keys
}

func removeAt(t *testing.T, home agents.Home, client cc.Client) {
	t.Helper()
	if err := cc.Remove(home, place(client)); err != nil {
		t.Fatalf("remove %s: %+v", client, err)
	}
}

func syncAt(t *testing.T, home agents.Home, inferenceURL string) {
	t.Helper()
	if err := cc.Sync(home, inferenceURL, tokens()); err != nil {
		t.Fatalf("sync: %v", err)
	}
}

// statusOf is client's status in home.
func statusOf(t *testing.T, home agents.Home, client cc.Client, inferenceURL string) cc.ClientStatus {
	t.Helper()
	statuses, err := cc.StatusOf(home, []cc.Client{client}, inferenceURL, tokens())
	if err != nil {
		t.Fatal(err)
	}
	return statuses[0]
}
