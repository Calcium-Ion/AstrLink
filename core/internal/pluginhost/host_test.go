package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/tidwall/gjson"
)

// memoPlugin asks for a project until the session has one, then keeps a
// snapshot of it in the system prompt and recalls the typed text each turn.
// observeTurn writes what it saw to the store.
const memoPlugin = `export const meta = {
  apiVersion: 1, key: "memo", name: "Memo", version: "1.0.0",
  capabilities: ["context.inject", "request.read", "storage", "ui.views"],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-project" }],
};
export function prepareSession(view) {
  const project = view.meta.project;
  if (project === undefined) return { system: { items: ["ASK"] } };
  if (project.value === "none") return undefined;
  return { system: { open: "<ctx>", items: ["snapshot of " + project.value], close: "</ctx>" } };
}
export function prepareTurn(turn) {
  const project = turn.meta.project;
  if (project === undefined || project.value === "none") return { skip: "no_project" };
  return {
    context: { open: "<mem>", items: ["recall: " + turn.user.typed], close: "</mem>" },
    record: { title: "记忆", status: "ok", summary: "召回 1/1", view: [{ type: "text", text: "ok" }] },
  };
}
export function observeTurn(turn) {
  astrlink.store.set("seen:" + turn.session.turn, turn.meta.project.value + " " + turn.user.typed);
}
`

type settingsSource map[string]plugin.Settings

func (settings settingsSource) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	return settings, nil
}

// pluginStore keeps the plugins' values and tells each write.
type pluginStore struct {
	mutex  sync.Mutex
	values map[string]json.RawMessage
	writes chan string
}

func (store *pluginStore) Get(_ context.Context, owner, key string) (json.RawMessage, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value, found := store.values[owner+"/"+key]
	return value, found, nil
}

func (store *pluginStore) Set(_ context.Context, owner, key string, value json.RawMessage) error {
	store.mutex.Lock()
	store.values[owner+"/"+key] = value
	store.mutex.Unlock()
	store.writes <- key + "=" + string(value)
	return nil
}

func (store *pluginStore) Delete(_ context.Context, owner, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, owner+"/"+key)
	return nil
}

func (store *pluginStore) Keys(context.Context, string, string) ([]string, error) { return nil, nil }

type harness struct {
	host     *Host
	bindings *sqlite.Store
	writes   chan string
	// added gets what captures add to written request records.
	added    recordSink
	manager  *plugin.Manager
	registry *metadata.Registry
	box      *settingsBox
}

// recordSink stands for the request records captures add to.
type recordSink chan []contract.RequestPluginRecord

func (sink recordSink) AppendRequestPluginRecords(_ context.Context, id contract.RequestID, records []contract.RequestPluginRecord) error {
	if id != "request_1" {
		return errors.New("an unknown request " + string(id))
	}
	sink <- records
	return nil
}

func newHarness(t *testing.T, settings plugin.Settings, sources map[string]string, fetcher plugin.Fetcher) *harness {
	t.Helper()
	ctx := context.Background()
	bindings, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bindings.Close() })
	store := &pluginStore{values: map[string]json.RawMessage{}, writes: make(chan string, 16)}
	builtin := fstest.MapFS{}
	all := settingsSource{}
	// A source is a plugin.js under its key.
	for key, source := range sources {
		builtin[key+"/plugin.js"] = &fstest.MapFile{Data: []byte(source)}
		all[key] = settings
	}
	box := &settingsBox{settings: all}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin: builtin, Settings: box, Services: plugin.Services{Store: store, Fetcher: fetcher},
	})
	if err := manager.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	registry := metadata.NewRegistry(bindings)
	for _, loaded := range manager.Current().Plugins() {
		if !loaded.Loaded() {
			t.Fatalf("plugin %s: %+v", loaded.Key, loaded.Err)
		}
		if err := registry.RegisterPlugin(ctx, loaded.Key, loaded.Attributes()); err != nil {
			t.Fatal(err)
		}
	}
	added := make(recordSink, 4)
	host := New(Options{
		Plugins: manager, Metadata: registry, Bindings: bindings, Store: bindings, Records: added,
		WorkspaceKey: []byte("workspace key"), Logf: t.Logf,
	})
	t.Cleanup(host.Close)
	return &harness{host: host, bindings: bindings, writes: store.writes, added: added, manager: manager, registry: registry, box: box}
}

// settingsBox lets a test change the settings the manager reads.
type settingsBox struct {
	mutex    sync.Mutex
	settings settingsSource
}

func (box *settingsBox) PluginSettings(ctx context.Context) (map[string]plugin.Settings, error) {
	box.mutex.Lock()
	defer box.mutex.Unlock()
	return box.settings.PluginSettings(ctx)
}

// reconfigure gives every plugin new settings, as saving the plugin page
// does.
func (h *harness) reconfigure(t *testing.T, settings plugin.Settings) {
	t.Helper()
	h.box.mutex.Lock()
	for key := range h.box.settings {
		h.box.settings[key] = settings
	}
	h.box.mutex.Unlock()
	if err := h.manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// markerRun binds sessions the way a user's marker line does.
type markerRun struct {
	h *harness
	t *testing.T
}

func (runner *markerRun) bind(session, line string) {
	runner.t.Helper()
	body := messages(user(line))
	run := runner.h.host.Begin(context.Background(), Input{
		RequestID: "request_1", Protocol: contract.ProtocolAnthropicMessages, Model: "claude", Client: "claude_code",
		Header: http.Header{}, Session: session, Turn: 7, Body: []byte(body),
	})
	if run == nil {
		runner.t.Fatal("Begin = nil")
	}
	run.Inject([]byte(body))
}

// restart is the same gateway after a restart: storage stays, memory goes.
func (h *harness) restart(t *testing.T) {
	t.Helper()
	h.host = New(Options{
		Plugins: h.manager, Metadata: h.registry, Bindings: h.bindings, Store: h.bindings, Records: h.added,
		WorkspaceKey: []byte("workspace key"), Logf: t.Logf,
	})
	t.Cleanup(h.host.Close)
	if err := h.host.LoadSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// allCapabilities are the settings of a plugin the user enabled with every
// capability, the other choices left as a new plugin has them.
func allCapabilities() plugin.Settings {
	return plugin.Settings{Enabled: true, WorkspaceBinding: true, Capabilities: []string{
		plugin.CapabilityInject, plugin.CapabilityRequest, plugin.CapabilityNetwork, plugin.CapabilityStorage, plugin.CapabilityViews,
	}}
}

func (h *harness) begin(t *testing.T, turn int, body string, inspect func(context.Context, []string) ([]string, error)) *Run {
	t.Helper()
	run := h.host.Begin(context.Background(), Input{
		RequestID: "request_1",
		Protocol:  contract.ProtocolAnthropicMessages, Model: "claude", Client: "claude_code",
		Header: http.Header{}, Session: "session_1", Turn: turn, Body: []byte(body), Inspect: inspect,
	})
	if run == nil {
		t.Fatal("Begin = nil")
	}
	return run
}

func messages(items ...string) string {
	return `{"model":"claude","messages":[` + strings.Join(items, ",") + `]}`
}

func user(text string) string {
	encoded, _ := json.Marshal(text)
	return `{"role":"user","content":[{"type":"text","text":` + string(encoded) + `}]}`
}

func assistant(text string) string {
	encoded, _ := json.Marshal(text)
	return `{"role":"assistant","content":[{"type":"text","text":` + string(encoded) + `}]}`
}

const toolRound = `{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}`

func recordOf(t *testing.T, records []contract.RequestPluginRecord, hook string) contract.RequestPluginRecord {
	t.Helper()
	for _, record := range records {
		if record.Hook == hook {
			if err := record.Validate(); err != nil {
				t.Fatalf("%s record does not validate: %v", hook, err)
			}
			return record
		}
	}
	t.Fatalf("no %s record in %+v", hook, records)
	return contract.RequestPluginRecord{}
}

func (h *harness) seen(t *testing.T) string {
	t.Helper()
	select {
	case write := <-h.writes:
		return write
	case <-time.After(5 * time.Second):
		t.Fatal("observeTurn did not run")
		return ""
	}
}

func TestASessionIsAskedThenBoundThenRecalled(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"memo": memoPlugin}, nil)

	// Turn 1: no project yet. The resident block asks; the turn gets nothing.
	turn1 := messages(user("帮我看看分页逻辑"))
	run := h.begin(t, 1, turn1, nil)
	body := run.Inject([]byte(turn1))
	if got := gjson.GetBytes(body, "system").Str; got != "ASK" {
		t.Fatalf("system = %q in %s", got, body)
	}
	if gjson.GetBytes(body, "messages.0.content.#").Int() != 1 {
		t.Fatalf("turn 1 got a block: %s", body)
	}
	records, events := run.Report()
	if session := recordOf(t, records, plugin.HookPrepareSession); session.Injection == nil || session.Injection.InjectedBytes != 3 {
		t.Fatalf("prepareSession record = %+v", session)
	}
	turnRecord := recordOf(t, records, plugin.HookPrepareTurn)
	if turnRecord.Status != contract.PluginRecordSkipped || turnRecord.SkippedReason != "no_project" || turnRecord.Observe != observeAwaiting {
		t.Fatalf("prepareTurn record = %+v", turnRecord)
	}
	if len(events) != 1 || events[0].Kind != contract.RequestEventPlugin || events[0].Plugin != "memo" || events[0].Validate() != nil {
		t.Fatalf("events = %+v", events)
	}
	// Without a project the capture waits for the reply.
	select {
	case write := <-h.writes:
		t.Fatalf("a turn without a project was captured before the reply: %s", write)
	case <-time.After(100 * time.Millisecond):
	}
	// The reply binds the session; the turn is captured into the project.
	scanner := run.NewMarkerScanner()
	scanner.Write("<memo-project>astrlink</memo-project>\n好的，先看分页。")
	bound := run.Finish(context.Background(), scanner.Close(), true)
	// No session had the project and nothing offered it: a new project.
	if len(bound) != 1 || bound[0] != (contract.MetadataEvent{Key: "project", Event: contract.MetadataEventBound, Value: "astrlink", SetBy: contract.MetadataSetByModelMarker, New: true}) {
		t.Fatalf("Finish = %+v", bound)
	}
	if captures := run.Captures(); captures["memo"] != observeQueued {
		t.Fatalf("captures = %+v", captures)
	}
	if write := h.seen(t); write != `seen:1="astrlink 帮我看看分页逻辑"` {
		t.Fatalf("observeTurn saw %s", write)
	}

	// The turn goes on: the next call settles it again with the project,
	// and captures nothing.
	call2 := messages(user("帮我看看分页逻辑"), toolRound)
	run = h.begin(t, 1, call2, nil)
	if value := run.Metadata().Values["project"]; value.Value != "astrlink" || value.SetBy != contract.MetadataSetByModelMarker || value.Turn != 1 {
		t.Fatalf("metadata = %+v", run.Metadata())
	}
	body = run.Inject([]byte(call2))
	if got := gjson.GetBytes(body, "system").Str; got != "<ctx>\nsnapshot of astrlink\n</ctx>" {
		t.Fatalf("system = %q", got)
	}
	block := gjson.GetBytes(body, "messages.0.content.0.text").Str
	if block != "<mem>\nrecall: 帮我看看分页逻辑\n</mem>" {
		t.Fatalf("turn block = %q in %s", block, body)
	}
	records, events = run.Report()
	turnRecord = recordOf(t, records, plugin.HookPrepareTurn)
	if turnRecord.Title != "记忆" || turnRecord.Summary != "召回 1/1" || turnRecord.Observe != observeSkippedBindingTurn ||
		turnRecord.Injection == nil || turnRecord.Injection.InjectedBytes != len(block) {
		t.Fatalf("prepareTurn record = %+v", turnRecord)
	}
	if len(events) != 1 || events[0].Title != "Memo" || events[0].Summary != "召回 1/1" {
		t.Fatalf("events = %+v", events)
	}

	// A third call of the turn reuses the block byte for byte.
	call3 := messages(user("帮我看看分页逻辑"), toolRound, toolRound)
	run = h.begin(t, 1, call3, nil)
	again := run.Inject([]byte(call3))
	if gjson.GetBytes(again, "messages.0").Raw != gjson.GetBytes(body, "messages.0").Raw || gjson.GetBytes(again, "system").Raw != gjson.GetBytes(body, "system").Raw {
		t.Fatalf("the turn's bytes changed:\n%s\n%s", body, again)
	}
	records, _ = run.Report()
	if turnRecord := recordOf(t, records, plugin.HookPrepareTurn); turnRecord.Observe != observeSkippedContinue || turnRecord.Title != "记忆" ||
		turnRecord.ElapsedMs != 0 || turnRecord.Status != contract.PluginRecordOK || turnRecord.SkippedReason != "" {
		t.Fatalf("continuation record = %+v", turnRecord)
	}

	// Turn 2 recalls anew and puts turn 1's block back.
	turn2 := messages(user("帮我看看分页逻辑"), assistant("<memo-project>astrlink</memo-project>\n改好了"), user("再补一个测试"))
	run = h.begin(t, 2, turn2, nil)
	body2 := run.Inject([]byte(turn2))
	if gjson.GetBytes(body2, "messages.0").Raw != gjson.GetBytes(body, "messages.0").Raw {
		t.Fatalf("turn 1's message changed: %s", body2)
	}
	if got := gjson.GetBytes(body2, "messages.2.content.0.text").Str; got != "<mem>\nrecall: 再补一个测试\n</mem>" {
		t.Fatalf("turn 2 block = %q", got)
	}
	records, _ = run.Report()
	if turnRecord := recordOf(t, records, plugin.HookPrepareTurn); turnRecord.Injection.ReplayedBlocks != 1 || turnRecord.Observe != observeQueued {
		t.Fatalf("turn 2 record = %+v", turnRecord)
	}
	if write := h.seen(t); write != `seen:2="astrlink 再补一个测试"` {
		t.Fatalf("observeTurn saw %s", write)
	}
	// A model's marker cannot rebind a bound session.
	scanner = run.NewMarkerScanner()
	scanner.Write("<memo-project>other</memo-project>")
	if events := run.Finish(context.Background(), scanner.Close(), true); len(events) != 1 || events[0].Reason != contract.MetadataIgnoredAlreadyBound {
		t.Fatalf("Finish = %+v", events)
	}

	// The user rebinds by sending one marker line: no recall, no capture.
	turn3 := messages(user("帮我看看分页逻辑"), assistant("改好了"), user("再补一个测试"), assistant("补好了"), user("<memo-project>paseo</memo-project>"))
	run = h.begin(t, 3, turn3, nil)
	if events := run.Metadata().Events; len(events) != 1 || events[0].Event != contract.MetadataEventRebound || events[0].Value != "paseo" {
		t.Fatalf("events = %+v", events)
	}
	body3 := run.Inject([]byte(turn3))
	if got := gjson.GetBytes(body3, "system").Str; got != "<ctx>\nsnapshot of paseo\n</ctx>" {
		t.Fatalf("system = %q", got)
	}
	if gjson.GetBytes(body3, "messages.4.content.#").Int() != 1 {
		t.Fatalf("the marker message got a block: %s", body3)
	}
	select {
	case write := <-h.writes:
		t.Fatalf("the marker message was captured: %s", write)
	default:
	}
}

func TestAHistoryRebuildsALostBinding(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"memo": memoPlugin}, nil)
	body := messages(user("把分页改成游标"), assistant("<memo-project>astrlink</memo-project>\n好"), user("继续改"))
	run := h.begin(t, 1, body, nil)
	if value := run.Metadata().Values["project"]; value.Value != "astrlink" || value.Source != contract.MetadataSourceSession {
		t.Fatalf("metadata = %+v", run.Metadata())
	}
	if events := run.Metadata().Events; len(events) != 1 || events[0].SetBy != contract.MetadataSetByModelMarker {
		t.Fatalf("events = %+v", events)
	}
	run.Inject([]byte(body))
	// A stored value, none included, stops the replay.
	if err := h.bindings.PutMetadataValue(context.Background(), storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: "session_1", Key: "project", Value: contract.MetadataNone,
		SetBy: contract.MetadataSetByManual, Turn: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	run = h.begin(t, 2, messages(user("把分页改成游标"), assistant("<memo-project>astrlink</memo-project>\n好"), user("继续改"), assistant("好"), user("再看看")), nil)
	if value := run.Metadata().Values["project"]; value.Value != contract.MetadataNone || len(run.Metadata().Events) != 0 {
		t.Fatalf("metadata = %+v", run.Metadata())
	}
}

func TestPrivacyDecidesWhatThePluginReads(t *testing.T) {
	bound := func(h *harness) {
		if err := h.bindings.PutMetadataValue(context.Background(), storage.MetadataValueRecord{
			TargetKind: storage.MetadataTargetSession, TargetID: "session_1", Key: "project", Value: "astrlink",
			SetBy: contract.MetadataSetByManual, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	body := messages(user("我的密钥是 sk-secret，帮我看看"))
	for _, test := range []struct {
		name    string
		inspect func(context.Context, []string) ([]string, error)
		trusted bool
		reason  string
		observe string
		recall  string
	}{
		{"redacted", func(_ context.Context, texts []string) ([]string, error) {
			return []string{strings.ReplaceAll(texts[0], "sk-secret", "[KEY_1]")}, nil
		}, false, "", observeQueued, "recall: 我的密钥是 [KEY_1]，帮我看看"},
		{"blocked", func(context.Context, []string) ([]string, error) { return nil, ErrBlocked }, false,
			contract.PluginSkipPrivacyBlocked, observeSkippedPrivacy, ""},
		{"unavailable", func(context.Context, []string) ([]string, error) { return nil, errors.New("engine down") }, false,
			contract.PluginSkipPrivacyUnavailable, observeSkippedPrivacy, ""},
		{"trusted", func(context.Context, []string) ([]string, error) { return nil, ErrBlocked }, true,
			"", observeQueued, "recall: 我的密钥是 sk-secret，帮我看看"},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := allCapabilities()
			settings.Trusted = test.trusted
			h := newHarness(t, settings, map[string]string{"memo": memoPlugin}, nil)
			bound(h)
			run := h.begin(t, 1, body, test.inspect)
			sent := run.Inject([]byte(body))
			records, _ := run.Report()
			record := recordOf(t, records, plugin.HookPrepareTurn)
			if record.SkippedReason != test.reason || record.Observe != test.observe {
				t.Fatalf("record = %+v", record)
			}
			block := gjson.GetBytes(sent, "messages.0.content.0.text").Str
			if test.recall == "" {
				if gjson.GetBytes(sent, "messages.0.content.#").Int() != 1 {
					t.Fatalf("a blocked turn got a block: %s", sent)
				}
				return
			}
			if block != "<mem>\n"+test.recall+"\n</mem>" {
				t.Fatalf("block = %q", block)
			}
			if write := h.seen(t); !strings.Contains(write, strings.TrimPrefix(test.recall, "recall: ")) {
				t.Fatalf("observeTurn saw %s", write)
			}
		})
	}
}

// stalled holds every request until its context ends.
type stalled struct{}

func (stalled) Fetch(ctx context.Context, _ *plugin.Plugin, _ plugin.HTTPRequest) plugin.HTTPResponse {
	<-ctx.Done()
	return plugin.HTTPResponse{Error: plugin.FetchTimeout}
}

func TestASlowTurnGoesOutWithoutItsBlock(t *testing.T) {
	slow := `export const meta = {
  apiVersion: 1, key: "slow", name: "Slow", version: "1.0.0",
  capabilities: ["context.inject", "request.read"],
};
export function prepareTurn(turn) {
  const until = astrlink.clock.now() + 300;
  while (astrlink.clock.now() < until) {}
  return { context: "late" };
}
`
	h := newHarness(t, allCapabilities(), map[string]string{"slow": slow}, nil)
	h.host.budget = 100 * time.Millisecond
	body := messages(user("帮我看看分页逻辑"))
	run := h.begin(t, 1, body, nil)
	if sent := run.Inject([]byte(body)); string(sent) != body {
		t.Fatalf("body = %s", sent)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareTurn); record.Status != contract.PluginRecordSkipped || record.SkippedReason != contract.PluginSkipTimeout {
		t.Fatalf("record = %+v", record)
	}
	// The late result is not kept for the turn's later calls.
	later := messages(user("帮我看看分页逻辑"), toolRound)
	run = h.begin(t, 1, later, nil)
	if sent := run.Inject([]byte(later)); string(sent) != later {
		t.Fatalf("a later call picked up a block: %s", sent)
	}
}

func TestStalledRequestsEndBeforeThePluginsWait(t *testing.T) {
	fallback := `export const meta = {
  apiVersion: 1, key: "fallback", name: "Fallback", version: "1.0.0",
  capabilities: ["context.inject", "request.read", "network"], network: { purpose: "test" },
};
export function prepareTurn(turn) {
  const [slow] = astrlink.http.fetch([{ method: "GET", url: "https://memory.test/slow", timeoutMs: 5000 }]);
  return { context: "fallback after " + slow.error };
}
`
	h := newHarness(t, allCapabilities(), map[string]string{"fallback": fallback}, stalled{})
	h.host.budget = 400 * time.Millisecond
	body := messages(user("帮我看看分页逻辑"))
	run := h.begin(t, 1, body, nil)
	started := time.Now()
	sent := run.Inject([]byte(body))
	if waited := time.Since(started); waited >= h.host.budget {
		t.Fatalf("the request waited %v", waited)
	}
	if got := gjson.GetBytes(sent, "messages.0.content.0.text").Str; got != "fallback after budget" {
		t.Fatalf("block = %q in %s", got, sent)
	}
}

// latePlugin writes each turn it sees, marked when it sees it late.
const latePlugin = `export const meta = {
  apiVersion: 1, key: "memo", name: "Memo", version: "1.0.0",
  capabilities: ["request.read", "storage"],
  metadata: [{ key: "project", label: "Project", pattern: "^[a-z0-9-]{1,32}$", marker: "memo-project" }],
};
export function observeTurn(turn) {
  astrlink.store.set("seen:" + turn.session.turn, (turn.late ? "late " : "") + turn.meta.project.value + " " + turn.user.typed);
}
`

// beginIn begins a request of session.
func (h *harness) beginIn(t *testing.T, session string, turn int, body string) *Run {
	t.Helper()
	run := h.host.Begin(context.Background(), Input{
		RequestID: "request_1", Protocol: contract.ProtocolAnthropicMessages, Model: "claude", Client: "claude_code",
		Header: http.Header{}, Session: session, Turn: turn, Body: []byte(body),
	})
	if run == nil {
		t.Fatal("Begin = nil")
	}
	run.Inject([]byte(body))
	run.Report()
	return run
}

func (h *harness) nothingWritten(t *testing.T, what string) {
	t.Helper()
	select {
	case write := <-h.writes:
		t.Fatalf("%s: %s", what, write)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTurnsBeforeTheSessionHasAProjectAreWrittenOnceItHasOne(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"memo": latePlugin}, nil)
	ctx := context.Background()
	opening := user("这个项目用来对比原厂和自部署的模型")
	// The session has no project and the reply sets none: the turn is held.
	run := h.beginIn(t, "session_1", 1, messages(opening))
	if events := run.Finish(ctx, nil, true); len(events) != 0 {
		t.Fatalf("Finish = %+v", events)
	}
	if captures := run.Captures(); captures["memo"] != observeHeld {
		t.Fatalf("captures = %+v", captures)
	}
	h.nothingWritten(t, "a turn without a project was written")
	// The next reply binds the session: the held turn is written late with
	// the project, and the binding turn after it.
	run = h.beginIn(t, "session_1", 2, messages(opening, assistant("要纳入哪个项目？"), user("llm-arena 吧")))
	scanner := run.NewMarkerScanner()
	scanner.Write("<memo-project>llm-arena</memo-project>\n好的。")
	run.Finish(ctx, scanner.Close(), true)
	if writes := map[string]bool{h.seen(t): true, h.seen(t): true}; !writes[`seen:1="late llm-arena 这个项目用来对比原厂和自部署的模型"`] || !writes[`seen:2="llm-arena llm-arena 吧"`] {
		t.Fatalf("writes = %v", writes)
	}

	// A user's marker line binds there and then: the turns held before it
	// are written with that request, the line itself is not.
	first := user("先看看大屏的刷新逻辑")
	run = h.beginIn(t, "session_2", 1, messages(first))
	run.Finish(ctx, nil, true)
	run = h.beginIn(t, "session_2", 2, messages(first, assistant("好"), user("再看推送间隔")))
	run.Finish(ctx, nil, true)
	h.beginIn(t, "session_2", 3, messages(first, assistant("好"), user("再看推送间隔"), assistant("要纳入哪个项目？"), user("<memo-project>datav</memo-project>")))
	if writes := map[string]bool{h.seen(t): true, h.seen(t): true}; !writes[`seen:1="late datav 先看看大屏的刷新逻辑"`] || !writes[`seen:2="late datav 再看推送间隔"`] {
		t.Fatalf("writes = %v", writes)
	}
	h.nothingWritten(t, "the marker line was written")

	// A session left out drops what it held.
	run = h.beginIn(t, "session_3", 1, messages(user("随便聊聊")))
	run.Finish(ctx, nil, true)
	h.beginIn(t, "session_3", 2, messages(user("随便聊聊"), assistant("要纳入哪个项目？"), user("<memo-project>none</memo-project>")))
	h.nothingWritten(t, "a session left out wrote a held turn")
	if held := h.host.held.take(lookupKey{"session_3", "memo"}); held != nil {
		t.Fatalf("held after the session was left out: %+v", held)
	}
}

func TestHeldTurnsAreTheFirstOnesWithinASize(t *testing.T) {
	held := newHeldCaptures()
	key := lookupKey{"session_1", "memo"}
	view := func(turn int, text string) TurnView {
		return TurnView{SessionView: SessionView{Session: plugin.SessionRef{ID: "session_1", Turn: turn}}, User: UserText{Text: text, Typed: text}}
	}
	for turn := 1; turn <= maxHeldTurns; turn++ {
		if !held.hold(key, view(turn, "第 "+strconv.Itoa(turn)+" 轮")) {
			t.Fatalf("turn %d was not held", turn)
		}
	}
	if held.hold(key, view(maxHeldTurns+1, "再多一轮")) {
		t.Fatal("a turn past the first ones was held")
	}
	// A turn sent again replaces itself.
	if !held.hold(key, view(2, "第 2 轮重发")) {
		t.Fatal("a turn sent again was not held")
	}
	turns := held.take(key)
	if len(turns) != maxHeldTurns || turns[1].User.Typed != "第 2 轮重发" || turns[0].Session.Turn != 1 {
		t.Fatalf("held = %+v", turns)
	}
	if held.take(key) != nil {
		t.Fatal("take left turns behind")
	}
	// A turn past the size is not held.
	if held.hold(key, view(1, strings.Repeat("长", maxHeldBytes/6+1))) {
		t.Fatal("a turn past the size was held")
	}
	// The sessions held least recently go first.
	for index := range maxHeldSessions + 1 {
		held.hold(lookupKey{"session_" + strconv.Itoa(index), "memo"}, view(1, "x"))
	}
	if held.take(lookupKey{"session_0", "memo"}) != nil || held.take(lookupKey{"session_1", "memo"}) == nil {
		t.Fatal("the session held least recently was kept")
	}
}

// gate answers each request once the test lets one through.
type gate chan struct{}

func (through gate) Fetch(ctx context.Context, _ *plugin.Plugin, _ plugin.HTTPRequest) plugin.HTTPResponse {
	select {
	case <-through:
		return plugin.HTTPResponse{Status: http.StatusOK, Body: []byte(`{}`)}
	case <-ctx.Done():
		return plugin.HTTPResponse{Error: plugin.FetchTimeout}
	}
}

const notePlugin = `export const meta = {
  apiVersion: 1, key: "note", name: "Note", version: "1.0.0",
  capabilities: ["request.read", "network", "ui.views"], network: { purpose: "test" },
};
export function observeTurn(turn) {
  if (turn.user.typed === "失败") throw new Error("boom");
  const [saved] = astrlink.http.fetch([{ method: "POST", url: "https://memory.test/save", json: { text: turn.user.typed } }]);
  return { record: { title: "写入", status: saved.status === 200 ? "ok" : "failed", summary: "写入 " + turn.user.typed } };
}
`

func TestCapturesKeepTheirRecordsWithTheirRequests(t *testing.T) {
	through := make(gate)
	h := newHarness(t, allCapabilities(), map[string]string{"note": notePlugin}, through)
	body := messages(user("把分页改成游标"))
	run := h.begin(t, 1, body, nil)
	run.Inject([]byte(body))
	run.Report()
	// A capture that ends before the request record is written goes in it;
	// one that ends after the last report, once the record is written.
	early := contract.RequestPluginRecord{Plugin: "note", PluginVersion: "1.0.0", Hook: plugin.HookObserveTurn, Title: "写入", Status: contract.PluginRecordOK}
	run.deliverObserve(early)
	if records, _ := run.Report(); len(records) != 1 || records[0].Hook != plugin.HookObserveTurn || records[0].Title != "写入" {
		t.Fatalf("records = %+v", records)
	}
	run.deliverObserve(early)
	run.Persisted()
	if added := <-h.added; len(added) != 1 || added[0].Title != "写入" {
		t.Fatalf("added = %+v", added)
	}
	// The capture itself ends after the record is written: its record is
	// added to it.
	through <- struct{}{}
	select {
	case added := <-h.added:
		if len(added) != 1 || added[0].Hook != plugin.HookObserveTurn || added[0].Status != contract.PluginRecordOK ||
			added[0].Summary != "写入 把分页改成游标" || added[0].Network.Requests != 1 || added[0].Validate() != nil {
			t.Fatalf("added = %+v", added)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the capture's record was not added")
	}
	// A capture that fails says so, whether it ends before the record is
	// written or after.
	failing := messages(user("把分页改成游标"), assistant("好"), user("失败"))
	run = h.begin(t, 2, failing, nil)
	run.Inject([]byte(failing))
	run.Persisted()
	select {
	case added := <-h.added:
		if len(added) != 1 || added[0].Status != contract.PluginRecordFailed || added[0].SkippedReason != contract.PluginSkipPluginError {
			t.Fatalf("added = %+v", added)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failed capture's record was not added")
	}
}

func TestReadTurnFindsTheUsersOwnWords(t *testing.T) {
	body := messages(
		user("把分页改成游标"),
		assistant("好"),
		`{"role":"user","content":[{"type":"text","text":"<system-reminder>\nCLAUDE.md\n</system-reminder>"},{"type":"text","text":"<memo-project>paseo</memo-project>"}]}`,
	)
	read := readTurn(contract.ProtocolAnthropicMessages, []byte(body))
	if !read.opened || read.own != "<memo-project>paseo</memo-project>" || read.user.Previous != "把分页改成游标" {
		t.Fatalf("turn = %+v", read)
	}
	if !strings.HasPrefix(read.user.Text, "<system-reminder>") || len(read.history) != 3 {
		t.Fatalf("turn = %+v", read)
	}
	stateful := readTurn(contract.ProtocolOpenAIResponses, []byte(`{"previous_response_id":"r1","input":[{"type":"function_call_output","call_id":"c","output":"ok"}]}`))
	if stateful.opened {
		t.Fatalf("a tool output opened a turn: %+v", stateful)
	}
	bare := readTurn(contract.ProtocolOpenAIResponses, []byte(`{"input":"改一下"}`))
	if !bare.opened || bare.user.Text != "改一下" || bare.user.Typed != "改一下" {
		t.Fatalf("bare input = %+v", bare)
	}
	gemini := readTurn(contract.ProtocolGoogleGenerateContent, []byte(`{"contents":[{"role":"user","parts":[{"text":"想法","thought":true},{"text":"改一下"}]}]}`))
	if gemini.user.Text != "改一下" {
		t.Fatalf("gemini = %+v", gemini)
	}
}

func TestASlowPrivacyCheckStillDecidesTheCapture(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"memo": memoPlugin}, nil)
	h.host.budget = 50 * time.Millisecond
	if err := h.bindings.PutMetadataValue(context.Background(), storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: "session_1", Key: "project", Value: "astrlink",
		SetBy: contract.MetadataSetByManual, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	slow := func(ctx context.Context, texts []string) ([]string, error) {
		select {
		case <-release:
			return texts, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	body := messages(user("帮我看看分页逻辑"))
	run := h.begin(t, 1, body, slow)
	sent := run.Inject([]byte(body))
	if gjson.GetBytes(sent, "messages.0.content.#").Int() != 1 {
		t.Fatalf("a turn whose check ran late got a block: %s", sent)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareTurn); record.SkippedReason != contract.PluginSkipTimeout || record.Observe != "" {
		t.Fatalf("record = %+v", record)
	}
	close(release)
	if write := h.seen(t); write != `seen:1="astrlink 帮我看看分页逻辑"` {
		t.Fatalf("observeTurn saw %s", write)
	}
}

func TestANewOpeningMessageSettlesEvenUnderTheSameTurnNumber(t *testing.T) {
	h := newHarness(t, allCapabilities(), map[string]string{"memo": memoPlugin}, nil)
	if err := h.bindings.PutMetadataValue(context.Background(), storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: "session_1", Key: "project", Value: "astrlink",
		SetBy: contract.MetadataSetByManual, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// A session linked through an older record gives both turns number 2.
	first := messages(user("帮我看看分页逻辑"), assistant("好了"), user("那按约定改写一下"))
	run := h.begin(t, 2, first, nil)
	run.Inject([]byte(first))
	h.seen(t)
	second := messages(user("帮我看看分页逻辑"), assistant("好了"), user("那按约定改写一下"), assistant("改好了"), user("那按约定改写一下"))
	run = h.begin(t, 2, second, nil)
	body := run.Inject([]byte(second))
	if got := gjson.GetBytes(body, "messages.4.content.0.text").Str; got != "<mem>\nrecall: 那按约定改写一下\n</mem>" {
		t.Fatalf("the second turn got no block of its own: %s", body)
	}
	records, _ := run.Report()
	if record := recordOf(t, records, plugin.HookPrepareTurn); record.Observe != observeQueued || record.Injection.ReplayedBlocks != 1 {
		t.Fatalf("record = %+v", record)
	}
}
