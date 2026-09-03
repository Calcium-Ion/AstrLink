package convo

import "testing"

var fuzzProtocols = []Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, GeminiGenerateContent}

func FuzzInspectBody(f *testing.F) {
	f.Add([]byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"there"}]}`))
	f.Add([]byte(`{"input":"hi","previous_response_id":"resp_1"}`))
	f.Add([]byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1"}]}]}`))
	f.Add([]byte(`{"contents":[{"parts":[{"functionResponse":{"name":"f"}}]}]}`))
	f.Add([]byte(`{"messages":[{"role":"user","content":null},{"role":"assistant","content":[1,2,{"type":"text"}]}]}`))
	f.Add([]byte(`{"messages":"not an array"}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, body []byte) {
		for _, protocol := range fuzzProtocols {
			summary, err := InspectBody(protocol, body)
			if err != nil {
				continue
			}
			if summary.UserTurnCount < 0 || (summary.UserTurnCount > 0) != summary.HasUserMessage {
				t.Fatalf("inconsistent turn count: %+v", summary)
			}
			for _, id := range summary.EchoIDs {
				if !validCursorValue(id) {
					t.Fatalf("invalid echo id %q", id)
				}
			}
			for _, id := range summary.ExplicitCursors {
				if !validCursorValue(id) {
					t.Fatalf("invalid explicit cursor %q", id)
				}
			}
		}
	})
}

func FuzzObserverWrite(f *testing.F) {
	f.Add([]byte("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n"), true)
	f.Add([]byte(`{"id":"msg_1","content":[{"type":"text","text":"hello"}]}`), false)
	f.Add([]byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n"), true)
	f.Add([]byte("data: not json\n\n: comment\nevent: foo\n"), true)
	f.Add([]byte(""), false)
	f.Fuzz(func(t *testing.T, body []byte, streaming bool) {
		for _, protocol := range fuzzProtocols {
			observer := NewResponseObserver(protocol, streaming)
			for offset := 0; offset < len(body); offset += 3 {
				end := min(offset+3, len(body))
				if n, err := observer.Write(body[offset:end]); err != nil || n != end-offset {
					t.Fatalf("Write returned %d, %v", n, err)
				}
			}
			summary := observer.Summary()
			for _, id := range summary.EchoIDs {
				if !validCursorValue(id) {
					t.Fatalf("invalid echo id %q", id)
				}
			}
			if summary.OutputID != "" && !validCursorValue(summary.OutputID) {
				t.Fatalf("invalid output id %q", summary.OutputID)
			}
		}
	})
}

func FuzzTextNormalizer(f *testing.F) {
	f.Add("<think>a</think> b  c", 3)
	f.Add("日本語 テキスト", 2)
	f.Add("\xff\xfe<thin", 1)
	f.Fuzz(func(t *testing.T, text string, chunk int) {
		if chunk <= 0 {
			chunk = 1
		}
		want := DigestText(text, true)
		n := NewTextNormalizer(true)
		data := []byte(text)
		for len(data) > 0 {
			size := min(chunk, len(data))
			_, _ = n.Write(data[:size])
			data = data[size:]
		}
		got := n.Digest()
		if string(got) != string(want) {
			t.Fatalf("chunked digest differs for %q chunk=%d", text, chunk)
		}
	})
}
