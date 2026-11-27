package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
			if task == nil || task.Meta.Key != source.Key || checked.Meta.Key != source.Key || checked.Meta.Name != task.Meta.Name {
				t.Fatalf("Check = %+v, want the task plugin %s", checked, source.Key)
			}
			for _, hook := range []string{"buildSubmitRequest", "parseSubmitResponse", "parseTaskResult"} {
				if !containsString(checked.Hooks, hook) {
					t.Fatalf("hooks %v lack %s", checked.Hooks, hook)
				}
			}
			for _, claim := range task.Meta.Protocols {
				if claim.Name == "openai_video" && !containsString(checked.Hooks, "protocols.openai_video.render") {
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
	// The paths and keys the gateway keeps for itself, and the load error
	// codes it gives what pluginkit refuses.
	for _, test := range []struct {
		name, text, message string
		code                string
	}{
		{"route outside", strings.Replace(taskSource("demo", `export const native = { make(ctx) { return { kind: "submit", model: "demo-1" }; }, made(ctx, task) { return task; } };`), `fetchMode: "per_task"`, `fetchMode: "per_task", routes: [{ method: "POST", path: "/control/jobs", type: "submit", decode: "make", render: "made" }]`, 1), "/control", CodeInvalidMeta},
		{"reserved key", taskSource("v1", ""), "reserved", CodeInvalidMeta},
		{"async", taskSource("demo", `export async function extractUsage() {}`), "", CodeAsync},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Check(Source{File: "plugin.js", Text: test.text})
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || loadErr.Code != test.code || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Check = %v, want a %s error mentioning %q", err, test.code, test.message)
			}
		})
	}
	checked, err := Check(Source{File: "plugin.js", Text: taskSource("demo", "")})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if checked.Meta.APIVersion != 1 || checked.Meta.Name != "Demo" || checked.Task.AuthKind() != "api_key" || !containsString(checked.Hooks, "buildQueryRequest") {
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

func TestTaskFetchReadsAllowedHostsOnly(t *testing.T) {
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path+" "+request.Header.Get("X-Test")+" "+request.Header.Get("User-Agent"))
		switch request.URL.Path {
		case "/json":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"url":"https://cdn.example/video.mp4"}`))
		case "/redirect":
			http.Redirect(writer, request, "/json", http.StatusFound)
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
  try { utils.fetch({ url: "http://elsewhere.invalid/" }); out.host = "fetched"; } catch (error) { out.host = String(error.message); }
  return { status: "SUCCESS", reason: JSON.stringify(out) };
}`,
	}, nil, Services{}, DefaultLimits())
	fetch := &Fetch{
		Allowed: func(target *url.URL) bool { return target.Host == base.Host },
		Client:  upstream.Client(),
	}
	raw, info, err := loadedPlugin(t, manager, "demo").CallTask(context.Background(), "parseTaskResult", []any{map[string]any{"baseUrl": upstream.URL}, map[string]any{}}, CallOptions{Fetch: fetch})
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
	// A redirect reaches the plugin as it is.
	for name, value := range map[string]string{"json": "https://cdn.example/video.mp4", "type": "application/json", "redirect": "302 /json"} {
		if got[name] != value {
			t.Errorf("%s = %v, want %q", name, got[name], value)
		}
	}
	if host, _ := got["host"].(string); !strings.Contains(host, "elsewhere.invalid is not allowed") {
		t.Errorf("host = %v", got["host"])
	}
	if got["bytes"] != float64(5) {
		t.Errorf("bytes = %v", got["bytes"])
	}
	// Go's client would name itself in User-Agent.
	if len(requests) != 3 || requests[0] != "GET /json 1 " {
		t.Errorf("requests = %q", requests)
	}
	if info.Network.Requests != 4 || info.Network.Denied != 1 {
		t.Errorf("network stats = %+v", info.Network)
	}
	if _, err := callTask(t, manager, "demo", "parseTaskResult", CallOptions{}, map[string]any{"baseUrl": upstream.URL}, map[string]any{}); err == nil || !strings.Contains(err.Error(), "utils.fetch is not available in parseTaskResult") {
		t.Fatalf("without Fetch: %v", err)
	}
}
