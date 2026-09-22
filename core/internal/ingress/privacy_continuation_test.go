package ingress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// A placeholder-looking sequence inside an opaque signature is data, never a
// visible response or tool argument. Check both complete responses and chunked
// SSE, including a restored real text field in the same logical frame.
func TestContinuationMetadataSurvivesResponseRestoration(t *testing.T) {
	const marker = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	const signature = "cipher" + marker + "suffix"
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{"responses", contract.ProtocolOpenAIResponses, `{"output":[{"type":"reasoning","encrypted_content":"CIPHER","summary":[]},{"type":"compaction","encrypted_content":"CIPHER"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"VISIBLE"}]}]}`},
		{"anthropic", contract.ProtocolAnthropicMessages, `{"role":"assistant","content":[{"type":"thinking","thinking":"CIPHER","signature":"CIPHER"},{"type":"redacted_thinking","data":"CIPHER"},{"type":"text","text":"VISIBLE"}]}`},
		{"gemini", contract.ProtocolGoogleGenerateContent, `{"candidates":[{"content":{"role":"model","parts":[{"text":"VISIBLE","thoughtSignature":"CIPHER"},{"thought":true,"text":"CIPHER","thought_signature":"CIPHER"}]}}]}`},
		{"chat", contract.ProtocolOpenAIChat, `{"choices":[{"message":{"role":"assistant","content":"VISIBLE","reasoning_details":[{"type":"reasoning.encrypted","data":"CIPHER"},{"type":"reasoning.text","text":"CIPHER","signature":"CIPHER"}],"thinking_blocks":[{"type":"thinking","thinking":"CIPHER","signature":"CIPHER"}],"tool_calls":[{"type":"function","id":"call_1","function":{"name":"noop","arguments":"{}","provider_specific_fields":{"thought_signature":"CIPHER"}},"extra_content":{"google":{"thought_signature":"CIPHER"}},"provider_specific_fields":{"thought_signature":"CIPHER"}}]}}]}`},
	} {
		for _, streaming := range []bool{false, true} {
			name := test.name + "/json"
			if streaming {
				name = test.name + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				body := strings.NewReplacer("CIPHER", signature, "VISIBLE", marker).Replace(test.body)
				recorder := httptest.NewRecorder()
				writer := newRestoringResponseWriter(recorder, []privacy.Redaction{{
					Placeholder: marker, Kind: privacy.KindEmail, Value: "alice@example.com",
				}}, streaming, test.protocol, true)
				wire := body
				contentType := "application/json"
				if streaming {
					wire = "data: " + body + "\n\n"
					contentType = "text/event-stream"
				}
				writer.Header().Set("Content-Type", contentType)
				writer.WriteHeader(http.StatusOK)
				for start := 0; start < len(wire); start += 7 {
					end := min(start+7, len(wire))
					if _, err := writer.Write([]byte(wire[start:end])); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Finish(); err != nil {
					t.Fatal(err)
				}
				got := recorder.Body.String()
				if streaming {
					got = strings.TrimSpace(strings.TrimPrefix(got, "data: "))
				}
				want := strings.NewReplacer("CIPHER", signature, "VISIBLE", "alice@example.com").Replace(test.body)
				var gotJSON, wantJSON any
				if err := json.Unmarshal([]byte(got), &gotJSON); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
					t.Fatal(err)
				}
				gotBytes, _ := json.Marshal(gotJSON)
				wantBytes, _ := json.Marshal(wantJSON)
				if string(gotBytes) != string(wantBytes) {
					t.Fatalf("response metadata changed:\ngot=%s\nwant=%s", gotBytes, wantBytes)
				}
			})
		}
	}
}

func TestAnthropicSignatureDeltaIsNeverRestored(t *testing.T) {
	const marker = "<PRIVATE_EMAIL_7f3a91c04d28be56>"
	const wire = "event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"` + marker + `"}}` + "\n\n"
	recorder := httptest.NewRecorder()
	writer := newRestoringResponseWriter(recorder, []privacy.Redaction{{
		Placeholder: marker, Kind: privacy.KindEmail, Value: "alice@example.com",
	}}, true, contract.ProtocolAnthropicMessages, true)
	writer.Header().Set("Content-Type", "text/event-stream")
	if _, err := writer.Write([]byte(wire)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Body.String(); got != wire {
		t.Fatalf("signature delta changed: %s", got)
	}
}
