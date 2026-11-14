package controlapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const panelPlugin = `export const meta = {
  apiVersion: 2, key: "panel", name: "Panel", version: "1.0.0",
  capabilities: ["ui.code", "storage"],
};
export async function onUiCall(call) {
  if (call.name === "fail") throw new Error("no such call");
  await astrlink.store.set("seen", call.input);
  return { echoed: call.input, language: call.language, seen: await astrlink.store.get("seen") };
}
`

func TestAPluginsInterfaceLoadsOnlyOnceEnabledAndCallsItsBackend(t *testing.T) {
	fixture := buildPluginHostFixture(t, map[string]string{"panel": panelPlugin})
	handler := fixture.handler
	client := "(() => {\n  console.log(\"hello\");\n})();\n//# sourceMappingURL=client.js.map\n"
	for name, body := range map[string]string{"client.js": client, "client.js.map": `{"version":3,"mappings":""}`} {
		if err := os.WriteFile(filepath.Join(fixture.directory, "panel", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	summary := func() contract.PluginSummary {
		t.Helper()
		response := controlRequest(t, handler, http.MethodGet, PluginsPath, "", "", "")
		list := decodeBody[struct {
			Plugins []contract.PluginSummary `json:"plugins"`
		}](t, response)
		for _, item := range list.Plugins {
			if item.Key == "panel" {
				return item
			}
		}
		t.Fatal("panel is not listed")
		return contract.PluginSummary{}
	}
	code := PluginsPath + "/panel/client.js"
	if response := controlRequest(t, handler, http.MethodGet, code, "", "", ""); response.Code != http.StatusNotFound || summary().UIVersion != "" {
		t.Fatalf("the code of a disabled plugin = %d", response.Code)
	}
	// Enabled with its interface turned off, the interface stays out.
	if response := controlRequest(t, handler, http.MethodPatch, PluginsPath+"/panel", "application/json", `{"enabled":true,"ui_enabled":false}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodGet, code, "", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("the code with the interface off = %d", response.Code)
	}
	if response := controlRequest(t, handler, http.MethodPatch, PluginsPath+"/panel", "application/json", `{"ui_enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("turn the interface on = %d %s", response.Code, response.Body.String())
	}
	version := summary().UIVersion
	if version == "" {
		t.Fatal("the summary says nothing loads")
	}
	response := controlRequest(t, handler, http.MethodGet, code, "", "", "")
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		!strings.HasPrefix(body, `globalThis.__astrlinkPlugins&&globalThis.__astrlinkPlugins.define("panel",function(__astrlinkClient){(() => {`) ||
		!strings.HasSuffix(body, "\n});\n//# sourceMappingURL=client.js.map\n") || strings.Count(body, "sourceMappingURL") != 1 {
		t.Fatalf("client.js = %d %q", response.Code, body)
	}
	if response := controlRequest(t, handler, http.MethodGet, code+".map", "", "", ""); response.Code != http.StatusOK || response.Body.String() != `{"version":3,"mappings":""}` {
		t.Fatalf("client.js.map = %d %s", response.Code, response.Body.String())
	}

	call := PluginsPath + "/panel/ui-call"
	response = controlRequest(t, handler, http.MethodPost, call, "application/json", `{"name":"echo","input":{"x":1}}`, "")
	result := decodeBody[contract.PluginUICallResult](t, response)
	if response.Code != http.StatusOK || string(result.Result) != `{"echoed":{"x":1},"language":"en","seen":{"x":1}}` {
		t.Fatalf("ui-call = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPost, call, "application/json", `{"name":"fail"}`, ""); response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "no such call") {
		t.Fatalf("a failing call = %d %s", response.Code, response.Body.String())
	}
	if response := observerRequest(handler, http.MethodPost, call, `{"name":"echo"}`); response.Code != http.StatusForbidden {
		t.Fatalf("an observer's call = %d", response.Code)
	}

	// Turned off, the interface stays out again.
	if response := controlRequest(t, handler, http.MethodPatch, PluginsPath+"/panel", "application/json", `{"ui_enabled":false}`, ""); response.Code != http.StatusOK {
		t.Fatalf("turn the interface off = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodGet, code, "", "", ""); response.Code != http.StatusNotFound || summary().UIVersion != "" {
		t.Fatalf("the code of an interface turned off = %d", response.Code)
	}
	if response := controlRequest(t, handler, http.MethodPost, call, "application/json", `{"name":"echo"}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("a call of an interface turned off = %d", response.Code)
	}
}
