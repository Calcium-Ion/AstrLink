package convo_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/convo"
	"github.com/QuantumNous/astrlink/convo/memindex"
)

// Example_toolLoop links the two calls of an OpenAI Chat agent loop into one
// session with one user turn, using memindex as the host's storage.
func Example_toolLoop() {
	policy := convo.DefaultPolicy()
	fp := convo.NewFingerprinter(convo.DeriveFingerprintKey([]byte("master-secret"), "example/fingerprint/v1"))
	index := memindex.New(memindex.Options{})
	principal := "api-key-1"
	now := time.Now()

	handle := func(sessionID, requestBody, responseSSE string) string {
		summary, _ := convo.InspectBody(convo.OpenAIChat, []byte(requestBody))
		decision, _ := policy.Resolve(context.Background(), summary, fp, index.LookupFor(principal), now)
		if decision.Matched {
			sessionID = decision.Match.SessionID
		}
		observer := convo.NewResponseObserver(convo.OpenAIChat, true)
		_, _ = observer.Write([]byte(responseSSE))
		record := memindex.Record{SessionID: sessionID, Principal: principal, At: now}
		if decision.TurnIndex != nil {
			record.TurnIndex, record.HasTurnIndex = *decision.TurnIndex, true
		}
		record.Cursors = append(decision.PersistentInbound(), policy.OutputCursors(observer.Summary(), fp)...)
		index.Put(record)
		fmt.Printf("session=%s turn=%d linked=%v\n", sessionID, record.TurnIndex, decision.Matched)
		return sessionID
	}

	// Call 1: the user asks; the model answers with a tool call.
	first := handle("sess-A",
		`{"messages":[{"role":"user","content":"List TODOs"}]}`,
		strings.Join([]string{
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_7f3a9c2e1b4d4e8fa1c2","type":"function","function":{"name":"grep","arguments":"{}"}}]}}]}`,
			`data: [DONE]`, ``,
		}, "\n"))

	// Call 2: the client replays the history plus the tool result. No session
	// id anywhere, but the tool call id was produced by call 1.
	handle("sess-B",
		`{"messages":[{"role":"user","content":"List TODOs"},{"role":"assistant","tool_calls":[{"id":"call_7f3a9c2e1b4d4e8fa1c2","type":"function","function":{"name":"grep","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_7f3a9c2e1b4d4e8fa1c2","content":"a.go:1 TODO"}]}`,
		`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"content":"There is one TODO in a.go."}}]}`+"\n")

	_ = first
	// Output:
	// session=sess-A turn=1 linked=false
	// session=sess-A turn=1 linked=true
}
