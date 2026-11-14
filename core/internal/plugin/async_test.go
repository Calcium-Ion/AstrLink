package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pluginSourceV2 is an apiVersion 2 plugin of key asking for capabilities,
// followed by body.
func pluginSourceV2(key string, capabilities []string, body string) string {
	return strings.Replace(pluginSource(key, capabilities, body), "apiVersion: 1", "apiVersion: 2", 1)
}

func TestAsyncHooksAwaitHostFunctions(t *testing.T) {
	var requests atomic.Int32
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		requests.Add(1)
		select {
		case <-time.After(60 * time.Millisecond):
		case <-ctx.Done():
			return HTTPResponse{Error: FetchTimeout}
		}
		return HTTPResponse{Status: 200, Header: map[string]string{"content-type": "application/json"}, Body: []byte(`{"path":"` + request.URL + `"}`)}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork, CapabilityStorage}, `
export async function prepareTurn() {
  await astrlink.store.set("seen", { count: 1 });
  const stored = await astrlink.store.get("seen");
  const [a, b] = await Promise.all([
    astrlink.http.fetch({ method: "GET", url: "https://a.example/one" }),
    astrlink.http.fetch({ method: "GET", url: "https://a.example/two" }),
  ]);
  return { stored, paths: [a.json.path, b.json.path], keys: await astrlink.store.keys() };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork, CapabilityStorage)}, Services{Fetcher: fetcher, Store: &memoryStore{}}, Limits{})
	started := time.Now()
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	if elapsed := time.Since(started); elapsed > 110*time.Millisecond {
		t.Fatalf("two awaited requests took %s, want them in parallel", elapsed)
	}
	var result struct {
		Stored struct{ Count int } `json:"stored"`
		Paths  []string            `json:"paths"`
		Keys   []string            `json:"keys"`
	}
	if err := json.Unmarshal(outcome.result, &result); err != nil {
		t.Fatalf("result %s: %v", outcome.result, err)
	}
	if result.Stored.Count != 1 || strings.Join(result.Paths, ",") != "https://a.example/one,https://a.example/two" || strings.Join(result.Keys, ",") != "seen" {
		t.Fatalf("result = %s", outcome.result)
	}
	if requests.Load() != 2 || outcome.info.Network.Requests != 2 {
		t.Fatalf("requests = %d, counted %d", requests.Load(), outcome.info.Network.Requests)
	}
	// The script time leaves out the waits.
	if outcome.info.Script > 50*time.Millisecond {
		t.Fatalf("script time = %s, want the waits left out", outcome.info.Script)
	}
}

func TestFetchGlobalSpeaksTheWebAPI(t *testing.T) {
	var seen HTTPRequest
	fetcher := fetcherFunc(func(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		seen = request
		if strings.Contains(request.URL, "denied") {
			return HTTPResponse{Error: FetchDenied}
		}
		return HTTPResponse{Status: 201, Header: map[string]string{"content-type": "application/json", "x-trace": "abc"}, Body: []byte(`{"ok":true}`)}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const url = new URL("/v1/items?limit=2", "https://api.example");
  url.searchParams.set("q", "a b&c");
  const response = await fetch(url, {
    method: "POST",
    headers: { "X-Mode": "test" },
    body: new Uint8Array([1, 2, 3]),
    credential: "token",
  });
  let denied = "";
  try {
    await fetch("https://denied.example/");
  } catch (error) {
    denied = error.name + ":" + error.cause.code;
  }
  return {
    status: response.status, ok: response.ok, trace: response.headers.get("X-Trace"),
    body: await response.json(), used: response.bodyUsed, denied, href: url.href,
  };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	want := `{"status":201,"ok":true,"trace":"abc","body":{"ok":true},"used":true,"denied":"TypeError:denied","href":"https://api.example/v1/items?limit=2&q=a+b%26c"}`
	if string(outcome.result) != want {
		t.Fatalf("result = %s\nwant     %s", outcome.result, want)
	}
	if seen.URL != "https://denied.example/" {
		t.Fatalf("last request = %+v", seen)
	}
}

func TestFetchSendsTheBody(t *testing.T) {
	var seen HTTPRequest
	fetcher := fetcherFunc(func(_ context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		seen = request
		return HTTPResponse{Status: 204}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const response = await fetch("https://api.example/upload", { method: "PUT", body: new Uint8Array([0, 255, 7]), credential: "token", timeoutMs: 500 });
  return [response.status, await response.text()];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil || string(outcome.result) != `[204,""]` {
		t.Fatalf("call = %s, %v", outcome.result, outcome.err)
	}
	if seen.Method != "PUT" || string(seen.BodyBytes) != "\x00\xff\x07" || seen.Credential != "token" || seen.TimeoutMS != 500 {
		t.Fatalf("request = %+v", seen)
	}
}

func TestAsyncCallsKeepTheirBudgets(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export function prepareTurn() { return new Promise(() => {}); }
export async function prepareSession() { await new Promise((resolve) => setTimeout(resolve, 10000)); }
export async function observeTurn() { await null; for (;;) {} }
export async function describeStatus() { await null; throw new RangeError("no status"); }
export async function testConnection() {
  try { await fetch("https://api.example/"); } catch (error) { return error.cause.code; }
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		return HTTPResponse{Status: 200}
	})}, Limits{ScriptTime: 30 * time.Millisecond})
	plugin := loadedPlugin(t, manager, "demo")
	callError(t, callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{}), ReasonPluginError, "never settles")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	callError(t, callWithin(t, ctx, plugin, HookPrepareSession, nil, CallOptions{}), ReasonTimeout, "ran out of time")
	callError(t, callWithin(t, context.Background(), plugin, HookObserveTurn, nil, CallOptions{}), ReasonTimeout, "JavaScript")
	callError(t, callWithin(t, context.Background(), plugin, HookDescribeStatus, nil, CallOptions{}), ReasonPluginError, "RangeError: no status")
	// No network budget, no request.
	outcome := callWithin(t, context.Background(), plugin, HookTestConnection, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `"budget"` {
		t.Fatalf("testConnection = %s, %v", outcome.result, outcome.err)
	}
}

func TestTimersRunWithinTheCall(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
export async function prepareTurn() {
  const order = [];
  const cleared = setTimeout(() => order.push("cleared"), 5);
  clearTimeout(cleared);
  let ticks = 0;
  await new Promise((resolve) => {
    const interval = setInterval(() => {
      ticks++;
      if (ticks === 3) { clearInterval(interval); resolve(); }
    }, 2);
  });
  await new Promise((resolve) => setTimeout(() => { order.push("timeout"); resolve(); }, 1, "unused"));
  queueMicrotask(() => order.push("microtask"));
  await null;
  return { order, ticks };
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil || string(outcome.result) != `{"order":["timeout","microtask"],"ticks":3}` {
		t.Fatalf("call = %s, %v", outcome.result, outcome.err)
	}
}

func TestWebAPIs(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
const hex = (buffer) => [...new Uint8Array(buffer)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
export async function prepareTurn() {
  const url = new URL("HTTPS://User:Pw@Example.COM:443/a/../b?x=1#frag");
  const params = new URLSearchParams({ b: "2", a: "1 2" });
  params.append("a", "3");
  params.sort();
  const relative = new URL("../c?d=é", "https://example.com/a/b/");
  const digest = hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode("abc")));
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode("key"), { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]);
  const mac = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode("The quick brown fox jumps over the lazy dog"));
  const verified = await crypto.subtle.verify("HMAC", key, mac, new TextEncoder().encode("The quick brown fox jumps over the lazy dog"));
  const random = crypto.getRandomValues(new Uint8Array(16));
  const headers = new Headers([["Accept", "a"], ["accept", "b"]]);
  console.log("logged", { n: 1 }, [2]);
  console.error(new TypeError("broken"));
  return {
    href: url.href, origin: url.origin, host: url.host, path: url.pathname, user: url.username,
    params: params.toString(), all: params.getAll("a"), relative: relative.href,
    canParse: [URL.canParse("nope"), URL.canParse("mailto:a@b.c")],
    digest, mac: hex(mac), verified, randomLength: random.length,
    uuid: /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(crypto.randomUUID()),
    accept: headers.get("ACCEPT"), clock: typeof performance.now(),
    json: await Response.json({ a: 1 }).json(),
  };
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	var result map[string]any
	if err := json.Unmarshal(outcome.result, &result); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"href":         "https://User:Pw@example.com/b?x=1#frag",
		"origin":       "https://example.com",
		"host":         "example.com",
		"path":         "/b",
		"user":         "User",
		"params":       "a=1+2&a=3&b=2",
		"relative":     "https://example.com/a/c?d=%C3%A9",
		"digest":       "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"mac":          "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		"verified":     true,
		"uuid":         true,
		"accept":       "a, b",
		"clock":        "number",
		"randomLength": float64(16),
	}
	for key, value := range want {
		if result[key] != value {
			t.Errorf("%s = %#v, want %#v", key, result[key], value)
		}
	}
	if got, _ := json.Marshal(result["all"]); string(got) != `["1 2","3"]` {
		t.Errorf("all = %s", got)
	}
	if got, _ := json.Marshal(result["canParse"]); string(got) != `[false,true]` {
		t.Errorf("canParse = %s", got)
	}
	if got, _ := json.Marshal(result["json"]); string(got) != `{"a":1}` {
		t.Errorf("json = %s", got)
	}
	if len(outcome.info.Logs) != 2 || outcome.info.Logs[0].Message != `logged {"n":1} [2]` || !strings.HasPrefix(outcome.info.Logs[1].Message, "TypeError: broken") || outcome.info.Logs[1].Level != "warn" {
		t.Fatalf("logs = %+v", outcome.info.Logs)
	}
}

func TestApiVersion1StaysSynchronous(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"sync":  pluginSource("sync", nil, "export async function prepareTurn() { return 1; }"),
		"timer": pluginSource("timer", nil, "export function prepareTurn() { setTimeout(() => {}, 1); }"),
	}, settingsMap{"sync": enabled(), "timer": enabled()}, Services{}, Limits{})
	if plugin := manager.Current().Plugin("sync"); plugin.Loaded() || plugin.Err == nil || plugin.Err.Code != CodeAsync {
		t.Fatalf("sync plugin = %+v, want an async load error", plugin.Err)
	}
	callError(t, callWithin(t, context.Background(), loadedPlugin(t, manager, "timer"), HookPrepareTurn, nil, CallOptions{}), ReasonPluginError, "apiVersion 2")
}

func TestJournalKeepsLogsFailuresAndTimings(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", nil, `
export async function prepareTurn(input) {
  console.info("turn", input.n);
  if (input.n === 2) throw new Error("second turn");
  return input.n;
}
`),
	}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	for n := 1; n <= 2; n++ {
		callWithin(t, context.Background(), plugin, HookPrepareTurn, map[string]int{"n": n}, CallOptions{})
	}
	callWithin(t, context.Background(), plugin, HookPrepareTurn, map[string]int{"n": 3}, CallOptions{DryRun: true})
	entries, timings := plugin.Journal(0)
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Message != "turn 1" || entries[2].Level != JournalError || !strings.Contains(entries[2].Message, "second turn") || !strings.Contains(entries[2].Stack, "plugin.js:") || !entries[3].DryRun {
		t.Fatalf("entries = %+v", entries)
	}
	if len(timings) != 1 || timings[0].Hook != HookPrepareTurn || timings[0].Calls != 2 || timings[0].Failures != 1 {
		t.Fatalf("timings = %+v", timings)
	}
	later, _ := plugin.Journal(entries[1].Seq)
	if len(later) != 2 {
		t.Fatalf("entries after %d = %+v", entries[1].Seq, later)
	}
}

// streamFetcher is a Fetcher whose Stream hands responses back as they
// arrive.
type streamFetcher struct {
	fetcherFunc
	stream func(ctx context.Context, request *http.Request, credential string) (*http.Response, error)
}

func (fetcher streamFetcher) Stream(ctx context.Context, _ *Plugin, request *http.Request, credential string) (*http.Response, error) {
	return fetcher.stream(ctx, request, credential)
}

func TestFetchStreamsTheResponseBody(t *testing.T) {
	var seen *http.Request
	var credential string
	fetcher := streamFetcher{stream: func(ctx context.Context, request *http.Request, slot string) (*http.Response, error) {
		seen, credential = request, slot
		reader, writer := io.Pipe()
		go func() {
			_, _ = writer.Write([]byte("first "))
			select {
			case <-time.After(150 * time.Millisecond):
			case <-ctx.Done():
			}
			_, _ = writer.Write([]byte("second"))
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/plain"}}, Body: reader, ContentLength: -1}, nil
	}}
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const response = await fetch("https://api.example/stream", { headers: { "X-AstrLink-Trace": "1", "X-Mode": "a" }, credential: "token", timeoutMs: 2000 });
  const decoder = new TextDecoder();
  const chunks = [];
  for await (const chunk of response.body) chunks.push([decoder.decode(chunk), Date.now()]);
  return { text: chunks.map(([text]) => text).join(""), apart: chunks.at(-1)[1] - chunks[0][1] };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{ScriptTime: 50 * time.Millisecond})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: 5 * time.Second})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	var result struct {
		Text  string
		Apart int
	}
	if err := json.Unmarshal(outcome.result, &result); err != nil || result.Text != "first second" || result.Apart < 100 {
		t.Fatalf("result = %s, %v; want the first chunk read before the second came", outcome.result, err)
	}
	if credential != "token" || seen.Header.Get("X-Mode") != "a" || seen.Header.Get("X-AstrLink-Trace") != "" || seen.Header.Get("X-AstrLink-Fetch") != "" || seen.Header.Get("X-AstrLink-Credential") != "" {
		t.Fatalf("credential %q, headers %v", credential, seen.Header)
	}
	if outcome.info.Network.Requests != 1 || outcome.info.Network.Errors != 0 {
		t.Fatalf("network = %+v", outcome.info.Network)
	}
}

func TestFetchFollowsNoRedirect(t *testing.T) {
	var requests atomic.Int32
	fetcher := fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		requests.Add(1)
		return HTTPResponse{Status: 302, Header: map[string]string{"location": "https://elsewhere.example/"}}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
export async function prepareTurn() {
  const response = await fetch("https://api.example/moved");
  return [response.status, response.redirected, response.headers.get("location")];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	if outcome.err != nil || string(outcome.result) != `[302,false,"https://elsewhere.example/"]` || requests.Load() != 1 {
		t.Fatalf("call = %s, %v after %d requests", outcome.result, outcome.err, requests.Load())
	}
}

func TestACallLeavesNothingBehind(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		if strings.Contains(request.URL, "slow") {
			<-ctx.Done()
			return HTTPResponse{Error: FetchNetwork}
		}
		return HTTPResponse{Status: 200, Body: []byte(`{"ok":true}`)}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
let calls = 0;
export async function prepareTurn() {
  calls++;
  const slow = fetch("https://slow.example/").then(() => "slow", () => { calls += 1000; return "slow failed"; });
  const fast = fetch("https://fast.example/").then((response) => response.json()).then((body) => body.ok && "fast");
  setTimeout(() => { calls += 100; }, 20);
  setInterval(() => { calls += 100; }, 5);
  return [await Promise.race([slow, fast]), calls];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	for want := 1; want <= 3; want++ {
		outcome := callWithin(t, context.Background(), plugin, HookPrepareTurn, nil, CallOptions{NetworkBudget: 5 * time.Second})
		// The runtime goes on to the next call, and the request, the
		// timer and the interval of the last one never come back to it:
		// the request ended with the call, out of its reach.
		if outcome.err != nil || string(outcome.result) != `["fast",`+strconv.Itoa(want+1000*(want-1))+`]` {
			t.Fatalf("call %d = %s, %v", want, outcome.result, outcome.err)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func TestWaitsAreNotScriptTime(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, _ HTTPRequest) HTTPResponse {
		select {
		case <-time.After(120 * time.Millisecond):
		case <-ctx.Done():
			return HTTPResponse{Error: FetchNetwork}
		}
		return HTTPResponse{Status: 200, Body: []byte(`{"ok":true}`)}
	})
	fixture := newNodeFixture(t, `
import fs from "node:fs";
const spin = () => { for (;;) {} };
export async function prepareTurn(input) {
  switch (input?.mode) {
    case "afterFetch": await fetch("https://api.example/"); spin();
    case "afterJSON": await (await fetch("https://api.example/")).json(); spin();
    case "timer": await new Promise(() => setTimeout(spin, 1));
    case "fs": fs.writeFileSync("x.txt", "x"); await fs.promises.readFile("x.txt"); spin();
    case "sleep": await new Promise((resolve) => setTimeout(resolve, 150)); return "slept";
  }
  return (await (await fetch("https://api.example/")).json()).ok;
}
`, map[string]string{}, Services{Fetcher: fetcher}, Limits{ScriptTime: 40 * time.Millisecond, Concurrency: 1}, CapabilityNetwork)
	outcome := callWithin(t, context.Background(), fixture.plugin, HookPrepareTurn, nil, CallOptions{NetworkBudget: 5 * time.Second})
	if outcome.err != nil || string(outcome.result) != "true" || outcome.info.Script > 30*time.Millisecond {
		t.Fatalf("waiting call = %s, %v, script %s", outcome.result, outcome.err, outcome.info.Script)
	}
	for _, mode := range []string{"afterFetch", "afterJSON", "timer", "fs"} {
		outcome := callWithin(t, context.Background(), fixture.plugin, HookPrepareTurn, map[string]string{"mode": mode}, CallOptions{NetworkBudget: 5 * time.Second})
		callError(t, outcome, ReasonTimeout, "JavaScript")
	}
	// A call that waits gives its slot to another.
	started := time.Now()
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			outcome := callWithin(t, context.Background(), fixture.plugin, HookPrepareTurn, map[string]string{"mode": "sleep"}, CallOptions{})
			if outcome.err != nil {
				t.Errorf("sleep: %v", outcome.err)
			}
		})
	}
	group.Wait()
	if elapsed := time.Since(started); elapsed > 280*time.Millisecond {
		t.Fatalf("two calls that sleep 150 ms with one slot took %s", elapsed)
	}
}

func TestWebStreamsAndSignals(t *testing.T) {
	fetcher := fetcherFunc(func(ctx context.Context, _ *Plugin, request HTTPRequest) HTTPResponse {
		if strings.Contains(request.URL, "slow") {
			<-ctx.Done()
			return HTTPResponse{Error: FetchNetwork}
		}
		return HTTPResponse{Status: 200, Body: []byte("line one\nline two\n")}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
import { text } from "node:stream/consumers";
import { Readable } from "node:stream";
export async function prepareTurn() {
  const response = await fetch("https://api.example/");
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let lines = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    lines += value;
  }
  const written = [];
  const writer = new WritableStream({ write(chunk) { written.push(chunk); } }).getWriter();
  await writer.write("a");
  await writer.close();
  const upper = new TransformStream({ transform(chunk, controller) { controller.enqueue(chunk.toUpperCase()); } });
  const transformed = await new Response(new Response("abc").body.pipeThrough(new TextDecoderStream()).pipeThrough(upper).pipeThrough(new TextEncoderStream())).text();
  return {
    lines, written, transformed,
    fromWeb: await text(Readable.fromWeb(new Response("node").body)),
    toWeb: await new Response(Readable.toWeb(Readable.from(["web", " too"]))).text(),
    aborted: await fetch("https://api.example/", { signal: AbortSignal.abort() }).then(() => "no", (error) => error.name),
    timedOut: await fetch("https://slow.example/", { signal: AbortSignal.timeout(5) }).then(() => "no", (error) => error.name),
    host: new URL("https://例子.测试/").hostname,
    number: new Intl.NumberFormat("de-DE").format(1234567.891),
  };
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: time.Second})
	want := `{"lines":"line one\nline two\n","written":["a"],"transformed":"ABC","fromWeb":"node","toWeb":"web too","aborted":"AbortError","timedOut":"TimeoutError","host":"xn--fsqu00a.xn--0zwm56d","number":"1.234.567,891"}`
	if outcome.err != nil || string(outcome.result) != want {
		t.Fatalf("call = %s, %v\nwant          %s", outcome.result, outcome.err, want)
	}
}

func TestFetchBodiesKeepToTheLimit(t *testing.T) {
	var requests atomic.Int32
	fetcher := fetcherFunc(func(context.Context, *Plugin, HTTPRequest) HTTPResponse {
		requests.Add(1)
		return HTTPResponse{Status: 204}
	})
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityNetwork}, `
const large = new Uint8Array(1024 * 1024 + 1);
const failure = (error) => error.name + ": " + error.message + " (" + (error.cause?.message ?? "") + ")";
export async function prepareTurn() {
  const whole = await fetch("https://api.example/", { method: "POST", body: large }).then((r) => r.status, failure);
  const stream = new ReadableStream({ pull(controller) { controller.enqueue(new Uint8Array(512 * 1024)); } });
  const streamed = await fetch("https://api.example/", { method: "POST", body: stream, duplex: "half" }).then((r) => r.status, failure);
  const fits = await fetch("https://api.example/", { method: "POST", body: large.subarray(1) }).then((r) => r.status, failure);
  return [whole, streamed, fits];
}
`),
	}, settingsMap{"demo": enabled(CapabilityNetwork)}, Services{Fetcher: fetcher}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{NetworkBudget: 5 * time.Second})
	var result []any
	if outcome.err != nil || json.Unmarshal(outcome.result, &result) != nil || len(result) != 3 {
		t.Fatalf("call = %s, %v", outcome.result, outcome.err)
	}
	for _, failed := range result[:2] {
		if text, _ := failed.(string); !strings.HasPrefix(text, "TypeError: fetch failed") || !strings.Contains(text, "larger than 1 MiB") {
			t.Errorf("a body past 1 MiB = %v", failed)
		}
	}
	if result[2] != float64(204) || requests.Load() != 1 {
		t.Fatalf("a body of 1 MiB = %v after %d requests", result[2], requests.Load())
	}
}
