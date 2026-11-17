package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

const linkBase = "astrlink://v1/providers/import"

func urlFor(query string) string { return linkBase + "?" + query }

func parse(raw string) (*link, *rejection) { return parseLink(raw, "astrlink") }

func rejectionOf(t *testing.T, query string) rejection {
	t.Helper()
	parsed, rejected := parse(urlFor(query))
	if rejected == nil {
		t.Fatalf("%s should be rejected: %+v", query, parsed)
	}
	return *rejected
}

func entryOf(t *testing.T, query string) *providerEntry {
	t.Helper()
	parsed, rejected := parse(urlFor(query))
	if rejected != nil {
		t.Fatalf("%s: %+v", query, rejected)
	}
	provider := parsed.provider
	return &providerEntry{notice: Notice{Status: NoticePending, ID: "id", Provider: &provider}, apiKey: parsed.apiKey}
}

func ptr[T any](v T) *T { return &v }

func TestParsesEncodedValuesAndKeepsTheKeyOutOfTheNotice(t *testing.T) {
	parsed, rejected := parse(urlFor("kind=openai_compatible&name=%E4%B8%AD%E8%BD%AC%20%26%20Co&base_url=https%3A%2F%2Fapi.example.com%2Fv1" +
		"&api_key=sk-test%2Bkey%26%3D1234&auth=custom_header&auth_header=X-Api-Key" +
		"&protocols=openai.chat,%20openai.models,&models=gpt-5,team%2Fmodel%20a"))
	if rejected != nil {
		t.Fatal(rejected)
	}
	want := Provider{
		Kind:       "openai_compatible",
		Name:       ptr("中转 & Co"),
		BaseURL:    "https://api.example.com/v1",
		Auth:       ptr("custom_header"),
		AuthHeader: ptr("X-Api-Key"),
		Protocols:  []string{"openai.chat", "openai.models"},
		Models:     []string{"gpt-5", "team/model a"},
		HasAPIKey:  true,
		APIKeyHint: ptr("…1234"),
	}
	if !reflect.DeepEqual(parsed.provider, want) {
		t.Fatalf("provider = %+v", parsed.provider)
	}
	if parsed.apiKey == nil || *parsed.apiKey != "sk-test+key&=1234" {
		t.Fatalf("key = %v", parsed.apiKey)
	}
	notice, _ := json.Marshal(Notice{Status: NoticePending, ID: "id", Provider: &parsed.provider})
	if strings.Contains(string(notice), "sk-test") {
		t.Fatal(string(notice))
	}
}

func TestNoticesSerializeAsTheTauriShellDid(t *testing.T) {
	pending, _ := json.Marshal(Notice{Status: NoticePending, ID: "a", Provider: &Provider{Kind: "openai", BaseURL: "https://a.example", Protocols: []string{}, Models: []string{}}})
	want := `{"status":"pending","id":"a","provider":{"kind":"openai","name":null,"base_url":"https://a.example","auth":null,"auth_header":null,"protocols":[],"models":[],"has_api_key":false,"api_key_hint":null}}`
	if string(pending) != want {
		t.Fatalf("pending = %s", pending)
	}
	reason := ReasonMalformed
	invalid, _ := json.Marshal(Notice{Status: NoticeInvalid, ID: "b", Reason: &reason})
	if string(invalid) != `{"status":"invalid","id":"b","reason":"malformed","field":null}` {
		t.Fatalf("invalid = %s", invalid)
	}
}

func TestOptionalFieldsFallBackAndBlankValuesCountAsAbsent(t *testing.T) {
	parsed, rejected := parse(urlFor("kind=deepseek&base_url=http://192.168.1.2:3000&name=&api_key=%20&models=,"))
	if rejected != nil {
		t.Fatal(rejected)
	}
	p := parsed.provider
	if p.Name != nil || p.BaseURL != "http://192.168.1.2:3000" || p.Auth != nil || len(p.Protocols) != 0 || len(p.Models) != 0 || p.HasAPIKey || parsed.apiKey != nil {
		t.Fatalf("provider = %+v", p)
	}
	// Short keys get no hint, as in Core.
	short, rejected := parse(urlFor("kind=deepseek&base_url=https://a.example&api_key=short"))
	if rejected != nil || !short.provider.HasAPIKey || short.provider.APIKeyHint != nil {
		t.Fatalf("short = %+v, %v", short, rejected)
	}
}

func TestRejectsOtherLinksAndParameters(t *testing.T) {
	for _, raw := range []string{
		"astrlink://v1/providers/import/?kind=openai&base_url=https://a.example",
		"astrlink://v2/providers/import?kind=openai&base_url=https://a.example",
		"astrlink://v1/providers/export?kind=openai&base_url=https://a.example",
		"astrlink://v1/providers/import?kind=openai&base_url=https://a.example#x",
		"astrlink://v1/providers/import?kind=openai&base_url=https://a.example#",
		"astrlink://v1:8080/providers/import?kind=openai&base_url=https://a.example",
		"astrlink://user@v1/providers/import?kind=openai&base_url=https://a.example",
		"astrlink://v1/providers/%69mport?kind=openai&base_url=https://a.example",
		"ccswitch://v1/providers/import?kind=openai&base_url=https://a.example",
		// The development instance takes only its own links.
		"astrlink-isolated://v1/providers/import?kind=openai&base_url=https://a.example",
	} {
		if _, rejected := parse(raw); rejected == nil || *rejected != (rejection{ReasonUnsupported, ""}) {
			t.Errorf("%s: %+v", raw, rejected)
		}
	}
	long := urlFor("kind=openai&base_url=https://a.example&name=" + strings.Repeat("a", maxLinkBytes))
	if _, rejected := parse(long); rejected == nil || *rejected != (rejection{ReasonMalformed, ""}) {
		t.Fatalf("long: %+v", rejected)
	}
	for _, tc := range []struct {
		query string
		want  rejection
	}{
		{"kind=openai&base_url=https://a.example&apiKey=x", rejection{ReasonUnknownParameter, "apiKey"}},
		{"kind=openai&base_url=https://a.example&%3Cb%3E=x", rejection{ReasonUnknownParameter, ""}},
		{"kind=openai&base_url=https://a.example&base_url=https://b.example", rejection{ReasonDuplicateParameter, "base_url"}},
		{"base_url=https://a.example", rejection{ReasonMissingParameter, "kind"}},
		{"kind=openai&base_url=", rejection{ReasonMissingParameter, "base_url"}},
		{"kind=codex_subscription&base_url=https://a.example", rejection{ReasonUnsupported, "kind"}},
	} {
		if got := rejectionOf(t, tc.query); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.query, got, tc.want)
		}
	}
	if _, rejected := parseLink("astrlink-isolated://v1/providers/import?kind=openai&base_url=https://a.example", "astrlink-isolated"); rejected != nil {
		t.Fatalf("isolated: %+v", rejected)
	}
}

func TestRejectsInvalidValues(t *testing.T) {
	base := "kind=openai&base_url=https://a.example"
	longModel := strings.Repeat("m", maxModelChars+1)
	var many []string
	for i := 0; i <= maxModels; i++ {
		many = append(many, fmt.Sprintf("m%d", i))
	}
	for _, tc := range []struct{ query, field string }{
		{"kind=OpenAI&base_url=https://a.example", "kind"},
		{"kind=openai&base_url=ftp://a.example", "base_url"},
		{"kind=openai&base_url=/v1", "base_url"},
		{"kind=openai&base_url=https://user:pass@a.example", "base_url"},
		{"kind=openai&base_url=https://a.example/v1%3Fkey%3D1", "base_url"},
		{"kind=openai&base_url=https://a.example/v1%23top", "base_url"},
		{base + "&name=a%0Ab", "name"},
		{base + "&name=" + strings.Repeat("n", maxNameChars+1), "name"},
		{base + "&auth=basic", "auth"},
		{base + "&auth=bearer&auth_header=X-Key", "auth_header"},
		{base + "&auth_header=X-Key", "auth_header"},
		{base + "&auth=custom_header&auth_header=X%20Key", "auth_header"},
		{base + "&auth=none&api_key=sk-test", "api_key"},
		{base + "&api_key=sk%0Dtest", "api_key"},
		{base + "&api_key=sk%FFtest", "api_key"},
		{base + "&protocols=OpenAI.Chat", "protocols"},
		{base + "&protocols=openai..chat", "protocols"},
		{base + "&models=a,a", "models"},
		{base + "&models=" + longModel, "models"},
		{base + "&models=" + strings.Join(many, ","), "models"},
	} {
		if got := rejectionOf(t, tc.query); got != (rejection{ReasonInvalidParameter, tc.field}) {
			t.Errorf("%s: %+v", tc.query, got)
		}
	}
	if got := rejectionOf(t, base+"&auth=custom_header"); got != (rejection{ReasonMissingParameter, "auth_header"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestARepeatedLinkKeepsItsIDAndANewOneReplacesIt(t *testing.T) {
	imports := NewProviderImports("astrlink")
	first := urlFor("kind=openai&base_url=https://a.example&api_key=sk-test")
	notice, err := imports.Accept(first)
	if err != nil {
		t.Fatal(err)
	}
	id := notice.ID
	if again, _ := imports.Accept(first); again.ID != id {
		t.Fatal("a repeated link got a new id")
	}
	invalid, _ := imports.Accept(urlFor("kind=openai"))
	if invalid.Status != NoticeInvalid || *invalid.Reason != ReasonMissingParameter || invalid.ID == id {
		t.Fatalf("invalid = %+v", invalid)
	}
	if imports.Pending().ID != invalid.ID || imports.take(invalid.ID) != nil {
		t.Fatal("an invalid link can be taken")
	}
	imports.Dismiss(invalid.ID)
	if imports.Pending() != nil {
		t.Fatal("dismissed link still pending")
	}
	again, _ := imports.Accept(first)
	if again.ID == id {
		t.Fatal("a dismissed link kept its id")
	}
	taken := imports.take(again.ID)
	if taken == nil || imports.take(again.ID) != nil {
		t.Fatal("take")
	}
	imports.restore(taken)
	if imports.Pending().ID != again.ID {
		t.Fatal("restore")
	}
}

func decoded(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	value, err := format.DecodeJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func at(value any, keys ...string) any {
	for _, key := range keys {
		object, _ := value.(map[string]any)
		value = object[key]
	}
	return value
}

func TestAttachesTheKeyOnlyToTheLinkedService(t *testing.T) {
	input := map[string]any{
		"name":         "Relay",
		"kind":         "openai_compatible",
		"http":         map[string]any{"base_url": "https://a.example/v1", "auth": map[string]any{"scheme": "bearer"}},
		"capabilities": []any{},
	}
	encode := func(value map[string]any) json.RawMessage { return json.RawMessage(format.Encode(value)) }
	linked := entryOf(t, "kind=openai_compatible&base_url=https://a.example/v1&api_key=sk-test")
	authorized, err := authorize(linked, encode(input))
	if err != nil {
		t.Fatal(err)
	}
	value := decoded(t, authorized)
	if !reflect.DeepEqual(at(value, "http", "credential"), map[string]any{"secret": "sk-test"}) || at(value, "name") != "Relay" {
		t.Fatalf("authorized = %s", authorized)
	}
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["kind"] = "openai" },
		func(v map[string]any) { v["http"].(map[string]any)["base_url"] = "https://evil.example/v1" },
		func(v map[string]any) { v["http"].(map[string]any)["auth"] = map[string]any{"scheme": "none"} },
		func(v map[string]any) { v["http"].(map[string]any)["credential"] = map[string]any{"secret": "other"} },
	} {
		changed := decoded(t, encode(input)).(map[string]any)
		change(changed)
		if _, err := authorize(linked, encode(changed)); err == nil {
			t.Fatalf("authorized %s", format.Encode(changed))
		}
	}
	pinned := entryOf(t, "kind=openai_compatible&base_url=https://a.example/v1&api_key=sk-test&auth=custom_header&auth_header=X-Key")
	custom := decoded(t, encode(input)).(map[string]any)
	custom["http"].(map[string]any)["auth"] = map[string]any{"scheme": "custom_header", "header_name": "X-Other"}
	if _, err := authorize(pinned, encode(custom)); err == nil {
		t.Fatal("another header was accepted")
	}
	custom["http"].(map[string]any)["auth"] = map[string]any{"scheme": "custom_header", "header_name": "X-Key"}
	if _, err := authorize(pinned, encode(custom)); err != nil {
		t.Fatal(err)
	}
	// Without a key in the link, the user may enter their own.
	keyless := entryOf(t, "kind=openai_compatible&base_url=https://a.example/v1")
	typed := decoded(t, encode(input)).(map[string]any)
	typed["http"].(map[string]any)["credential"] = map[string]any{"secret": "typed"}
	authorized, err = authorize(keyless, encode(typed))
	if err != nil || at(decoded(t, authorized), "http", "credential", "secret") != "typed" {
		t.Fatal(string(authorized), err)
	}
}

func TestRedactsAnEchoedKey(t *testing.T) {
	if got := redact("bad key sk-test-123", ptr("sk-test-123")); got != "unable to add the service" {
		t.Fatal(got)
	}
	if got := redact("name taken", ptr("sk-test-123")); got != "name taken" {
		t.Fatal(got)
	}
	if got := redact("a bad a", ptr("a")); got != "a bad a" {
		t.Fatal(got)
	}
}

type fakeCore struct {
	created []json.RawMessage
	err     error
}

func (f *fakeCore) CreateService(_ context.Context, input json.RawMessage) (control.ServiceRecord, error) {
	f.created = append(f.created, input)
	if f.err != nil {
		return control.ServiceRecord{}, f.err
	}
	return control.ServiceRecord{Service: json.RawMessage(`{"id":"svc"}`), ETag: "e"}, nil
}

func TestConfirmCreatesOnceAndKeepsTheLinkWhenCoreRefuses(t *testing.T) {
	imports := NewProviderImports("astrlink")
	notice, _ := imports.Accept(urlFor("kind=openai_compatible&base_url=https://a.example/v1&api_key=sk-secret-key"))
	input := json.RawMessage(`{"kind":"openai_compatible","http":{"base_url":"https://a.example/v1","auth":{"scheme":"bearer"}}}`)
	core := &fakeCore{err: errors.New("Core rejected sk-secret-key")}
	if _, err := imports.Confirm(context.Background(), core, notice.ID, input); err == nil || err.Error() != "unable to add the service" {
		t.Fatalf("err = %v", err)
	}
	if imports.Pending() == nil {
		t.Fatal("a failed confirm dropped the link")
	}
	core.err = nil
	record, err := imports.Confirm(context.Background(), core, notice.ID, input)
	if err != nil || record.ETag != "e" {
		t.Fatal(record, err)
	}
	if !strings.Contains(string(core.created[1]), `"credential":{"secret":"sk-secret-key"}`) {
		t.Fatal(string(core.created[1]))
	}
	if _, err := imports.Confirm(context.Background(), core, notice.ID, input); err == nil || err.Error() != unknownProviderLink {
		t.Fatalf("err = %v", err)
	}
	if imports.Pending() != nil || len(core.created) != 2 {
		t.Fatal("the link was created twice")
	}
}
