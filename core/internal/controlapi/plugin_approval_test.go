package controlapi

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const gatePlugin = `export const meta = {
  apiVersion: 2, key: "gate", name: "Gate", version: "1.0.0",
  capabilities: ["request.gate", "storage"],
  failClosed: true,
  limits: { onRequest: 2000 },
};
export async function onRequest() { return { action: "allow" }; }
export async function onStreamEvent(ctx, event, state) { return { events: [event], state }; }
`

func TestChangesThatTrustAPluginMoreNeedConfirmation(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	writePlugin(t, directory, "gate", "plugin.js", gatePlugin)
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := PluginsPath + "/gate"
	patch := func(body string, approved bool) *contract.PluginDetail {
		t.Helper()
		send := controlRequest
		if approved {
			send = approvedRequest
		}
		response := send(t, handler, http.MethodPatch, path, "application/json", body, "")
		switch response.Code {
		case http.StatusOK:
			detail := decodeBody[contract.PluginDetail](t, response)
			return &detail
		case http.StatusConflict:
			return nil
		}
		t.Fatalf("PATCH %s = %d %s", body, response.Code, response.Body.String())
		return nil
	}
	confirmation := func(body string) contract.PluginApproval {
		t.Helper()
		response := controlRequest(t, handler, http.MethodPatch, path, "application/json", body, "")
		envelope := decodeBody[approvalEnvelope](t, response)
		if response.Code != http.StatusConflict || envelope.Error.Code != "approval_required" {
			t.Fatalf("PATCH %s = %d %s, want a confirmation", body, response.Code, response.Body.String())
		}
		return envelope.Approval
	}

	// Plugins are trusted code: enabling one gives it what it declares,
	// which its install review listed, without a confirmation.
	if detail := patch(`{"enabled":true}`, false); detail == nil || !detail.Enabled || !slices.Equal(detail.GrantedCapabilities, []string{"request.gate", "storage"}) {
		t.Fatalf("enable = %+v", detail)
	}
	if approval := confirmation(`{"fail_closed":true}`); !approval.FailClosed || approval.Name != "Gate" {
		t.Fatalf("approval = %+v", approval)
	}
	if detail := patch(`{"fail_closed":true}`, true); detail == nil || !detail.FailClosed || !detail.FailClosedRequested {
		t.Fatalf("fail closed = %+v", detail)
	}

	// What takes nothing away or grants nothing goes through.
	detail := patch(`{"hook_limits_ms":{"onRequest":3000,"onStreamEvent":200}}`, false)
	if detail == nil {
		t.Fatal("setting time limits asked for a confirmation")
	}
	limits := map[string]contract.PluginHookLimitSetting{}
	for _, limit := range detail.Limits {
		limits[limit.Hook] = limit
	}
	if limits["onRequest"] != (contract.PluginHookLimitSetting{Hook: "onRequest", DefaultMs: 1000, MaxMs: 5000, PluginMs: 2000, Ms: 3000, EffectiveMs: 3000}) ||
		limits["onStreamEvent"].EffectiveMs != 200 || len(detail.Limits) != 2 {
		t.Fatalf("limits = %+v", detail.Limits)
	}
	if loaded := manager.Current().Plugin("gate"); loaded.Limit("onRequest").Milliseconds() != 3000 || !loaded.FailsClosed() {
		t.Fatalf("the plugin waits %v, fails closed %v", loaded.Limit("onRequest"), loaded.FailsClosed())
	}
	if detail := patch(`{"hook_limits_ms":{"onRequest":0}}`, false); detail.Limits[0].EffectiveMs != 2000 {
		t.Fatalf("limits after the reset = %+v", detail.Limits)
	}
	for _, body := range []string{`{"hook_limits_ms":{"onRequest":5001}}`, `{"hook_limits_ms":{"prepareTurn":100}}`} {
		if response := controlRequest(t, handler, http.MethodPatch, path, "application/json", body, ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH %s = %d", body, response.Code)
		}
	}
	if detail := patch(`{"enabled":false}`, false); detail == nil || detail.Enabled {
		t.Fatalf("disable = %+v", detail)
	}
	// The demo plugin asks for nothing of the sort.
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/demo", "application/json", `{"fail_closed":true}`, ""); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("fail closed for a plugin that does not ask = %d", response.Code)
	}
}

func TestPluginOrderAndTheWebConsoleSwitch(t *testing.T) {
	handler := newPluginHostFixture(t).handler
	response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"order":["loader","demo"]}`, "")
	if settings := decodeBody[contract.PluginHostSettings](t, response); response.Code != http.StatusOK || !slices.Equal(settings.Order, []string{"loader", "demo"}) {
		t.Fatalf("order = %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"order":["demo","demo"]}`, `{"order":["Not A Key"]}`} {
		if response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", body, ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PATCH %s = %d", body, response.Code)
		}
	}
	if response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"web_ui":true}`, ""); response.Code != http.StatusConflict {
		t.Fatalf("web_ui without confirmation = %d", response.Code)
	}
	response = approvedRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"web_ui":true}`, "")
	if settings := decodeBody[contract.PluginHostSettings](t, response); response.Code != http.StatusOK || !settings.WebUI || !slices.Equal(settings.Order, []string{"loader", "demo"}) {
		t.Fatalf("web_ui = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPatch, PluginHostPath, "application/json", `{"web_ui":false}`, ""); response.Code != http.StatusOK {
		t.Fatalf("turning web_ui off = %d", response.Code)
	}
}
