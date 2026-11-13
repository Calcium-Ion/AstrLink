package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/tidwall/gjson"
)

func extensionHost(t *testing.T, key, source string, settings plugin.Settings) *Host {
	t.Helper()
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{key + "/plugin.js": &fstest.MapFile{Data: []byte(source)}},
		Settings: settingsSource{key: settings},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if loaded := manager.Current().Plugin(key); !loaded.Loaded() {
		t.Fatalf("plugin did not load: %v", loaded.Err)
	}
	host := New(Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	return host
}

// redactingView stands for the privacy check: it replaces an e-mail address
// with a placeholder and puts it back.
func redactingView(_ context.Context, _ contract.ProtocolID, body []byte) (BodyView, error) {
	const original, placeholder = "alice@example.com", "<PRIVATE_EMAIL_1>"
	if !bytes.Contains(body, []byte(original)) {
		return BodyView{Body: body}, nil
	}
	return BodyView{
		Body:    bytes.ReplaceAll(body, []byte(original), []byte(placeholder)),
		Restore: func(edited []byte) []byte { return bytes.ReplaceAll(edited, []byte(placeholder), []byte(original)) },
	}, nil
}

func TestRawEditsSeeTheCheckedBodyAndKeepTheOriginals(t *testing.T) {
	source := `export const meta = { apiVersion: 2, key: "raw", name: "Raw", version: "1.0.0", capabilities: ["request.write.raw", "request.read"] };
export async function beforeForward(ctx) {
  const said = ctx.body.messages[0].content;
  return { body: { ...ctx.body, metadata: { saw: said } } };
}
`
	host := extensionHost(t, "raw", source, plugin.Settings{Enabled: true, Capabilities: []string{plugin.CapabilityRequestWriteRaw, plugin.CapabilityRequest}})
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"mail alice@example.com"}]}`)
	ext := host.Extend(ExtensionInput{Protocol: contract.ProtocolOpenAIChat, Model: "m", View: redactingView})
	edited := ext.BeforeForward(context.Background(), body)
	if got := gjson.GetBytes(edited, "messages.0.content").Str; got != "mail alice@example.com" {
		t.Fatalf("the message = %q", got)
	}
	// What the plugin saw had the placeholder; what goes on has the address.
	if got := gjson.GetBytes(edited, "metadata.saw").Str; got != "mail alice@example.com" {
		t.Fatalf("the plugin's field = %q", got)
	}
	// A plugin the user trusts sees the original, and a blocked body is
	// not seen at all.
	blocked := func(context.Context, contract.ProtocolID, []byte) (BodyView, error) {
		return BodyView{Blocked: true}, nil
	}
	ext = host.Extend(ExtensionInput{Protocol: contract.ProtocolOpenAIChat, Model: "m", View: blocked})
	if edited := ext.BeforeForward(context.Background(), body); !bytes.Equal(edited, body) {
		t.Fatalf("a blocked body was edited: %s", edited)
	}
	if records := ext.Records(); len(records) != 1 || records[0].SkippedReason != "privacy_blocked" {
		t.Fatalf("records = %+v", records)
	}
}

func TestTurnBlocksSettleOncePerTurn(t *testing.T) {
	source := `export const meta = { apiVersion: 2, key: "turns", name: "Turns", version: "1.0.0", capabilities: ["request.write"] };
let calls = 0;
export async function beforeForward() {
  calls++;
  return { turn: { prepend: "block " + calls } };
}
`
	host := extensionHost(t, "turns", source, plugin.Settings{Enabled: true, Capabilities: []string{plugin.CapabilityRequestWrite}})
	first := []byte(`{"model":"m","messages":[{"role":"user","content":"one"}]}`)
	forward := func(body []byte) string {
		ext := host.Extend(ExtensionInput{Protocol: contract.ProtocolOpenAIChat, Model: "m", Session: "s1", Turn: 1})
		return string(ext.BeforeForward(context.Background(), body))
	}
	settled := forward(first)
	if !strings.Contains(settled, "block 1") {
		t.Fatalf("first call = %s", settled)
	}
	// A tool loop's later call of the same turn puts the same block back,
	// whatever the plugin returns now.
	if again := forward(first); !strings.Contains(again, "block 1") || strings.Contains(again, "block 2") {
		t.Fatalf("the same turn again = %s", again)
	}
	// The next turn settles its own and keeps the first in its place.
	next := []byte(`{"model":"m","messages":[{"role":"user","content":"block 1\n\none"},{"role":"assistant","content":"hi"},{"role":"user","content":"two"}]}`)
	got := forward(next)
	if !strings.Contains(got, "block 3") {
		t.Fatalf("the next turn = %s", got)
	}
}

func TestSchedulesRunAtTheirIntervals(t *testing.T) {
	tick, unit := scheduleTick, scheduleUnit
	scheduleTick, scheduleUnit = 5*time.Millisecond, 30*time.Millisecond
	t.Cleanup(func() { scheduleTick, scheduleUnit = tick, unit })
	source := `export const meta = { apiVersion: 2, key: "cron", name: "Cron", version: "1.0.0", capabilities: ["schedule", "storage"], schedules: [{ name: "sync", everyMinutes: 1 }] };
export async function onSchedule(run) {
  const count = ((await astrlink.store.get(run.name)) ?? 0) + 1;
  await astrlink.store.set(run.name, count);
}
`
	store := &memoryValues{values: map[string]json.RawMessage{}}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Builtin:  fstest.MapFS{"cron/plugin.js": &fstest.MapFile{Data: []byte(source)}},
		Settings: settingsSource{"cron": {Enabled: true, Capabilities: []string{plugin.CapabilitySchedule, plugin.CapabilityStorage}}},
		Services: plugin.Services{Store: store},
	})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := New(Options{Plugins: manager, Logf: t.Logf})
	t.Cleanup(host.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { host.RunSchedules(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && store.count("sync") < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if got := store.count("sync"); got < 2 {
		t.Fatalf("the schedule ran %d times", got)
	}
}

func TestPluginHTTPAnswersThroughOnHttp(t *testing.T) {
	source := `export const meta = { apiVersion: 2, key: "web", name: "Web", version: "1.0.0", capabilities: ["http.serve"] };
export async function onHttp(request) {
  if (request.path === "/boom") throw new Error("boom");
  return { status: 201, headers: { "X-Plugin": "yes", "Set-Cookie": "a=b" }, json: { path: request.path, q: request.query.q, auth: request.headers.authorization ?? null, got: request.json } };
}
`
	host := extensionHost(t, "web", source, plugin.Settings{Enabled: true, Capabilities: []string{plugin.CapabilityHTTP}})
	response, found := host.ServePluginHTTP(context.Background(), "web", PluginHTTPRequest{Method: "POST", Path: "/hello", Query: map[string]string{"q": "1"}, Headers: map[string]string{}, JSON: json.RawMessage(`{"a":1}`)})
	if !found || response.Status != 201 || response.Headers["X-Plugin"] != "yes" || response.Headers["Set-Cookie"] != "" || response.Headers["Content-Type"] != "application/json" {
		t.Fatalf("response = %+v %v", response, found)
	}
	if string(response.Body) != `{"path":"/hello","q":"1","auth":null,"got":{"a":1}}` {
		t.Fatalf("body = %s", response.Body)
	}
	if response, found := host.ServePluginHTTP(context.Background(), "web", PluginHTTPRequest{Method: "GET", Path: "/boom"}); !found || response.Status != 502 {
		t.Fatalf("a failing call = %+v", response)
	}
	if _, found := host.ServePluginHTTP(context.Background(), "other", PluginHTTPRequest{Method: "GET", Path: "/"}); found {
		t.Fatal("a plugin that does not exist answered")
	}
	undeclared := extensionHost(t, "web", strings.Replace(source, `capabilities: ["http.serve"]`, `capabilities: []`, 1), plugin.Settings{Enabled: true})
	if _, found := undeclared.ServePluginHTTP(context.Background(), "web", PluginHTTPRequest{Method: "GET", Path: "/"}); found {
		t.Fatal("a plugin without http.serve answered")
	}
}

// memoryValues is astrlink.store in memory.
type memoryValues struct {
	mutex  sync.Mutex
	values map[string]json.RawMessage
}

func (store *memoryValues) Get(_ context.Context, _, key string) (json.RawMessage, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value, found := store.values[key]
	return value, found, nil
}

func (store *memoryValues) Set(_ context.Context, _, key string, value json.RawMessage) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.values[key] = value
	return nil
}

func (store *memoryValues) Delete(_ context.Context, _, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, key)
	return nil
}

func (store *memoryValues) Keys(context.Context, string, string) ([]string, error) { return nil, nil }

func (store *memoryValues) count(key string) int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var count int
	_ = json.Unmarshal(store.values[key], &count)
	return count
}
