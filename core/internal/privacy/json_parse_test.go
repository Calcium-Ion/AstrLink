package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestPrivacyJSONMatchesStandardDecoder(t *testing.T) {
	for _, body := range []string{
		`{"message":"hello","empty":[],"object":{},"bool":[true,false,null],"numbers":[-0,1.25,1e-20,123456789012345678901234567890]}`,
		` { "escaped": "a\\b\"c\n\t\/", "unicode": "\ud83d\ude00\ud800\ufffd", "nested": [ [1], {"x":2} ] } `,
		"{\"invalid-utf8\":\"\xff\",\"\xfe\":1}",
		`null`, `true`, `123e4`, `"hello"`, `[]`, `{}`,
	} {
		t.Run(body, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(body))
			decoder.UseNumber()
			var want any
			if err := decoder.Decode(&want); err != nil {
				t.Fatal(err)
			}
			got, duplicate, err := parsePrivacyJSON([]byte(body), true)
			if err != nil || duplicate || !reflect.DeepEqual(got, want) {
				t.Fatalf("parsed = %#v, duplicate = %t, error = %v; want %#v", got, duplicate, err, want)
			}
		})
	}
}

func TestPrivacyJSONChecksSkippedValues(t *testing.T) {
	for _, value := range []string{
		`{"same":1,"same":2}`,
		`{"same":1,"s\u0061me":2}`,
		`[{"same":1,"same":2}]`,
		`{"image":"\x"}`,
		`[1,]`,
		`{"x":NaN}`,
		`{"x":01}`,
		`{"x":1} true`,
		strings.Repeat("[", maxJSONDepth) + "0" + strings.Repeat("]", maxJSONDepth),
	} {
		t.Run(value, func(t *testing.T) {
			// image_url is skipped by inspection, but its entire JSON value
			// must still satisfy the same grammar, duplicate, and depth rules.
			body := []byte(`{"messages":[{"role":"user","content":"hello","image_url":` + value + `}]}`)
			if _, _, err := extractDocument(contract.ProtocolOpenAIChat, body, InspectionOptions{}); !errors.Is(err, ErrUnsafeInput) {
				t.Fatalf("unsafe media input was accepted: %v", err)
			}
		})
	}
}

func TestPrivacyJSONDepthBoundaryAndEscapedDuplicates(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		accepted := strings.Repeat("[", maxJSONDepth) + "0" + strings.Repeat("]", maxJSONDepth)
		if _, _, err := parsePrivacyJSON([]byte(accepted), materialize); err != nil {
			t.Fatalf("maximum permitted depth rejected: %v", err)
		}
		if _, _, err := parsePrivacyJSON([]byte("["+accepted+"]"), materialize); !errors.Is(err, ErrUnsafeInput) {
			t.Fatalf("excess depth accepted: %v", err)
		}
		for _, body := range []string{`{"same":1,"s\u0061me":2}`, `{"\ud800":1,"\ufffd":2}`, "{\"\xff\":1,\"\xfe\":2}"} {
			if _, duplicate, err := parsePrivacyJSON([]byte(body), materialize); err != nil || !duplicate {
				t.Fatalf("decoded duplicate was missed: duplicate = %t, error = %v", duplicate, err)
			}
		}
	}
}

// The reference uses the original standard-library Token approach, independent
// of the cursor and its grammar validator. Fuzzing compares both structure
// checks and the materialized value, including malformed and escaped input.
func FuzzPrivacyJSON(f *testing.F) {
	for _, seed := range []string{
		`{"messages":[{"content":"a\\b\u1234","role":"user"}],"image":"AAAA"}`,
		`{"a":1,"\u0061":2}`, `{"x":[true,null,-0.3e12]}`, `"\ud800"`, `[]`, `{"x":}`, "\xff",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		wantDuplicate, wantErr := referencePrivacyJSONStructure(decoder, 0)
		if wantErr == nil {
			if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
				wantErr = ErrUnsafeInput
			}
		}
		for _, materialize := range []bool{false, true} {
			got, duplicate, err := parsePrivacyJSON(body, materialize)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("error = %v; reference error = %v", err, wantErr)
			}
			if err != nil {
				continue
			}
			if duplicate != wantDuplicate {
				t.Fatalf("duplicate = %t; reference duplicate = %t", duplicate, wantDuplicate)
			}
			if materialize {
				decoder := json.NewDecoder(bytes.NewReader(body))
				decoder.UseNumber()
				var want any
				if err := decoder.Decode(&want); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("parsed = %#v; reference = %#v, error = %v", got, want, err)
				}
			}
		}
	})
}

func referencePrivacyJSONStructure(decoder *json.Decoder, depth int) (bool, error) {
	if depth > maxJSONDepth {
		return false, ErrUnsafeInput
	}
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return false, nil
	}
	seen := make(map[string]struct{})
	duplicate := false
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return false, err
			}
			name, ok := key.(string)
			if !ok {
				return false, ErrUnsafeInput
			}
			if _, exists := seen[name]; exists {
				duplicate = true
			}
			seen[name] = struct{}{}
		}
		child, err := referencePrivacyJSONStructure(decoder, depth+1)
		if err != nil {
			return false, err
		}
		duplicate = duplicate || child
	}
	end, err := decoder.Token()
	if err != nil || (delimiter == '{' && end != json.Delim('}')) ||
		(delimiter == '[' && end != json.Delim(']')) {
		return false, ErrUnsafeInput
	}
	return duplicate, nil
}
