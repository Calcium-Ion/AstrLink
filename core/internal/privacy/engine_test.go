package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const testLocalModelID contract.PrivacyModelID = "model_00000000000000000000000000000001"

func TestEnginePreservesCodeInToolResultsWhileRedactingSensitiveValues(t *testing.T) {
	toolText := `command: ["npx", "-y", "@qwen-code/qwen-code@0.20.1", "--acp"]
<path d="M0 0 c 175 105 101 38 184 51 328"/>
<path d="M0 0 c-18 1248 3 1319 4 1322 21 2"/>
<path d="M0 0 c 33 532 175 650 70 59 97 75"/>
contact alice@example.com; card 4242 4242 4242 4242`
	encodedText, err := json.Marshal(toolText)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		protocol contract.ProtocolID
		body     string
	}{
		{contract.ProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","call_id":"call_test","output":` + string(encodedText) + `}]}`},
		{contract.ProtocolOpenAIChat, `{"messages":[{"role":"tool","tool_call_id":"call_test","content":` + string(encodedText) + `}]}`},
		{contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_test","content":[{"type":"text","text":` + string(encodedText) + `}]}]}]}`},
	} {
		t.Run(string(test.protocol), func(t *testing.T) {
			result, err := mustTestEngine(t).Inspect(t.Context(), tokenPolicy(), test.protocol, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != DecisionRedact || len(result.Redactions) != 2 {
				t.Fatalf("decision = %s, redactions = %#v", result.Decision, result.Redactions)
			}
			for _, sensitive := range []string{"alice@example.com", "4242 4242 4242 4242"} {
				if bytes.Contains(result.Body, []byte(sensitive)) {
					t.Fatalf("sensitive value %q was not redacted", sensitive)
				}
			}
			for _, code := range []string{
				"@qwen-code/qwen-code@0.20.1",
				"175 105 101 38 184 51 328",
				"18 1248 3 1319 4 1322 21 2",
				"33 532 175 650 70 59 97 75",
			} {
				if !bytes.Contains(result.Body, []byte(code)) {
					t.Fatalf("code %q was changed", code)
				}
			}
			var original, restored any
			if err := json.Unmarshal([]byte(test.body), &original); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(RestorePlaceholders(result.Body, result.Redactions), &restored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored, original) {
				t.Fatal("redaction and restoration changed the tool result structure")
			}
		})
	}
}

func TestEngineProtocolAwareRedactionFixtures(t *testing.T) {
	tests := []struct {
		name         string
		protocol     contract.ProtocolID
		body         string
		mustRedact   []string
		mustPreserve []string
	}{
		{
			name:     "responses",
			protocol: contract.ProtocolOpenAIResponses,
			body: `{
				"model":"model@example.com",
				"stream":false,
				"instructions":"Email admin@example.com",
				"input":[{
					"role":"user",
					"content":[
						{"type":"input_text","text":"Card 4242 4242 4242 4242"},
						{"type":"input_image","image_url":"https://binary.example/private"}
					],
					"tool_call":{"arguments":"{\"email\":\"tool@example.com\"}"}
				}]
			}`,
			mustRedact:   []string{"admin@example.com", "4242 4242 4242 4242", "tool@example.com"},
			mustPreserve: []string{"model@example.com", "https://binary.example/private"},
		},
		{
			name:     "responses compact",
			protocol: contract.ProtocolOpenAIResponsesCompact,
			body:     `{"model":"safe","input":"alice@example.com","instructions":"call +65 6123 4567"}`,
			mustRedact: []string{
				"alice@example.com", "+65 6123 4567",
			},
		},
		{
			name:     "chat completions",
			protocol: contract.ProtocolOpenAIChat,
			body: `{
				"model":"gpt@example.com",
				"messages":[
					{"role":"system","content":"secret=abcdefghijklmnop123456"},
					{"role":"user","content":[{"type":"text","text":"alice@example.com"}]},
					{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"card\":\"4242424242424242\"}"}}]}
				]
			}`,
			mustRedact:   []string{"secret=abcdefghijklmnop123456", "alice@example.com", "4242424242424242"},
			mustPreserve: []string{"gpt@example.com"},
		},
		{
			name:     "legacy completions",
			protocol: contract.ProtocolOpenAICompletions,
			body:     `{"model":"model@example.com","stream":true,"prompt":["alice@example.com","192.168.1.8"],"suffix":"https://private.example/x"}`,
			mustRedact: []string{
				"alice@example.com", "192.168.1.8", "https://private.example/x",
			},
			mustPreserve: []string{"model@example.com"},
		},
		{
			name:     "anthropic messages",
			protocol: contract.ProtocolAnthropicMessages,
			body: `{
				"model":"claude@example.com",
				"system":"admin@example.com",
				"messages":[{
					"role":"user",
					"content":[
						{"type":"text","text":"GB82WEST12345698765432"},
						{"type":"image","source":{"type":"base64","media_type":"image/png","data":"sk-proj-abcdefghijklmnopqrstuvwxyz123456"}},
						{"type":"tool_use","input":{"phone":"+65 6123 4567"}}
					]
				}]
			}`,
			mustRedact:   []string{"admin@example.com", "GB82WEST12345698765432", "+65 6123 4567"},
			mustPreserve: []string{"claude@example.com", "sk-proj-abcdefghijklmnopqrstuvwxyz123456"},
		},
		{
			name:     "gemini generate content",
			protocol: contract.ProtocolGoogleGenerateContent,
			body: `{
				"model":"gemini@example.com",
				"systemInstruction":{"parts":[{"text":"admin@example.com"}]},
				"contents":[{"role":"user","parts":[
					{"text":"https://private.example/path"},
					{"inlineData":{"mimeType":"image/png","data":"sk-proj-abcdefghijklmnopqrstuvwxyz123456"}},
					{"functionCall":{"name":"lookup","args":{"email":"tool@example.com"}}}
				]}]
			}`,
			mustRedact:   []string{"admin@example.com", "https://private.example/path", "tool@example.com"},
			mustPreserve: []string{"gemini@example.com", "sk-proj-abcdefghijklmnopqrstuvwxyz123456"},
		},
	}

	engine := newTestEngine(t, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := engine.Inspect(context.Background(), Policy{
				Enabled: true, Mode: ModeRegex, Action: ActionRedact,
			}, test.protocol, []byte(test.body))
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if result.Decision != DecisionRedact || len(result.Findings) == 0 {
				t.Fatalf("result = %#v", result)
			}
			for _, secret := range test.mustRedact {
				if bytes.Contains(result.Body, []byte(secret)) {
					t.Fatalf("redacted body retained %q: %s", secret, result.Body)
				}
			}
			for _, preserved := range test.mustPreserve {
				if !bytes.Contains(result.Body, []byte(preserved)) {
					t.Fatalf("redacted body changed excluded value %q: %s", preserved, result.Body)
				}
			}
		})
	}
}

func TestEngineActionsPreserveBodyUnlessRedacting(t *testing.T) {
	const body = " {\n \"input\":\"alice@example.com\", \"model\":\"safe\"\n} "
	engine := newTestEngine(t, nil)
	for _, test := range []struct {
		action   Action
		decision Decision
	}{
		{action: ActionWarn, decision: DecisionWarn},
		{action: ActionBlock, decision: DecisionBlock},
	} {
		result, err := engine.Inspect(context.Background(), Policy{
			Enabled: true, Mode: ModeRegex, Action: test.action,
		}, contract.ProtocolOpenAIResponses, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if result.Decision != test.decision || result.Body != nil {
			t.Fatalf("%s result = %#v", test.action, result)
		}
	}
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionWarn,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"ordinary text"}`))
	if err != nil || result.Decision != DecisionAllow || result.Body != nil {
		t.Fatalf("no-match result = %#v, %v", result, err)
	}
}

func TestEngineDoesNotScanJSONKeysModelStreamOrBinary(t *testing.T) {
	body := []byte(`{
		"alice@example.com":"key only",
		"model":"model@example.com",
		"stream":"admin@example.com",
		"input":[{
			"type":"input_image",
			"image_url":"https://private.example/image"
		}]
	}`)
	result, err := newTestEngine(t, nil).Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionBlock,
	}, contract.ProtocolOpenAIResponses, body)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionAllow || len(result.Findings) != 0 {
		t.Fatalf("excluded fields produced findings: %#v", result)
	}
}

func TestProtocolExtractionExcludesStructuralStringsButKeepsArgumentValues(t *testing.T) {
	body := []byte(`{
		"messages":[{
			"role":"user",
			"name":"participant",
			"content":[{"type":"text","text":"message text"}],
				"tool_calls":[{
					"id":"call_1",
					"type":"function",
					"function":{
						"name":"lookup",
						"arguments":{
							"name":"Alice",
							"email":"alice@example.com",
							"id":"argument-id@example.com",
							"type":"argument-type@example.com",
							"status":"argument-status@example.com",
							"model":"argument-model@example.com",
							"stream":"argument-stream@example.com",
							"format":"argument-format@example.com"
						}
					}
				}]
		}],
		"tools":[{
			"type":"function",
			"function":{
				"name":"lookup",
				"description":"Look up a customer",
				"parameters":{
					"type":"object",
					"required":["email"],
					"properties":{
						"email":{"type":"string","enum":["business","personal"]}
					}
				}
			}
		}]
	}`)
	_, extracted, err := extractDocument(contract.ProtocolOpenAIChat, body)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]string, len(extracted))
	for index := range extracted {
		values[index] = extracted[index].Value
	}
	// Tool call arguments carry data the operator supplied and stay inspectable.
	for _, want := range []string{
		"message text",
		"Alice",
		"alice@example.com",
		"argument-id@example.com",
		"argument-type@example.com",
		"argument-status@example.com",
		"argument-model@example.com",
		"argument-stream@example.com",
		"argument-format@example.com",
	} {
		if !containsString(values, want) {
			t.Fatalf("content value %q not extracted: %#v", want, values)
		}
	}
	for _, excluded := range []string{
		"user",
		"participant",
		"text",
		"call_1",
		"function",
		"lookup",
		"object",
		"string",
		"email",
		"business",
		"personal",
		// A tool declaration is written by the harness author, not the operator.
		// Inspecting it produced placeholders for documentation prose and links
		// while protecting nothing.
		"Look up a customer",
	} {
		if containsString(values, excluded) {
			t.Fatalf("structural value %q was extracted: %#v", excluded, values)
		}
	}
}

func TestProtocolExtractionScansOfficialObjectToolPayloads(t *testing.T) {
	tests := []struct {
		name     string
		protocol contract.ProtocolID
		body     string
		wants    []string
	}{
		{
			name:     "anthropic tool use input",
			protocol: contract.ProtocolAnthropicMessages,
			body: `{
				"messages":[{
					"role":"assistant",
					"content":[{
						"type":"tool_use",
						"id":"toolu_1",
						"name":"lookup",
						"input":{
							"id":"anthropic-id@example.com",
							"type":"anthropic-type@example.com",
							"status":"anthropic-status@example.com"
						}
					}]
				}]
			}`,
			wants: []string{
				"anthropic-id@example.com",
				"anthropic-type@example.com",
				"anthropic-status@example.com",
			},
		},
		{
			name:     "gemini function arguments and response",
			protocol: contract.ProtocolGoogleGenerateContent,
			body: `{
				"contents":[{
					"role":"user",
					"parts":[
						{"functionCall":{"name":"lookup","args":{
							"id":"gemini-id@example.com",
							"type":"gemini-type@example.com",
							"bytes":"gemini-bytes@example.com",
							"blob":"gemini-blob@example.com",
							"file_uri":"gemini-file@example.com",
							"image_url":"gemini-image@example.com",
							"inlineData":"gemini-inline@example.com",
							"fileData":"gemini-file-data@example.com",
							"input_audio":"gemini-audio@example.com",
							"b64_json":"gemini-b64@example.com"
						}}},
						{"functionResponse":{"name":"lookup","response":{
							"status":"gemini-status@example.com",
							"model":"gemini-model@example.com",
							"actual_media":{
								"inlineData":{
									"mimeType":"image/png",
									"data":"media-secret@example.com"
								}
							}
						}}}
					]
				}]
			}`,
			wants: []string{
				"gemini-id@example.com",
				"gemini-type@example.com",
				"gemini-bytes@example.com",
				"gemini-blob@example.com",
				"gemini-file@example.com",
				"gemini-image@example.com",
				"gemini-inline@example.com",
				"gemini-file-data@example.com",
				"gemini-audio@example.com",
				"gemini-b64@example.com",
				"gemini-status@example.com",
				"gemini-model@example.com",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, extracted, err := extractDocument(test.protocol, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			values := make([]string, len(extracted))
			for index := range extracted {
				values[index] = extracted[index].Value
			}
			for _, want := range test.wants {
				if !containsString(values, want) {
					t.Fatalf("tool payload value %q not extracted: %#v", want, values)
				}
			}
			if containsString(values, "media-secret@example.com") {
				t.Fatalf("non-text media body was extracted: %#v", values)
			}
		})
	}
}

func TestEngineRejectsUnsafeInputWithoutReportingPolicyBlockOrLeakingIt(t *testing.T) {
	const sensitive = "alice@example.com"
	engine := newTestEngine(t, nil)
	for _, body := range []string{
		`{"input":"alice@example.com"`,
		`{"input":"alice@example.com","input":"safe"}`,
		string([]byte{'{', '"', 'i', 'n', 'p', 'u', 't', '"', ':', '"', 0xff, '"', '}'}),
	} {
		result, err := engine.Inspect(context.Background(), Policy{
			Enabled: true, Mode: ModeRegex, Action: ActionRedact,
		}, contract.ProtocolOpenAIResponses, []byte(body))
		if !errors.Is(err, ErrUnsafeInput) && !errors.Is(err, ErrUnsafeRewrite) {
			t.Fatalf("Inspect(%q) error = %v", body, err)
		}
		if result.Decision != "" {
			t.Fatalf("failed inspection reported policy decision %q", result.Decision)
		}
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("error leaked match: %v", err)
		}
	}
}

func TestEngineRevalidatesStructuredToolArgumentStringsAfterRedaction(t *testing.T) {
	const body = `{"input":[{"type":"function_call","name":"lookup","arguments":"{\"email\":\"alice@example.com\"}"}]}`

	regexResult, err := newTestEngine(t, nil).Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var rewritten struct {
		Input []struct {
			Arguments string `json:"arguments"`
		} `json:"input"`
	}
	if err := json.Unmarshal(regexResult.Body, &rewritten); err != nil {
		t.Fatalf("outer JSON is invalid: %v", err)
	}
	if len(rewritten.Input) != 1 || !validStructuredJSON(rewritten.Input[0].Arguments) {
		t.Fatalf("structured arguments were not preserved: %#v", rewritten)
	}

	// A model span on the opening brace alone covers no JSON literal, so it is
	// discarded instead of rewriting syntax and rejecting the request.
	syntaxModel := DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		if len(input.Segments) != 1 {
			t.Fatalf("model input = %#v", input)
		}
		return []Finding{{
			Segment: 0,
			Start:   0,
			End:     1,
			Kind:    KindEmail,
		}}, nil
	})
	result, err := newTestEngine(t, syntaxModel).Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(body))
	if err != nil || result.Decision != DecisionAllow || len(result.Findings) != 0 {
		t.Fatalf("syntax-only structured finding result = %#v, error = %v", result, err)
	}
}

func TestEngineRedactsInvalidLookingToolResultTextWithoutBlocking(t *testing.T) {
	// Anthropic tool_result text that starts with '{' but is not JSON — the
	// shape of `cat package.json | head` plus stderr. Redact must continue.
	const body = `{
		"messages":[{
			"role":"user",
			"content":[{
				"type":"tool_result",
				"tool_use_id":"call_1",
				"content":[{
					"type":"text",
					"text":"{\n  \"name\": \"paseo\",\n  \"author\": {\"email\":\"alice@example.com\"}\n[stderr]\nls: missing\n"
				}]
			}]
		}]
	}`
	result, err := newTestEngine(t, nil).Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionRedact,
	}, contract.ProtocolAnthropicMessages, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionRedact {
		t.Fatalf("decision = %s, findings = %#v", result.Decision, result.Findings)
	}
	if bytes.Contains(result.Body, []byte("alice@example.com")) {
		t.Fatalf("email was not redacted: %s", result.Body)
	}
}

func TestModelModeNeverFallsBackToRegex(t *testing.T) {
	called := false
	model := DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		called = true
		if input.ExpectedLocalModelID != testLocalModelID ||
			len(input.Segments) != 1 || input.Segments[0].Value != "alice@example.com" {
			t.Fatalf("model input = %#v", input)
		}
		return nil, nil
	})
	engine := newTestEngine(t, model)
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionBlock,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !called || result.Decision != DecisionAllow {
		t.Fatalf("model result = %#v, called=%t", result, called)
	}
}

func TestModelConfidenceThresholdIsInclusiveAndReportsSuppressedFindings(t *testing.T) {
	const sample = "画一张猫的图片"
	body := []byte(`{"input":"` + sample + `"}`)
	for _, test := range []struct {
		name           string
		score          float64
		wantDecision   Decision
		wantAccepted   int
		wantSuppressed int
	}{
		{
			name: "below default threshold", score: 0.799999,
			wantDecision: DecisionAllow, wantSuppressed: 1,
		},
		{
			name: "equal to threshold", score: 0.80,
			wantDecision: DecisionBlock, wantAccepted: 1,
		},
		{
			name: "above threshold", score: 0.91,
			wantDecision: DecisionBlock, wantAccepted: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
				if len(input.Segments) != 1 || input.Segments[0].Value != sample {
					t.Fatalf("model input = %#v", input)
				}
				return []Finding{{
					Segment: 0, Start: 0, End: len(sample),
					Kind: KindPerson, Confidence: test.score,
				}}, nil
			})
			result, err := newTestEngine(t, model).Inspect(
				context.Background(),
				Policy{
					Enabled: true, Mode: ModeLocalModel,
					LocalModelID:  testLocalModelID,
					MinConfidence: contract.DefaultPrivacyMinConfidence,
					Action:        ActionBlock,
				},
				contract.ProtocolOpenAIResponses,
				body,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.wantDecision ||
				len(result.Findings) != test.wantAccepted ||
				len(result.SuppressedFindings) != test.wantSuppressed {
				t.Fatalf("result = %#v", result)
			}
			all := append(append([]Finding{}, result.Findings...), result.SuppressedFindings...)
			if len(all) != 1 || all[0].Confidence != test.score {
				t.Fatalf("confidence was not preserved: %#v", result)
			}
		})
	}
}

func TestModelDuplicateCandidatesKeepHighestConfidenceBeforeThreshold(t *testing.T) {
	model := DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		end := len(input.Segments[0].Value)
		return []Finding{
			{Segment: 0, Start: 0, End: end, Kind: KindPerson, Confidence: 0.70},
			{Segment: 0, Start: 0, End: end, Kind: KindPerson, Confidence: 0.85},
		}, nil
	})
	result, err := newTestEngine(t, model).Inspect(
		context.Background(),
		Policy{
			Enabled: true, Mode: ModeLocalModel,
			LocalModelID: testLocalModelID, MinConfidence: 0.80,
			Action: ActionBlock,
		},
		contract.ProtocolOpenAIResponses,
		[]byte(`{"input":"candidate"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != DecisionBlock || len(result.Findings) != 1 ||
		result.Findings[0].Confidence != 0.85 ||
		len(result.SuppressedFindings) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestEngineRejectsInvalidModelConfidenceAndLeavesRegexUnaffected(t *testing.T) {
	for _, score := range []float64{-0.01, 1.01, math.NaN()} {
		model := DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
			return []Finding{{
				Segment: 0, Start: 0, End: len(input.Segments[0].Value),
				Kind: KindPerson, Confidence: score,
			}}, nil
		})
		_, err := newTestEngine(t, model).Inspect(
			context.Background(),
			Policy{
				Enabled: true, Mode: ModeLocalModel,
				LocalModelID: testLocalModelID, MinConfidence: 0.8,
				Action: ActionBlock,
			},
			contract.ProtocolOpenAIResponses,
			[]byte(`{"input":"candidate"}`),
		)
		if !errors.Is(err, ErrDetectorUnavailable) {
			t.Fatalf("score %v error = %v", score, err)
		}
	}

	result, err := newTestEngine(t, nil).Inspect(
		context.Background(),
		Policy{
			Enabled: true, Mode: ModeRegex,
			MinConfidence: 1, Action: ActionBlock,
		},
		contract.ProtocolOpenAIResponses,
		[]byte(`{"input":"alice@example.com"}`),
	)
	if err != nil || result.Decision != DecisionBlock ||
		len(result.Findings) != 1 || result.Findings[0].Confidence != 1 {
		t.Fatalf("regex result = %#v, error = %v", result, err)
	}
}

func TestModelModeWithoutDetectorIsUnavailableAndNeverFallsBack(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeOpenAIPrivacyFilter, LocalModelID: testLocalModelID, Action: ActionBlock,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if !errors.Is(err, ErrSafetyEngineUnavailable) {
		t.Fatalf("Inspect error = %v, want %v", err, ErrSafetyEngineUnavailable)
	}
}

func TestInspectFailsClosedForUnknownModeAndBypassesAllowAction(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: Mode("unknown"), Action: ActionBlock,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("Inspect error = %v, want %v", err, ErrPolicyUnavailable)
	}

	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionAllow,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":`))
	if err != nil || result.Decision != DecisionAllow {
		t.Fatalf("allow result = %#v, %v", result, err)
	}
}

func TestEngineNormalizesModelDetectorFailuresAndFindings(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "unavailable", err: ErrDetectorUnavailable, want: ErrDetectorUnavailable},
		{name: "limit", err: ErrDetectorLimit, want: ErrDetectorLimit},
		{name: "timeout", err: context.DeadlineExceeded, want: ErrDetectorTimeout},
		{name: "private", err: errors.New("alice@example.com"), want: ErrDetectorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := newTestEngine(t, DetectorFunc(func(context.Context, DetectInput) ([]Finding, error) {
				return nil, test.err
			}))
			_, err := engine.Inspect(context.Background(), Policy{
				Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionBlock,
			}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
			if !errors.Is(err, test.want) {
				t.Fatalf("Inspect error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "alice@example.com") {
				t.Fatalf("normalized error leaked detector detail: %v", err)
			}
		})
	}

	engine := newTestEngine(t, DetectorFunc(func(_ context.Context, input DetectInput) ([]Finding, error) {
		start := strings.Index(input.Segments[0].Value, "alice@example.com")
		finding := Finding{Segment: 0, Start: start, End: start + len("alice@example.com"), Kind: KindEmail}
		return []Finding{finding, finding}, nil
	}))
	result, err := engine.Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeModel, LocalModelID: testLocalModelID, Action: ActionRedact,
	}, contract.ProtocolOpenAIResponses, []byte(`{"input":"alice@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 1 || bytes.Contains(result.Body, []byte("alice@example.com")) {
		t.Fatalf("normalized model result = %#v body=%s", result.Findings, result.Body)
	}
}

func TestResolvePolicyValidatesConfigurationAndSanitizesProviderErrors(t *testing.T) {
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, errors.New("database alice@example.com")
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePolicy(context.Background(), Scope{}); !errors.Is(err, ErrPolicyUnavailable) ||
		strings.Contains(err.Error(), "alice@example.com") {
		t.Fatalf("ResolvePolicy error = %v", err)
	}

	engine, err = New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{Enabled: true, Mode: Mode("unknown"), Action: ActionWarn}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePolicy(context.Background(), Scope{}); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("invalid model policy error = %v", err)
	}

	engine, err = New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{Enabled: true, Mode: ModeLocalModel, Action: ActionWarn}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePolicy(context.Background(), Scope{}); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("missing model snapshot error = %v", err)
	}
}

func newTestEngine(t *testing.T, model Detector) *Engine {
	t.Helper()
	engine, err := New(PolicyProviderFunc(func(context.Context, Scope) (Policy, error) {
		return Policy{}, nil
	}), model)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
