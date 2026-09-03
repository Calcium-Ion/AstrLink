package ingress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	inherited.resolveSession(context.Background(), store, nil)
	if inherited.sessionID != sessionID {
		t.Fatalf("inherited session = %q", inherited.sessionID)
	}
	if inherited.previousResponseID != "resp_prev" {
		t.Fatalf("cursor = %q", inherited.previousResponseID)
	}
	if inherited.sessionLink == nil || inherited.sessionLink.Kind != contract.SessionCursorExplicit || inherited.sessionLink.Value != "resp_prev" {
		t.Fatalf("session link = %#v", inherited.sessionLink)
	}
	if inherited.inputPreview != "下一轮" {
		t.Fatalf("preview = %q", inherited.inputPreview)
	}
	if len(inherited.events) != 1 || inherited.events[0].Kind != contract.RequestEventAccepted {
		t.Fatalf("events=%#v", inherited.events)
	}

	viaConversation := &recordSession{classified: Request{ConversationID: "resp_prev"}}
	viaConversation.resolveSession(context.Background(), store, nil)
	if viaConversation.sessionID != sessionID {
		t.Fatalf("conversation link = %q", viaConversation.sessionID)
	}

	broken := &recordSession{classified: Request{PreviousResponseID: "resp_missing"}}
	broken.resolveSession(context.Background(), store, nil)
	if broken.sessionID == "" || broken.sessionID == sessionID {
		t.Fatalf("broken session = %q", broken.sessionID)
	}
	if broken.previousResponseID != "resp_missing" || broken.sessionLink != nil {
		t.Fatalf("broken cursor = %q link=%#v", broken.previousResponseID, broken.sessionLink)
	}
	if !hasSessionCursor(broken.inboundCursors, contract.SessionCursorExplicit, contract.SessionCursorIn, "resp_missing") {
		t.Fatalf("broken inbound cursors = %#v", broken.inboundCursors)
	}

	fresh := &recordSession{classified: Request{}}
	fresh.resolveSession(context.Background(), store, nil)
	if fresh.sessionID == "" || fresh.sessionID == sessionID || fresh.previousResponseID != "" {
		t.Fatalf("fresh=%#v", fresh)
	}

	failing := &memoryRequestRecordStore{findErr: errors.New("database locked")}
	degraded := &recordSession{classified: Request{PreviousResponseID: "resp_prev"}}
	degraded.resolveSession(context.Background(), failing, func(string, ...any) {})
	if degraded.sessionID == "" || degraded.sessionID == sessionID {
		t.Fatalf("lookup failure must mint a fresh session, got %q", degraded.sessionID)
	}
	if !hasSessionCursor(degraded.inboundCursors, contract.SessionCursorExplicit, contract.SessionCursorIn, "resp_prev") {
		t.Fatalf("degraded inbound cursors = %#v", degraded.inboundCursors)
	}
}

// sessionHarness drives one protocol through the handler with a scripted
// upstream and exposes the recorded roots.
type sessionHarness struct {
	t            *testing.T
	handler      *Handler
	store        *memoryRequestRecordStore
	path         string
	responseBody string
	contentType  string
	token        string
}

type sessionHarnessOptions struct {
	auditBlobs AuditBlobPersister
	tokens     map[string]contract.AccessTokenID
	streaming  bool
}

func newSessionHarness(t *testing.T, protocol contract.ProtocolID, path string, options sessionHarnessOptions) *sessionHarness {
	t.Helper()
	harness := &sessionHarness{t: t, store: &memoryRequestRecordStore{}, path: path, contentType: "application/json"}
	dependencies := Dependencies{
		Resolver: candidateResolver{candidates: []endpoint.Resolved{{
			Endpoint:      validEndpoint(protocol, options.streaming),
			UpstreamModel: "provider/secret-upstream",
			RouteID:       "route_alias",
		}}},
		RequestRecords: harness.store,
		AuditBlobs:     options.auditBlobs,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			writer.Header().Set("Content-Type", harness.contentType)
			writer.WriteHeader(http.StatusOK)
			_, err := writer.Write([]byte(harness.responseBody))
			return err
		}),
	}
	if len(options.tokens) > 0 {
		dependencies.AccessTokenAuthenticator = AccessTokenAuthenticatorFunc(func(_ context.Context, raw string) (contract.AccessTokenID, error) {
			id, ok := options.tokens[raw]
			if !ok {
				return "", errors.New("invalid token")
			}
			return id, nil
		})
	}
	harness.handler = NewWithDependencies(dependencies)
	return harness
}

func (harness *sessionHarness) serve(body, response string) contract.RequestRecord {
	harness.t.Helper()
	harness.responseBody = response
	harness.contentType = "application/json"
	if strings.HasPrefix(response, "data:") || strings.HasPrefix(response, "event:") {
		harness.contentType = "text/event-stream"
	}
	request := httptest.NewRequest(http.MethodPost, harness.path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if harness.token != "" {
		request.Header.Set("Authorization", "Bearer "+harness.token)
	}
	recorder := httptest.NewRecorder()
	harness.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		harness.t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	return latestRoot(harness.t, harness.store)
}

func hasSessionCursor(cursors []contract.SessionCursor, kind contract.SessionCursorKind, direction contract.SessionCursorDirection, value string) bool {
	for _, cursor := range cursors {
		if cursor.Kind == kind && cursor.Direction == direction && cursor.Value == value {
			return true
		}
	}
	return false
}

func assertSameSession(t *testing.T, first, second contract.RequestRecord) {
	t.Helper()
	if first.ID == second.ID {
		t.Fatalf("expected two records, got the same id %s", first.ID)
	}
	if first.SessionID == nil || second.SessionID == nil || *first.SessionID != *second.SessionID {
		t.Fatalf("sessions differ: first=%v second=%v", first.SessionID, second.SessionID)
	}
}

func assertNewSession(t *testing.T, first, second contract.RequestRecord) {
	t.Helper()
	if first.SessionID == nil || second.SessionID == nil {
		t.Fatalf("missing session ids first=%v second=%v", first.SessionID, second.SessionID)
	}
	if *first.SessionID == *second.SessionID {
		t.Fatalf("records unexpectedly share session %s", *first.SessionID)
	}
	if second.SessionLink != nil {
		t.Fatalf("new session must not carry a link, got %#v", second.SessionLink)
	}
}

func assertTurn(t *testing.T, record contract.RequestRecord, want int) {
	t.Helper()
	if record.TurnIndex == nil || *record.TurnIndex != want {
		t.Fatalf("turn_index=%v, want %d (record %s)", record.TurnIndex, want, record.ID)
	}
}

func assertLinkedVia(t *testing.T, record contract.RequestRecord, kind contract.SessionCursorKind) {
	t.Helper()
	if record.SessionLink == nil || record.SessionLink.Kind != kind {
		t.Fatalf("session_link=%#v, want kind %s", record.SessionLink, kind)
	}
}

const chatToolCallID = "call_8f3kd92ls0a1Qz7"

const chatLoopResponseStep1 = `{"id":"chatcmpl-loop-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"` + chatToolCallID + `","type":"function","function":{"name":"list_files","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

const chatLoopResponseStep2 = `{"id":"chatcmpl-loop-2","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"仓库里有 a.go 和 b.go 两个文件。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}`

func TestInferencePlaneLinksChatToolLoopIntoOneTurnAndFollowUpIntoNext(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{})

	step1 := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"帮我看看仓库里有哪些文件"}],"tools":[{"type":"function","function":{"name":"list_files"}}]}`,
		chatLoopResponseStep1,
	)
	assertTurn(t, step1, 1)
	if step1.SessionLink != nil {
		t.Fatalf("first request must start its own session, link=%#v", step1.SessionLink)
	}
	if !hasSessionCursor(step1.Cursors, contract.SessionCursorEchoID, contract.SessionCursorOut, chatToolCallID) {
		t.Fatalf("step1 cursors=%#v, want tool call id as outbound echo id", step1.Cursors)
	}
	if !hasSessionCursor(step1.Cursors, contract.SessionCursorExplicit, contract.SessionCursorOut, "chatcmpl-loop-1") {
		t.Fatalf("step1 cursors=%#v, want completion id as explicit out", step1.Cursors)
	}
	if step1.InputPreview == nil || *step1.InputPreview != "帮我看看仓库里有哪些文件" {
		t.Fatalf("step1 preview=%v", step1.InputPreview)
	}

	step2 := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"帮我看看仓库里有哪些文件"},{"role":"assistant","content":null,"tool_calls":[{"id":"`+chatToolCallID+`","type":"function","function":{"name":"list_files","arguments":"{}"}}]},{"role":"tool","tool_call_id":"`+chatToolCallID+`","content":"a.go\nb.go"}],"tools":[{"type":"function","function":{"name":"list_files"}}]}`,
		chatLoopResponseStep2,
	)
	assertSameSession(t, step1, step2)
	assertLinkedVia(t, step2, contract.SessionCursorEchoID)
	assertTurn(t, step2, 1)
	if step2.SessionLink.Value != chatToolCallID {
		t.Fatalf("step2 link value=%q", step2.SessionLink.Value)
	}
	if step2.InputPreview == nil || *step2.InputPreview != "帮我看看仓库里有哪些文件" {
		t.Fatalf("tool results are not user text; preview=%v", step2.InputPreview)
	}

	followUp := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"帮我看看仓库里有哪些文件"},{"role":"assistant","content":null,"tool_calls":[{"id":"`+chatToolCallID+`","type":"function","function":{"name":"list_files","arguments":"{}"}}]},{"role":"tool","tool_call_id":"`+chatToolCallID+`","content":"a.go\nb.go"},{"role":"assistant","content":"仓库里有 a.go 和 b.go 两个文件。"},{"role":"user","content":"第二个文件是做什么的"}]}`,
		`{"id":"chatcmpl-loop-3","choices":[{"index":0,"message":{"role":"assistant","content":"b.go 是入口。"},"finish_reason":"stop"}]}`,
	)
	assertSameSession(t, step1, followUp)
	assertLinkedVia(t, followUp, contract.SessionCursorEchoID)
	assertTurn(t, followUp, 2)
	if followUp.InputPreview == nil || *followUp.InputPreview != "第二个文件是做什么的" {
		t.Fatalf("preview must be the newest user text, got %v", followUp.InputPreview)
	}
}

func TestInferencePlaneLinksChatStreamingToolCallIDsAndFingerprints(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{
		streaming:  true,
		auditBlobs: &memoryAuditBlobs{},
	})
	toolStream := strings.Join([]string{
		`data: {"id":"chatcmpl-s1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_stream9a8b7c6d5e","type":"function","function":{"name":"grep","arguments":""}}]}}]}`,
		`data: {"id":"chatcmpl-s1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]}}]}`,
		`data: {"id":"chatcmpl-s1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n")
	first := harness.serve(`{"model":"public-alias","stream":true,"messages":[{"role":"user","content":"搜一下 x"}]}`, toolStream)
	if !hasSessionCursor(first.Cursors, contract.SessionCursorEchoID, contract.SessionCursorOut, "call_stream9a8b7c6d5e") {
		t.Fatalf("streaming tool call id not captured: %#v", first.Cursors)
	}

	// The visible reply arrives split across deltas, including inside a
	// multi-byte rune boundary-safe chunking of the normalizer.
	textStream := strings.Join([]string{
		`data: {"id":"chatcmpl-s2","choices":[{"index":0,"delta":{"role":"assistant","content":"Go 语言的并发模型建立在 goroutine "}}]}`,
		`data: {"id":"chatcmpl-s2","choices":[{"index":0,"delta":{"content":"和 channel 之上：goroutine 是由运行时调度的轻量级执行单元，"}}]}`,
		`data: {"id":"chatcmpl-s2","choices":[{"index":0,"delta":{"content":"channel 负责在它们之间安全地传递数据，select 则用于同时等待多个通道操作。"}}]}`,
		`data: {"id":"chatcmpl-s2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n")
	second := harness.serve(
		`{"model":"public-alias","stream":true,"messages":[{"role":"user","content":"搜一下 x"},{"role":"assistant","tool_calls":[{"id":"call_stream9a8b7c6d5e","type":"function","function":{"name":"grep","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call_stream9a8b7c6d5e","content":"found"}]}`,
		textStream,
	)
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorEchoID)
	assertTurn(t, second, 1)

	// The client compacts the tool exchange and replays only the reply.
	third := harness.serve(
		`{"model":"public-alias","stream":true,"messages":[{"role":"user","content":"搜一下 x"},{"role":"assistant","content":"`+longAssistantReply+`"},{"role":"user","content":"继续"}]}`,
		`data: {"id":"chatcmpl-s3","choices":[{"index":0,"delta":{"content":"好"}}]}`+"\n\ndata: [DONE]\n\n",
	)
	assertSameSession(t, first, third)
	assertLinkedVia(t, third, contract.SessionCursorFingerprint)
	assertTurn(t, third, 2)
}

func TestInferencePlaneRejectsLowEntropyToolCallIDs(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{})
	first := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"列出文件"}]}`,
		`{"id":"chatcmpl-seq-1","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_0","type":"function","function":{"name":"ls","arguments":"{}"}}]}}]}`,
	)
	if hasSessionCursor(first.Cursors, contract.SessionCursorEchoID, contract.SessionCursorOut, "call_0") {
		t.Fatalf("sequential ids must not be stored as echo ids: %#v", first.Cursors)
	}
	second := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"列出文件"},{"role":"assistant","tool_calls":[{"id":"call_0","type":"function","function":{"name":"ls","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_0","content":"a.go"}]}`,
		`{"id":"chatcmpl-seq-2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`,
	)
	assertNewSession(t, first, second)
}

func TestInferencePlaneScopesEchoIDsToTokenAndWindow(t *testing.T) {
	tokens := map[string]contract.AccessTokenID{"astr_alpha": "token_alpha", "astr_beta": "token_beta"}
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{tokens: tokens})
	harness.token = "astr_alpha"
	first := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"列出文件"}]}`,
		chatLoopResponseStep1,
	)
	replay := `{"model":"public-alias","messages":[{"role":"user","content":"列出文件"},{"role":"assistant","tool_calls":[{"id":"` + chatToolCallID + `","type":"function","function":{"name":"list_files","arguments":"{}"}}]},{"role":"tool","tool_call_id":"` + chatToolCallID + `","content":"a.go"}]}`

	harness.token = "astr_beta"
	otherToken := harness.serve(replay, chatLoopResponseStep2)
	assertNewSession(t, first, otherToken)

	harness.token = "astr_alpha"
	sameToken := harness.serve(replay, chatLoopResponseStep2)
	assertSameSession(t, first, sameToken)
	assertLinkedVia(t, sameToken, contract.SessionCursorEchoID)

	// Age every stored root past the policy window; the same replay must no
	// longer find them.
	for index := range harness.store.records {
		harness.store.records[index].StartedAt = time.Now().Add(-8 * 24 * time.Hour)
	}
	stale := harness.serve(replay, chatLoopResponseStep2)
	assertNewSession(t, first, stale)
}

func TestInferencePlaneExplicitCursorBeatsEchoID(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{})
	loopA := harness.serve(
		`{"model":"public-alias","prompt_cache_key":"cherry:conv-a","messages":[{"role":"user","content":"列出文件"}]}`,
		chatLoopResponseStep1,
	)
	convB := harness.serve(
		`{"model":"public-alias","prompt_cache_key":"cherry:conv-b","messages":[{"role":"user","content":"另一个对话"}]}`,
		`{"id":"chatcmpl-b","choices":[{"index":0,"message":{"role":"assistant","content":"好的"}}]}`,
	)
	assertNewSession(t, loopA, convB)

	// Names conversation B but replays A's tool call id: the explicit cursor
	// is what the client chose, so it wins.
	mixed := harness.serve(
		`{"model":"public-alias","prompt_cache_key":"cherry:conv-b","messages":[{"role":"user","content":"列出文件"},{"role":"assistant","tool_calls":[{"id":"`+chatToolCallID+`","type":"function","function":{"name":"list_files","arguments":"{}"}}]},{"role":"tool","tool_call_id":"`+chatToolCallID+`","content":"a.go"}]}`,
		chatLoopResponseStep2,
	)
	assertSameSession(t, convB, mixed)
	assertLinkedVia(t, mixed, contract.SessionCursorExplicit)
	if mixed.SessionLink.Value != "cherry:conv-b" {
		t.Fatalf("link value=%q", mixed.SessionLink.Value)
	}
	if mixed.PreviousResponseID == nil || *mixed.PreviousResponseID != "cherry:conv-b" {
		t.Fatalf("legacy cursor column=%v", mixed.PreviousResponseID)
	}
}

const longAssistantReply = "Go 语言的并发模型建立在 goroutine 和 channel 之上：goroutine 是由运行时调度的轻量级执行单元，channel 负责在它们之间安全地传递数据，select 则用于同时等待多个通道操作。"

func TestInferencePlaneLinksTextOnlyTurnsByFingerprint(t *testing.T) {
	tokens := map[string]contract.AccessTokenID{"astr_alpha": "token_alpha", "astr_beta": "token_beta"}
	harness := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{
		auditBlobs: &memoryAuditBlobs{},
		tokens:     tokens,
	})
	harness.token = "astr_alpha"
	first := harness.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"请介绍一下 Go 语言的并发模型"}]}`,
		`{"id":"chatcmpl-fp-1","choices":[{"index":0,"message":{"role":"assistant","content":"<think>用户想了解并发。</think>`+longAssistantReply+`"}}]}`,
	)
	var fingerprint string
	for _, cursor := range first.Cursors {
		if cursor.Kind == contract.SessionCursorFingerprint && cursor.Direction == contract.SessionCursorOut {
			fingerprint = cursor.Value
		}
	}
	if !strings.HasPrefix(fingerprint, "fp1_") {
		t.Fatalf("first response must store a fingerprint, cursors=%#v", first.Cursors)
	}
	if strings.Contains(fingerprint, "goroutine") {
		t.Fatal("fingerprint leaked assistant text")
	}

	// Cherry-style clients strip the reasoning and replay only the visible
	// reply, with existing whitespace reflowed.
	replay := `{"model":"public-alias","messages":[{"role":"user","content":"请介绍一下 Go 语言的并发模型"},{"role":"assistant","content":"` + strings.Replace(longAssistantReply, " 和 ", "\\n\\n和  ", 1) + `"},{"role":"user","content":"那 select 有什么坑"}]}`
	second := harness.serve(replay, `{"id":"chatcmpl-fp-2","choices":[{"index":0,"message":{"role":"assistant","content":"最常见的是空 select 永久阻塞。"}}]}`)
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorFingerprint)
	if second.SessionLink.Value != fingerprint {
		t.Fatalf("link value=%q want stored fingerprint %q", second.SessionLink.Value, fingerprint)
	}
	assertTurn(t, second, 2)
	if second.InputPreview == nil || *second.InputPreview != "那 select 有什么坑" {
		t.Fatalf("preview=%v", second.InputPreview)
	}

	harness.token = "astr_beta"
	otherToken := harness.serve(replay, `{"id":"chatcmpl-fp-3","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
	assertNewSession(t, first, otherToken)

	// Without audit storage there is no key to derive, so text-only turns
	// stay separate rather than falling back to an unkeyed hash.
	unkeyed := newSessionHarness(t, contract.ProtocolOpenAIChat, "/v1/chat/completions", sessionHarnessOptions{})
	unkeyedFirst := unkeyed.serve(
		`{"model":"public-alias","messages":[{"role":"user","content":"请介绍一下 Go 语言的并发模型"}]}`,
		`{"id":"chatcmpl-nk-1","choices":[{"index":0,"message":{"role":"assistant","content":"`+longAssistantReply+`"}}]}`,
	)
	for _, cursor := range unkeyedFirst.Cursors {
		if cursor.Kind == contract.SessionCursorFingerprint {
			t.Fatalf("fingerprint stored without a key: %#v", cursor)
		}
	}
	unkeyedSecond := unkeyed.serve(replay, `{"id":"chatcmpl-nk-2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
	assertNewSession(t, unkeyedFirst, unkeyedSecond)
}

func TestInferencePlaneLinksAnthropicToolLoopAndTextTurns(t *testing.T) {
	const toolUseID = "toolu_01A09q90qw90lq917835lq9"
	harness := newSessionHarness(t, contract.ProtocolAnthropicMessages, "/v1/messages", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})

	first := harness.serve(
		`{"model":"claude-sonnet-5","max_tokens":64,"messages":[{"role":"user","content":"列出当前目录"}],"tools":[{"name":"ls","input_schema":{"type":"object"}}]}`,
		`{"id":"msg_loop_1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"`+toolUseID+`","name":"ls","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":5}}`,
	)
	assertTurn(t, first, 1)
	if !hasSessionCursor(first.Cursors, contract.SessionCursorEchoID, contract.SessionCursorOut, toolUseID) {
		t.Fatalf("tool_use id not stored: %#v", first.Cursors)
	}

	second := harness.serve(
		`{"model":"claude-sonnet-5","max_tokens":64,"messages":[{"role":"user","content":"列出当前目录"},{"role":"assistant","content":[{"type":"tool_use","id":"`+toolUseID+`","name":"ls","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+toolUseID+`","content":"a.go"}]}]}`,
		`{"id":"msg_loop_2","type":"message","role":"assistant","content":[{"type":"text","text":"`+longAssistantReply+`"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":5}}`,
	)
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorEchoID)
	assertTurn(t, second, 1)

	// The client compacted the tool exchange away and kept only the visible
	// reply: the fingerprint still links the next question.
	third := harness.serve(
		`{"model":"claude-sonnet-5","max_tokens":64,"messages":[{"role":"user","content":"列出当前目录"},{"role":"assistant","content":[{"type":"text","text":"`+longAssistantReply+`"}]},{"role":"user","content":[{"type":"text","text":"<system-reminder>\nToday is 2026-09-03\n</system-reminder>"},{"type":"text","text":"再说说 channel"}]}]}`,
		`{"id":"msg_loop_3","type":"message","role":"assistant","content":[{"type":"text","text":"好"}],"stop_reason":"end_turn"}`,
	)
	assertSameSession(t, first, third)
	assertLinkedVia(t, third, contract.SessionCursorFingerprint)
	assertTurn(t, third, 2)
	if third.InputPreview == nil || *third.InputPreview != "再说说 channel" {
		t.Fatalf("harness reminder must not become the preview: %v", third.InputPreview)
	}
}

func TestInferencePlaneLinksGeminiTextTurnsByFingerprintAndThoughtSignature(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolGoogleGenerateContent, "/v1beta/models/gemini-2.5-pro:generateContent", sessionHarnessOptions{auditBlobs: &memoryAuditBlobs{}})

	first := harness.serve(
		`{"contents":[{"role":"user","parts":[{"text":"请介绍一下 Go 语言的并发模型"}]}]}`,
		`{"responseId":"gemini-resp-1","candidates":[{"content":{"role":"model","parts":[{"text":"`+longAssistantReply+`"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":9,"totalTokenCount":12}}`,
	)
	assertTurn(t, first, 1)
	if first.InputPreview == nil || *first.InputPreview != "请介绍一下 Go 语言的并发模型" {
		t.Fatalf("preview=%v", first.InputPreview)
	}

	second := harness.serve(
		`{"contents":[{"role":"user","parts":[{"text":"请介绍一下 Go 语言的并发模型"}]},{"role":"model","parts":[{"text":"`+longAssistantReply+`"}]},{"role":"user","parts":[{"text":"那 select 呢"}]}]}`,
		`{"responseId":"gemini-resp-2","candidates":[{"content":{"role":"model","parts":[{"thought":true,"text":"想一想"},{"text":"select 用于多路等待。","thoughtSignature":"CikB7Z2p0kzXQ2sample_signature_bytes_base64=="}]},"finishReason":"STOP"}]}`,
	)
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorFingerprint)
	assertTurn(t, second, 2)
	var signatureCursor string
	for _, cursor := range second.Cursors {
		if cursor.Kind == contract.SessionCursorEchoID && cursor.Direction == contract.SessionCursorOut {
			signatureCursor = cursor.Value
		}
	}
	if !strings.HasPrefix(signatureCursor, "ts_") {
		t.Fatalf("thought signature must be stored hashed, cursors=%#v", second.Cursors)
	}

	// Thinking models: the client echoes the signature verbatim; the short
	// reply alone would not fingerprint.
	third := harness.serve(
		`{"contents":[{"role":"user","parts":[{"text":"请介绍一下 Go 语言的并发模型"}]},{"role":"model","parts":[{"text":"`+longAssistantReply+`"}]},{"role":"user","parts":[{"text":"那 select 呢"}]},{"role":"model","parts":[{"text":"select 用于多路等待。","thoughtSignature":"CikB7Z2p0kzXQ2sample_signature_bytes_base64=="}]},{"role":"user","parts":[{"text":"举个例子"}]}]}`,
		`{"responseId":"gemini-resp-3","candidates":[{"content":{"role":"model","parts":[{"text":"好"}]}}]}`,
	)
	assertSameSession(t, first, third)
	assertLinkedVia(t, third, contract.SessionCursorEchoID)
	if third.SessionLink.Value != signatureCursor {
		t.Fatalf("link value=%q want %q", third.SessionLink.Value, signatureCursor)
	}
	assertTurn(t, third, 3)
}

func TestInferencePlaneLinksStatelessResponsesReplayByItemID(t *testing.T) {
	const itemID = "msg_0a1b2c3d4e5f6a7b8c9d"
	harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{})

	first := harness.serve(
		`{"model":"public-alias","input":[{"role":"user","content":"你能做什么"}]}`,
		`{"id":"resp_item_1","object":"response","output":[{"type":"message","id":"`+itemID+`","role":"assistant","status":"completed","content":[{"type":"output_text","text":"能聊天"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	)
	if !hasSessionCursor(first.Cursors, contract.SessionCursorEchoID, contract.SessionCursorOut, itemID) {
		t.Fatalf("output item id not stored: %#v", first.Cursors)
	}
	second := harness.serve(
		`{"model":"public-alias","input":[{"role":"user","content":"你能做什么"},{"type":"message","id":"`+itemID+`","role":"assistant","status":"completed","content":[{"type":"output_text","text":"能聊天"}]},{"role":"user","content":"你能搜索吗"}]}`,
		`{"id":"resp_item_2","object":"response","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
	)
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorEchoID)
	assertTurn(t, second, 2)
	if second.PreviousResponseID != nil {
		t.Fatalf("echo links must not populate the legacy explicit column: %v", second.PreviousResponseID)
	}
}

func TestInferencePlaneLinksResponsesTurnsAndKeepsBrokenCursor(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{})
	respond := func(id string) string {
		return `{"id":"` + id + `","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}

	first := harness.serve(`{"model":"public-alias","input":"第一轮"}`, respond("resp_one"))
	if first.SessionID == nil || first.OutputResponseID == nil || *first.OutputResponseID != "resp_one" {
		t.Fatalf("first=%#v", first)
	}
	assertTurn(t, first, 1)
	if !hasSessionCursor(first.Cursors, contract.SessionCursorExplicit, contract.SessionCursorOut, "resp_one") {
		t.Fatalf("response id must be an explicit out cursor: %#v", first.Cursors)
	}

	second := harness.serve(`{"model":"public-alias","previous_response_id":"resp_one","input":"第二轮"}`, respond("resp_two"))
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorExplicit)
	if second.PreviousResponseID == nil || *second.PreviousResponseID != "resp_one" {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "第二轮" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}
	// Stateful continuation: the body only holds the new turn, so the turn
	// is the matched turn plus one.
	assertTurn(t, second, 2)

	third := harness.serve(`{"model":"public-alias","previous_response_id":"resp_one","input":[{"type":"function_call_output","call_id":"call_x9y8z7w6v5u4","output":"done"}]}`, respond("resp_three"))
	assertSameSession(t, first, third)
	assertTurn(t, third, 1)

	broken := harness.serve(`{"model":"public-alias","previous_response_id":"resp_missing","input":"断链"}`, respond("resp_other"))
	assertNewSession(t, first, broken)
	if broken.PreviousResponseID == nil || *broken.PreviousResponseID != "resp_missing" {
		t.Fatalf("broken cursor=%v", broken.PreviousResponseID)
	}
	assertTurn(t, broken, 1)
	if !hasSessionCursor(broken.Cursors, contract.SessionCursorExplicit, contract.SessionCursorIn, "resp_missing") {
		t.Fatalf("unmatched explicit cursor must still be stored inbound: %#v", broken.Cursors)
	}
}

func TestInferencePlaneLinksClaudeMessagesByMetadataSession(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolAnthropicMessages, "/v1/messages", sessionHarnessOptions{})
	respond := func(id string) string {
		return `{"id":"` + id + `","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}]}`
	}

	const session = "94460a08-ccc1-4a26-b7e0-fdeae8c82009"
	first := harness.serve(`{"model":"claude-sonnet-5","max_tokens":16,"metadata":{"user_id":"user_ab_account_acc_session_`+session+`"},"messages":[{"role":"user","content":"你能做什么"}]}`, respond("msg_one"))
	if first.SessionID == nil || first.PreviousResponseID == nil || *first.PreviousResponseID != session {
		t.Fatalf("first=%#v", first)
	}
	if !hasSessionCursor(first.Cursors, contract.SessionCursorExplicit, contract.SessionCursorIn, session) {
		t.Fatalf("claude session must be stored inbound: %#v", first.Cursors)
	}

	second := harness.serve(`{"model":"claude-sonnet-5","max_tokens":16,"metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"acc\",\"session_id\":\"`+session+`\"}"},"messages":[{"role":"user","content":"你能做什么"},{"role":"assistant","content":"能聊天"},{"role":"user","content":"你能搜索吗"}]}`, respond("msg_two"))
	assertSameSession(t, first, second)
	assertLinkedVia(t, second, contract.SessionCursorExplicit)
	if second.PreviousResponseID == nil || *second.PreviousResponseID != session {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "你能搜索吗" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}
	assertTurn(t, second, 2)

	third := harness.serve(`{"model":"claude-sonnet-5","max_tokens":16,"messages":[{"role":"user","content":"你能做什么"},{"role":"user","content":"你能搜索吗"}]}`, respond("msg_other"))
	assertNewSession(t, first, third)
	if third.PreviousResponseID != nil {
		t.Fatalf("cherry-style cursor=%v", third.PreviousResponseID)
	}
}

func TestInferencePlaneLinksResponsesByPromptCacheKey(t *testing.T) {
	harness := newSessionHarness(t, contract.ProtocolOpenAIResponses, "/v1/responses", sessionHarnessOptions{})
	respond := func(id string) string {
		return `{"id":"` + id + `","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}

	const cacheKey = "cherry-agent:cdd2c1c84409d105a4fe9c878d39a23d"
	first := harness.serve(`{"model":"gpt-5.6-sol","prompt_cache_key":"`+cacheKey+`","input":[{"role":"user","content":"你能做什么"}]}`, respond("resp_one"))
	if first.SessionID == nil || first.PreviousResponseID == nil || *first.PreviousResponseID != cacheKey {
		t.Fatalf("first=%#v", first)
	}

	second := harness.serve(`{"model":"gpt-5.6-sol","prompt_cache_key":"`+cacheKey+`","input":[{"role":"user","content":"你能做什么"},{"role":"assistant","content":"能聊天"},{"role":"user","content":"你能搜索吗"}]}`, respond("resp_two"))
	assertSameSession(t, first, second)
	if second.PreviousResponseID == nil || *second.PreviousResponseID != cacheKey {
		t.Fatalf("second cursor=%v", second.PreviousResponseID)
	}
	if second.InputPreview == nil || *second.InputPreview != "你能搜索吗" {
		t.Fatalf("second preview=%v", second.InputPreview)
	}
	assertTurn(t, second, 2)

	official := harness.serve(`{"model":"gpt-5.6-sol","previous_response_id":"resp_one","prompt_cache_key":"cherry-agent:other","input":"官方链"}`, respond("resp_official"))
	assertSameSession(t, first, official)
	if official.PreviousResponseID == nil || *official.PreviousResponseID != "resp_one" {
		t.Fatalf("official cursor=%v", official.PreviousResponseID)
	}
	if official.SessionLink.Value != "resp_one" {
		t.Fatalf("previous_response_id should beat cache key, linked via %#v", official.SessionLink)
	}

	replay := harness.serve(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"你能做什么"},{"role":"user","content":"你能搜索吗"}]}`, respond("resp_replay"))
	assertNewSession(t, first, replay)
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
