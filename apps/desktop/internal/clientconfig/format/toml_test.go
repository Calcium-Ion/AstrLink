package format

import (
	"strings"
	"testing"
)

const testToken = "astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0"

// codexProvider is the table Codex's provider keys go to, made as the
// Codex client makes it.
func codexProvider(t *testing.T, doc *TOMLDocument) *TOMLTable {
	t.Helper()
	root := doc.Root()
	if !root.ContainsKey("model_providers") {
		providers := NewTOMLTable()
		providers.SetImplicit(true)
		root.Insert("model_providers", providers)
	}
	providers := TableLike(root.Get("model_providers"))
	inline := providers.IsInline()
	if TableLike(providers.Get("astrlink")) == nil {
		if inline {
			providers.Insert("astrlink", NewTOMLInlineTable())
		} else {
			providers.Insert("astrlink", NewTOMLTable())
		}
	}
	return TableLike(providers.Get("astrlink"))
}

func writeCodex(t *testing.T, doc *TOMLDocument) {
	t.Helper()
	doc.Set("model_provider", "astrlink")
	doc.Set("model", "gpt-route")
	provider := codexProvider(t, doc)
	for _, kv := range [][2]string{
		{"name", "AstrLink"},
		{"base_url", "http://127.0.0.1:18317/v1"},
		{"wire_api", "responses"},
		{"experimental_bearer_token", testToken},
	} {
		SetTOMLString(provider, kv[0], kv[1])
	}
	if TableLike(provider.Get("http_headers")) == nil {
		provider.Insert("http_headers", NewTOMLInlineTable())
	}
	headers := TableLike(provider.Get("http_headers"))
	name := "x-openai-actor-authorization"
	for _, known := range headers.Names() {
		if strings.EqualFold(known, name) {
			name = known
		}
	}
	SetTOMLString(headers, name, "codex-imagegen")
}

func removeCodex(doc *TOMLDocument, model string) {
	doc.Remove("model_provider")
	if model != "" {
		doc.Set("model", model)
	} else {
		doc.Remove("model")
	}
	providers := TableLike(doc.Root().Get("model_providers"))
	if providers != nil {
		providers.Remove("astrlink")
		if providers.IsEmpty() {
			doc.Root().Remove("model_providers")
		}
	}
}

func parseTOML(t *testing.T, text string) *TOMLDocument {
	t.Helper()
	doc, err := ParseTOML(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return doc
}

func TestTOMLCodexKeepsCommentsAndOtherTables(t *testing.T) {
	existing := "# user settings\nmodel = \"gpt-5\" # mine\napproval_policy = \"never\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n"
	doc := parseTOML(t, existing)
	writeCodex(t, doc)
	want := "# user settings\nmodel = \"gpt-route\" # mine\napproval_policy = \"never\"\nmodel_provider = \"astrlink\"\n\n[profiles.fast]\nmodel = \"o3\"\n\n[model_providers.other]\nname = \"Other\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"" + testToken + "\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
	written := doc.Render()
	if written != want {
		t.Fatalf("written:\n%q\nwant:\n%q", written, want)
	}
	doc = parseTOML(t, written)
	removeCodex(doc, "gpt-5")
	if got := doc.Render(); got != existing {
		t.Fatalf("removed:\n%q\nwant:\n%q", got, existing)
	}
}

func TestTOMLCodexNewFile(t *testing.T) {
	doc := parseTOML(t, "")
	writeCodex(t, doc)
	want := "model_provider = \"astrlink\"\nmodel = \"gpt-route\"\n\n[model_providers.astrlink]\nname = \"AstrLink\"\nbase_url = \"http://127.0.0.1:18317/v1\"\nwire_api = \"responses\"\nexperimental_bearer_token = \"" + testToken + "\"\nhttp_headers = { x-openai-actor-authorization = \"codex-imagegen\" }\n"
	written := doc.Render()
	if written != want {
		t.Fatalf("written:\n%q\nwant:\n%q", written, want)
	}
	doc = parseTOML(t, written)
	removeCodex(doc, "")
	if !doc.IsEmpty() {
		t.Fatalf("left %q", doc.Render())
	}
}

func TestTOMLCodexHeadersBesideTheUsers(t *testing.T) {
	doc := parseTOML(t, "[model_providers.astrlink.http_headers]\nx-team = \"blue\"\n")
	writeCodex(t, doc)
	written := doc.Render()
	for _, want := range []string{"x-team = \"blue\"", "x-openai-actor-authorization = \"codex-imagegen\""} {
		if !strings.Contains(written, want) {
			t.Fatalf("%q lacks %q", written, want)
		}
	}

	doc = parseTOML(t, "[model_providers.astrlink]\nhttp_headers = { X-OpenAI-Actor-Authorization = \"mine\", x-team = \"blue\" }\n")
	writeCodex(t, doc)
	want := "http_headers = { X-OpenAI-Actor-Authorization = \"codex-imagegen\", x-team = \"blue\" }"
	if written := doc.Render(); !strings.Contains(written, want) {
		t.Fatalf("%q lacks %q", written, want)
	}
}

func TestTOMLCodexCRLFStaysCRLF(t *testing.T) {
	existing := "# a\r\napproval_policy = \"never\"\r\n"
	doc := parseTOML(t, existing)
	writeCodex(t, doc)
	written := doc.Render()
	if !strings.HasPrefix(written, existing) || strings.Contains(strings.ReplaceAll(written, "\r\n", ""), "\n") {
		t.Fatalf("written %q", written)
	}
	doc = parseTOML(t, written)
	removeCodex(doc, "")
	if got := doc.Render(); got != existing {
		t.Fatalf("removed %q, want %q", got, existing)
	}
}

func TestTOMLCommentsAboveRemovedKeysStay(t *testing.T) {
	for _, existing := range []string{
		"# header\n",
		"# header\nmodel = \"old\"\n",
		"approval_policy = \"never\"\n# pick one\nmodel = \"old\"\n\n[tui]\nx = 1\n",
	} {
		doc := parseTOML(t, existing)
		_, _, hadModel := doc.Get("model")
		writeCodex(t, doc)
		doc = parseTOML(t, doc.Render())
		model := ""
		if hadModel {
			model = "old"
		}
		removeCodex(doc, model)
		if got := doc.Render(); got != existing {
			t.Fatalf("removed %q, want %q", got, existing)
		}
	}
}

func TestTOMLRemovedKeyCommentMovesToANeighbour(t *testing.T) {
	for _, tc := range []struct{ existing, key, want string }{
		{"# note\nmodel = \"x\"\nother = 1\n", "model", "# note\nother = 1\n"},
		{"a = 1\n# note\nmodel = \"x\"\n", "model", "a = 1\n# note\n"},
		{"# note\nmodel = \"x\"\n\n[t]\ny = 1\n", "model", "# note\n\n[t]\ny = 1\n"},
	} {
		doc := parseTOML(t, tc.existing)
		doc.Remove(tc.key)
		if got := doc.Render(); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.existing, got, tc.want)
		}
	}
}

func TestTOMLArrayOfTablesAppends(t *testing.T) {
	const config = "# my vibe\nactive_model = \"devstral\"\n\n[[providers]]\nname = \"mistral\"\napi_base = \"https://api.mistral.ai/v1\"\napi_key_env_var = \"MISTRAL_API_KEY\"\n\n[[models]]\nname = \"devstral-2\"\nprovider = \"mistral\"\nalias = \"devstral\"\n"
	rank := func(name string) int {
		for i, known := range []string{"name", "provider", "alias", "api_base", "api_key_env_var", "api_style", "backend", "max_context_length", "max_output_tokens"} {
			if known == name {
				return i
			}
		}
		return 1 << 30
	}
	doc := parseTOML(t, config)
	doc.Set("active_model", "deepseek-v4")
	for _, list := range []struct {
		key   string
		entry map[string]any
	}{
		{"providers", map[string]any{"name": "local-chat", "api_base": "http://127.0.0.1:18317/v1", "api_key_env_var": "LOCAL_GATEWAY_API_KEY", "api_style": "openai", "backend": "generic"}},
		{"models", map[string]any{"name": "deepseek-v4", "provider": "local-chat", "alias": "deepseek-v4", "max_context_length": Number("400000")}},
	} {
		tables := doc.Root().Get(list.key).(*TOMLArrayOfTables)
		table, ok := TOMLTableOf(list.entry, rank)
		if !ok {
			t.Fatal("table")
		}
		tables.Push(table)
	}
	want := "# my vibe\nactive_model = \"deepseek-v4\"\n\n[[providers]]\nname = \"mistral\"\napi_base = \"https://api.mistral.ai/v1\"\napi_key_env_var = \"MISTRAL_API_KEY\"\n\n[[providers]]\nname = \"local-chat\"\napi_base = \"http://127.0.0.1:18317/v1\"\napi_key_env_var = \"LOCAL_GATEWAY_API_KEY\"\napi_style = \"openai\"\nbackend = \"generic\"\n\n[[models]]\nname = \"devstral-2\"\nprovider = \"mistral\"\nalias = \"devstral\"\n\n[[models]]\nname = \"deepseek-v4\"\nprovider = \"local-chat\"\nalias = \"deepseek-v4\"\nmax_context_length = 400000\n"
	if got := doc.Render(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// A new file lists the tables after the root's values.
	doc = parseTOML(t, "")
	doc.Set("active_model", "gpt-5.5")
	tables := &TOMLArrayOfTables{}
	doc.Root().Insert("providers", tables)
	table, _ := TOMLTableOf(map[string]any{"name": "local-chat", "backend": "generic"}, rank)
	tables.Push(table)
	if got, want := doc.Render(), "active_model = \"gpt-5.5\"\n\n[[providers]]\nname = \"local-chat\"\nbackend = \"generic\"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTOMLRoundTripsWhatItReads(t *testing.T) {
	for _, text := range []string{
		"",
		"# only a comment\n",
		"a = 1\r\nb = 'x'\r\n",
		"key = \"value\"  # trailing\n\n\n[table]   # header note\n  indented = true\n",
		"a.b.c = 1\na.d = 2\n[x.y]\nz = [1, 2 , 3,]\n",
		"arr = [\n  # first\n  1,\n  2, # second\n]\n",
		"inline = { a = 1, b.c = \"two\" , d = { e = [] } }\n",
		"[[fruit]]\nname = \"apple\"\n[fruit.physical]\ncolor = \"red\"\n[[fruit]]\nname = \"banana\"\n",
		"s1 = \"\"\"\nmulti\nline\"\"\"\ns2 = '''\nraw \\ text'''\nd = 1979-05-27T07:32:00Z\nt = 07:32:00\nf = 6.626e-34\nh = 0xDEAD_BEEF\n",
		"[b]\nx = 1\n[a]\ny = 2\n[b.c]\nz = 3\n",
		"no_final_newline = 1",
	} {
		doc := parseTOML(t, text)
		want := strings.ReplaceAll(text, "\r", "")
		// Every value ends its line.
		if !strings.HasSuffix(want, "\n") && want != "" {
			want += "\n"
		}
		if got := doc.String(); got != want {
			t.Errorf("round trip of %q gave %q", text, got)
		}
	}
}

func TestTOMLInvalidReportsALine(t *testing.T) {
	for _, tc := range []struct {
		text string
		line int
	}{
		{"token = \"astr_x\n", 1},
		{"a = 1\nb = \n", 2},
		{"a = 1\na = 2\n", 2},
		{"[t]\nx = 1\n[t]\n", 3},
		{"d = 1979-13-27\n", 1},
	} {
		_, err := ParseTOML(tc.text)
		inv, ok := err.(*Invalid)
		if !ok {
			t.Fatalf("%q: %v", tc.text, err)
		}
		if inv.Message != "is not valid TOML" || inv.Line != tc.line {
			t.Errorf("%q: %q at line %d, want line %d", tc.text, inv.Message, inv.Line, tc.line)
		}
		if strings.Contains(inv.Error(), "astr_x") {
			t.Errorf("%q: the error shows the value", tc.text)
		}
	}
}

func TestTOMLStringReprs(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"plain", `"plain"`},
		{`C:\path`, `'C:\path'`},
		{`say "hi"`, `'say "hi"'`},
		{`it's "x"`, `'''it's "x"'''`},
		{"two\nlines", "\"\"\"\ntwo\nlines\"\"\""},
		{"tab\there", `"tab\there"`},
		{"bell\x07", `"bell\u0007"`},
		{`end'`, `"end'"`},
		{`q" end'`, `"q\" end'"`},
	} {
		if got := tomlStringRepr(tc.value, styleNone, literalNone); got != tc.want {
			t.Errorf("%q: got %s, want %s", tc.value, got, tc.want)
		}
	}
	for _, tc := range []struct{ key, want string }{
		{"plain-key_1", "plain-key_1"},
		{"model.name", `"model.name"`},
		{"", `""`},
		{`back\slash`, `'back\slash'`},
	} {
		if got := tomlKeyRepr(tc.key); got != tc.want {
			t.Errorf("key %q: got %s, want %s", tc.key, got, tc.want)
		}
	}
	for _, tc := range []struct {
		f    float64
		want string
	}{{1, "1.0"}, {0.5, "0.5"}, {1e21, "1000000000000000000000.0"}, {1e-7, "0.0000001"}} {
		if got := tomlFloatRepr(tc.f); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.f, got, tc.want)
		}
	}
}

func TestTOMLPrefixedTables(t *testing.T) {
	doc := parseTOML(t, "[models.mine]\nmodel = \"x\"\n")
	rank := func(name string) int {
		if name == "provider" {
			return 0
		}
		return 1
	}
	if err := doc.SetPrefixed("models", "astrlink/", map[string]any{
		"astrlink/b": map[string]any{"model": "b", "provider": "astrlink-chat"},
		"astrlink/a": map[string]any{"model": "a", "provider": "astrlink-chat"},
	}, rank); err != nil {
		t.Fatal(err)
	}
	want := "[models.mine]\nmodel = \"x\"\n\n[models.\"astrlink/a\"]\nprovider = \"astrlink-chat\"\nmodel = \"a\"\n\n[models.\"astrlink/b\"]\nprovider = \"astrlink-chat\"\nmodel = \"b\"\n"
	if got := doc.Render(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	tables, ok := doc.PrefixedTables("models", "astrlink/")
	if !ok || len(tables) != 2 || Encode(tables["astrlink/a"]) != `{"model":"a","provider":"astrlink-chat"}` {
		t.Fatalf("read %v", tables)
	}
	doc = parseTOML(t, doc.Render())
	doc.RemovePrefixed("models", "astrlink/")
	if got := doc.Render(); got != "[models.mine]\nmodel = \"x\"\n" {
		t.Fatalf("removed %q", got)
	}
	doc.RemovePrefixed("models", "")
	if got := doc.Render(); got != "" {
		t.Fatalf("emptied %q", got)
	}

	// A table whose header carries a comment stays.
	doc = parseTOML(t, "# keep\n[models]\n\"astrlink/x\" = 1\n")
	doc.RemovePrefixed("models", "astrlink/")
	if got := doc.Render(); got != "# keep\n[models]\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTOMLReplacedTableKeepsItsPlace(t *testing.T) {
	doc := parseTOML(t, "[providers.astrlink]\nold = 1\n\n[other]\nx = 1\n")
	providers, err := doc.ParentTable("providers")
	if err != nil {
		t.Fatal(err)
	}
	table, _ := TOMLTableOf(map[string]any{"base_url": "http://x/v1"}, NoRank)
	if current, ok := providers.Get("astrlink").(*TOMLTable); ok {
		table.TakeLayout(current)
	}
	providers.Insert("astrlink", table)
	if got, want := doc.Render(), "[providers.astrlink]\nbase_url = \"http://x/v1\"\n\n[other]\nx = 1\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := parseTOML(t, "providers = 1\n").ParentTable("providers"); err == nil {
		t.Fatal("a value is not a table")
	}
}

func TestTOMLJSONOfValues(t *testing.T) {
	doc := parseTOML(t, "[t]\ns = \"x\"\ni = 3\nf = 1.5\nb = true\nd = 2024-01-02\na = [1, \"two\"]\nin = { k = \"v\" }\n")
	value, ok := TOMLJSON(doc.Root().Get("t"))
	if !ok {
		t.Fatal("not JSON")
	}
	want := `{"a":[1,"two"],"b":true,"d":"2024-01-02","f":1.5,"i":3,"in":{"k":"v"},"s":"x"}`
	if got := Encode(value); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
