package convo

import (
	"bytes"
	"crypto/sha256"
	"hash"
	"io"
	"unicode"
	"unicode/utf8"
)

// NormalizationVersion is embedded in every fingerprint. Any change to
// TextNormalizer's observable output must increment it so digests produced
// under different rules are never compared.
const NormalizationVersion = 1

var (
	thinkOpen  = []byte("<think>")
	thinkClose = []byte("</think>")
)

type thinkState uint8

const (
	// thinkOff: stripping disabled, or the decision has been made and text
	// flows through normally.
	thinkOff thinkState = iota
	// thinkUndecided: only whitespace seen so far; buffering to test for a
	// leading "<think>".
	thinkUndecided
	// thinkInside: consuming a leading think block until "</think>".
	thinkInside
)

// TextNormalizer folds visible text into a canonical byte stream and hashes it
// incrementally. It is safe to feed arbitrary chunk boundaries, including
// splits inside a UTF-8 sequence or inside a <think> tag.
//
// Rules (NormalizationVersion 1):
//   - runs of Unicode whitespace collapse to one ASCII space;
//   - leading and trailing whitespace are removed;
//   - when enabled, one leading <think>...</think> block is dropped;
//   - invalid UTF-8 bytes become U+FFFD.
//
// No text is retained; only the hash state and a few bytes of lookbehind.
type TextNormalizer struct {
	sink         io.Writer
	digest       hash.Hash
	runes        int
	started      bool
	pendingSpace bool
	partial      []byte
	think        thinkState
	thinkBuf     []byte
	stripThink   bool
}

// NewTextNormalizer returns a normalizer that hashes with SHA-256.
func NewTextNormalizer(stripLeadingThink bool) *TextNormalizer {
	digest := sha256.New()
	normalizer := &TextNormalizer{sink: digest, digest: digest, stripThink: stripLeadingThink}
	normalizer.Reset()
	return normalizer
}

func newTextNormalizerTo(sink io.Writer, stripLeadingThink bool) *TextNormalizer {
	normalizer := &TextNormalizer{sink: sink, stripThink: stripLeadingThink}
	normalizer.Reset()
	return normalizer
}

// Reset clears all state so the normalizer can hash a new text.
func (normalizer *TextNormalizer) Reset() {
	if normalizer.digest != nil {
		normalizer.digest.Reset()
	}
	normalizer.runes = 0
	normalizer.started = false
	normalizer.pendingSpace = false
	normalizer.partial = normalizer.partial[:0]
	normalizer.thinkBuf = normalizer.thinkBuf[:0]
	if normalizer.stripThink {
		normalizer.think = thinkUndecided
	} else {
		normalizer.think = thinkOff
	}
}

// Write feeds a chunk of text. It never returns an error.
func (normalizer *TextNormalizer) Write(chunk []byte) (int, error) {
	data := chunk
	if len(normalizer.partial) > 0 {
		data = append(normalizer.partial, chunk...)
		normalizer.partial = normalizer.partial[:0]
	}
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			normalizer.partial = append(normalizer.partial[:0], data...)
			break
		}
		r, size := utf8.DecodeRune(data)
		normalizer.consume(r, data[:size])
		data = data[size:]
	}
	return len(chunk), nil
}

// WriteString is Write for strings.
func (normalizer *TextNormalizer) WriteString(text string) {
	_, _ = normalizer.Write([]byte(text))
}

func (normalizer *TextNormalizer) consume(r rune, raw []byte) {
	switch normalizer.think {
	case thinkUndecided:
		if len(normalizer.thinkBuf) == 0 && unicode.IsSpace(r) {
			return
		}
		normalizer.thinkBuf = append(normalizer.thinkBuf, raw...)
		if bytes.Equal(normalizer.thinkBuf, thinkOpen) {
			normalizer.think = thinkInside
			normalizer.thinkBuf = normalizer.thinkBuf[:0]
			return
		}
		if bytes.HasPrefix(thinkOpen, normalizer.thinkBuf) {
			return
		}
		normalizer.flushUndecided()
	case thinkInside:
		normalizer.thinkBuf = append(normalizer.thinkBuf, raw...)
		if bytes.HasSuffix(normalizer.thinkBuf, thinkClose) {
			normalizer.think = thinkOff
			normalizer.thinkBuf = normalizer.thinkBuf[:0]
			return
		}
		if excess := len(normalizer.thinkBuf) - (len(thinkClose) - 1); excess > 0 {
			normalizer.thinkBuf = append(normalizer.thinkBuf[:0], normalizer.thinkBuf[excess:]...)
		}
	default:
		normalizer.emit(r, raw)
	}
}

// flushUndecided replays bytes buffered while testing for "<think>" through
// the normal path and stops testing.
func (normalizer *TextNormalizer) flushUndecided() {
	normalizer.think = thinkOff
	buffered := normalizer.thinkBuf
	normalizer.thinkBuf = nil
	for len(buffered) > 0 {
		r, size := utf8.DecodeRune(buffered)
		normalizer.emit(r, buffered[:size])
		buffered = buffered[size:]
	}
}

func (normalizer *TextNormalizer) emit(r rune, raw []byte) {
	if unicode.IsSpace(r) {
		if normalizer.started {
			normalizer.pendingSpace = true
		}
		return
	}
	if normalizer.pendingSpace {
		_, _ = normalizer.sink.Write([]byte{' '})
		normalizer.runes++
		normalizer.pendingSpace = false
	}
	if r == utf8.RuneError && len(raw) == 1 {
		_, _ = normalizer.sink.Write([]byte("\uFFFD"))
	} else {
		_, _ = normalizer.sink.Write(raw)
	}
	normalizer.runes++
	normalizer.started = true
}

// finalize resolves state that only an end-of-text can settle: a buffered
// "<thin" that never became a tag is ordinary text; an unfinished think block
// contributes nothing; a dangling partial rune becomes U+FFFD.
func (normalizer *TextNormalizer) finalize() {
	if len(normalizer.partial) > 0 {
		partial := normalizer.partial
		normalizer.partial = nil
		normalizer.consume(utf8.RuneError, partial[:1])
	}
	if normalizer.think == thinkUndecided && len(normalizer.thinkBuf) > 0 {
		normalizer.flushUndecided()
	}
}

// Runes returns how many runes the normalized text contains so far.
func (normalizer *TextNormalizer) Runes() int {
	normalizer.finalize()
	return normalizer.runes
}

// Digest returns the SHA-256 of the normalized text, or nil when the text is
// empty after normalization. Calling Digest does not disturb further Writes.
func (normalizer *TextNormalizer) Digest() []byte {
	normalizer.finalize()
	if normalizer.runes == 0 || normalizer.digest == nil {
		return nil
	}
	return normalizer.digest.Sum(nil)
}

// NormalizeText returns the normalized form of text as a string. It is the
// non-streaming reference for TextNormalizer and is mainly useful in tests.
func NormalizeText(text string, stripLeadingThink bool) string {
	var buffer bytes.Buffer
	normalizer := newTextNormalizerTo(&buffer, stripLeadingThink)
	normalizer.WriteString(text)
	normalizer.finalize()
	return buffer.String()
}

// DigestText returns the SHA-256 digest NormalizeText's output would hash to,
// or nil for text that normalizes to nothing.
func DigestText(text string, stripLeadingThink bool) []byte {
	normalizer := NewTextNormalizer(stripLeadingThink)
	normalizer.WriteString(text)
	return normalizer.Digest()
}
