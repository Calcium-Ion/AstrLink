package metadata

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func newMarkerRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, store := newRegistry(t)
	ctx := context.Background()
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterPlugin(ctx, "powercontext", []contract.MetadataAttribute{{Key: "project", Label: "项目", Pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", Marker: "memory-project"}}); err != nil {
		t.Fatal(err)
	}
	return registry
}

func scanInChunks(registry *Registry, text string, size int) []MarkerHit {
	scanner := registry.NewMarkerScanner()
	for start := 0; start < len(text); start += size {
		scanner.Write(text[start:min(start+size, len(text))])
	}
	return scanner.Close()
}

func TestMarkerScannerReadsWholeMarkerLines(t *testing.T) {
	registry := newMarkerRegistry(t)
	for _, test := range []struct {
		name  string
		reply string
		want  []MarkerHit
	}{
		{"a line of its own", "好的。\n  <memory-project>astrlink</memory-project>  \n接着改分页。", []MarkerHit{{Key: "project", Value: "astrlink"}}},
		{"at the very end", "接着改分页。\n<memory-project>none</memory-project>", []MarkerHit{{Key: "project", Value: "none"}}},
		{"only the first line counts", "<memory-project>astrlink</memory-project>\n<memory-project>paseo</memory-project>", []MarkerHit{{Key: "project", Value: "astrlink"}}},
		{"inside a sentence", "我会输出 <memory-project>astrlink</memory-project> 这一行。", nil},
		{"quoted or bolded", "`<memory-project>astrlink</memory-project>`\n**<memory-project>astrlink</memory-project>**", nil},
		{"in fenced code", "示例：\n```xml\n<memory-project>demo</memory-project>\n```\n<memory-project>astrlink</memory-project>", []MarkerHit{
			{Key: "project", Value: "demo", Reason: contract.MetadataIgnoredInCodeBlock},
			{Key: "project", Value: "astrlink"},
		}},
		{"in tilde code", "~~~~\n<memory-project>demo</memory-project>\n~~~\n<memory-project>other</memory-project>\n~~~~", []MarkerHit{
			{Key: "project", Value: "demo", Reason: contract.MetadataIgnoredInCodeBlock},
			{Key: "project", Value: "other", Reason: contract.MetadataIgnoredInCodeBlock},
		}},
		{"an invalid value", "<memory-project>Not Valid</memory-project>\n<memory-project>astrlink</memory-project>", []MarkerHit{
			{Key: "project", Value: "Not Valid", Reason: contract.MetadataIgnoredInvalidValue},
			{Key: "project", Value: "astrlink"},
		}},
		{"mismatched tags", "<memory-project>astrlink</memory-other>", nil},
		{"an attribute without a marker", "<env>prod</env>", nil},
		{"windows line ends", "好的\r\n<memory-project>astrlink</memory-project>\r\n", []MarkerHit{{Key: "project", Value: "astrlink"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, size := range []int{1, 3, 7, len(test.reply)} {
				if got := scanInChunks(registry, test.reply, max(size, 1)); !reflect.DeepEqual(got, test.want) {
					t.Fatalf("chunks of %d: hits = %+v, want %+v", size, got, test.want)
				}
			}
		})
	}
}

func TestMarkerScannerSkipsLongLines(t *testing.T) {
	registry := newMarkerRegistry(t)
	long := strings.Repeat("字", maxMarkerLine) + "<memory-project>astrlink</memory-project>"
	if hits := scanInChunks(registry, long+"\n<memory-project>paseo</memory-project>", 64); !reflect.DeepEqual(hits, []MarkerHit{{Key: "project", Value: "paseo"}}) {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestUserMarkerIsTheWholeMessage(t *testing.T) {
	registry := newMarkerRegistry(t)
	if hit, ok := registry.UserMarker("  <memory-project>paseo</memory-project>\n"); !ok || hit != (MarkerHit{Key: "project", Value: "paseo", User: true}) {
		t.Fatalf("UserMarker = %+v, %v", hit, ok)
	}
	for _, typed := range []string{
		"改成 <memory-project>paseo</memory-project>",
		"<memory-project>paseo</memory-project>\n顺便看看分页",
		"",
	} {
		if hit, ok := registry.UserMarker(typed); ok {
			t.Fatalf("UserMarker(%q) = %+v", typed, hit)
		}
	}
	if hit, ok := registry.UserMarker("<memory-project>Paseo</memory-project>"); !ok || hit.Valid() || hit.Reason != contract.MetadataIgnoredInvalidValue {
		t.Fatalf("an invalid user marker = %+v, %v", hit, ok)
	}
}

func TestReplayRebuildsALostBinding(t *testing.T) {
	registry := newMarkerRegistry(t)
	history := []HistoryEntry{
		{User: true, Text: "把分页改成游标"},
		{Text: "改好了。要把当前项目纳入记忆吗？"},
		{User: true, Text: "纳入，叫 astrlink"},
		{Text: "<memory-project>astrlink</memory-project>\n好的。"},
		// Once bound, the model cannot rebind; the user can.
		{Text: "<memory-project>paseo</memory-project>"},
	}
	if values := registry.Replay(history); !reflect.DeepEqual(values, map[string]MarkerHit{"project": {Key: "project", Value: "astrlink"}}) {
		t.Fatalf("Replay = %+v", values)
	}
	history = append(history, HistoryEntry{User: true, Text: "<memory-project>paseo</memory-project>"})
	if values := registry.Replay(history); values["project"] != (MarkerHit{Key: "project", Value: "paseo", User: true}) {
		t.Fatalf("Replay after the user's marker = %+v", values)
	}
}
