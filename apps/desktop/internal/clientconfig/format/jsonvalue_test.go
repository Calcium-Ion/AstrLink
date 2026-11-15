package format

import "testing"

func TestObjectsKeepTheirMemberOrder(t *testing.T) {
	value := Object{{"version", Number("2")}, {"b", map[string]any{"y": true, "x": nil}}, {"a", Object{}}}
	if got := Encode(value); got != `{"version":2,"b":{"x":null,"y":true},"a":{}}` {
		t.Errorf("compact %s", got)
	}
	want := "{\n  \"version\": 2,\n  \"b\": {\n    \"x\": null,\n    \"y\": true\n  },\n  \"a\": {}\n}"
	if got := EncodePretty(value); got != want {
		t.Errorf("pretty %q", got)
	}
}

func TestInvalidJSONNamesThePositionSerdeDoes(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		// At the end of the text the column is the last character's.
		{`{"env": `, "is not valid JSON (line 1, column 8)"},
		{"{\n  \"a\": 1,\n", "is not valid JSON (line 3, column 0)"},
		{`{"a" 1}`, "is not valid JSON (line 1, column 6)"},
	} {
		_, err := ParseJSON(tc.text, JSONFormat{})
		if invalid, ok := err.(*Invalid); !ok || invalid.Message != tc.want {
			t.Errorf("%q: %v", tc.text, err)
		}
	}
}
