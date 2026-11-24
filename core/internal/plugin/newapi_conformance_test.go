package plugin

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testdata/newapi-conformance holds new-api's official plugins and the
// top-level meta fields of its plugin API v1 schema, as SOURCE says, and
// fixtures of fields no official plugin uses yet. Refresh it with
// scripts/refresh-newapi-conformance.sh to see where the gateway falls
// behind new-api's plugin contract.
const newAPIConformance = "testdata/newapi-conformance"

// legacyTaskMetaFields are meta fields of earlier task plugin APIs, which
// new-api names only to reject them, as the gateway does.
var legacyTaskMetaFields = []string{"channelType", "compatibleChannelTypes", "endpoints", "submitPaths", "actions"}

func TestNewAPIPluginsLoad(t *testing.T) {
	var sources []Source
	for _, dir := range []string{newAPIConformance + "/plugins", newAPIConformance + "/fixtures"} {
		found, err := Discover(nil, dir)
		if err != nil {
			t.Fatalf("Discover %s: %v", dir, err)
		}
		sources = append(sources, found...)
	}
	if len(sources) < 2 {
		t.Fatalf("found %d plugins in %s", len(sources), newAPIConformance)
	}
	for _, source := range sources {
		t.Run(source.Key, func(t *testing.T) {
			checked, err := Check(source)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if checked.Task == nil || checked.Task.Key != source.Key {
				t.Fatalf("Check = %+v, want the new-api plugin %s", checked, source.Key)
			}
		})
	}
}

func TestNewAPIScopeLoads(t *testing.T) {
	sources, err := Discover(nil, newAPIConformance+"/fixtures")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	index := slices.IndexFunc(sources, func(source Source) bool { return source.Key == "scoped-request" })
	if index < 0 {
		t.Fatal("the scoped-request fixture is missing")
	}
	checked, err := Check(sources[index])
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	scope := checked.Task.Scope
	if scope == nil || !slices.Equal(scope.Models, []string{"gpt-scoped"}) || !slices.Equal(scope.ChannelTypes, []int{1, 14}) {
		t.Fatalf("scope = %+v", scope)
	}
	for model, want := range map[string]bool{"gpt-scoped": true, "gpt-scoped@effort:high": true, "other": false} {
		if got := checked.Task.InScope(model); got != want {
			t.Fatalf("InScope(%q) = %v", model, got)
		}
	}
}

func TestNewAPIScopeNeedsARequestPart(t *testing.T) {
	for _, test := range []struct{ name, meta, message string }{
		{"no request part", `scope: { models: ["demo-1"] }`, "scope are for a plugin with a request processing part"},
	} {
		text := strings.Replace(taskSource("demo", ""), `fetchMode: "per_task"`, `fetchMode: "per_task", `+test.meta, 1)
		if _, err := Check(Source{Key: "demo", Text: text}); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("%s: err = %v, want %q", test.name, err, test.message)
		}
	}
	request := func(scope string) string {
		return `export const meta = { apiVersion: 1, key: "demo", name: "Demo", version: "1.0.0", author: { name: "Test" }, scope: ` + scope + ` };
export function ingress(ctx, req) { return { action: "continue" }; }
`
	}
	for scope, message := range map[string]string{
		`{}`:                            "at least one model or channel type",
		`{ models: [] }`:                "at least one model or channel type",
		`{ models: ["a", "a"] }`:        `lists "a" twice`,
		`{ models: [" a"] }`:            "without surrounding spaces",
		`{ channelTypes: [0] }`:         "positive channel types",
		`{ models: ["a"], groups: [] }`: `unknown field "groups"`,
	} {
		if _, err := Check(Source{Key: "demo", Text: request(scope)}); err == nil || !strings.Contains(err.Error(), message) {
			t.Fatalf("scope %s: err = %v, want %q", scope, err, message)
		}
	}
}

func TestNewAPIMetaFieldsAreAccepted(t *testing.T) {
	text, err := os.ReadFile(newAPIConformance + "/meta-fields.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields []string
	if err := json.Unmarshal(text, &fields); err != nil || len(fields) == 0 {
		t.Fatalf("meta-fields.json: %v", err)
	}
	accepted := map[string]bool{}
	kind := reflect.TypeFor[TaskMeta]()
	for index := range kind.NumField() {
		name, _, _ := strings.Cut(kind.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			accepted[name] = true
		}
	}
	for _, field := range fields {
		if !accepted[field] && !slices.Contains(legacyTaskMetaFields, field) {
			t.Errorf("new-api accepts meta.%s, which TaskMeta does not", field)
		}
	}
}
