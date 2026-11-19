package plugin

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVersionTwoManifestsAreChecked(t *testing.T) {
	base := `export const meta = { apiVersion: 2, key: "demo", name: "Demo", version: "1.0.0", capabilities: %s%s };
export async function onRequest() { return { action: "allow" }; }
`
	for _, test := range []struct {
		name, capabilities, extra, want string
	}{
		{"unknown limit", `["request.gate"]`, `, limits: { prepareTurn: 100 }`, "not an extension point"},
		{"limit past the bound", `["request.gate"]`, `, limits: { onRequest: 6000 }`, "1 to 5000"},
		{"fail closed without the gate", `[]`, `, failClosed: true`, "needs the request.gate capability"},
		{"schedule without schedules", `["schedule"]`, ``, "needs meta.schedules"},
		{"schedules without the capability", `[]`, `, schedules: [{ name: "sync", everyMinutes: 5 }]`, "needs the schedule capability"},
		{"bad schedule name", `["schedule"]`, `, schedules: [{ name: "Sync!", everyMinutes: 5 }]`, "must match"},
		{"schedule too often", `["schedule"]`, `, schedules: [{ name: "sync", everyMinutes: 0 }]`, "everyMinutes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Check(Source{File: "plugin.js", Text: strings.Replace(strings.Replace(base, "%s", test.capabilities, 1), "%s", test.extra, 1)})
			var loadErr *LoadError
			if !errors.As(err, &loadErr) || !strings.Contains(loadErr.Message, test.want) {
				t.Fatalf("Check = %v, want %q", err, test.want)
			}
		})
	}
	checked, err := Check(Source{File: "plugin.js", Text: strings.Replace(strings.Replace(base, "%s", `["request.gate", "schedule"]`, 1), "%s", `, failClosed: true, limits: { onRequest: 1500 }, schedules: [{ name: "sync", everyMinutes: 15 }]`, 1)})
	if err != nil || !checked.Meta.FailClosed || checked.Meta.Limits["onRequest"] != 1500 || len(checked.Meta.Schedules) != 1 {
		t.Fatalf("Check = %+v, %v", checked, err)
	}
}

func TestVersionOneKeepsToItsCapabilitiesAndHooks(t *testing.T) {
	for _, source := range []string{
		`export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: ["routing"] };`,
		`export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [] };
export function onRequest() { return { action: "allow" }; }`,
		`export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", capabilities: [], limits: { onRequest: 100 } };`,
	} {
		if _, err := Check(Source{File: "plugin.js", Text: source}); err == nil || !strings.Contains(err.Error(), "apiVersion 2") {
			t.Fatalf("Check = %v, want apiVersion 2 to be required", err)
		}
	}
}

func TestLimitsFollowTheUserThenThePlugin(t *testing.T) {
	manager := newTestManager(t, map[string]string{
		"demo": pluginSourceV2("demo", []string{CapabilityRequestGate}, "export async function onRequest() {}"),
	}, settingsMap{"demo": enabled(CapabilityRequestGate)}, Services{}, Limits{})
	plugin := loadedPlugin(t, manager, "demo")
	if plugin.Limit(HookOnRequest) != time.Second || plugin.Limit(HookOnStreamEvent) != 50*time.Millisecond || plugin.Limit(HookPrepareTurn) != 0 {
		t.Fatalf("defaults = %v %v %v", plugin.Limit(HookOnRequest), plugin.Limit(HookOnStreamEvent), plugin.Limit(HookPrepareTurn))
	}
	plugin.Meta.Limits = map[string]int{HookOnRequest: 2000}
	if plugin.Limit(HookOnRequest) != 2*time.Second {
		t.Fatalf("the plugin's own limit = %v", plugin.Limit(HookOnRequest))
	}
	plugin.Settings.LimitsMs = map[string]int{HookOnRequest: 9000}
	if plugin.Limit(HookOnRequest) != 5*time.Second {
		t.Fatalf("the user's limit past the bound = %v", plugin.Limit(HookOnRequest))
	}
	if plugin.FailsClosed() {
		t.Fatal("a plugin that does not ask fails closed")
	}
}
