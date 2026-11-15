package clients

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Pair is a test-only client written in two places, like a client's two
// editions.
var Pair = cc.New(pair{})

func init() { cc.RegisterUnlisted(Pair) }

type pair struct{}

// pairConfig is `config.json`, strict JSON.
type pairConfig struct{}

func (pair) ID() string          { return "pair" }
func (pair) Name() string        { return "Pair" }
func (pair) Protocols() []string { return []string{"openai.chat", "anthropic.messages"} }
func (pair) Targets() []string   { return []string{"cn", "intl"} }

func (pair) Dir(home agents.Home, target string) string {
	if target == "" {
		target = "none"
	}
	return home.Join(".pair-" + target)
}

func (pair) Files() []cc.File { return []cc.File{pairConfig{}} }

func (p pair) ConfigPaths(home agents.Home, target string, _ *cc.Record) []string {
	return []string{filepath.Join(p.Dir(home, target), "config.json")}
}

func (pair) ConnectionKeys() []string { return []string{"baseUrl", "apiKey"} }

func (pair) CurrentModels(config *cc.Config) cc.CurrentModels {
	var models cc.CurrentModels
	if model, ok := config.Model("model"); ok {
		models.Model = &model
	}
	return models
}

func (pair) DesiredKeys(connection *cc.Connection, _ cc.Catalog) []cc.Setting {
	keys := []cc.Setting{{Key: "baseUrl", Value: Pair.BaseURL(connection.Origin)}, {Key: "apiKey", Value: connection.Token}}
	if model, ok := connection.Model("model"); ok {
		keys = append(keys, cc.Setting{Key: "model", Value: model})
	}
	return keys
}

func (pair) BaseURLKey() string { return "baseUrl" }

func (pairConfig) Format() format.Format { return format.JSONFormat{} }

func (c pairConfig) Group(document format.Document) []format.KeyValue {
	var keys []format.KeyValue
	for _, key := range []string{"baseUrl", "apiKey", "model"} {
		cc.PushPresent(&keys, c, document, key)
	}
	return keys
}

func pairRecords(t *testing.T, home agents.Home) any { return readJSON(t, cc.RecordsPath(home)) }

func pairConfigOf(t *testing.T, home agents.Home, target string) any {
	return readJSON(t, filepath.Join(home.Dir(), ".pair-"+target, "config.json"))
}

func pairPlace(t *testing.T, target string) cc.Place {
	t.Helper()
	place, err := cc.NewPlace(Pair, &target)
	if err != nil {
		t.Fatal(err)
	}
	return place
}

func pairSelection(t *testing.T, model string) *cc.Selection {
	t.Helper()
	selection, err := cc.Select(&cc.Models{Model: &model}, Pair)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func asJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, string(data))
}

func TestPairEachTargetIsAPlaceOfItsOwn(t *testing.T) {
	home := scratchHome(t)
	cn, intl := pairPlace(t, "cn"), pairPlace(t, "intl")
	type row struct {
		target   string
		detected bool
		state    cc.State
	}
	statuses := func() []row {
		got, err := cc.StatusOf(home, []cc.Client{Pair}, origin+"/", tokens())
		if err != nil {
			t.Fatal(err)
		}
		var rows []row
		for _, status := range got {
			rows = append(rows, row{*status.Target, status.Detected, status.State})
		}
		return rows
	}
	if got := statuses(); !reflect.DeepEqual(got, []row{{"cn", false, cc.NotConfigured}, {"intl", false, cc.NotConfigured}}) {
		t.Fatalf("%+v", got)
	}
	fields := models("model", "pair-route")
	if _, _, failed := cc.WriteWith(home, cn, connection(fields), cc.Catalog{}, tokens(), false); failed == nil {
		t.Fatal("wrote an absent client")
	}
	mkdir(t, home.Dir(), ".pair-cn")
	mkdir(t, home.Dir(), ".pair-intl")

	// Version 1 holds the clients released builds know, and nothing else.
	writeAt(t, home, Claude, connection(nil), false)
	if got := at(pairRecords(t, home), "version"); !reflect.DeepEqual(got, decode(t, "1")) {
		t.Fatal(got)
	}
	chosen := connection(fields)
	chosen.Target = "cn"
	if asked := writePlace(t, home, cn, chosen, cc.Catalog{}, false); asked != nil {
		t.Fatalf("%+v", asked)
	}
	written := pairRecords(t, home).(map[string]any)
	if !reflect.DeepEqual(written["version"], decode(t, "2")) || at(written, "pair:cn", "token_id") != "token_01" || written["pair:intl"] != nil || written["pair"] != nil {
		t.Fatalf("%v", written)
	}
	if got := pairConfigOf(t, home, "cn"); !reflect.DeepEqual(got, decode(t, `{"baseUrl": "`+origin+`/v1", "apiKey": "`+token+`", "model": "pair-route"}`)) {
		t.Fatalf("%v", got)
	}
	if got := pairConfigOf(t, home, "intl"); got != nil {
		t.Fatalf("%v", got)
	}
	if got := statuses(); !reflect.DeepEqual(got, []row{{"cn", true, cc.Configured}, {"intl", true, cc.NotConfigured}}) {
		t.Fatalf("%+v", got)
	}
	shown, err := cc.StatusOf(home, []cc.Client{Pair}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if at(asJSON(t, shown[0]), "target") != "cn" || at(asJSON(t, shown[1]), "client") != "pair" {
		t.Fatalf("%+v", shown)
	}

	// A batch names the place by its target.
	secret := &cc.Secret{ID: "token_01", Token: token}
	jobs := []cc.Job{{Home: &home, Place: intl, Action: cc.ActionWrite, Selection: pairSelection(t, "intl-route"), Details: cc.Catalog{}}}
	outcome := cc.Run(jobs, secret, origin, tokens(), false)
	if len(outcome.Results) != 1 || !outcome.Results[0].OK {
		t.Fatalf("%+v", outcome)
	}
	if at(pairConfigOf(t, home, "intl"), "model") != "intl-route" || at(pairConfigOf(t, home, "cn"), "model") != "pair-route" {
		t.Fatal("models")
	}
	records, err := cc.ReadRecords(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := records.Places(); !reflect.DeepEqual(got, []cc.Place{place(Claude), cn, intl}) {
		t.Fatalf("%v", got)
	}

	// Port sync follows every place.
	syncAt(t, home, "http://127.0.0.1:9000/")
	for _, target := range []string{"cn", "intl"} {
		if got := at(pairConfigOf(t, home, target), "baseUrl"); got != "http://127.0.0.1:9000/v1" {
			t.Fatalf("%s: %v", target, got)
		}
	}
	preview, err := cc.PreviewPlace(home, intl, origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || !strings.HasSuffix(preview.Files[0].Path, "config.json") {
		t.Fatalf("%+v", preview)
	}

	// Removing one target leaves the other alone, and the file goes back to
	// version 1 once only the released keys are left.
	removePlace(t, home, cn)
	if pairConfigOf(t, home, "cn") != nil || pairConfigOf(t, home, "intl") == nil {
		t.Fatal("removed the wrong place")
	}
	left := pairRecords(t, home).(map[string]any)
	if left["pair:cn"] != nil || at(left, "pair:intl", "token_id") != "token_01" {
		t.Fatalf("%v", left)
	}
	removePlace(t, home, intl)
	if got := at(pairRecords(t, home), "version"); !reflect.DeepEqual(got, decode(t, "1")) {
		t.Fatal(got)
	}
	if records, _ := cc.ReadRecords(home); records.Get(intl) != nil {
		t.Fatal("intl record left")
	}
}

func TestPairABatchTellsTheTargetsItAsksAboutApart(t *testing.T) {
	home := scratchHome(t)
	for _, target := range []string{"cn", "intl"} {
		writeFile(t, filepath.Join(home.Dir(), ".pair-"+target, "config.json"), `{"apiKey": "sk-`+target+`-secret"}`+"\n")
	}
	writeAt(t, home, Claude, connection(nil), false)
	secret := &cc.Secret{ID: "token_01", Token: token}
	job := func(place cc.Place, action cc.Action) cc.Job {
		job := cc.Job{Home: &home, Place: place, Action: action, Details: cc.Catalog{}}
		if action == cc.ActionWrite {
			job.Selection = pairSelection(t, "pair-route")
		}
		return job
	}
	jobs := []cc.Job{job(pairPlace(t, "cn"), cc.ActionWrite), job(pairPlace(t, "intl"), cc.ActionWrite), job(place(Claude), cc.ActionRemove)}
	places := func(value any, list string) [][2]any {
		var out [][2]any
		items, _ := at(value, list).([]any)
		for _, item := range items {
			out = append(out, [2]any{at(item, "client"), at(item, "target")})
		}
		return out
	}
	expected := [][2]any{{"pair", "cn"}, {"pair", "intl"}, {"claude", nil}}

	asked := asJSON(t, cc.Run(jobs, secret, origin, tokens(), false))
	if at(asked, "status") != "needs_confirmation" || !reflect.DeepEqual(places(asked, "items"), expected) {
		t.Fatalf("%v", asked)
	}
	items := at(asked, "items").([]any)
	for i, target := range []string{"cn", "intl"} {
		keys, _ := at(items[i], "keys").([]any)
		dir, _ := at(items[i], "dir").(string)
		if len(keys) == 0 || at(keys[0], "key") != "apiKey" || !strings.HasSuffix(dir, ".pair-"+target) {
			t.Fatalf("%v", items[i])
		}
	}
	if at(items[2], "remove") != true || at(pairConfigOf(t, home, "cn"), "apiKey") != "sk-cn-secret" {
		t.Fatalf("%v", asked)
	}

	done := asJSON(t, cc.Run(jobs, secret, origin, tokens(), true))
	if at(done, "status") != "done" || !reflect.DeepEqual(places(done, "results"), expected) {
		t.Fatalf("%v", done)
	}
	for _, result := range at(done, "results").([]any) {
		if at(result, "ok") != true {
			t.Fatalf("%v", done)
		}
	}
	for _, target := range []string{"cn", "intl"} {
		if got := at(pairConfigOf(t, home, target), "apiKey"); got != token {
			t.Fatalf("%s: %v", target, got)
		}
	}
}

func TestPairItemsNameATargetAndAKnownClient(t *testing.T) {
	item := func(text string) (cc.ApplyItem, error) {
		var out cc.ApplyItem
		err := json.Unmarshal([]byte(text), &out)
		return out, err
	}
	named, err := item(`{"client": "pair", "wsl": null, "target": "cn", "action": "remove"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cc.NewPlace(named.Client, named.Target); err != nil || got != pairPlace(t, "cn") {
		t.Fatalf("%+v %v", got, err)
	}
	// The round-1 shape still reads, without a target.
	plain, err := item(`{"client": "codex", "wsl": null, "action": "remove"}`)
	if err != nil || plain.Target != nil {
		t.Fatalf("%+v %v", plain, err)
	}
	if _, err := item(`{"client": "cursor", "wsl": null, "action": "remove"}`); err == nil {
		t.Fatal("read an unknown client")
	}
}
