package privacy

import "sort"

type restoreGuard uint8

const (
	restoreUnrestricted restoreGuard = iota
	restoreWordStart
	restoreUnescapedBackslash
	restoreGuardCount
)

func (guard restoreGuard) allows(input string, at int) bool {
	switch guard {
	case restoreWordStart:
		return at == 0 || !isASCIIAlphanumeric(input[at-1])
	case restoreUnescapedBackslash:
		return precedingBackslashes(input, at)%2 == 0
	default:
		return true
	}
}

type restorePrefix struct {
	text     string
	spelling int
}

// restorePrefixIndex is a sorted, compact prefix tree. At each input byte a
// binary search narrows the matching interval, so ordinary '<' or '&' text
// does not invoke every marker's matcher. Only prefixes that actually match
// reach the original matcher, which still owns endings and syntax rules.
type restorePrefixIndex struct {
	byStart [256][]restorePrefix
}

func (index *restorePrefixIndex) add(prefix string, spelling int) {
	index.byStart[prefix[0]] = append(index.byStart[prefix[0]], restorePrefix{prefix, spelling})
}

func (index *restorePrefixIndex) sort() {
	for _, prefixes := range index.byStart {
		sort.Slice(prefixes, func(left, right int) bool {
			if prefixes[left].text != prefixes[right].text {
				return prefixes[left].text < prefixes[right].text
			}
			return prefixes[left].spelling < prefixes[right].spelling
		})
	}
}

func (index *restorePrefixIndex) match(
	input string,
	at int,
	final bool,
	spellings []restoreSpelling,
) (bestIndex, bestEnd int, partial bool) {
	bestIndex, bestEnd = -1, -1
	prefixes := index.byStart[input[at]]
	for depth := 1; len(prefixes) > 0; depth++ {
		// A shorter prefix sorts before its extensions. Keep all terminal
		// matches: the longest full spelling wins, with original insertion
		// order breaking ties, and any partial spelling holds the text.
		for len(prefixes) > 0 && len(prefixes[0].text) == depth {
			candidate := prefixes[0].spelling
			end, status := spellings[candidate].match(input, at, final)
			if status == matchPartial {
				return -1, -1, true
			}
			if status == matchFull && (end > bestEnd ||
				(end == bestEnd && candidate < bestIndex)) {
				bestIndex, bestEnd = candidate, end
			}
			prefixes = prefixes[1:]
		}
		if len(prefixes) == 0 {
			break
		}
		if at+depth == len(input) {
			// All remaining prefixes extend the exact input suffix. The
			// caller already checked the shared start guard; every such
			// spelling is partial, without visiting all its descendants.
			return bestIndex, bestEnd, !final
		}
		character := input[at+depth]
		first := sort.Search(len(prefixes), func(position int) bool {
			return prefixes[position].text[depth] >= character
		})
		prefixes = prefixes[first:]
		end := sort.Search(len(prefixes), func(position int) bool {
			return prefixes[position].text[depth] > character
		})
		prefixes = prefixes[:end]
	}
	return bestIndex, bestEnd, false
}

type markerEscapes struct {
	opens, closes, prefixes []string
}

// Escape introducers fold ASCII case, while marker bodies are case-sensitive.
// Expanding only the few characters in the introducer keeps the index exact:
// a suffix inside a prefix is truly partial, never just a case-folded near miss.
// The expansion is shared by all markers and requests.
func newMarkerEscapes(opens, closes []string) markerEscapes {
	var prefixes []string
	for _, open := range opens {
		variants := []string{""}
		for position := range len(open) {
			character := open[position]
			count := len(variants)
			for index := range count {
				prefix := variants[index]
				variants[index] += string(character)
				lower := asciiLower(character)
				if lower >= 'a' && lower <= 'z' {
					other := lower
					if character == lower {
						other = lower - ('a' - 'A')
					}
					variants = append(variants, prefix+string(other))
				}
			}
		}
		prefixes = append(prefixes, variants...)
	}
	return markerEscapes{opens: opens, closes: closes, prefixes: prefixes}
}
