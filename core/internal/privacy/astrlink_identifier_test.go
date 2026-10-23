package privacy

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Fictional values in the shapes AstrLink generates.
const (
	testAccessTokenID = "access_token_0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	testDeviceID      = "device_a1b2c3d4e5f60718293a4b5c6d7e8f90"
	testSessionID     = "session_9a8b7c6d5e4f3a2b1c0d9e8f"
	// testLocalToken has the shape of a local access token: astr_ followed
	// by 32 bytes in unpadded base64url.
	testLocalToken = "astr_Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7"
)

func TestAstrLinkIdentifierShapesMatchTheirGenerators(t *testing.T) {
	for _, value := range []string{
		testAccessTokenID,
		testDeviceID,
		"model_00112233445566778899aabbccddeeff",
		"classifier_ffeeddccbbaa99887766554433221100",
		"rawgrant_0123456789abcdef",
		"request_0123456789abcdef01234567",
		testSessionID,
		"req_fedcba9876543210fedcba98",
		"service_0123456789abcdef",
		"service_0123456789abcdef01234567",
	} {
		if !isAstrLinkIdentifier(value) {
			t.Errorf("%q is not recognised as an AstrLink identifier", value)
		}
		if !isAstrLinkIdentifierSpan(value, 0, len(value)) {
			t.Errorf("a span covering exactly %q is not exempt", value)
		}
	}
	if !contractPrivacyModelIDValid("model_00112233445566778899aabbccddeeff") {
		t.Fatal("the model_ shape drifted from contract.PrivacyModelID")
	}
}

func contractPrivacyModelIDValid(value string) bool {
	return contract.PrivacyModelID(value).Validate() == nil
}

func TestAstrLinkIdentifierNearMissesStayFindings(t *testing.T) {
	hex32 := "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	for name, value := range map[string]string{
		"one hex digit short":    "access_token_" + hex32[:31],
		"one hex digit long":     "access_token_" + hex32 + "a",
		"uppercase hex":          "access_token_" + strings.ToUpper(hex32),
		"one uppercase digit":    "access_token_" + hex32[:31] + "F",
		"uppercase prefix":       "ACCESS_TOKEN_" + hex32,
		"non-hex letter":         "access_token_" + hex32[:31] + "g",
		"letter suffix":          testAccessTokenID + "x",
		"dash suffix":            testAccessTokenID + "-1",
		"word before":            "x" + testAccessTokenID,
		"other prefix":           "access_tokens_" + hex32,
		"truncated prefix":       "token_" + hex32,
		"bare hex":               hex32,
		"session with 32 hex":    "session_" + hex32,
		"request with 32 hex":    "request_" + hex32,
		"service with 32 hex":    "service_" + hex32,
		"rawgrant with 32 hex":   "rawgrant_" + hex32,
		"device with 24 hex":     "device_" + hex32[:24],
		"model with 16 hex":      "model_" + hex32[:16],
		"prefix only":            "access_token_",
		"local access token":     testLocalToken,
		"astr prefix with hex":   "astr_" + hex32,
		"raw grant token":        "Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7",
		"id and token together":  testAccessTokenID + " " + testLocalToken,
		"id joined with token":   testAccessTokenID + "." + testLocalToken,
		"id with trailing colon": testAccessTokenID + ":" + hex32,
	} {
		if isAstrLinkIdentifier(value) {
			t.Errorf("%s: %q matched an identifier shape", name, value)
		}
		if isAstrLinkIdentifierSpan(value, 0, len(value)) {
			t.Errorf("%s: a span covering %q was exempt", name, value)
		}
	}
}

func TestAstrLinkIdentifierSpanShapes(t *testing.T) {
	hex := testAccessTokenID[len("access_token_"):]
	tests := []struct {
		name   string
		text   string
		span   string
		exempt bool
	}{
		{name: "exact", text: "token id " + testAccessTokenID + " is active", span: testAccessTokenID, exempt: true},
		{name: "with quotes", text: `{"local_access_token_id":"` + testAccessTokenID + `"}`, span: `"` + testAccessTokenID + `"`, exempt: true},
		{name: "with leading space", text: "id: " + testAccessTokenID, span: " " + testAccessTokenID, exempt: true},
		{name: "escaped quotes", text: `{\"id\":\"` + testAccessTokenID + `\"}`, span: `\"` + testAccessTokenID + `\"`, exempt: true},
		{name: "hex only", text: "id=" + testAccessTokenID + ";", span: hex, exempt: true},
		{name: "prefix tail and hex", text: "(" + testAccessTokenID + ")", span: "token_" + hex, exempt: true},
		{name: "after an escaped newline", text: `line\n` + testAccessTokenID, span: testAccessTokenID, exempt: true},
		{name: "in Chinese text", text: "令牌" + testAccessTokenID + "已停用", span: testAccessTokenID, exempt: true},
		{name: "end of sentence", text: "Session " + testSessionID + ".", span: testSessionID, exempt: true},
		{name: "longer run", text: "x" + testAccessTokenID, span: testAccessTokenID, exempt: false},
		{name: "longer hex tail", text: testAccessTokenID + "ff", span: testAccessTokenID, exempt: false},
		{name: "dash tail", text: testAccessTokenID + "-backup", span: hex, exempt: false},
		{name: "span reaches the next word", text: testAccessTokenID + " " + testLocalToken, span: testAccessTokenID + " astr", exempt: false},
		{name: "token beside an id", text: testAccessTokenID + " " + testLocalToken, span: testLocalToken, exempt: false},
		{name: "quotes only", text: `""`, span: `""`, exempt: false},
		{name: "bare hex elsewhere", text: "digest " + hex, span: hex, exempt: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := strings.Index(test.text, test.span)
			if start < 0 {
				t.Fatalf("span %q not in %q", test.span, test.text)
			}
			if got := isAstrLinkIdentifierSpan(test.text, start, start+len(test.span)); got != test.exempt {
				t.Fatalf("exempt = %v, want %v", got, test.exempt)
			}
		})
	}
}

// identifierFixtureDetector flags every occurrence of each needle the way a
// model that mistakes AstrLink's identifiers for secrets would.
func identifierFixtureDetector(needles map[string]Kind) Detector {
	return DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		var findings []Finding
		for index, segment := range input.Segments {
			for needle, kind := range needles {
				for offset := 0; ; {
					found := strings.Index(segment.Value[offset:], needle)
					if found < 0 {
						break
					}
					start := offset + found
					findings = append(findings, Finding{
						Segment: index, Start: start, End: start + len(needle),
						Kind: kind, Confidence: 0.99,
					})
					offset = start + len(needle)
				}
			}
		}
		return findings, nil
	})
}

func TestEngineKeepsAstrLinkIdentifiersAndRedactsTheToken(t *testing.T) {
	engine := newTestEngine(t, identifierFixtureDetector(map[string]Kind{
		testAccessTokenID: KindCommonSecret,
		testDeviceID:      KindAccount,
		testLocalToken:    KindCommonSecret,
	}))
	body := `{"messages":[{"role":"user","content":"My AstrLink token ` + testLocalToken +
		` has id ` + testAccessTokenID + ` on device ` + testDeviceID + `."}]}`
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeLocalModel, LocalModelID: testLocalModelID,
		MinConfidence: 0.8, Action: ActionRedact,
	}, contract.ProtocolOpenAIChat, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	redacted := string(result.Body)
	if result.Decision != DecisionRedact || strings.Contains(redacted, testLocalToken) {
		t.Fatalf("the local access token was not redacted: %q %s", result.Decision, redacted)
	}
	if !strings.Contains(redacted, testAccessTokenID) || !strings.Contains(redacted, testDeviceID) {
		t.Fatalf("an AstrLink identifier was redacted: %s", redacted)
	}
	if len(result.Findings) != 1 || len(result.Redactions) != 1 || result.Redactions[0].Value != testLocalToken {
		t.Fatalf("findings = %#v, redactions = %#v", result.Findings, result.Redactions)
	}
	if len(result.SuppressedFindings) != 2 {
		t.Fatalf("suppressed = %#v", result.SuppressedFindings)
	}
	for _, finding := range result.SuppressedFindings {
		if finding.Suppression != SuppressionAstrLinkIdentifier {
			t.Fatalf("suppression reason = %q", finding.Suppression)
		}
	}
}

// TestAstrLinkIdentifiersAreKeptWhereverInspectionRuns covers every place an
// agent may echo an identifier it read from AstrLink: its own text, tool
// arguments, tool output, and reasoning, in each protocol family.
func TestAstrLinkIdentifiersAreKeptWhereverInspectionRuns(t *testing.T) {
	record := `{\"id\":\"request_0123456789abcdef01234567\",\"session_id\":\"` + testSessionID +
		`\",\"local_access_token_id\":\"` + testAccessTokenID + `\"}`
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		paths    []string
	}{
		{
			name:     "chat completions",
			protocol: contract.ProtocolOpenAIChat,
			body: `{"messages":[` +
				`{"role":"user","content":"token ` + testLocalToken + ` id ` + testAccessTokenID + `"},` +
				`{"role":"assistant","reasoning_content":"Look up ` + testAccessTokenID + `",` +
				`"tool_calls":[{"id":"call_1","type":"function","function":{"name":"requests",` +
				`"arguments":"{\"local_access_token_id\":\"` + testAccessTokenID + `\"}"}}]},` +
				`{"role":"tool","tool_call_id":"call_1","content":"` + record + `"}]}`,
			paths: []string{"/messages/0/content", "/messages/1/reasoning_content",
				"/messages/1/tool_calls/0/function/arguments", "/messages/2/content"},
		},
		{
			name:     "responses",
			protocol: contract.ProtocolOpenAIResponses,
			body: `{"input":[` +
				`{"role":"user","content":[{"type":"input_text","text":"token ` + testLocalToken + ` id ` + testAccessTokenID + `"}]},` +
				`{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Look up ` + testAccessTokenID + `"}]},` +
				`{"type":"function_call","call_id":"c","name":"requests","arguments":"{\"local_access_token_id\":\"` + testAccessTokenID + `\"}"},` +
				`{"type":"function_call_output","call_id":"c","output":"` + record + `"}]}`,
			paths: []string{"/input/0/content/0/text", "/input/1/summary/0/text",
				"/input/2/arguments", "/input/3/output"},
		},
		{
			name:     "anthropic messages",
			protocol: contract.ProtocolAnthropicMessages,
			body: `{"messages":[` +
				`{"role":"user","content":[{"type":"text","text":"token ` + testLocalToken + ` id ` + testAccessTokenID + `"}]},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"t","name":"requests","input":{"local_access_token_id":"` + testAccessTokenID + `"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"` + record + `"}]}]}`,
			paths: []string{"/messages/0/content/0/text", "/messages/1/content/0/input/local_access_token_id",
				"/messages/2/content/0/content"},
		},
		{
			name:     "gemini generate content",
			protocol: contract.ProtocolGoogleGenerateContent,
			body: `{"contents":[` +
				`{"role":"user","parts":[{"text":"token ` + testLocalToken + ` id ` + testAccessTokenID + `"}]},` +
				`{"role":"model","parts":[{"text":"Look up ` + testAccessTokenID + `","thought":true},` +
				`{"functionCall":{"name":"requests","args":{"local_access_token_id":"` + testAccessTokenID + `"}}}]},` +
				`{"role":"user","parts":[{"functionResponse":{"name":"requests","response":{"session_id":"` + testSessionID + `"}}}]}]}`,
			paths: []string{"/contents/0/parts/0/text", "/contents/1/parts/0/text",
				"/contents/1/parts/1/functionCall/args/local_access_token_id",
				"/contents/2/parts/0/functionResponse/response/session_id"},
		},
	}
	engine := newTestEngine(t, identifierFixtureDetector(map[string]Kind{
		testAccessTokenID: KindCommonSecret,
		testSessionID:     KindAccount,
		testLocalToken:    KindCommonSecret,
	}))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := engine.Inspect(context.Background(), Policy{
				Enabled: true, Mode: ModeLocalModel, LocalModelID: testLocalModelID, Action: ActionRedact,
			}, test.protocol, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			redacted := string(result.Body)
			if result.Decision != DecisionRedact || strings.Contains(redacted, testLocalToken) {
				t.Fatalf("the local access token was not redacted: %q %s", result.Decision, redacted)
			}
			if strings.Count(redacted, testAccessTokenID) != strings.Count(test.body, testAccessTokenID) ||
				strings.Count(redacted, testSessionID) != strings.Count(test.body, testSessionID) {
				t.Fatalf("an AstrLink identifier was redacted: %s", redacted)
			}
			for _, finding := range result.Findings {
				if finding.Kind != KindCommonSecret || finding.Path != test.paths[0] {
					t.Fatalf("unexpected accepted finding %#v", finding)
				}
			}
			kept := map[string]bool{}
			for _, finding := range result.SuppressedFindings {
				if finding.Suppression != SuppressionAstrLinkIdentifier {
					t.Fatalf("suppression reason = %q at %s", finding.Suppression, finding.Path)
				}
				kept[finding.Path] = true
			}
			for _, path := range test.paths {
				if !kept[path] {
					t.Errorf("no identifier was kept at %s; kept at %v", path, kept)
				}
			}
		})
	}
}
