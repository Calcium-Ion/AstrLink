package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const failurePolicyJSON = `{"max_retries":1,"initial_delay_ms":500,"max_delay_ms":5000,"network_error":"retry_and_failover","response_timeout":"retry_and_failover","http_status":{"401":"failover","429":"retry_and_failover","418":"retry"}}`

func routingOK(t *testing.T, settings string, patch bool) {
	t.Helper()
	if err := validateRoutingSettings(v(t, settings), patch); err != nil {
		t.Errorf("%.80s: %v", settings, err)
	}
}

func routingRejected(t *testing.T, settings string, patch bool) {
	t.Helper()
	if err := validateRoutingSettings(v(t, settings), patch); err == nil {
		t.Errorf("%.80s accepted", settings)
	}
}

func TestProxyModesAndSecretBoundary(t *testing.T) {
	accepted := []struct {
		proxy     string
		serviceID string
		input     bool
	}{
		{`{"mode":"inherit"}`, "", true},
		{`{"mode":"direct"}`, "", true},
		{`null`, "", true},
		{`{"mode":"custom","url":"socks5://127.0.0.1:1080","credential":{"username":"user","password":""}}`, "", true},
		{`{"mode":"custom","url":"socks5://127.0.0.1:1080","credential":null}`, "", true},
		{`{"mode":"custom","url":"socks5://127.0.0.1:1080"}`, "", true},
		{`{"mode":"custom","url":"http://proxy:8080","credential_ref":"local://service-proxy/service_one"}`, "service_one", false},
	}
	for _, scheme := range []string{"http", "https", "socks5"} {
		accepted = append(accepted, struct {
			proxy     string
			serviceID string
			input     bool
		}{`{"mode":"custom","url":"` + scheme + `://127.0.0.1:1080","credential":{"username":"user","password":"secret"}}`, "", true})
	}
	for _, tc := range accepted {
		if err := validateProxy(v(t, tc.proxy), tc.serviceID, tc.input); err != nil {
			t.Errorf("%s: %v", tc.proxy, err)
		}
	}
	for _, url := range []string{
		"ftp://proxy",
		"http://user:secret@proxy",
		"http://proxy?secret",
		"http://proxy/#secret",
		"http://proxy:0",
		"http://proxy/path",
	} {
		if err := validateProxy(v(t, `{"mode":"custom","url":"`+url+`"}`), "", true); err == nil {
			t.Errorf("%s accepted", url)
		}
	}
	for _, proxy := range []string{
		`{"mode":"custom","url":"http://proxy","credential":{"username":"user","password":"secret"}}`,
		`{"mode":"custom","url":"http://proxy","credential_ref":"local://service-proxy/service_two"}`,
	} {
		if err := validateProxy(v(t, proxy), "service_one", false); err == nil {
			t.Errorf("Core proxy %s accepted", proxy)
		}
	}
}

func TestValidatesBuiltinToolSettingsWithoutCredentials(t *testing.T) {
	routingOK(t, `{"builtin_tools":{"web_search":{"enabled":true,"backend":"external","base_url":"https://search.example"},"image_generation":{"enabled":false,"backend":"upstream"}}}`, true)
	for _, tool := range []string{
		`{"enabled":false}`,
		`{"enabled":true,"backend":"external"}`,
		`{"enabled":false,"backend":"upstream","secret":"private"}`,
		`{"enabled":false,"backend":"service_images"}`,
	} {
		if err := validateBuiltinTool("web_search", v(t, tool)); err == nil {
			t.Errorf("%s accepted", tool)
		}
	}
	if err := validateBuiltinTool("image_generation", v(t, `{"enabled":true,"backend":"service_images","service_id":"newapi_main","model":"gpt-image-1"}`)); err != nil {
		t.Error(err)
	}
	if err := validateBuiltinTool("image_generation", v(t, `{"enabled":true,"backend":"service_images","service_id":"newapi_main"}`)); err == nil {
		t.Error("service images without a model accepted")
	}
}

func TestValidatesProviderSearchForSettingsAndTests(t *testing.T) {
	search := `{"enabled":true,"backend":"service_search","service_id":"codex_main","model":"gpt-5.5"}`
	if err := validateBuiltinTool("web_search", v(t, search)); err != nil {
		t.Fatal(err)
	}
	routingOK(t, `{"builtin_tools":{"web_search":`+search+`,"image_generation":{"enabled":false,"backend":"upstream"}}}`, true)
	for _, enabled := range []string{"false", "true"} {
		tool := obj(t, search, "enabled="+enabled)
		if err := validateBuiltinTool("image_generation", tool); err == nil {
			t.Errorf("image generation took service search (enabled=%s)", enabled)
		}
		for _, field := range []string{"service_id", "model"} {
			incomplete := obj(t, search, "enabled="+enabled, field)
			if err := validateBuiltinTool("web_search", incomplete); (err == nil) != (enabled == "false") {
				t.Errorf("enabled=%s without %s: %v", enabled, field, err)
			}
		}
	}
}

func TestAcceptsCompleteGlobalDefaultsAndOptionalTimeout(t *testing.T) {
	policy := obj(t, failurePolicyJSON)
	if err := validateFailurePolicy(policy); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []string{
		"response_start_timeout_seconds=0",
		"thinking_signature_recovery=false",
		"openai_reasoning_recovery=true",
		"openai_function_output_recovery=true",
	} {
		key, value, _ := strings.Cut(edit, "=")
		policy[key] = v(t, value)
		if err := validateFailurePolicy(policy); err != nil {
			t.Fatalf("%s: %v", edit, err)
		}
	}
	routingOK(t, `{"default_failure_policy":`+string(encoded(t, policy))+`,"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}`, false)
	routingOK(t, `{"default_failure_policy":`+failurePolicyJSON+`}`, true)
}

func TestValidatesSubscriptionIdentitySettings(t *testing.T) {
	for _, key := range []string{
		"codex_identity_enforcement",
		"claude_identity_enforcement",
		"grok_identity_enforcement",
		"official_client_passthrough",
		"subscription_risk_protection",
		"codex_request_normalization",
		"claude_request_normalization",
		"subscription_session_isolation",
		"claude_identity_auto_learn",
		"codex_identity_auto_learn",
		"grok_identity_auto_learn",
	} {
		for _, enabled := range []string{"true", "false"} {
			routingOK(t, `{"`+key+`":`+enabled+`}`, true)
		}
		for _, invalid := range []string{"null", `"false"`, "0"} {
			routingRejected(t, `{"`+key+`":`+invalid+`}`, true)
		}
	}
	for _, key := range []string{"claude_identity_version", "codex_identity_version", "grok_identity_version"} {
		for _, version := range []string{"", "2.1.300", "0.160.0", "2.2.0-beta.1", "1.0.0+build.5"} {
			routingOK(t, `{"`+key+`":"`+version+`"}`, true)
		}
		for _, invalid := range []string{
			"null", "2", `"v2.1.300"`, `"claude-cli/2.1.300"`, `"2.1.300 (external, cli)"`, `"2.1.300\n"`,
			`"1.0.0-` + strings.Repeat("a", 64) + `"`,
		} {
			routingRejected(t, `{"`+key+`":`+invalid+`}`, true)
		}
	}
}

func TestLoadsCompleteRoutingSettingsWithBuiltinToolsAndIdentityControls(t *testing.T) {
	routingOK(t, `{
		"default_failure_policy": `+failurePolicyJSON+`,
		"allow_unmatched_failover": true,
		"strategy": "failover_only",
		"max_attempts": 6,
		"model_redirects": [],
		"channel_stickiness": {"enabled": true, "ttl_seconds": 3600},
		"codex_identity_enforcement": true,
		"claude_identity_enforcement": true,
		"grok_identity_enforcement": true,
		"official_client_passthrough": false,
		"subscription_risk_protection": true,
		"codex_request_normalization": true,
		"claude_request_normalization": true,
		"subscription_session_isolation": true,
		"claude_identity_auto_learn": true,
		"codex_identity_auto_learn": false,
		"claude_identity_version": "2.1.300",
		"codex_identity_version": "0.160.0",
		"system_prompt_enabled": true,
		"system_prompt_prefix": "Answer in English.\n",
		"system_prompt_suffix": "",
		"reasoning_mode": "adaptive",
		"reasoning_level": "max",
		"image_compression": {"enabled": true, "mode": "lossy", "quality": 80},
		"builtin_tools": {
			"web_search": {"enabled": true, "backend": "upstream", "service_id": "service_test", "model": "search-model"},
			"image_generation": {"enabled": false, "backend": "upstream"}
		}
	}`, false)
}

func TestValidatesGlobalPromptAndThinkingSettings(t *testing.T) {
	longest := strings.Repeat("提", maxSystemPromptChars)
	valid := []string{
		`{"system_prompt_enabled":true}`,
		`{"system_prompt_enabled":false}`,
		`{"system_prompt_prefix":""}`,
		`{"system_prompt_prefix":"  Answer in English.\n\nBe brief.  "}`,
		`{"system_prompt_suffix":"` + longest + `"}`,
	}
	modes := []string{"passthrough", "fixed", "adaptive"}
	levels := []string{"off", "low", "medium", "high", "max"}
	for _, mode := range modes {
		valid = append(valid, `{"reasoning_mode":"`+mode+`"}`)
		for _, level := range levels {
			valid = append(valid, `{"reasoning_mode":"`+mode+`","reasoning_level":"`+level+`"}`)
		}
	}
	for _, level := range levels {
		valid = append(valid, `{"reasoning_level":"`+level+`"}`)
	}
	for _, preset := range []string{"everyday", "research", "custom"} {
		valid = append(valid, `{"adaptive_preset":"`+preset+`"}`)
	}
	for index, low := range levels {
		// One end alone is checked by Core against the stored other end.
		valid = append(valid, `{"adaptive_min":"`+low+`"}`, `{"adaptive_max":"`+low+`"}`)
		for _, high := range levels[index:] {
			valid = append(valid, `{"adaptive_min":"`+low+`","adaptive_max":"`+high+`"}`)
		}
	}
	for _, patch := range valid {
		routingOK(t, patch, true)
	}
	for _, invalid := range []string{
		`{"system_prompt_enabled":null}`,
		`{"system_prompt_enabled":"true"}`,
		`{"system_prompt_enabled":1}`,
		`{"system_prompt_prefix":null}`,
		`{"system_prompt_prefix":1}`,
		`{"system_prompt_prefix":["text"]}`,
		`{"system_prompt_suffix":"` + longest + `提"}`,
		`{"reasoning_mode":""}`,
		`{"reasoning_mode":null}`,
		`{"reasoning_mode":"Fixed"}`,
		`{"reasoning_mode":"manual"}`,
		// The modes were renamed before release.
		`{"reasoning_mode":"follow"}`,
		`{"reasoning_mode":"auto"}`,
		`{"reasoning_mode":1}`,
		`{"reasoning_mode":["adaptive"]}`,
		`{"reasoning_level":""}`,
		`{"reasoning_level":null}`,
		`{"reasoning_level":"High"}`,
		`{"reasoning_level":"xhigh"}`,
		`{"reasoning_level":"minimal"}`,
		`{"reasoning_level":"none"}`,
		`{"reasoning_level":3}`,
		`{"reasoning_level":false}`,
		`{"reasoning_mode":"fixed","reasoning_level":"auto"}`,
		// The single override key was replaced by the mode and level.
		`{"reasoning_override":""}`,
		`{"reasoning_override":"high"}`,
		`{"adaptive_preset":""}`,
		`{"adaptive_preset":null}`,
		`{"adaptive_preset":"daily"}`,
		`{"adaptive_preset":"Everyday"}`,
		`{"adaptive_preset":1}`,
		`{"adaptive_min":"xhigh"}`,
		`{"adaptive_min":null}`,
		`{"adaptive_max":"none"}`,
		`{"adaptive_max":""}`,
		`{"adaptive_min":"low","adaptive_max":"off"}`,
		`{"adaptive_min":"max","adaptive_max":"high"}`,
		`{"adaptive_preset":"everyday","adaptive_min":"high","adaptive_max":"medium"}`,
	} {
		routingRejected(t, invalid, true)
	}
	settings := obj(t, `{"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}`)
	settings["default_failure_policy"] = obj(t, failurePolicyJSON)
	check := func(ok bool) {
		t.Helper()
		if err := validateRoutingSettings(settings, false); (err == nil) != ok {
			t.Errorf("%v: %v", settings, err)
		}
	}
	// Every key above is optional in a full document too.
	check(true)
	settings["reasoning_mode"], settings["reasoning_level"] = "fixed", "off"
	check(true)
	settings["adaptive_preset"], settings["adaptive_min"], settings["adaptive_max"] = "custom", "medium", "medium"
	check(true)
	settings["adaptive_min"] = "high"
	check(false)
	settings["adaptive_min"] = "low"
	settings["reasoning_override"] = ""
	check(false)
	delete(settings, "reasoning_override")
	settings["system_prompt_prefix"] = longest + "x"
	check(false)
}

func TestValidatesImageCompressionSettings(t *testing.T) {
	for _, valid := range []string{
		`{"enabled":false,"mode":"lossless","quality":85}`,
		`{"enabled":true,"mode":"lossy","quality":1}`,
		`{"enabled":true,"mode":"lossy","quality":100}`,
	} {
		routingOK(t, `{"image_compression":`+valid+`}`, true)
	}
	for _, invalid := range []string{
		`null`,
		`true`,
		`{"enabled":true}`,
		`{"enabled":true,"mode":"lossless"}`,
		`{"enabled":"true","mode":"lossless","quality":85}`,
		`{"enabled":true,"mode":"webp","quality":85}`,
		`{"enabled":true,"mode":"lossy","quality":0}`,
		`{"enabled":true,"mode":"lossy","quality":101}`,
		`{"enabled":true,"mode":"lossy","quality":80.5}`,
		`{"enabled":true,"mode":"lossy","quality":80,"max_side":2048}`,
	} {
		routingRejected(t, `{"image_compression":`+invalid+`}`, true)
	}
}

func redirect(from, to string) string {
	raw, _ := json.Marshal(map[string]any{"from": from, "to": to, "enabled": true})
	return string(raw)
}

func redirectRules(rules ...string) string {
	return `{"model_redirects":[` + strings.Join(rules, ",") + `]}`
}

func TestAcceptsValidModelRedirects(t *testing.T) {
	long := strings.Repeat("模", maxRedirectModelChars)
	many := make([]string, maxModelRedirects)
	for i := range many {
		many[i] = redirect(fmt.Sprintf("client-%d", i), fmt.Sprintf("upstream-%d", i))
	}
	for _, settings := range []string{
		redirectRules(),
		redirectRules(redirect("gpt-4o", "gpt-5")),
		redirectRules(
			redirect("astrlink/auto", "gpt-5"),
			`{"from":"Claude","to":"claude-sonnet","enabled":false}`,
			redirect("claude", "claude-sonnet"),
			redirect("tab\tinside", long),
		),
		redirectRules(many...),
	} {
		routingOK(t, settings, true)
	}
	routingOK(t, `{"model_redirects":[`+redirect("a", "b")+`],"default_failure_policy":`+failurePolicyJSON+`,"allow_unmatched_failover":false,"strategy":"retry_first","max_attempts":6}`, false)
}

func TestRejectsInvalidModelRedirects(t *testing.T) {
	many := make([]string, maxModelRedirects+1)
	for i := range many {
		many[i] = redirect(fmt.Sprintf("client-%d", i), fmt.Sprintf("upstream-%d", i))
	}
	long := strings.Repeat("m", maxRedirectModelChars+1)
	for _, settings := range []string{
		`{"model_redirects":null}`,
		`{"model_redirects":{}}`,
		`{"model_redirects":"gpt-4o"}`,
		redirectRules(many...),
		redirectRules(`"gpt-4o"`),
		redirectRules(`null`),
		redirectRules(`{"from":"a","to":"b"}`),
		redirectRules(`{"from":"a","enabled":true}`),
		redirectRules(`{"from":"a","to":"b","enabled":true,"note":"x"}`),
		redirectRules(`{"from":"a","to":"b","enabled":"true"}`),
		redirectRules(`{"from":"a","to":"b","enabled":null}`),
		redirectRules(`{"from":1,"to":"b","enabled":true}`),
		redirectRules(`{"from":"a","to":null,"enabled":true}`),
		redirectRules(redirect("", "b")),
		redirectRules(redirect("a", "")),
		redirectRules(redirect(long, "b")),
		redirectRules(redirect("a", long)),
		redirectRules(redirect(" a", "b")),
		redirectRules(redirect("a", "b\t")),
		redirectRules(redirect("a", "b　")),
		redirectRules(redirect("a\nb", "c")),
		redirectRules(redirect("a", "b\x00c")),
		redirectRules(redirect("a", "a")),
		redirectRules(redirect("a", "astrlink/auto")),
		redirectRules(redirect("a", "b"), redirect("a", "c")),
		redirectRules(redirect("a", "b"), redirect("b", "c")),
		redirectRules(redirect("b", "c"), `{"from":"a","to":"b","enabled":false}`),
	} {
		routingRejected(t, settings, true)
	}
}

func TestRejectsIncompleteNullAndOutOfRangePolicies(t *testing.T) {
	for _, edit := range []string{
		"max_retries=6",
		"initial_delay_ms=-1",
		"max_delay_ms=100",
		"response_start_timeout_seconds=null",
		"thinking_signature_recovery=null",
		`thinking_signature_recovery="true"`,
		"openai_reasoning_recovery=null",
		`openai_reasoning_recovery="true"`,
		"openai_function_output_recovery=null",
		`openai_function_output_recovery="true"`,
		`network_error="ignore"`,
		`http_status={"200":"retry"}`,
		"max_retries", "initial_delay_ms", "max_delay_ms", "network_error", "response_timeout", "http_status",
	} {
		if err := validateFailurePolicy(obj(t, failurePolicyJSON, edit)); err == nil {
			t.Errorf("%s accepted", edit)
		}
	}
	routingRejected(t, `{"max_attempts":21}`, true)
	routingRejected(t, `{"default_failure_policy":`+failurePolicyJSON+`,"allow_unmatched_failover":false,"strategy":"retry_first"}`, false)
}
