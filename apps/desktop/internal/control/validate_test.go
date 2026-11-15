package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// v decodes a JSON literal the way IPC arguments arrive.
func v(t *testing.T, literal string) any {
	t.Helper()
	value, err := decodeValue([]byte(literal))
	if err != nil {
		t.Fatalf("decode %s: %v", literal, err)
	}
	return value
}

func repeatJSON(s string, n int) string {
	return `"` + strings.Repeat(s, n) + `"`
}

func TestServiceRiskEventsPathBoundsTheLimit(t *testing.T) {
	limit := func(n uint32) *uint32 { return &n }
	for _, tc := range []struct {
		limit *uint32
		want  string
	}{
		{nil, "/control/v1/services/service_claude/risk-events"},
		{limit(1), "/control/v1/services/service_claude/risk-events?limit=1"},
		{limit(50), "/control/v1/services/service_claude/risk-events?limit=50"},
	} {
		got, err := serviceRiskEventsPath("service_claude", tc.limit)
		if err != nil || got != tc.want {
			t.Errorf("limit %v = %q, %v", tc.limit, got, err)
		}
	}
	for _, n := range []uint32{0, 51} {
		if _, err := serviceRiskEventsPath("service_claude", limit(n)); err == nil {
			t.Errorf("limit %d accepted", n)
		}
	}
}

func TestPackageIDsAndStrategiesAreCheckedBeforePatching(t *testing.T) {
	for _, id := range []string{"service:service_a", "subscription:claude:max_20x", "subscription:codex:unknown"} {
		if err := validatePackageID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"service:bad/path", "subscription:claude", "subscription::pro", "subscription:claude:\npro", "other:claude:pro"} {
		if validatePackageID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if got := PercentEncodeQuery("subscription:claude:max 20x"); got != "subscription%3Aclaude%3Amax%2020x" {
		t.Errorf("encoded package id = %q", got)
	}
	if err := validatePackageStrategy("most_remaining"); err != nil {
		t.Error(err)
	}
	for _, strategy := range []string{"", "fastest"} {
		if validatePackageStrategy(strategy) == nil {
			t.Errorf("strategy %q accepted", strategy)
		}
	}
}

func TestServiceOrderRequiresStrongETagAndDistinctIDs(t *testing.T) {
	tag := `"sha256:` + strings.Repeat("a", 64) + `"`
	body := []byte(`{"service_ids":["service_b","service_a"]}`)
	order, err := ServiceOrderFrom(tag, body)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, ServiceOrder{ServiceIDs: []string{"service_b", "service_a"}, ETag: tag}) {
		t.Fatalf("order = %+v", order)
	}
	for _, tc := range []struct{ etag, body string }{
		{"", string(body)},
		{`W/"old"`, string(body)},
		{tag, `{"service_ids":["service_a","service_a"]}`},
		{tag, `{"service_ids":["bad/path"]}`},
		{tag, `{}`},
	} {
		if _, err := ServiceOrderFrom(tc.etag, []byte(tc.body)); err == nil {
			t.Errorf("%s %s accepted", tc.etag, tc.body)
		}
	}
}

func TestServiceRecordsValidateTheirProxyAndFailurePolicy(t *testing.T) {
	tag := `"sha256:` + strings.Repeat("b", 64) + `"`
	body := []byte(`{"id":"service_a","proxy":{"mode":"custom","url":"http://127.0.0.1:7890","credential_ref":"local://service-proxy/service_a"}}`)
	record, err := ServiceRecordFrom(tag, body)
	if err != nil {
		t.Fatal(err)
	}
	if string(record.Service) != string(body) || record.ETag != tag {
		t.Fatalf("record = %+v", record)
	}
	for _, tc := range []struct{ etag, body string }{
		{"", `{"id":"service_a"}`},
		{tag, `{"id":"service_a","proxy":{"mode":"custom","url":"http://127.0.0.1:7890","credential_ref":"local://service-proxy/service_b"}}`},
		{tag, `{"id":"service_a","proxy":{"mode":"direct","url":"http://127.0.0.1:7890"}}`},
		{tag, `{"id":"service_a","failure_policy":{"unknown":true}}`},
		{tag, `not json`},
	} {
		if _, err := ServiceRecordFrom(tc.etag, []byte(tc.body)); err == nil {
			t.Errorf("%s %s accepted", tc.etag, tc.body)
		}
	}
}

func authorizationSession(t *testing.T, edits ...func(object)) any {
	t.Helper()
	session := v(t, `{
		"id": "authorization_01",
		"provider": "openai_codex",
		"status": "pending",
		"service_id": "service_codex_01",
		"expires_at": "2026-07-28T08:15:00Z",
		"created_at": "2026-07-28T08:00:00Z",
		"updated_at": "2026-07-28T08:00:00Z"
	}`).(object)
	for _, edit := range edits {
		edit(session)
	}
	return session
}

func set(key string, value any) func(object) {
	return func(o object) { o[key] = value }
}

func unset(key string) func(object) {
	return func(o object) { delete(o, key) }
}

func TestStrictlyParsesBrowserAndDeviceAuthorizationSessions(t *testing.T) {
	browser := []func(object){set("flow", "browser"), set("authorization_url", "https://auth.openai.com/oauth/authorize")}
	device := []func(object){set("flow", "device_code"), set("device_code", map[string]any{
		"verification_url": "https://auth.openai.com/codex/device",
		"user_code":        "ABCD-EFGH",
	})}
	with := func(base []func(object), more ...func(object)) []func(object) {
		return append(append([]func(object){}, base...), more...)
	}
	deviceURL := func(url string) func(object) {
		return set("device_code", map[string]any{"verification_url": url, "user_code": "ABCD-EFGH"})
	}
	claude := with(browser, set("provider", "claude_code"), set("flow", "authorization_code"), set("authorization_url", "https://claude.com/cai/oauth/authorize"))
	for _, tc := range []struct {
		name  string
		edits []func(object)
		ok    bool
	}{
		{"browser", browser, true},
		{"antigravity browser", with(browser, set("provider", "antigravity"), set("authorization_url", "https://accounts.google.com/o/oauth2/v2/auth")), true},
		{"antigravity device", with(device, set("provider", "antigravity"), deviceURL("https://accounts.google.com/device")), false},
		{"claude code", claude, true},
		{"claude code with its code", with(claude, set("code", "secret#state")), false},
		{"claude cli", with(claude, set("flow", "claude_cli"), set("authorization_url", "https://claude.ai/oauth/authorize")), true},
		{"claude cli without url", with(claude, set("flow", "claude_cli"), unset("authorization_url")), false},
		{"codex claude cli", with(browser, set("flow", "claude_cli")), false},
		{"device", device, true},
		{"grok device", with(device, set("provider", "xai_grok"), deviceURL("https://accounts.x.ai/oauth2/device?user_code=ABCD-EFGH")), true},
		{"grok browser", with(browser, set("provider", "xai_grok")), false},
		{"copilot device", with(device, set("provider", "github_copilot"), deviceURL("https://github.com/login/device")), true},
		{"copilot browser", with(browser, set("provider", "github_copilot")), false},
		{"droid device", with(device, set("provider", "factory_droid"), deviceURL("https://auth.factory.ai/device?user_code=DROI-D001")), true},
		{"droid browser", with(browser, set("provider", "factory_droid")), false},
		{"device with url", with(device, set("authorization_url", "https://auth.openai.com/oauth/authorize")), false},
		{"completed device with code", with(device, set("status", "completed")), false},
		{"completed device", with(device, set("status", "completed"), unset("device_code")), true},
		{"error with a secret", with(device, set("status", "completed"), unset("device_code"), set("error", map[string]any{
			"code":    "oauth_device_code_poll_failed",
			"message": "device_auth_id=device-secret",
		})), false},
	} {
		parsed, err := parseAuthorizationSessionValue(authorizationSession(t, tc.edits...))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if tc.name == "device" && err == nil {
			if code, _ := parsed["device_code"].(object); code["user_code"] != "ABCD-EFGH" {
				t.Errorf("device code = %v", parsed["device_code"])
			}
		}
	}
}

func TestSyncOperationsMapToTheirControlRequests(t *testing.T) {
	for _, tc := range []struct {
		operation string
		input     string
		method    string
		path      string
		body      string
	}{
		{"status", "", http.MethodGet, "/control/v1/sync/status", ""},
		{"config", "", http.MethodGet, "/control/v1/sync/config", ""},
		{"run", "", http.MethodPost, "/control/v1/sync/run", ""},
		{"forget", "", http.MethodDelete, "/control/v1/sync/passphrase", ""},
		{"configure", `{"enabled":true}`, http.MethodPut, "/control/v1/sync/config", `{"enabled":true}`},
		{"passphrase", `"twelve-chars"`, http.MethodPut, "/control/v1/sync/passphrase", `{"passphrase":"twelve-chars"}`},
		{"passphrase", `{"passphrase":"twelve-chars"}`, http.MethodPut, "/control/v1/sync/passphrase", `{"passphrase":"twelve-chars"}`},
	} {
		var input any
		if tc.input != "" {
			input = v(t, tc.input)
		}
		req, err := syncRequest(tc.operation, input, tc.input != "")
		if err != nil {
			t.Fatalf("%s: %v", tc.operation, err)
		}
		if req.Method != tc.method || req.Path != tc.path {
			t.Errorf("%s = %s %s", tc.operation, req.Method, req.Path)
		}
		var body string
		if req.Body != nil {
			raw, _ := json.Marshal(req.Body)
			body = string(raw)
		}
		if body != tc.body {
			t.Errorf("%s body = %s", tc.operation, body)
		}
	}
	for _, tc := range []struct{ operation, input string }{
		{"configure", ""},
		{"passphrase", ""},
		{"passphrase", "7"},
		{"nonsense", ""},
	} {
		var input any
		if tc.input != "" {
			input = v(t, tc.input)
		}
		if _, err := syncRequest(tc.operation, input, tc.input != ""); err == nil {
			t.Errorf("%s %s accepted", tc.operation, tc.input)
		}
	}
}

func TestPercentEncodeQueryLeavesUnreservedAndEncodesSpecials(t *testing.T) {
	for in, want := range map[string]string{
		"~/proj": "~%2Fproj", "Aa0-._~": "Aa0-._~", "+": "%2B", "=": "%3D",
		"&": "%26", "/": "%2F", " ": "%20", "猫": "%E7%8C%AB",
	} {
		if got := PercentEncodeQuery(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProjectMemoryQueryChecksEachField(t *testing.T) {
	for _, tc := range []struct {
		query string
		entry bool
		want  string
	}{
		{"null", false, ""},
		{"{}", false, ""},
		{`{"day_start":"2026-10-26T00:00:00+08:00","client_type":"claude_code","project":"~/GolandProjects/astrlink 项目"}`, false,
			"?project=~%2FGolandProjects%2Fastrlink%20%E9%A1%B9%E7%9B%AE&client_type=claude_code&day_start=2026-10-26T00%3A00%3A00%2B08%3A00"},
		{`{"no_project":true}`, false, "?no_project=true"},
		{`{"project":"~/proj","file":"no jitter.md"}`, true, "?project=~%2Fproj&file=no%20jitter.md"},
	} {
		got, err := buildProjectMemoryQuery(v(t, tc.query), tc.entry)
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v", tc.query, got, err)
		}
	}
	for _, query := range []string{
		"null",
		`{"project":"~/proj"}`,
		`{"file":""}`,
		`{"file":"a\nb.md"}`,
		`{"file":` + repeatJSON("x", 129) + `}`,
	} {
		if _, err := buildProjectMemoryQuery(v(t, query), true); err == nil {
			t.Errorf("entry %s accepted", query)
		}
	}
	for _, query := range []string{
		`"~/proj"`,
		`{"project":""}`,
		`{"project":"a\nb"}`,
		`{"project":` + repeatJSON("x", 513) + `}`,
		`{"project":"~/proj","no_project":true}`,
		`{"no_project":false}`,
		`{"no_project":"true"}`,
		`{"client_type":"Claude"}`,
		`{"client_type":"claude&code"}`,
		`{"day_start":"yesterday"}`,
		`{"since":"2026-10-26T00:00:00Z"}`,
		`{"file":"a.md"}`,
		`{"limit":5}`,
	} {
		if _, err := buildProjectMemoryQuery(v(t, query), false); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}

func TestBuildRequestSessionQueryClassifiesWithoutChangingRecordQueries(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`{"kind":"inference","limit":50}`, "?limit=50&kind=inference"},
		{`{"kind":"discovery","cursor":"a+b"}`, "?cursor=a%2Bb&kind=discovery"},
		{`{"kind":"inference","service_id":"service_01","local_access_token_ids":["token_01","token_02"],"status":"succeeded"}`,
			"?service_id=service_01&local_access_token_id=token_01&local_access_token_id=token_02&status=succeeded&kind=inference"},
		{`{"kind":"inference","models":["claude-opus-5-5","gpt-6.1/sol 模型"]}`,
			"?model=claude-opus-5-5&model=gpt-6.1%2Fsol%20%E6%A8%A1%E5%9E%8B&kind=inference"},
		{`{}`, ""},
	} {
		got, err := buildRequestSessionQuery(v(t, tc.query))
		if err != nil || got != tc.want {
			t.Errorf("%s = %q, %v", tc.query, got, err)
		}
	}
	tooMany := `["` + strings.TrimSuffix(strings.Repeat(`model","`, 101), `","`) + `"]`
	for _, models := range []string{`"claude-opus-5-5"`, `[""]`, `[1]`, `["model\nname"]`, `[` + repeatJSON("m", 257) + `]`, tooMany} {
		if _, err := buildRequestSessionQuery(v(t, `{"models":`+models+`}`)); err == nil {
			t.Errorf("models %.40s accepted", models)
		}
	}
	for _, kind := range []string{`"all"`, "null", "true"} {
		if _, err := buildRequestSessionQuery(v(t, `{"kind":`+kind+`}`)); err == nil {
			t.Errorf("kind %s accepted", kind)
		}
	}
	if _, err := buildRequestRecordQuery(v(t, `{"kind":"inference"}`)); err == nil {
		t.Error("record query accepted kind")
	}
	if _, err := buildRequestSessionQuery(v(t, `{"client_type":"zed"}`)); err == nil {
		t.Error("session query accepted client_type")
	}
}

func TestBuildRequestRecordQueryIsDeterministicAndStrict(t *testing.T) {
	if got, err := buildRequestRecordQuery(v(t, `{}`)); err != nil || got != "" {
		t.Fatalf("empty query = %q, %v", got, err)
	}
	got, err := buildRequestRecordQuery(v(t, `{
		"client_type": "zed",
		"status": "succeeded",
		"service_id": "service_01",
		"local_access_token_ids": ["token_01", "token_02"],
		"protocol": "openai_responses",
		"to": "2026-07-25T12:00:00Z",
		"from": "2026-07-24T00:00:00Z",
		"cursor": "a+b=c&d/e",
		"limit": 50
	}`))
	want := "?limit=50&cursor=a%2Bb%3Dc%26d%2Fe&from=2026-07-24T00%3A00%3A00Z&to=2026-07-25T12%3A00%3A00Z&protocol=openai_responses&service_id=service_01&local_access_token_id=token_01&local_access_token_id=token_02&status=succeeded&client_type=zed"
	if err != nil || got != want {
		t.Fatalf("query = %q, %v", got, err)
	}
	if _, err := buildRequestRecordQuery(v(t, `{"unknown":1}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown key error = %v", err)
	}
	if _, err := buildRequestRecordQuery(v(t, `{"local_access_token_id":"token_01"}`)); err == nil ||
		!strings.Contains(err.Error(), "unknown key local_access_token_id") {
		t.Errorf("singular token key error = %v", err)
	}
	for _, query := range []string{
		`{"local_access_token_ids":"token_01"}`,
		`{"local_access_token_ids":["token_01",2]}`,
		`{"limit":0}`,
		`{"limit":201}`,
		`{"limit":1.5}`,
		`{"status":"ok"}`,
		`{"protocol":"OpenAI"}`,
		`{"client_type":"Zed"}`,
		`{"client_type":"_goose"}`,
		`{"client_type":"zed&x=1"}`,
		`{"client_type":""}`,
		`{"client_type":["zed"]}`,
		`{"cursor":` + repeatJSON("x", 513) + `}`,
	} {
		if _, err := buildRequestRecordQuery(v(t, query)); err == nil {
			t.Errorf("%.60s accepted", query)
		}
	}
}

func TestValidatesPurgeRequestRecordsInput(t *testing.T) {
	for _, input := range []string{
		`{"scope":"all","confirm":true}`,
		`{"scope":"sessions","confirm":true,"session_ids":["session_a","req_bbbbbbbb"]}`,
	} {
		if err := validatePurgeRequestRecordsInput(v(t, input)); err != nil {
			t.Errorf("%s: %v", input, err)
		}
	}
	ids := make([]string, maxPurgeSessionIDs+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", fmt.Sprintf("session_%d", i))
	}
	for _, input := range []string{
		`{"scope":"all","confirm":false}`,
		`{"scope":"before","confirm":true}`,
		`{"scope":"all","confirm":true,"before":"2026-07-01T00:00:00Z"}`,
		`{"scope":"all","confirm":true,"extra":true}`,
		`{"scope":"sessions","confirm":true}`,
		`{"scope":"sessions","confirm":true,"session_ids":[]}`,
		`{"scope":"sessions","confirm":true,"session_ids":["session_a","session_a"]}`,
		`{"scope":"sessions","confirm":true,"session_ids":["../x"]}`,
		`{"scope":"sessions","confirm":true,"session_ids":[7]}`,
		`{"scope":"sessions","confirm":true,"session_ids":["session_a"],"before":"2026-07-01T00:00:00Z"}`,
		`{"scope":"all","confirm":true,"session_ids":["session_a"]}`,
		`{"scope":"sessions","confirm":true,"session_ids":[` + strings.Join(ids, ",") + `]}`,
	} {
		if err := validatePurgeRequestRecordsInput(v(t, input)); err == nil {
			t.Errorf("%.80s accepted", input)
		}
	}
}

func TestValidatesAuditSettingsPatchRiskAcknowledgement(t *testing.T) {
	for _, patch := range []string{`{}`, `{"unknown":true}`} {
		if err := validateAuditSettingsPatch(v(t, patch)); err == nil {
			t.Errorf("%s accepted", patch)
		}
	}
	err := validateAuditSettingsPatch(v(t, `{"request_body_enabled":true}`))
	if err == nil || err.Error() != "enabling body audit requires audit_risk_acknowledged=true" {
		t.Errorf("unacknowledged body audit error = %v", err)
	}
	if err := validateAuditSettingsPatch(v(t, `{"request_body_enabled":false,"response_content_enabled":false}`)); err != nil {
		t.Error(err)
	}
}
