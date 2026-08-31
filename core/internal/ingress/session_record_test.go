package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestResolveSessionInheritsMatchingCursorAndMintsOnBreak(t *testing.T) {
	sessionID := contract.SessionID("session_keep")
	output := "resp_prev"
	store := &memoryRequestRecordStore{records: []contract.RequestRecord{{
		ID:               "request_prev",
		SessionID:        &sessionID,
		OutputResponseID: &output,
	}}}

	inherited := &recordSession{classified: Request{
		PreviousResponseID: "resp_prev",
		InputPreview:       "下一轮",
	}}
	inherited.resolveSession(context.Background(), store)
	if inherited.sessionID != sessionID {
		t.Fatalf("inherited session = %q", inherited.sessionID)
	}
	if inherited.previousResponseID != "resp_prev" {
		t.Fatalf("cursor = %q", inherited.previousResponseID)
	}
	if inherited.inputPreview != "下一轮" {
		t.Fatalf("preview = %q", inherited.inputPreview)
	}
	if len(inherited.events) != 1 || inherited.events[0].Kind != contract.RequestEventAccepted {
		t.Fatalf("events=%#v", inherited.events)
	}

	viaConversation := &recordSession{classified: Request{ConversationID: "resp_prev"}}
	viaConversation.resolveSession(context.Background(), store)
	if viaConversation.sessionID != sessionID {
		t.Fatalf("conversation link = %q", viaConversation.sessionID)
	}

	broken := &recordSession{classified: Request{PreviousResponseID: "resp_missing"}}
	broken.resolveSession(context.Background(), store)
	if broken.sessionID == "" || broken.sessionID == sessionID {
		t.Fatalf("broken session = %q", broken.sessionID)
	}
	if broken.previousResponseID != "resp_missing" {
		t.Fatalf("broken cursor = %q", broken.previousResponseID)
	}

	fresh := &recordSession{classified: Request{}}
	fresh.resolveSession(context.Background(), store)
	if fresh.sessionID == "" || fresh.sessionID == sessionID || fresh.previousResponseID != "" {
		t.Fatalf("fresh=%#v", fresh)
	}
}

func TestInferencePlaneLinksResponsesTurnsAndKeepsBrokenCursor(t *testing.T) {
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	store := &memoryRequestRecordStore{}
	var responseID string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "provider/secret-upstream",
			RouteID:       "route_alias",
		}}},
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"id":"` + responseID + `","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			return err
		}),
	})

	serve := func(body string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}

	responseID = "resp_one"
	serve(`{"model":"public-alias","input":"第一轮"}`)
	first := latestRoot(t, store)
	if first.SessionID == nil || first.OutputResponseID == nil || *first.OutputResponseID != "resp_one" {
		t.Fatalf("first=%#v", first)
	}

	responseID = "resp_two"
	serve(`{"model":"public-alias","previous_response_id":"resp_one","input":"第二轮"}`)
	second := latestRoot(t, store)
	if second.ID == first.ID || second.SessionID == nil || *second.SessionID != *first.SessionID {
		t.Fatalf("second session first=%#v second=%#v", first, second)
	}
	if second.PreviousResponseID == nil || *second.PreviousResponseID != "resp_one" {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "第二轮" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}

	responseID = "resp_other"
	serve(`{"model":"public-alias","previous_response_id":"resp_missing","input":"断链"}`)
	third := latestRoot(t, store)
	if third.SessionID == nil || *third.SessionID == *first.SessionID {
		t.Fatalf("broken chain reused session third=%#v", third)
	}
	if third.PreviousResponseID == nil || *third.PreviousResponseID != "resp_missing" {
		t.Fatalf("broken cursor=%v", third.PreviousResponseID)
	}
}

func TestInferencePlaneLinksClaudeMessagesByMetadataSession(t *testing.T) {
	upstream := validEndpoint(contract.ProtocolAnthropicMessages, false)
	store := &memoryRequestRecordStore{}
	var messageID string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "provider/secret-upstream",
			RouteID:       "route_alias",
		}}},
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"id":"` + messageID + `","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}]}`))
			return err
		}),
	})

	serve := func(body string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}

	const session = "94460a08-ccc1-4a26-b7e0-fdeae8c82009"
	messageID = "msg_one"
	serve(`{"model":"claude-sonnet-5","max_tokens":16,"metadata":{"user_id":"user_ab_account_acc_session_` + session + `"},"messages":[{"role":"user","content":"你能做什么"}]}`)
	first := latestRoot(t, store)
	if first.SessionID == nil || first.PreviousResponseID == nil || *first.PreviousResponseID != session {
		t.Fatalf("first=%#v", first)
	}

	messageID = "msg_two"
	serve(`{"model":"claude-sonnet-5","max_tokens":16,"metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"acc\",\"session_id\":\"` + session + `\"}"},"messages":[{"role":"user","content":"你能做什么"},{"role":"assistant","content":"能聊天"},{"role":"user","content":"你能搜索吗"}]}`)
	second := latestRoot(t, store)
	if second.ID == first.ID || second.SessionID == nil || *second.SessionID != *first.SessionID {
		t.Fatalf("second session first=%#v second=%#v", first, second)
	}
	if second.PreviousResponseID == nil || *second.PreviousResponseID != session {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "你能做什么" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}

	messageID = "msg_other"
	serve(`{"model":"claude-sonnet-5","max_tokens":16,"messages":[{"role":"user","content":"你能做什么"},{"role":"user","content":"你能搜索吗"}]}`)
	third := latestRoot(t, store)
	if third.SessionID == nil || *third.SessionID == *first.SessionID {
		t.Fatalf("cherry-style replay reused session third=%#v", third)
	}
	if third.PreviousResponseID != nil {
		t.Fatalf("cherry-style cursor=%v", third.PreviousResponseID)
	}
}

func TestInferencePlaneLinksResponsesByPromptCacheKey(t *testing.T) {
	upstream := validEndpoint(contract.ProtocolOpenAIResponses, false)
	store := &memoryRequestRecordStore{}
	var responseID string
	handler := NewWithDependencies(Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      upstream,
			UpstreamModel: "provider/secret-upstream",
			RouteID:       "route_alias",
		}}},
		RequestRecords: store,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(`{"id":"` + responseID + `","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
			return err
		}),
	})

	serve := func(body string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}

	const cacheKey = "cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d"
	responseID = "resp_one"
	serve(`{"model":"gpt-5.6-sol","prompt_cache_key":"` + cacheKey + `","input":[{"role":"user","content":"你能做什么"}]}`)
	first := latestRoot(t, store)
	if first.SessionID == nil || first.PreviousResponseID == nil || *first.PreviousResponseID != cacheKey {
		t.Fatalf("first=%#v", first)
	}

	responseID = "resp_two"
	serve(`{"model":"gpt-5.6-sol","prompt_cache_key":"` + cacheKey + `","input":[{"role":"user","content":"你能做什么"},{"role":"assistant","content":"能聊天"},{"role":"user","content":"你能搜索吗"}]}`)
	second := latestRoot(t, store)
	if second.ID == first.ID || second.SessionID == nil || *second.SessionID != *first.SessionID {
		t.Fatalf("second session first=%#v second=%#v", first, second)
	}
	if second.PreviousResponseID == nil || *second.PreviousResponseID != cacheKey {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "你能做什么" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}

	responseID = "resp_official"
	serve(`{"model":"gpt-5.6-sol","previous_response_id":"resp_one","prompt_cache_key":"cherry-agent:other","input":"官方链"}`)
	official := latestRoot(t, store)
	if official.SessionID == nil || *official.SessionID != *first.SessionID {
		t.Fatalf("previous_response_id should beat cache key official=%#v first=%#v", official, first)
	}
	if official.PreviousResponseID == nil || *official.PreviousResponseID != "resp_one" {
		t.Fatalf("official cursor=%v", official.PreviousResponseID)
	}

	responseID = "resp_replay"
	serve(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"你能做什么"},{"role":"user","content":"你能搜索吗"}]}`)
	replay := latestRoot(t, store)
	if replay.SessionID == nil || *replay.SessionID == *first.SessionID {
		t.Fatalf("replay without cache key reused session replay=%#v", replay)
	}
	if replay.PreviousResponseID != nil {
		t.Fatalf("replay cursor=%v", replay.PreviousResponseID)
	}
}

func latestRoot(t *testing.T, store *memoryRequestRecordStore) contract.RequestRecord {
	t.Helper()
	for index := len(store.records) - 1; index >= 0; index-- {
		if store.records[index].ParentRequestID == nil {
			return store.records[index]
		}
	}
	t.Fatal("no root record")
	return contract.RequestRecord{}
}
