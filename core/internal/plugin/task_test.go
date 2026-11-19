package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The official task plugins of new-api, the newest version of each, are in
// testdata/newapi (Apache-2.0, LICENSES/new-api-plugins-Apache-2.0.txt).
const officialTaskPlugins = "testdata/newapi"

func TestOfficialTaskPluginsLoad(t *testing.T) {
	sources, err := Discover(nil, officialTaskPlugins)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(sources) != 13 {
		t.Fatalf("found %d plugins, want the 13 official ones", len(sources))
	}
	for _, source := range sources {
		t.Run(source.Key, func(t *testing.T) {
			checked, err := Check(source)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			task := checked.Task
			if task == nil || task.Key != source.Key || checked.Meta.Key != source.Key || checked.Meta.Name != task.Name.In("en") {
				t.Fatalf("Check = %+v, want the task plugin %s", checked, source.Key)
			}
			for _, hook := range []string{"buildSubmitRequest", "parseSubmitResponse", "parseTaskResult"} {
				if !containsString(checked.Hooks, hook) {
					t.Fatalf("hooks %v lack %s", checked.Hooks, hook)
				}
			}
			for _, claim := range task.Protocols {
				if claim.Name == TaskProtocolVideo && !containsString(checked.Hooks, "protocols.openai_video.render") {
					t.Fatalf("hooks %v lack the openai_video renderer", checked.Hooks)
				}
			}
		})
	}
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// taskSource is a minimal task plugin with body appended.
func taskSource(key, body string) string {
	return `export const meta = { apiVersion: 1, key: "` + key + `", name: "Demo", version: "1.0.0", author: { name: "Test" }, models: ["demo-1"], fetchMode: "per_task", icon: "Demo.Color", description: { en: "Demo", zh: "演示" } };
export function buildSubmitRequest(ctx) { return { url: ctx.baseUrl + "/jobs", method: "POST", body: ctx.requestBody }; }
export function parseSubmitResponse(ctx, response) { return { taskId: response.body.id }; }
export function buildQueryRequest(ctx) { return { url: ctx.baseUrl + "/jobs/" + ctx.taskId }; }
export function parseTaskResult(ctx, body) { return { status: "SUCCESS" }; }
` + body
}

func TestTaskManifestIsChecked(t *testing.T) {
	for _, test := range []struct {
		name, text, message string
	}{
		{"unknown field", strings.Replace(taskSource("demo", ""), `fetchMode: "per_task"`, `fetchMode: "per_task", colour: "red"`, 1), `unknown field "colour"`},
		{"remote icon", strings.Replace(taskSource("demo", ""), `"Demo.Color"`, `"https://example.com/a.png"`, 1), "meta.icon"},
		{"batch hooks", strings.Replace(taskSource("demo", ""), `"per_task"`, `"batch"`, 1), "buildBatchQueryRequest"},
		{"route outside", strings.Replace(taskSource("demo", `export const native = { make(ctx) { return { kind: "submit", model: "demo-1" }; }, made(ctx, task) { return task; } };`), `fetchMode: "per_task"`, `fetchMode: "per_task", routes: [{ method: "POST", path: "/v1/tasks/jobs", type: "submit", decode: "make", render: "made" }]`, 1), "stay out of /v1/tasks"},
		{"reserved key", taskSource("v1", ""), "is a path of the gateway"},
		{"repeated route", strings.Replace(taskSource("demo", `export const native = { make(ctx) { return { kind: "submit", model: "demo-1" }; }, made(ctx, task) { return task; } };`), `fetchMode: "per_task"`, `fetchMode: "per_task", routes: [{ method: "POST", path: "/v1/jobs/:id", type: "submit", decode: "make", render: "made" }, { method: "POST", path: "/v1/jobs/:name", type: "submit", decode: "make", render: "made" }]`, 1), "repeats the route"},
		{"missing renderer", strings.Replace(taskSource("demo", `export const protocols = { openai_video: { decodeRequest(ctx) { return { kind: "submit", model: "demo-1" }; } } };`), `fetchMode: "per_task"`, `fetchMode: "per_task", protocols: ["openai_video"]`, 1), "decodeRequest and render"},
		{"unknown capability", strings.Replace(taskSource("demo", ""), `fetchMode: "per_task"`, `fetchMode: "per_task", requiredCapabilities: ["teleport@1"]`, 1), "teleport@1"},
		{"earlier api", taskSource("demo", `export function resolveRequest() {}`), "earlier task plugin API"},
		{"async", taskSource("demo", `export async function extractUsage() {}`), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Check(Source{File: "plugin.js", Text: test.text})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Check = %v, want an error mentioning %q", err, test.message)
			}
		})
	}
	checked, err := Check(Source{File: "plugin.js", Text: taskSource("demo", "")})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if checked.Task.Description.In("zh-CN") != "演示" || checked.Task.Description.In("fr") != "Demo" || checked.Task.Auth.Kind() != TaskAuthAPIKey {
		t.Fatalf("task = %+v", checked.Task)
	}
}

// callTask runs a task plugin's hook path of the plugin key in manager.
func callTask(t *testing.T, manager *Manager, key, path string, options CallOptions, args ...any) (json.RawMessage, error) {
	t.Helper()
	target := loadedPlugin(t, manager, key)
	if target.Task() == nil {
		t.Fatalf("%s is not a task plugin", key)
	}
	result, _, err := target.CallTask(context.Background(), path, args, options)
	return result, err
}

func TestTaskUtilsMatchNewAPI(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": taskSource("demo", `
export function extractUsage(ctx) {
  const claims = { sub: "a", n: 1 };
  return {
    hmac: utils.hmacSHA256("message", "secret"),
    jwt: utils.jwtSignHS256(claims, "secret"),
    b64: utils.base64("héllo"),
    b64url: utils.base64URL("??>"),
    decoded: utils.base64URLDecode(utils.base64URL("??>")),
    padded: utils.base64URLDecode("Pz8-"),
    has: utils.hasCapability("json-order@1"),
    hasNot: utils.hasCapability("teleport@1"),
    uuid: /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(utils.uuid()),
    now: utils.unixNow() > 1700000000,
    clone: (() => { const copy = utils.json.clone(ctx.value); copy.list.push(3); return copy; })(),
    original: ctx.value,
  };
}`),
	}, nil, Services{}, DefaultLimits())
	raw, err := callTask(t, manager, "demo", "extractUsage", CallOptions{}, map[string]any{"value": map[string]any{"list": []any{1, 2}}})
	if err != nil {
		t.Fatalf("extractUsage: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("message"))
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"n":1,"sub":"a"}`))
	signer := hmac.New(sha256.New, []byte("secret"))
	signer.Write([]byte(header + "." + payload))
	want := map[string]any{
		"hmac":    hex.EncodeToString(mac.Sum(nil)),
		"jwt":     header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(signer.Sum(nil)),
		"b64":     base64.StdEncoding.EncodeToString([]byte("héllo")),
		"b64url":  "Pz8-",
		"decoded": "??>",
		"padded":  "??>",
		"has":     true,
		"hasNot":  false,
		"uuid":    true,
		"now":     true,
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %v, want %v", name, got[name], value)
		}
	}
	if clone, _ := json.Marshal(got["clone"]); string(clone) != `{"list":[1,2,3]}` {
		t.Errorf("clone = %s", clone)
	}
	if original, _ := json.Marshal(got["original"]); string(original) != `{"list":[1,2]}` {
		t.Errorf("original = %s", original)
	}
}

func TestTaskJSONCloneRefusesWhatIsNotJSON(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": taskSource("demo", `
export function extractUsage(ctx) {
  const deep = {}; let at = deep;
  for (let i = 0; i < 40; i++) { at.next = {}; at = at.next; }
  const out = {};
  for (const [name, value] of [["fn", () => 1], ["deep", deep], ["undef", undefined], ["cycle", (() => { const a = {}; a.a = a; return a; })()]]) {
    try { utils.json.clone(value); out[name] = "cloned"; } catch (error) { out[name] = "refused"; }
  }
  return out;
}`),
	}, nil, Services{}, DefaultLimits())
	raw, err := callTask(t, manager, "demo", "extractUsage", CallOptions{}, map[string]any{})
	if err != nil {
		t.Fatalf("extractUsage: %v", err)
	}
	if string(raw) != `{"fn":"refused","deep":"refused","undef":"refused","cycle":"refused"}` {
		t.Fatalf("result = %s", raw)
	}
}

func TestTaskFetchReadsAllowedHostsOnly(t *testing.T) {
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path+" "+request.Header.Get("X-Test"))
		switch request.URL.Path {
		case "/json":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"url":"https://cdn.example/video.mp4"}`))
		case "/redirect":
			http.Redirect(writer, request, "/json", http.StatusFound)
		case "/large":
			_, _ = writer.Write([]byte(strings.Repeat("a", maxTaskFetchBody+1)))
		default:
			_, _ = writer.Write([]byte("plain"))
		}
	}))
	defer upstream.Close()
	base, _ := url.Parse(upstream.URL)
	manager := newTestManager(t, map[string]string{
		"demo": strings.Replace(taskSource("demo", ""), "export function parseTaskResult", "function unused", 1) + `
export function parseTaskResult(ctx, body) {
  const out = {};
  const json = utils.fetch({ url: ctx.baseUrl + "/json", headers: { "X-Test": "1" } });
  out.json = json.body.url;
  out.type = json.headers["Content-Type"];
  const redirect = utils.fetch({ url: ctx.baseUrl + "/redirect" });
  out.redirect = redirect.status + " " + redirect.headers.Location;
  const bytes = utils.fetch({ url: ctx.baseUrl + "/text", responseType: "bytes" });
  out.bytes = bytes.body.byteLength;
  for (const [name, request] of [
    ["host", { url: "http://elsewhere.invalid/" }],
    ["method", { url: ctx.baseUrl + "/json", method: "POST" }],
    ["header", { url: ctx.baseUrl + "/json", headers: { Host: "x" } }],
    ["member", { url: ctx.baseUrl + "/json", body: "x" }],
    ["count", { url: ctx.baseUrl + "/json" }],
  ]) {
    try { utils.fetch(request); out[name] = "fetched"; } catch (error) { out[name] = String(error.message); }
  }
  return { status: "SUCCESS", reason: JSON.stringify(out) };
}`,
	}, nil, Services{}, DefaultLimits())
	fetch := &TaskFetch{
		Allowed: func(target *url.URL) bool { return target.Host == base.Host },
		Client:  upstream.Client(),
	}
	raw, err := callTask(t, manager, "demo", "parseTaskResult", CallOptions{TaskFetch: fetch}, map[string]any{"baseUrl": upstream.URL}, map[string]any{})
	if err != nil {
		t.Fatalf("parseTaskResult: %v", err)
	}
	var result struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(raw, &result)
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Reason), &got); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	checks := map[string]string{
		"json":     "https://cdn.example/video.mp4",
		"type":     "application/json",
		"redirect": "302 /json",
		"host":     "utils.fetch: the host elsewhere.invalid is not allowed",
		"method":   "utils.fetch: the method must be GET or HEAD",
		"header":   `utils.fetch: the header "Host" is not one a plugin sets`,
		"member":   `utils.fetch: the request has the member "body"; it takes url, method, headers and responseType`,
	}
	for name, value := range checks {
		if got[name] != value {
			t.Errorf("%s = %v, want %q", name, got[name], value)
		}
	}
	if got["bytes"] != float64(5) {
		t.Errorf("bytes = %v", got["bytes"])
	}
	// The three fetches before count are allowed; the fourth fetch of the
	// call is the last.
	if got["count"] != "fetched" || len(requests) != 4 || requests[0] != "GET /json 1" {
		t.Errorf("count = %v, requests = %v", got["count"], requests)
	}
	if _, err := callTask(t, manager, "demo", "parseTaskResult", CallOptions{}, map[string]any{"baseUrl": upstream.URL}, map[string]any{}); err == nil || !strings.Contains(err.Error(), "utils.fetch is not available in parseTaskResult") {
		t.Fatalf("without TaskFetch: %v", err)
	}
}

func TestVolcSignV4IsStableAndCaseInsensitive(t *testing.T) {
	when := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	lower, err := volcSignV4(map[string]any{
		"method": "POST", "url": "https://visual.volcengineapi.com/?Version=2022-08-31&Action=CVSync2AsyncSubmitTask",
		"headers": map[string]any{"Content-Type": "application/json"}, "body": `{"a":1}`,
		"accessKey": "AK", "secretKey": "SK", "region": "cn-north-1", "service": "cv",
	}, when)
	if err != nil {
		t.Fatalf("volcSignV4: %v", err)
	}
	upper, err := volcSignV4(map[string]any{
		"Method": "POST", "URL": "https://visual.volcengineapi.com/?Version=2022-08-31&Action=CVSync2AsyncSubmitTask",
		"Headers": map[string]any{"Content-Type": "application/json"}, "Body": `{"a":1}`,
		"AccessKey": "AK", "SecretKey": "SK", "Region": "cn-north-1", "Service": "cv",
	}, when)
	if err != nil {
		t.Fatalf("volcSignV4: %v", err)
	}
	if lower["Authorization"] != upper["Authorization"] || lower["X-Date"] != "20261025T080000Z" {
		t.Fatalf("signatures differ: %v / %v", lower, upper)
	}
	payload := sha256.Sum256([]byte(`{"a":1}`))
	if lower["X-Content-Sha256"] != hex.EncodeToString(payload[:]) {
		t.Fatalf("X-Content-Sha256 = %s", lower["X-Content-Sha256"])
	}
	// The canonical request, signed as Volcengine documents it.
	canonical := strings.Join([]string{
		"POST", "/", "Action=CVSync2AsyncSubmitTask&Version=2022-08-31",
		"content-type:application/json\nhost:visual.volcengineapi.com\nx-content-sha256:" + hex.EncodeToString(payload[:]) + "\nx-date:20261025T080000Z\n",
		"content-type;host;x-content-sha256;x-date", hex.EncodeToString(payload[:]),
	}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	toSign := "HMAC-SHA256\n20261025T080000Z\n20261025/cn-north-1/cv/request\n" + hex.EncodeToString(digest[:])
	key := hmacSum([]byte("SK"), []byte("20261025"))
	key = hmacSum(key, []byte("cn-north-1"))
	key = hmacSum(key, []byte("cv"))
	key = hmacSum(key, []byte("request"))
	want := "HMAC-SHA256 Credential=AK/20261025/cn-north-1/cv/request, SignedHeaders=content-type;host;x-content-sha256;x-date, Signature=" + hex.EncodeToString(hmacSum(key, []byte(toSign)))
	if lower["Authorization"] != want {
		t.Fatalf("Authorization = %s\nwant %s", lower["Authorization"], want)
	}
}
