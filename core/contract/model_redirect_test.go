package contract

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidateModelRedirects(t *testing.T) {
	maxLength := strings.Repeat("模", 256)
	tooLong := strings.Repeat("m", 257)
	fullTable := func(size int) []ModelRedirect {
		redirects := make([]ModelRedirect, size)
		for i := range redirects {
			redirects[i] = ModelRedirect{From: fmt.Sprintf("client-%03d", i), To: "target", Enabled: true}
		}
		return redirects
	}
	tests := []struct {
		name      string
		redirects []ModelRedirect
		wantErr   string
	}{
		{name: "accepts nil table"},
		{name: "accepts empty table", redirects: []ModelRedirect{}},
		{name: "accepts disabled and enabled rules", redirects: []ModelRedirect{
			{From: "gpt-4o", To: "gpt-4.1", Enabled: true},
			{From: "claude-old", To: "claude-new", Enabled: false},
		}},
		{name: "accepts shared targets", redirects: []ModelRedirect{
			{From: "a", To: "shared", Enabled: true},
			{From: "b", To: "shared", Enabled: true},
		}},
		{name: "accepts 256 runes", redirects: []ModelRedirect{{From: maxLength, To: "target", Enabled: true}}},
		{name: "accepts interior spaces", redirects: []ModelRedirect{{From: "my model", To: "target", Enabled: true}}},
		{name: "accepts retired auto model as source", redirects: []ModelRedirect{{From: AstrLinkAutoModelID, To: "gpt-4.1", Enabled: true}}},
		{name: "accepts exactly the rule limit", redirects: fullTable(MaxModelRedirects)},
		{name: "rejects empty source", redirects: []ModelRedirect{{To: "target", Enabled: true}}, wantErr: "from must not be empty"},
		{name: "rejects empty target", redirects: []ModelRedirect{{From: "client", Enabled: true}}, wantErr: "to must not be empty"},
		{name: "rejects whitespace-only source", redirects: []ModelRedirect{{From: "   ", To: "target", Enabled: true}}, wantErr: "from must not start or end with whitespace"},
		{name: "rejects leading whitespace", redirects: []ModelRedirect{{From: " client", To: "target", Enabled: true}}, wantErr: "from must not start or end with whitespace"},
		{name: "rejects trailing whitespace", redirects: []ModelRedirect{{From: "client", To: "target\t", Enabled: true}}, wantErr: "to must not start or end with whitespace"},
		{name: "rejects too long source", redirects: []ModelRedirect{{From: tooLong, To: "target", Enabled: true}}, wantErr: "from must contain at most 256 characters"},
		{name: "rejects too long target", redirects: []ModelRedirect{{From: "client", To: tooLong, Enabled: true}}, wantErr: "to must contain at most 256 characters"},
		{name: "rejects control characters in source", redirects: []ModelRedirect{{From: "cli\x01ent", To: "target", Enabled: true}}, wantErr: "from must not contain control characters"},
		{name: "rejects newline in target", redirects: []ModelRedirect{{From: "client", To: "tar\nget", Enabled: true}}, wantErr: "to must not contain control characters"},
		{name: "rejects same model", redirects: []ModelRedirect{{From: "same", To: "same", Enabled: true}}, wantErr: "must target a different model"},
		{name: "rejects auto target", redirects: []ModelRedirect{{From: "client", To: AstrLinkAutoModelID, Enabled: true}}, wantErr: "must not target astrlink/auto"},
		{name: "rejects disabled auto target", redirects: []ModelRedirect{{From: "client", To: AstrLinkAutoModelID}}, wantErr: "must not target astrlink/auto"},
		{name: "rejects duplicate source", redirects: []ModelRedirect{
			{From: "client", To: "one", Enabled: true},
			{From: "client", To: "two", Enabled: false},
		}, wantErr: `source "client" is duplicated`},
		{name: "rejects chained target", redirects: []ModelRedirect{
			{From: "a", To: "b", Enabled: true},
			{From: "b", To: "c", Enabled: true},
		}, wantErr: `target "b" must not be another rule's source`},
		{name: "rejects chain through a disabled rule", redirects: []ModelRedirect{
			{From: "b", To: "c", Enabled: false},
			{From: "a", To: "b", Enabled: true},
		}, wantErr: `target "b" must not be another rule's source`},
		{name: "rejects a two-rule cycle", redirects: []ModelRedirect{
			{From: "a", To: "b", Enabled: true},
			{From: "b", To: "a", Enabled: true},
		}, wantErr: "must not be another rule's source"},
		{name: "rejects more than the rule limit", redirects: fullTable(MaxModelRedirects + 1), wantErr: "at most 200 rules"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateModelRedirects(test.redirects)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			settings := DefaultRoutingSettings()
			settings.ModelRedirects = test.redirects
			if settings.Validate() == nil {
				t.Fatal("routing settings accepted invalid redirects")
			}
		})
	}
}

func TestModelRedirectStrictJSON(t *testing.T) {
	var redirect ModelRedirect
	if err := json.Unmarshal([]byte(`{"from":"client","to":"target","enabled":false}`), &redirect); err != nil {
		t.Fatal(err)
	}
	if redirect != (ModelRedirect{From: "client", To: "target", Enabled: false}) {
		t.Fatalf("decoded %+v", redirect)
	}
	for _, document := range []string{
		`{"from":"client","to":"target","enabled":true,"id":"rule_1"}`,
		`{"from":"client","to":"target"}`,
		`{"from":"client","to":"target","enabled":null}`,
		`{"from":"client","to":"target","enabled":"true"}`,
		`{"to":"target","enabled":true}`,
		`{"from":"client","enabled":true}`,
		`{"from":null,"to":"target","enabled":true}`,
		`{"from":"same","to":"same","enabled":true}`,
		`{"from":"client","to":"astrlink/auto","enabled":true}`,
		`{"from":" client","to":"target","enabled":true}`,
		`["client","target",true]`,
		`null`,
	} {
		var decoded ModelRedirect
		if err := json.Unmarshal([]byte(document), &decoded); err == nil {
			t.Fatalf("accepted %s as %+v", document, decoded)
		}
	}
	var settings RoutingSettings
	if err := json.Unmarshal([]byte(`{"model_redirects":[{"from":"client","to":"target","enabled":true,"extra":1}]}`), &settings); err == nil {
		t.Fatal("routing settings accepted an unknown rule key")
	}
}

func TestResolveModelRedirect(t *testing.T) {
	redirects := []ModelRedirect{
		{From: "gpt-4o", To: "gpt-4.1", Enabled: true},
		{From: "claude-old", To: "claude-new", Enabled: false},
		{From: AstrLinkAutoModelID, To: "gpt-4.1-mini", Enabled: true},
	}
	if got, ok := ResolveModelRedirect(redirects, "gpt-4o"); !ok || got != redirects[0] {
		t.Fatalf("exact match = %+v %v", got, ok)
	}
	if got, ok := ResolveModelRedirect(redirects, AstrLinkAutoModelID); !ok || got.To != "gpt-4.1-mini" {
		t.Fatalf("auto source = %+v %v", got, ok)
	}
	for _, model := range []string{"claude-old", "GPT-4o", "gpt-4o ", "gpt-4", "gpt-4.1", ""} {
		if got, ok := ResolveModelRedirect(redirects, model); ok {
			t.Fatalf("%q resolved to %+v", model, got)
		}
	}
	if got, ok := ResolveModelRedirect([]ModelRedirect{{From: "", To: "target", Enabled: true}}, ""); ok {
		t.Fatalf("empty model resolved to %+v", got)
	}
	if _, ok := ResolveModelRedirect(nil, "gpt-4o"); ok {
		t.Fatal("nil table resolved")
	}
}

func TestRoutingSettingsModelRedirectsRoundTrip(t *testing.T) {
	defaults := DefaultRoutingSettings()
	if defaults.ModelRedirects == nil || len(defaults.ModelRedirects) != 0 {
		t.Fatalf("default redirects = %#v", defaults.ModelRedirects)
	}
	encoded, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"model_redirects":[]`) {
		t.Fatalf("defaults did not emit an empty list: %s", encoded)
	}
	var decoded RoutingSettings
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, defaults) {
		t.Fatalf("round trip = %+v, want %+v", decoded, defaults)
	}
	configured := DefaultRoutingSettings()
	configured.ModelRedirects = []ModelRedirect{
		{From: "gpt-4o", To: "gpt-4.1", Enabled: true},
		{From: AstrLinkAutoModelID, To: "gpt-4.1-mini", Enabled: false},
	}
	encoded, err = json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	decoded = DefaultRoutingSettings()
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, configured) || decoded.Validate() != nil {
		t.Fatalf("configured round trip = %+v", decoded)
	}
}

func TestRequestModelRedirectValidation(t *testing.T) {
	started := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	model := "gpt-4o"
	record := RequestRecord{
		ID: "request_redirect", StartedAt: started, Status: RequestStatusPending,
		InputProtocol: ProtocolOpenAIChat, RequestedModel: &model, Audit: NotCapturedAuditSummary(),
		ModelRedirect: &RequestModelRedirect{From: "gpt-4o", To: "gpt-4.1"},
		Events: []RequestEvent{{
			Kind: RequestEventModelRedirect, StartedAt: started, EndedAt: &started,
			Status: RequestStatusSucceeded, Summary: "gpt-4o → gpt-4.1",
		}},
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("valid record: %v", err)
	}
	session := RequestSession{
		ID: "session_redirect", Title: "redirect", StartedAt: started, LastStartedAt: started,
		TurnCount: 1, CallCount: 1, Status: SessionStatusPending, InputProtocol: ProtocolOpenAIChat,
		RequestedModel: &model, ModelRedirect: &RequestModelRedirect{From: "gpt-4o", To: "gpt-4.1"},
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("valid session: %v", err)
	}
	for _, invalid := range []RequestModelRedirect{
		{To: "gpt-4.1"},
		{From: "gpt-4o"},
		{From: "gpt-4o", To: "gpt-4o"},
		{From: strings.Repeat("m", 257), To: "gpt-4.1"},
		{From: "gpt-4o", To: "gpt\x00-4.1"},
	} {
		record := record
		record.ModelRedirect = &invalid
		if record.Validate() == nil {
			t.Fatalf("record accepted model_redirect %+v", invalid)
		}
		session := session
		session.ModelRedirect = &invalid
		if session.Validate() == nil {
			t.Fatalf("session accepted model_redirect %+v", invalid)
		}
	}
	encoded, err := json.Marshal(RequestRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "model_redirect") {
		t.Fatalf("empty record emitted model_redirect: %s", encoded)
	}
}
