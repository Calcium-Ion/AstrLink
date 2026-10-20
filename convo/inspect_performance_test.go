package convo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Embedding only the public interface exercises custom-adapter compatibility,
// without the built-in shared-decode optimization.
type publicAdapter struct{ Adapter }

func TestInspectBuiltinAndCustomAdaptersAgree(t *testing.T) {
	for _, entry := range []struct {
		protocol Protocol
		dir      string
	}{
		{OpenAIChat, "openai_chat"},
		{OpenAIResponses, "openai_responses"},
		{AnthropicMessages, "anthropic"},
		{GeminiGenerateContent, "gemini"},
	} {
		paths, err := filepath.Glob(filepath.Join("testdata", entry.dir, "*.json"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("fixtures: %v", err)
		}
		custom := DefaultPolicy()
		custom.Registry = NewRegistry()
		custom.Registry.Register(entry.protocol, publicAdapter{builtinAdapters[entry.protocol]})
		for _, path := range paths {
			t.Run(path, func(t *testing.T) {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				got, err := DefaultPolicy().InspectBody(entry.protocol, body)
				want, wantErr := custom.InspectBody(entry.protocol, body)
				if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("summary mismatch: got %+v (%v), want %+v (%v)", got, err, want, wantErr)
				}
			})
		}
	}
}

func FuzzInspectItemMatchesPublicAdapter(f *testing.F) {
	for _, seed := range []string{
		`{"role":"user","content":"hello"}`,
		`{"role":"user","content":[{"type":"image"}]}`,
		`{"role":"assistant","content":"answer","tool_calls":[{"id":"call_example"}]}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_example"}]}`,
		`{"role":"user","parts":[{"text":"hello"},{"functionResponse":{"id":"example"}}]}`,
		`{"role":"assistant","type":"reasoning","id":"reasoning_example"}`,
		`null`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for protocol, adapter := range builtinAdapters {
			got := inspectRequestItem(adapter, json.RawMessage(raw))
			want := inspectRequestItem(publicAdapter{adapter}, json.RawMessage(raw))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: got %+v, want %+v", protocol, got, want)
			}
		}
	})
}

func BenchmarkInspectHistory(b *testing.B) {
	for _, count := range []int{1, 64} {
		messages := make([]map[string]string, count)
		for i := range messages {
			messages[i] = map[string]string{"role": "user", "content": strings.Repeat("a", (1<<20)/count)}
		}
		body, err := json.Marshal(map[string]any{"messages": messages})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("messages=%d", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				if _, err := InspectBody(OpenAIChat, body); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
