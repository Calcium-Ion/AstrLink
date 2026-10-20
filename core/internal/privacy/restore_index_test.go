package privacy

import (
	"fmt"
	"strings"
	"testing"
)

func TestTextRestorerIndexPreservesStreamingAndPriority(t *testing.T) {
	body := strings.Trim(testPersonMarker, "<>")
	redactions := []Redaction{
		{Placeholder: testPersonMarker, Value: "A & B"},
		{Placeholder: body, Value: "second mapping"},
		{Placeholder: "prefix", Value: "short"},
		{Placeholder: "prefix-long", Value: "long"},
	}
	inputs := []string{
		"ordinary HTML <p>prefix-long & text</p>",
		"Hello " + testPersonMarker + ".",
		"Hello " + body + ".",
		"Hello &LT;" + body + "&#X3E;.",
		"Hello &#x3C;" + body + "&GT;.",
		"Hello %3C" + body + "%3E.",
		`Hello \U003C` + body + `\u003e.`,
		`Hello \\u003c` + body + `\u003e.`,
		"x" + body + "x",
		"&lt;" + strings.ToLower(body) + "&gt;",
	}
	for _, encoding := range []ValueEncoding{ValuePlain, ValueJSONString} {
		for _, reverse := range []bool{false, true} {
			ordered := append([]Redaction(nil), redactions...)
			if reverse {
				ordered[0], ordered[1] = ordered[1], ordered[0]
			}
			restorer := NewTextRestorer(ordered, encoding)
			for _, input := range inputs {
				for split := 0; split <= len(input); split++ {
					for _, final := range []bool{false, true} {
						prefix := input[:split]
						got, count, hold := restorer.Restore(prefix, final)
						want, wantCount, wantHold := referenceTextRestore(restorer, prefix, final)
						if got != want || count != wantCount || hold != wantHold {
							t.Fatalf("input %q final %t: (%q, %d, %d), want (%q, %d, %d)", prefix, final, got, count, hold, want, wantCount, wantHold)
						}
					}
				}
			}
		}
	}
}

func TestTextRestorerIndexFindsMappingsWithSharedPrefixes(t *testing.T) {
	redactions := performanceRedactions(t, 1000)
	restorer := NewTextRestorer(redactions, ValuePlain)
	for _, position := range []int{0, 500, 999} {
		redaction := redactions[position]
		input := "<p>" + redaction.Placeholder + "</p>"
		got, count, hold := restorer.Restore(input, true)
		if want := "<p>" + redaction.Value + "</p>"; got != want || count != 1 || hold != 0 {
			t.Fatalf("mapping %d: got (%q, %d, %d), want %q", position, got, count, hold, want)
		}
	}
}

func FuzzTextRestorerIndex(f *testing.F) {
	body := strings.Trim(testPersonMarker, "<>")
	for _, seed := range []string{
		testPersonMarker, body, "&LT;" + body + "&GT;", `%3C` + body,
		`\u003C` + body + `\u003e`, "x" + body, "prefix-long", "<p>&amp; text</p>",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	restorer := NewTextRestorer([]Redaction{
		{Placeholder: testPersonMarker, Value: "first"},
		{Placeholder: body, Value: "second"},
		{Placeholder: "prefix", Value: "short"},
		{Placeholder: "prefix-long", Value: "long"},
	}, ValuePlain)
	f.Fuzz(func(t *testing.T, input string, final bool) {
		got, count, hold := restorer.Restore(input, final)
		want, wantCount, wantHold := referenceTextRestore(restorer, input, final)
		if got != want || count != wantCount || hold != wantHold {
			t.Fatalf("got (%q, %d, %d), want (%q, %d, %d)", got, count, hold, want, wantCount, wantHold)
		}
	})
}

// This exhaustive reference retains the original matching order and partial
// precedence, independent of prefix indexing and its shared start guards.
func referenceTextRestore(restorer *TextRestorer, input string, final bool) (string, int, int) {
	var out strings.Builder
	flushed, count := 0, 0
	for at := 0; at < len(input); at++ {
		bestEnd := -1
		var best *restoreSpelling
		partial := false
		for index := range restorer.spellings {
			spelling := &restorer.spellings[index]
			end, status := spelling.match(input, at, final)
			if status == matchPartial {
				partial = true
			}
			if status == matchFull && end > bestEnd {
				bestEnd, best = end, spelling
			}
		}
		if partial {
			out.WriteString(input[flushed:at])
			return out.String(), count, len(input) - at
		}
		if best == nil {
			continue
		}
		out.WriteString(input[flushed:at])
		out.WriteString(best.value)
		flushed, at = bestEnd, bestEnd-1
		count++
	}
	if count == 0 {
		return input, 0, 0
	}
	out.WriteString(input[flushed:])
	return out.String(), count, 0
}

func performanceRedactions(t testing.TB, count int) []Redaction {
	t.Helper()
	allocator := newPlaceholderAllocator([]byte("performance-regression-test-key"), nil, nil)
	redactions := make([]Redaction, count)
	for index := range redactions {
		value := fmt.Sprintf("person%04d@example.com", index)
		placeholder, style, err := allocator.allocate(KindEmail, value)
		if err != nil {
			t.Fatal(err)
		}
		redactions[index] = Redaction{Placeholder: placeholder, Value: value, Kind: KindEmail, Style: style}
	}
	return redactions
}
