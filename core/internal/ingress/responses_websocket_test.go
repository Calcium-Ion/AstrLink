package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/gorilla/websocket"
)

func wsCandidate(url string) endpoint.Resolved {
	service := contract.ServiceFromEndpoint(validEndpoint(contract.ProtocolOpenAIResponses, true))
	service.Kind = contract.ServiceKindOpenAI
	service.HTTP.BaseURL = url
	enabled := true
	service.ResponsesWebSocketEnabled = &enabled
	return endpoint.Resolved{Service: service, BaseURL: url}
}
func dialResponses(t *testing.T, handler http.Handler, headers http.Header) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", headers)
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, response)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
func sendWS(t *testing.T, client *websocket.Conn, body string) {
	t.Helper()
	if err := client.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
		t.Fatal(err)
	}
}
func readWS(t *testing.T, client *websocket.Conn) map[string]any {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event map[string]any
	if err := client.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	return event
}
func wsUpstream(t *testing.T, serve func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		serve(conn, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestResponsesWebSocketMultiTurnPrivacyModelRewriteAndAuthentication(t *testing.T) {
	var connections, authorizations atomic.Int32
	bodies := make(chan map[string]any, 2)
	upstream := wsUpstream(t, func(conn *websocket.Conn, r *http.Request) {
		connections.Add(1)
		if r.URL.Path != "/prefix/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Bearer local-token" || r.Header.Get("Cookie") != "" {
			t.Error("local credentials leaked")
		}
		for turn := 0; turn < 2; turn++ {
			var event map[string]any
			if err := conn.ReadJSON(&event); err != nil {
				t.Error(err)
				return
			}
			bodies <- event
			_ = conn.WriteJSON(map[string]any{"type": "response.output_text.delta", "delta": event["input"], "output_index": 0, "content_index": 0, "item_id": "msg_1"})
			_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", turn), "model": "private-model", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 2, "total_tokens": 7}}})
		}
	})
	candidate := wsCandidate(upstream.URL + "/prefix/v1")
	candidate.UpstreamModel = "private-model"
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{candidate}},
		AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(_ context.Context, token string) (contract.AccessTokenID, error) {
			authorizations.Add(1)
			if token != "local-token" {
				return "", errors.New("bad token")
			}
			return "token_test", nil
		}),
		PrivacyFilter: testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: privacy.ActionRedact, ResponseRestore: true}, nil),
	})
	client := dialResponses(t, handler, http.Header{"Authorization": {"Bearer local-token"}, "Cookie": {"private=cookie"}})
	for turn := 0; turn < 2; turn++ {
		previous := ""
		if turn == 1 {
			previous = `,"previous_response_id":"resp_0"`
		}
		sendWS(t, client, `{"type":"response.create","model":"public-model","input":"alice@example.com"`+previous+`}`)
		delta := readWS(t, client)
		if delta["delta"] != "alice@example.com" {
			t.Fatalf("restored delta = %#v", delta)
		}
		completed := readWS(t, client)
		if completed["type"] != "response.completed" || completed["response"].(map[string]any)["model"] != "private-model" {
			t.Fatalf("completed = %#v", completed)
		}
		event := <-bodies
		if event["model"] != "private-model" || event["type"] != "response.create" || event["stream"] != nil || strings.Contains(event["input"].(string), "alice@example.com") {
			t.Fatalf("upstream event = %#v", event)
		}
	}
	if connections.Load() != 1 || authorizations.Load() != 3 {
		t.Fatalf("connections=%d authorizations=%d", connections.Load(), authorizations.Load())
	}
}

func TestResponsesWebSocketRejectsDisabledAndConvertedChannels(t *testing.T) {
	var calls atomic.Int32
	upstream := wsUpstream(t, func(*websocket.Conn, *http.Request) { calls.Add(1) })
	for _, test := range []struct {
		name             string
		disable, convert bool
	}{{"disabled", true, false}, {"converted", false, true}} {
		t.Run(test.name, func(t *testing.T) {
			candidate := wsCandidate(upstream.URL)
			if test.disable {
				enabled := false
				candidate.Service.ResponsesWebSocketEnabled = &enabled
			}
			if test.convert {
				candidate.Service.Capabilities[0].ConvertTo = contract.ProtocolOpenAIChat
			}
			client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{candidate}}}), nil)
			sendWS(t, client, `{"type":"response.create","model":"test","input":"hello"}`)
			if event := readWS(t, client); event["type"] != "error" || event["status"] != float64(422) {
				t.Fatalf("event=%#v", event)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("ineligible upstream dialed")
	}
}

func TestResponsesWebSocketRechecksSwitchAndTokenOnEveryTurn(t *testing.T) {
	for _, revokeToken := range []bool{false, true} {
		t.Run(fmt.Sprint(revokeToken), func(t *testing.T) {
			var disabled atomic.Bool
			upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
				var event map[string]any
				if err := conn.ReadJSON(&event); err != nil {
					t.Error(err)
					return
				}
				_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_switch", "status": "completed"}})
				_, _, _ = conn.ReadMessage()
			})
			handler := NewWithDependencies(Dependencies{
				Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
					candidate := wsCandidate(upstream.URL)
					enabled := !disabled.Load()
					candidate.Service.ResponsesWebSocketEnabled = &enabled
					return candidate, nil
				}),
				AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
					if revokeToken && disabled.Load() {
						return "", errors.New("revoked")
					}
					return "token_test", nil
				}),
			})
			client := dialResponses(t, handler, http.Header{"Authorization": {"Bearer local"}})
			sendWS(t, client, `{"type":"response.create","model":"test"}`)
			if event := readWS(t, client); event["type"] != "response.completed" {
				t.Fatal(event)
			}
			disabled.Store(true)
			sendWS(t, client, `{"type":"response.create","model":"test"}`)
			event := readWS(t, client)
			want := 422
			if revokeToken {
				want = 401
			}
			if event["type"] != "error" || event["status"] != float64(want) {
				t.Fatal(event)
			}
		})
	}
}

func TestResponsesWebSocketCancellationAndConcurrentCreate(t *testing.T) {
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_cancel"}})
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
			return
		}
		if event["type"] != "response.cancel" {
			t.Errorf("control=%#v", event)
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.cancelled", "response": map[string]any{"id": "resp_cancel", "status": "cancelled"}})
	})
	client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{wsCandidate(upstream.URL)}}}), nil)
	sendWS(t, client, `{"type":"response.create","model":"test"}`)
	if event := readWS(t, client); event["type"] != "response.created" {
		t.Fatal(event)
	}
	sendWS(t, client, `{"type":"response.create","event_id":"duplicate","model":"test"}`)
	if event := readWS(t, client); event["status"] != float64(409) || event["event_id"] != "duplicate" {
		t.Fatal(event)
	}
	sendWS(t, client, `{"type":"response.cancel"}`)
	if event := readWS(t, client); event["type"] != "response.cancelled" {
		t.Fatal(event)
	}
}

func TestResponsesWebSocketBoundaryBeforeUpgrade(t *testing.T) {
	handler := NewWithDependencies(Dependencies{AccessTokenAuthenticator: AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) { return "", errors.New("invalid") })})
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, header := range []http.Header{{}, {"Origin": {"https://untrusted.example"}}} {
		conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", header)
		if conn != nil {
			conn.Close()
			t.Fatal("boundary accepted upgrade")
		}
		if err == nil || response == nil || (response.StatusCode != 401 && response.StatusCode != 403) {
			t.Fatalf("response=%v err=%v", response, err)
		}
		response.Body.Close()
	}
}

func TestResponsesWebSocketDisconnectCancelsUpstreamWithoutReplay(t *testing.T) {
	var attempts atomic.Int32
	ended := make(chan struct{})
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		attempts.Add(1)
		defer close(ended)
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.created"})
		_, _, _ = conn.ReadMessage()
	})
	client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{wsCandidate(upstream.URL)}}}), nil)
	sendWS(t, client, `{"type":"response.create","model":"test"}`)
	readWS(t, client)
	client.Close()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream was not cancelled")
	}
	if attempts.Load() != 1 {
		t.Fatal("turn replayed")
	}
}

func TestResponsesWebSocketNormalize(t *testing.T) {
	for _, input := range []string{`null`, `{"type":"response.create"}`, `{"response":null}`, `{"model":"test","generate":"false"}`} {
		if _, err := normalizeResponsesWSCreate([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	body, err := normalizeResponsesWSCreate([]byte(`{"type":"response.create","event_id":"secret","response":{"model":"test","generate":false}}`))
	var object map[string]any
	_ = json.Unmarshal(body, &object)
	if err != nil || object["generate"] != false || object["stream"] != true || object["event_id"] != nil {
		t.Fatalf("normalized %s %v", body, err)
	}
}

func TestResponsesWebSocketCodexDefaultPathAndFailedRecord(t *testing.T) {
	records := &memoryRequestRecordStore{}
	upstream := wsUpstream(t, func(conn *websocket.Conn, request *http.Request) {
		if request.URL.Path != "/backend-api/codex/responses" {
			t.Errorf("Codex path=%s", request.URL.Path)
		}
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
			return
		}
		if event["generate"] != false || event["stream_id"] != "main" {
			t.Errorf("warmup envelope=%#v", event)
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_failed", "status": "failed", "error": map[string]string{"code": "server_error", "message": "unavailable"}}})
	})
	candidate := wsCandidate(upstream.URL + "/backend-api/codex")
	candidate.Service.Kind = contract.ServiceKindCodexSubscription
	candidate.Service.HTTP = nil
	candidate.Service.ResponsesWebSocketEnabled = nil
	candidate.Service.Subscription = &contract.SubscriptionConnection{Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected, CredentialRef: "keyring://astrlink/endpoint_test", AuthorizationBoundary: "local-only"}
	candidate.Service.Capabilities = contract.SubscriptionProviderOpenAICodex.Capabilities()
	client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{candidate}}, RequestRecords: records, Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
		return http.Header{"Authorization": {"Bearer codex-upstream"}}, nil
	})}), nil)
	sendWS(t, client, `{"type":"response.create","model":"test","generate":false,"stream_id":"main"}`)
	if event := readWS(t, client); event["type"] != "response.failed" {
		t.Fatal(event)
	}
	if len(records.records) != 1 || records.records[0].Status != contract.RequestStatusFailed {
		t.Fatalf("failed record=%#v", records.records)
	}
}

func TestResponsesWebSocketHandshakeFailoverAndNoReplayAfterSend(t *testing.T) {
	var rejected, accepted atomic.Int32
	reject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rejected.Add(1)
		http.Error(w, `{"error":{"message":"unavailable"}}`, 503)
	}))
	defer reject.Close()
	upstream := wsUpstream(t, func(conn *websocket.Conn, _ *http.Request) {
		accepted.Add(1)
		var event map[string]any
		if err := conn.ReadJSON(&event); err != nil {
			t.Error(err)
		}
		// Drop without any event after accepting the create. Must not replay it.
	})
	first, second := wsCandidate(reject.URL), wsCandidate(upstream.URL)
	second.Service.ID = "service_second"
	client := dialResponses(t, NewWithDependencies(Dependencies{Resolver: candidateResolver{candidates: []endpoint.Resolved{first, second}}}), nil)
	sendWS(t, client, `{"type":"response.create","model":"test"}`)
	if event := readWS(t, client); event["type"] != "error" {
		t.Fatal(event)
	}
	if rejected.Load() != 1 || accepted.Load() != 1 {
		t.Fatalf("attempt counts %d %d", rejected.Load(), accepted.Load())
	}
}

func TestResponsesWebSocketOversizedMessage(t *testing.T) {
	client := dialResponses(t, NewWithDependencies(Dependencies{MaxRequestBodyMiB: 1}), nil)
	_ = client.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", (1<<20)+1)))
	_, _, err := client.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("close=%v", err)
	}
}
