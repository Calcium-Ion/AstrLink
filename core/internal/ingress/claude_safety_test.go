package ingress

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/tidwall/gjson"
)

const claudeSafetyAttribution = "x-anthropic-billing-header: cc_version=2.1.292.abc; cc_entrypoint=cli;"

// Side queries use their own system prompt without the main Claude Code banner.
// The attribution must remain its own first block even on the compatibility path.
func TestClaudeSafetyAttributionSurvivesCompatibility(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint("passthrough=", passthrough), func(t *testing.T) {
			upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
			harness := newIdentityHarness(t, passthroughSettings(passthrough, true), nil, claudeConversionCandidate(upstream.url))
			body := strings.Replace(officialClaudeBody, `"messages":`, `"system":[{"type":"text","text":"`+claudeSafetyAttribution+`"},{"type":"text","text":"Evaluate the requested action."}],"messages":`, 1)
			harness.serve(t, "/v1/messages", body, officialClaudeHeaders(learnedClaudeUA))
			_, forwarded := upstream.request(t)
			if got := gjson.GetBytes(forwarded, "system.0.text").String(); got != claudeSafetyAttribution {
				t.Fatalf("first system block = %q, want unchanged attribution", got)
			}
			if passthrough && string(forwarded) != body {
				t.Fatal("official classifier request was rewritten")
			}
		})
	}
}

func TestClaudeSafetySeparatesMixedAttribution(t *testing.T) {
	const instruction = "Never modify production files.\n\tKeep this newline."
	mixed := claudeSafetyAttribution + "\n\n" + instruction
	native := func(system string) string {
		return strings.Replace(officialClaudeBody, `"messages":`, `"system":`+system+`,"messages":`, 1)
	}
	mixedBlock := `{"type":"text","text":` + safetyJSONString(mixed) + `,"cache_control":{"type":"ephemeral","ttl":"1h"},"future_field":{"enabled":true}}`
	for _, test := range []struct {
		name, path, body string
		cache            bool
	}{
		{"system string", "/v1/messages", native(safetyJSONString(mixed)), false},
		{"mixed first block", "/v1/messages", native(`[` + mixedBlock + `,{"type":"text","text":"Tail instructions"}]`), true},
		{"existing banner", "/v1/messages", native(`[` + mixedBlock + `,{"type":"text","text":` + safetyJSONString(claudeCodeBanner) + `}]`), true},
		{"CRLF and joined banner", "/v1/messages", native(safetyJSONString(claudeSafetyAttribution + "\r\n\r\n" + claudeCodeBanner + "\n\n" + instruction)), false},
		{"chat conversion", "/v1/chat/completions", `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"system","content":` + safetyJSONString(mixed) + `},{"role":"user","content":"hello"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := newCapturedUpstream(t, http.StatusOK, "application/json", identityClaudeResponse)
			harness := newIdentityHarness(t, passthroughSettings(false, true), nil, claudeConversionCandidate(upstream.url))
			harness.serve(t, test.path, test.body, officialClaudeHeaders(learnedClaudeUA))
			_, sent := upstream.request(t)
			blocks := gjson.GetBytes(sent, "system").Array()
			if len(blocks) < 3 || blocks[0].Get("text").String() != claudeSafetyAttribution {
				t.Fatalf("attribution is not isolated: %s", gjson.GetBytes(sent, "system").Raw)
			}
			if test.cache && blocks[0].Get("cache_control").Exists() {
				t.Error("cache marker moved to the discarded attribution block")
			}
			// The provider removes the first attribution block. Its remaining
			// input must still include the caller's instructions and metadata.
			found, banners := false, 0
			for _, block := range blocks[1:] {
				text := block.Get("text").String()
				banners += strings.Count(text, claudeCodeBanner)
				if text != instruction {
					continue
				}
				found = true
				if test.cache && (block.Get("cache_control.ttl").String() != "1h" || !block.Get("future_field.enabled").Bool()) {
					t.Error("splitting attribution lost prompt block metadata")
				}
			}
			if !found || banners != 1 {
				t.Fatalf("effective prompt changed: instruction present=%t, banners=%d, system=%s", found, banners, gjson.GetBytes(sent, "system").Raw)
			}
			_, second := prepareClaudeBody(t, string(sent))
			if !bytes.Equal(second, sent) {
				t.Fatal("normalizing an already split request changed it again")
			}
		})
	}
}

// Exercise the final HTTP boundary, including privacy restoration and SSE tail
// events: a model response is not complete until its safety verdict arrives.
func TestClaudeSafetyFieldsSurviveNativeForwarding(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, redact := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/redact=%t", stream, redact), func(t *testing.T) {
				const email = "alice@example.com"
				const safeguards = `[{"type":"dangerous_tool_use","classifier_context":{"environment":"local test","future_field":9007199254740993}}]`
				const toolSchema = `[{"name":"Bash","description":"Run a shell command.","input_schema":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]`
				verdicts := make(chan string, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					if r.URL.RawQuery != "beta=true" || gjson.GetBytes(body, "safeguards").Raw != safeguards {
						t.Errorf("safety request changed: query=%q safeguards=%s", r.URL.RawQuery, gjson.GetBytes(body, "safeguards").Raw)
					}
					if gjson.GetBytes(body, "tools").Raw != toolSchema {
						t.Error("tool schema changed")
					}
					if r.Header.Get("Anthropic-Beta") != "claude-code-20250219,oauth-2025-04-20,dangerous-tool-use-2026-09-03,auto-mode-classifier-2026-07-16" {
						t.Error("safety capability headers changed")
					}
					if gjson.GetBytes(body, "system.0.text").String() != claudeSafetyAttribution {
						t.Error("client attribution changed")
					}
					value := gjson.GetBytes(body, "messages.0.content").String()
					if (value != email) != redact {
						t.Error("privacy policy was not applied as configured")
					}
					// The verdict's explanation is opaque protocol data, even when it
					// happens to quote a spelling the visible response restores.
					wantVerdict := `[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{"toolu_test":{"type":"evaluated","outcome":"flagged","explanation":` + safetyJSONString(value) + `}}},"future_field":9007199254740993}]`
					select {
					case verdicts <- wantVerdict:
					default:
						t.Error("unexpected repeated safety request")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					w.Header().Set("Request-Id", "req_safety_test")
					w.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
					if !stream {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"id":"msg_safety","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_test","name":"Bash","input":{"command":%s}}],"stop_reason":"tool_use","safeguard_results":%s}`, safetyJSONString("echo "+value), wantVerdict)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_safety\",\"type\":\"message\",\"content\":[]}}\n\n")
					_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"Bash\",\"input\":{}}}\n\n")
					arguments := `{"command":` + safetyJSONString("echo "+value) + `}`
					for _, chunk := range []string{arguments[:len(arguments)/2], arguments[len(arguments)/2:]} {
						_, _ = fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", safetyJSONString(chunk))
						w.(http.Flusher).Flush()
					}
					_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
					_, _ = fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1},\"safeguard_results\":%s}\n\n", wantVerdict)
					_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}))
				t.Cleanup(upstream.Close)
				harness := newIdentityHarness(t, passthroughSettings(true, true), nil, claudeConversionCandidate(upstream.URL))
				if redact {
					harness.handler.(*Handler).privacyFilter = testPrivacyEngine(t, privacy.Policy{
						Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact,
						ResponseRestore: true, RestoreToolArguments: true,
					}, nil)
				}
				body := strings.Replace(officialClaudeBody, `"messages":`, `"stream":`+fmt.Sprint(stream)+`,"safeguards":`+safeguards+`,"tools":`+toolSchema+`,"system":[{"type":"text","text":"`+claudeSafetyAttribution+`"}],"messages":`, 1)
				body = strings.Replace(body, `"hello"`, safetyJSONString(email), 1)
				req := httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", strings.NewReader(body))
				req.Header = officialClaudeHeaders(learnedClaudeUA)
				req.Header.Set("Anthropic-Beta", req.Header.Get("Anthropic-Beta")+",dangerous-tool-use-2026-09-03,auto-mode-classifier-2026-07-16")
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				harness.handler.ServeHTTP(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("response = %d %s", response.Code, response.Body.String())
				}
				if response.Header().Get("Request-Id") != "req_safety_test" || response.Header().Get("Anthropic-Ratelimit-Unified-Status") != "allowed" {
					t.Fatal("response diagnostics were lost")
				}
				verdict, command := "", ""
				if stream {
					var arguments strings.Builder
					for _, frame := range strings.Split(response.Body.String(), "\n\n") {
						_, data := parseSSEFrame([]byte(frame))
						if got := gjson.GetBytes(data, "safeguard_results"); got.Exists() {
							verdict = got.Raw
						}
						arguments.WriteString(gjson.GetBytes(data, "delta.partial_json").String())
					}
					command = gjson.Get(arguments.String(), "command").String()
					if !strings.HasSuffix(response.Body.String(), "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n") {
						t.Fatal("stream ended before its terminal event")
					}
				} else {
					verdict = gjson.GetBytes(response.Body.Bytes(), "safeguard_results").Raw
					command = gjson.GetBytes(response.Body.Bytes(), "content.0.input.command").String()
				}
				var wantVerdict string
				select {
				case wantVerdict = <-verdicts:
				default:
					t.Fatal("upstream did not produce a safety verdict")
				}
				got, err := decodeJSONValue([]byte(verdict))
				want, wantErr := decodeJSONValue([]byte(wantVerdict))
				if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("safety verdict changed: got %s, want %s", verdict, wantVerdict)
				}
				if command != "echo "+email {
					t.Fatalf("tool command = %q", command)
				}
			})
		}
	}
}

func TestClaudeSafetyErrorsSurvivePrivacyRestoration(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, 529} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/gzip=%t", status, compressed), func(t *testing.T) {
				errorType := "rate_limit_error"
				if status == 529 {
					errorType = "overloaded_error"
				}
				payload := []byte(`{"type":"error","error":{"type":"` + errorType + `","message":"classifier busy","details":{"reason":"no-verdict"}}}`)
				if compressed {
					var buffer bytes.Buffer
					encoder := gzip.NewWriter(&buffer)
					_, _ = encoder.Write(payload)
					if err := encoder.Close(); err != nil {
						t.Fatal(err)
					}
					payload = buffer.Bytes()
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "120")
					w.Header().Set("X-Should-Retry", "false")
					w.Header().Set("Request-Id", "req_classifier_busy")
					w.Header().Set("Anthropic-Ratelimit-Unified-Status", "rejected")
					if compressed {
						w.Header().Set("Content-Encoding", "gzip")
					}
					w.WriteHeader(status)
					_, _ = w.Write(payload)
				}))
				t.Cleanup(upstream.Close)
				harness := newIdentityHarness(t, passthroughSettings(true, true), nil, claudeConversionCandidate(upstream.URL))
				harness.handler.(*Handler).privacyFilter = testPrivacyEngine(t, privacy.Policy{
					Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true,
				}, nil)
				request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(strings.Replace(officialClaudeBody, `"hello"`, `"alice@example.com"`, 1)))
				request.Header = officialClaudeHeaders(learnedClaudeUA)
				response := httptest.NewRecorder()
				harness.handler.ServeHTTP(response, request)
				if response.Code != status || !bytes.Equal(response.Body.Bytes(), payload) {
					t.Fatalf("upstream error changed: status=%d, body bytes=%d; want status=%d, body bytes=%d", response.Code, response.Body.Len(), status, len(payload))
				}
				if compressed && response.Header().Get("Content-Encoding") != "gzip" {
					t.Error("compressed error lost its encoding header")
				}
				for name, want := range map[string]string{"Retry-After": "120", "X-Should-Retry": "false", "Request-Id": "req_classifier_busy", "Anthropic-Ratelimit-Unified-Status": "rejected"} {
					if got := response.Header().Get(name); got != want {
						t.Errorf("%s = %q, want %q", name, got, want)
					}
				}
			})
		}
	}
}

func safetyJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
