package clients

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
)

func TestClaudeSettingsKeepOtherKeysOrderAndIndent(t *testing.T) {
	existing := "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n"
	plan := planWrite(t, Claude, texts(existing), connection(models("model", "main-route", "fableModel", "fable-route")), nil)
	sameStrings(t, plan.Conflicts)
	want := "{\n    \"theme\": \"dark\",\n    \"env\": {\n        \"EDITOR\": \"vim\",\n        \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:18317\",\n        \"ANTHROPIC_AUTH_TOKEN\": \"astr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA0\",\n        \"ANTHROPIC_MODEL\": \"main-route\",\n        \"ANTHROPIC_DEFAULT_FABLE_MODEL\": \"fable-route\"\n    },\n    \"permissions\": {\"allow\": []}\n}\n"
	if plan.Contents[0] != want {
		t.Fatalf("%q", plan.Contents[0])
	}
	if plan.Record.CreatedFile || plan.Record.BaseURL != origin {
		t.Fatalf("%+v", plan.Record)
	}
	sameStrings(t, recordKeys(plan.Record), "env.ANTHROPIC_AUTH_TOKEN", "env.ANTHROPIC_BASE_URL", "env.ANTHROPIC_DEFAULT_FABLE_MODEL", "env.ANTHROPIC_MODEL")
	sameRemovals(t, planRemove(t, Claude, texts(plan.Contents[0]), plan.Record), write(existing))
}

func TestClaudeSettingsAreCreatedAndRemovedWhole(t *testing.T) {
	for _, existing := range []string{missing, ""} {
		plan := planWrite(t, Claude, texts(existing), connection(nil), nil)
		sameJSON(t, plan.Contents[0], `{"env": {"ANTHROPIC_BASE_URL": "`+origin+`", "ANTHROPIC_AUTH_TOKEN": "`+token+`"}}`)
		if !strings.HasSuffix(plan.Contents[0], "}\n") || plan.Record.CreatedFile != (existing == missing) {
			t.Fatalf("%q %+v", plan.Contents[0], plan.Record)
		}
		removal := planRemove(t, Claude, texts(plan.Contents[0]), plan.Record)
		if existing == missing {
			sameRemovals(t, removal, deleted)
		} else {
			sameRemovals(t, removal, write("{}\n"))
		}
	}
}

func TestClaudeConflictsAreNamedAndRemovedOnlyWhenReplacing(t *testing.T) {
	existing := `{"apiKeyHelper": "secret-helper", "env": {"ANTHROPIC_API_KEY": "sk-secret", "ANTHROPIC_BASE_URL": "https://other.example", "CLAUDE_CODE_USE_BEDROCK": "1", "ANTHROPIC_DEFAULT_OPUS_MODEL": "old-opus", "HTTP_PROXY": "http://proxy"}}`
	plan := planWrite(t, Claude, texts(existing), connection(nil), nil)
	sameStrings(t, plan.Conflicts, "env.ANTHROPIC_API_KEY", "env.ANTHROPIC_BASE_URL", "env.CLAUDE_CODE_USE_BEDROCK", "env.ANTHROPIC_DEFAULT_OPUS_MODEL", "apiKeyHelper")
	sameJSON(t, plan.Contents[0], `{"env": {"ANTHROPIC_BASE_URL": "`+origin+`", "HTTP_PROXY": "http://proxy", "ANTHROPIC_AUTH_TOKEN": "`+token+`"}}`)
	for _, secret := range []string{"sk-secret", "secret-helper", "other.example", token} {
		if strings.Contains(strings.Join(plan.Conflicts, ","), secret) {
			t.Fatal(secret)
		}
	}
	// The record keeps what was replaced, and never the token.
	backup := map[string]string{
		"apiKeyHelper":                     "secret-helper",
		"env.ANTHROPIC_API_KEY":            "sk-secret",
		"env.ANTHROPIC_BASE_URL":           "https://other.example",
		"env.ANTHROPIC_DEFAULT_OPUS_MODEL": "old-opus",
		"env.CLAUDE_CODE_USE_BEDROCK":      "1",
	}
	if !reflect.DeepEqual(plan.Record.Backup, backup) {
		t.Fatalf("%v", plan.Record.Backup)
	}
	if data, _ := json.Marshal(plan.Record); strings.Contains(string(data), token) {
		t.Fatal("the record holds the token")
	}
	// Removing AstrLink's config puts every replaced value back.
	removals, restored, err := cc.PlanRestore(Claude, texts(plan.Contents[0]), plan.Record)
	if err != nil || restored != 5 || len(removals) != 1 || removals[0].Kind != cc.RemovalWrite {
		t.Fatalf("%v %d %v", removals, restored, err)
	}
	sameJSON(t, removals[0].Contents, existing)
}

func TestRewritingReplacesOurOwnValuesWithoutAsking(t *testing.T) {
	first := planWrite(t, Claude, texts(missing), connection(models("opusModel", "opus-route")), nil)
	next := connection(nil)
	next.TokenID, next.Token, next.Origin = "token_02", otherToken, "http://127.0.0.1:9000"
	second := planWrite(t, Claude, texts(first.Contents[0]), next, first.Record)
	sameStrings(t, second.Conflicts)
	sameJSON(t, second.Contents[0], `{"env": {"ANTHROPIC_BASE_URL": "http://127.0.0.1:9000", "ANTHROPIC_AUTH_TOKEN": "`+otherToken+`"}}`)
	if !second.Record.CreatedFile || second.Record.TokenID != "token_02" {
		t.Fatalf("%+v", second.Record)
	}
}

func TestRemovalKeepsKeysChangedOrAddedAfterWriting(t *testing.T) {
	plan := planWrite(t, Claude, texts(missing), connection(models("model", "main-route")), nil)
	edited := strings.NewReplacer("main-route", "user-route", `"env": {`, "\"env\": {\n    \"EDITOR\": \"vim\",").Replace(plan.Contents[0])
	removals := planRemove(t, Claude, texts(edited), plan.Record)
	if len(removals) != 1 || removals[0].Kind != cc.RemovalWrite {
		t.Fatalf("%v", removals)
	}
	sameJSON(t, removals[0].Contents, `{"env": {"EDITOR": "vim", "ANTHROPIC_MODEL": "user-route"}}`)
}

func TestInvalidClaudeSettingsAreRejected(t *testing.T) {
	for _, existing := range []string{"{broken", "{/*c*/}", "[]", `{"env": "x"}`, `{"a": 1,}`} {
		_, err := cc.PlanWrite(Claude, texts(existing), connection(nil), cc.Catalog{}, nil)
		if err == nil || err.File != 0 || strings.Contains(err.Message, token) {
			t.Fatalf("%q: %+v", existing, err)
		}
		if state := inspect(Claude, texts(existing), nil, origin); state != cc.Invalid {
			t.Fatalf("%q: %s", existing, state)
		}
	}
}

func TestAChangedConnectionIsModifiedAndNeverSynced(t *testing.T) {
	plan := planWrite(t, Claude, texts(missing), connection(nil), nil)
	written := texts(plan.Contents[0])
	edited := texts(strings.ReplaceAll(plan.Contents[0], token, otherToken))
	for _, tc := range []struct {
		existing []*string
		record   *cc.Record
		origin   string
		want     cc.State
	}{
		{written, plan.Record, origin, cc.Configured},
		{written, plan.Record, "http://127.0.0.1:9000", cc.Outdated},
		{edited, plan.Record, "http://127.0.0.1:9000", cc.Modified},
		{texts(missing), plan.Record, origin, cc.Modified},
		{texts("{}"), nil, origin, cc.NotConfigured},
	} {
		if got := inspect(Claude, tc.existing, tc.record, tc.origin); got != tc.want {
			t.Errorf("%s, want %s", got, tc.want)
		}
	}
	if contents, _ := planSync(t, Claude, edited, plan.Record, "http://127.0.0.1:9000"); contents != nil {
		t.Fatal("synced a modified config")
	}
}

func TestASyncedClaudeConfigChangesOnlyTheBaseURL(t *testing.T) {
	plan := planWrite(t, Claude, texts(missing), connection(models("model", "main-route")), nil)
	synced, record := planSync(t, Claude, texts(plan.Contents[0]), plan.Record, "http://127.0.0.1:9000")
	if len(synced) != 1 || synced[0] != strings.ReplaceAll(plan.Contents[0], "http://127.0.0.1:18317", "http://127.0.0.1:9000") {
		t.Fatalf("%q", synced)
	}
	if record.Keys["env.ANTHROPIC_AUTH_TOKEN"] != cc.SHA256Hex(token) {
		t.Fatal(record.Keys)
	}
	if contents, _ := planSync(t, Claude, texts(synced[0]), record, "http://127.0.0.1:9000"); contents != nil {
		t.Fatal("synced twice")
	}
}
