package privacy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func BenchmarkExtractDocumentSkippedImage(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello world"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", size) + `"}}]}]}`)
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				_, segments, err := extractDocument(contract.ProtocolOpenAIChat, body, InspectionOptions{})
				if err != nil || len(segments) != 1 {
					b.Fatalf("segments %d, error %v", len(segments), err)
				}
			}
		})
	}
}

func BenchmarkTextRestorerCode(b *testing.B) {
	input := strings.Repeat(`<p class="row">Render a private result & display ordinary text.</p>`+"\n", 4096)
	for _, count := range []int{10, 100, 1000} {
		restorer := NewTextRestorer(performanceRedactions(b, count), ValuePlain)
		b.Run(fmt.Sprintf("%d_mappings", count), func(b *testing.B) {
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			for b.Loop() {
				output, count, hold := restorer.Restore(input, true)
				if output != input || count != 0 || hold != 0 {
					b.Fatal("unexpected restore")
				}
			}
		})
	}
}

func BenchmarkNewTextRestorer(b *testing.B) {
	redactions := performanceRedactions(b, 1000)
	b.ReportAllocs()
	for b.Loop() {
		if restorer := NewTextRestorer(redactions, ValuePlain); restorer.Len() != len(redactions) {
			b.Fatal("missing mappings")
		}
	}
}
