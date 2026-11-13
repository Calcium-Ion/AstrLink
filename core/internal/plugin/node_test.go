package plugin

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// runNode runs body in the prepareTurn of an apiVersion 2 plugin and
// returns what it returns.
func runNode(t *testing.T, body string, settings ...func(*ManagerOptions)) json.RawMessage {
	t.Helper()
	manager := newTestManager(t, map[string]string{"demo": pluginSourceV2("demo", nil, body)}, settingsMap{"demo": enabled()}, Services{}, Limits{})
	outcome := callWithin(t, context.Background(), loadedPlugin(t, manager, "demo"), HookPrepareTurn, nil, CallOptions{})
	if outcome.err != nil {
		t.Fatalf("call: %v", outcome.err)
	}
	return outcome.result
}

func TestNodeModulesExportWhatTheyList(t *testing.T) {
	names := make([]string, 0, len(nodeModules))
	for name := range nodeModules {
		names = append(names, name)
	}
	slices.Sort(names)
	list, _ := json.Marshal(names)
	result := runNode(t, `
export async function prepareTurn() {
  const found = {};
  for (const name of `+string(list)+`) {
    const module = require("node:" + name);
    found[name] = Object.keys(module).filter((key) => key !== "default").sort();
  }
  return { system: JSON.stringify(found) };
}
`)
	var outer struct{ System string }
	if err := json.Unmarshal(result, &outer); err != nil {
		t.Fatal(err)
	}
	var found map[string][]string
	if err := json.Unmarshal([]byte(outer.System), &found); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		listed := strings.Fields(nodeModules[name].exports)
		slices.Sort(listed)
		if name == "constants" {
			// The flags of fs.constants beyond the portable ones differ
			// from system to system, and the list leaves them out.
			found[name] = slices.DeleteFunc(found[name], func(key string) bool { return !slices.Contains(listed, key) && strings.HasPrefix(key, "O_") })
		}
		if !slices.Equal(listed, found[name]) {
			t.Errorf("node:%s exports\n%q\nwant the list\n%q", name, strings.Join(found[name], " "), strings.Join(listed, " "))
		}
	}
}
