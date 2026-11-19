package sqlite

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestReadPluginOutcomesStepsOverEverythingElse(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       []string
		readable   bool
	}{
		{"empty column", ``, nil, true},
		{"no records", ` [ ] `, nil, true},
		{"in any order", `[{"status":"ok","plugin":"memory"},{"plugin":"rules","hook":"onRequest","status":"failed"}]`,
			[]string{"memory ok", "rules failed"}, true},
		// Keys inside strings, views and nested records are not the record's.
		{"nested and quoted", `[{"plugin":"memory","title":"say \"status\":\"failed\" \\","view":[{"status":"failed","rows":[["plugin"]]}],"status":"ok","network":{"requests":2}}]`,
			[]string{"memory ok"}, true},
		{"white space", "[\n  {\n    \"plugin\" : \"memory\" ,\n    \"status\" : \"skipped\"\n  }\n]\n",
			[]string{"memory skipped"}, true},
		{"not strings", `[{"plugin":7,"status":null,"elapsed_ms":-1.5e3,"truncated":true}]`, []string{" "}, true},
		{"truncated", `[{"plugin":"memory","status":"ok"},{"plugin":`, nil, false},
		{"open string", `[{"plugin":"memory\"]`, nil, false},
		{"an object", `{"plugin":"memory","status":"ok"}`, nil, false},
		{"a scalar in the array", `[1]`, nil, false},
		{"trailing text", `[{"plugin":"memory","status":"ok"}] x`, nil, false},
	} {
		outcomes, readable := readPluginOutcomes([]byte(test.text), nil)
		var got []string
		for _, outcome := range outcomes {
			got = append(got, string(outcome.plugin)+" "+string(outcome.status))
		}
		if readable != test.readable || (readable && !reflect.DeepEqual(got, test.want)) {
			t.Errorf("%s: got %q readable=%v, want %q readable=%v", test.name, got, readable, test.want, test.readable)
		}
	}
}

func TestCountPluginOutcomesCountsEachPluginOncePerRequest(t *testing.T) {
	groups := map[string]*storage.UsagePluginGroup{}
	count := func(text string) {
		outcomes, readable := readPluginOutcomes([]byte(text), nil)
		if !readable {
			t.Fatalf("unreadable %s", text)
		}
		countPluginOutcomes(groups, outcomes)
	}
	count(`[{"plugin":"memory","status":"skipped"},{"plugin":"memory","status":"ok"},{"plugin":"memory","status":"failed"},{"plugin":"rules","status":"ok"},{"plugin":"rules","status":"ok"}]`)
	count(`[{"plugin":"memory","status":"ok"},{"plugin":"Not A Key","status":"ok"},{"plugin":"quiet","status":"skipped"}]`)
	want := map[string]storage.UsagePluginGroup{
		"memory": {Plugin: "memory", Requests: 1, FailedRequests: 1},
		"rules":  {Plugin: "rules", Requests: 1},
	}
	got := map[string]storage.UsagePluginGroup{}
	for key, group := range groups {
		got[key] = *group
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups=%+v want %+v", got, want)
	}
}
