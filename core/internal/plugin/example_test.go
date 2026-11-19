package plugin

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// TestTheDocumentedExampleLoads keeps docs/plugins/team-rules loading and
// doing what the plugin API guide says it does.
func TestTheDocumentedExampleLoads(t *testing.T) {
	manager := NewManager(ManagerOptions{Dir: "../../../docs/plugins", Settings: settingsMap{"team-rules": {
		Enabled: true, Capabilities: []string{CapabilityInject, CapabilityViews},
		Config: json.RawMessage(`{"rules":["Answer in Chinese","  ","Prefer table-driven tests"]}`),
	}}})
	if _, err := os.Stat("../../../docs/plugins/team-rules/plugin.ts"); err != nil {
		t.Fatalf("the example is missing: %v", err)
	}
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	plugin := manager.Current().Plugin("team-rules")
	if plugin == nil || !plugin.Enabled() || plugin.Err != nil {
		t.Fatalf("team-rules = %+v", plugin)
	}
	result, _, err := plugin.Call(context.Background(), HookPrepareSession, nil, CallOptions{})
	want := `{"system":{"open":"<team-rules>","items":["- Answer in Chinese","- Prefer table-driven tests"],"close":"</team-rules>"}}`
	if err != nil || string(result) != want {
		t.Fatalf("prepareSession = %s, %v", result, err)
	}
	if result, _, err = plugin.Call(context.Background(), HookDescribeStatus, nil, CallOptions{}); err != nil || string(result) != `[{"type":"table","columns":["#","Rule"],"rows":[[1,"Answer in Chinese"],[2,"Prefer table-driven tests"]]}]` {
		t.Fatalf("describeStatus = %s, %v", result, err)
	}
}
