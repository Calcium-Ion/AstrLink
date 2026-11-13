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
