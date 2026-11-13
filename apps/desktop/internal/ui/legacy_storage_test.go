package ui

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTheLegacyStorageScriptIsEmptyWithoutItems(t *testing.T) {
	if script := legacyStorageScript(nil); script != "" {
		t.Fatalf("script %q", script)
	}
}

// runInPage runs script against a localStorage holding stored, as a page
// would at its start, and returns what it then holds.
func runInPage(t *testing.T, node, script string, stored map[string]string) map[string]string {
	t.Helper()
	initial, _ := json.Marshal(stored)
	program := `var data = ` + string(initial) + `;
var window = {localStorage: {
  getItem: function (k) { return Object.prototype.hasOwnProperty.call(data, k) ? data[k] : null; },
  setItem: function (k, v) { data[k] = String(v); }
}};
` + script + `
process.stdout.write(JSON.stringify(data));`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-")
	cmd.Stdin = strings.NewReader(program)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", node, err)
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return result
}

func TestTheLegacyStorageScriptFillsOnlyMissingItemsOnce(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := legacyStorageScript(map[string]string{
		"astrlink.theme":         "dark",
		"astrlink.onboarding.v1": "已完成  </script>",
	})

	got := runInPage(t, node, script, map[string]string{})
	if got["astrlink.theme"] != "dark" || got["astrlink.onboarding.v1"] != "已完成  </script>" || got[legacyStorageFlag] != "1" {
		t.Fatalf("empty storage: %v", got)
	}
	// What the page already chose stays.
	got = runInPage(t, node, script, map[string]string{"astrlink.theme": "light"})
	if got["astrlink.theme"] != "light" || got["astrlink.onboarding.v1"] == "" {
		t.Fatalf("chosen theme: %v", got)
	}
	// Once handed over, a removed item stays removed.
	got = runInPage(t, node, script, map[string]string{legacyStorageFlag: "1"})
	if len(got) != 1 {
		t.Fatalf("second run: %v", got)
	}
}
