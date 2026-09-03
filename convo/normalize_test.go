package convo

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestNormalizeTextRules(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		strip bool
		want  string
	}{
		{"collapses whitespace", "  Hello \n\t world  ", true, "Hello world"},
		{"unicode whitespace", "你好\u3000世界\u00a0！", true, "你好 世界 ！"},
		{"strips leading think", "<think>\nreasoning here\n</think>\n\nAnswer.", true, "Answer."},
		{"strips leading think after whitespace", "  \n<think>x</think>Answer", true, "Answer"},
		{"keeps think when disabled", "<think>x</think>Answer", false, "<think>x</think>Answer"},
		{"keeps non-leading think", "Answer <think>x</think>", true, "Answer <think>x</think>"},
		{"unclosed think yields nothing", "<think>never closed", true, ""},
		{"think prefix that is not a tag", "<thin", true, "<thin"},
		{"think lookalike", "<thinking>x</thinking>", true, "<thinking>x</thinking>"},
		{"only think block", "<think>just thoughts</think>", true, ""},
		{"empty", "   \n ", true, ""},
		{"invalid utf8 becomes replacement", "a\xffb", true, "a\uFFFDb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeText(tc.in, tc.strip); got != tc.want {
				t.Fatalf("NormalizeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizerRunesAndDigest(t *testing.T) {
	n := NewTextNormalizer(true)
	if n.Digest() != nil {
		t.Fatal("empty normalizer must have nil digest")
	}
	n.WriteString("<think>ignored</think>  Hello   世界 ")
	if got := n.Runes(); got != 8 {
		t.Fatalf("Runes = %d, want 8", got)
	}
	want := DigestText("Hello 世界", false)
	if !bytes.Equal(n.Digest(), want) {
		t.Fatal("digest differs from reference text digest")
	}
	// Digest must be repeatable and must not disturb later writes.
	if !bytes.Equal(n.Digest(), want) {
		t.Fatal("second Digest call changed result")
	}
	n.WriteString("!")
	if bytes.Equal(n.Digest(), want) {
		t.Fatal("digest did not change after writing more text")
	}
	n.Reset()
	if n.Digest() != nil || n.Runes() != 0 {
		t.Fatal("Reset did not clear state")
	}
}

// TestNormalizerChunkingInvariance is the streaming guarantee: any split of
// the input, including inside UTF-8 sequences and inside <think> tags, yields
// the same digest as feeding the text at once.
func TestNormalizerChunkingInvariance(t *testing.T) {
	texts := []string{
		"<think>\n用户想要一句话介绍 goroutine，简短回答。\n</think>\n\nGoroutine 是 Go 运行时调度的轻量级线程。",
		"  \t<think>a</think>   Hello,\n\n  wörld   ✨  ",
		"plain ascii with   spaces and <thin fake tag",
		"</think> stray close then <think>inner</think> text",
		"日本語のテキスト、改行\nと\tタブ",
	}
	for _, text := range texts {
		want := DigestText(text, true)
		data := []byte(text)
		for split := 0; split <= len(data); split++ {
			n := NewTextNormalizer(true)
			_, _ = n.Write(data[:split])
			_, _ = n.Write(data[split:])
			if !bytes.Equal(n.Digest(), want) {
				t.Fatalf("split at %d of %q changed digest", split, text)
			}
		}
		rng := rand.New(rand.NewSource(42))
		for round := 0; round < 50; round++ {
			n := NewTextNormalizer(true)
			rest := data
			for len(rest) > 0 {
				size := 1 + rng.Intn(len(rest))
				_, _ = n.Write(rest[:size])
				rest = rest[size:]
			}
			if !bytes.Equal(n.Digest(), want) {
				t.Fatalf("random chunking changed digest for %q", text)
			}
		}
	}
}

func TestNormalizerWithoutStripDiffersFromStrip(t *testing.T) {
	text := "<think>x</think>Answer that is long enough to matter"
	if bytes.Equal(DigestText(text, true), DigestText(text, false)) {
		t.Fatal("strip setting must affect digest")
	}
}
