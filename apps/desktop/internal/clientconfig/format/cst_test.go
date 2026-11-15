package format

import (
	"strings"
	"testing"
)

// The jsonc-parser crate's own CST tests that cover what the editor uses,
// so the port inserts and removes as the crate does.

func buildCST(t *testing.T, text string) *cstNode {
	t.Helper()
	root, err := parseJSONText(text, anySyntax, true)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	if got := root.String(); got != text {
		t.Fatalf("round trip of %q gave %q", text, got)
	}
	return root
}

func input(t *testing.T, text string) cstInput {
	t.Helper()
	value, err := DecodeJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	return cstInputOf(value, NoRank)
}

func TestCSTRemoveProperties(t *testing.T) {
	for _, tc := range []struct{ name, json, want string }{
		{"value2", "{\n    \"value\": 5,\n    // comment\n    \"value2\": \"hello\",\n    value3: true\n}",
			"{\n    \"value\": 5,\n    // comment\n    value3: true\n}"},
		{"value2", "{\n    \"value\": 5,\n    // comment\n    \"value2\": \"hello\"\n    ,value3: true\n}",
			"{\n    \"value\": 5,\n    // comment\nvalue3: true\n}"},
		{"value", `{ "value": 5 }`, `{}`},
		{"value", `{ "value": 5, "value2": 6 }`, `{ "value2": 6 }`},
		{"value2", `{ "value": 5, "value2": 6 }`, `{ "value": 5 }`},
	} {
		cst := buildCST(t, tc.json)
		cst.rootValue().get(tc.name).remove()
		if got := cst.String(); got != tc.want {
			t.Errorf("remove %s from %q:\ngot  %q\nwant %q", tc.name, tc.json, got, tc.want)
		}
	}
}

func TestCSTInsertProperties(t *testing.T) {
	for _, tc := range []struct {
		index             int
		name, value, json string
		want              string
	}{
		{0, "propName", `[1]`, `{}`, "{\n  \"propName\": [1]\n}"},
		{0, "value0", `[1]`, "{\n    \"value1\": 5\n}", "{\n    \"value0\": [1],\n    \"value1\": 5\n}"},
		{0, "value0", `[1]`, "{\n    // some comment\n    \"value1\": 5\n}",
			"{\n    \"value0\": [1],\n    // some comment\n    \"value1\": 5\n}"},
		{1, "value1", `{"value": 1}`, "{\n    \"value0\": 5 // comment\n}",
			"{\n    \"value0\": 5, // comment\n    \"value1\": {\n        \"value\": 1\n    }\n}"},
		{1, "propName", `true`, "{\n  \"value\": 4,\n}", "{\n  \"value\": 4,\n  \"propName\": true,\n}"},
		{1, "propName", `true`, `{ "value": 4 }`, "{\n  \"value\": 4,\n  \"propName\": true\n}"},
		{1, "propName", `true`, `{ "value": 4, }`, "{\n  \"value\": 4,\n  \"propName\": true,\n}"},
	} {
		cst := buildCST(t, tc.json)
		cst.rootValue().insertProp(tc.index, tc.name, input(t, tc.value))
		if got := cst.String(); got != tc.want {
			t.Errorf("insert into %q:\ngot  %q\nwant %q", tc.json, got, tc.want)
		}
	}
}

func TestCSTRemoveArrayElements(t *testing.T) {
	for _, tc := range []struct {
		index      int
		json, want string
	}{
		{0, "[\n      1,\n]", "[]"},
		{0, "[\n      1,\n      2,\n]", "[\n      2,\n]"},
		{1, "[\n      1, // other comment\n      2, // trailing comment\n]", "[\n      1, // other comment\n]"},
		{1, "[\n      1, // comment\n      2\n]", "[\n      1 // comment\n]"},
		{1, `[1, 2]`, `[1]`},
		{1, `[ 1, 2 /* test */ ]`, `[ 1 ]`},
		{1, `[1, /* test */ 2]`, `[1]`},
		{1, `[1 /* a */, /* b */ 2 /* c */, /* d */ true]`, `[1 /* a */, /* d */ true]`},
	} {
		cst := buildCST(t, tc.json)
		cst.rootValue().elements()[tc.index].remove()
		if got := cst.String(); got != tc.want {
			t.Errorf("remove %d from %q:\ngot  %q\nwant %q", tc.index, tc.json, got, tc.want)
		}
	}
}

func TestCSTInsertArrayElements(t *testing.T) {
	for _, tc := range []struct {
		index             int
		value, json, want string
	}{
		{0, `[1]`, `[]`, `[[1]]`},
		{0, `[1, true, false, {}]`, `[]`, `[[1, true, false, {}]]`},
		{0, `10`, `[]`, `[10]`},
		{0, `10`, `[1]`, `[10, 1]`},
		{1, `10`, `[1]`, `[1, 10]`},
		{0, `10`, "[\n    1\n]", "[\n    10,\n    1\n]"},
		{0, `10`, "[\n    /* test */ 1\n]", "[\n    10,\n    /* test */ 1\n]"},
		{0, `{"value": 1}`, `[]`, "[\n  {\n    \"value\": 1\n  }\n]"},
		{0, `{"value": 1}`, "[\n    // comment\n]", "[\n    // comment\n    {\n        \"value\": 1\n    }\n]"},
		{0, `{"value": 1}`, "[\n\n]", "[\n  {\n    \"value\": 1\n  }\n]"},
	} {
		cst := buildCST(t, tc.json)
		cst.rootValue().insertElement(tc.index, input(t, tc.value))
		if got := cst.String(); got != tc.want {
			t.Errorf("insert %s into %q:\ngot  %q\nwant %q", tc.value, tc.json, got, tc.want)
		}
	}
	cst := buildCST(t, "{\n    \"prop\": [\n      1,\n      2,\n    ]\n}")
	cst.rootValue().get("prop").propValue().appendElement(input(t, "3"))
	if got, want := cst.String(), "{\n    \"prop\": [\n      1,\n      2,\n      3,\n    ]\n}"; got != want {
		t.Errorf("append with trailing commas:\ngot  %q\nwant %q", got, want)
	}
	cst = buildCST(t, "{\n  \"servers\": [\n    {\"name\": \"linear\"},\n    {\"name\": \"supabase\"}\n  ]\n}")
	servers := cst.rootValue().get("servers").propValue()
	servers.appendElement(input(t, `{"name": "github"}`))
	for _, element := range servers.elements() {
		if element.kind != cstObject {
			t.Errorf("element %q is not an object", element.String())
		}
	}
}

func TestCSTRemoveComment(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{"{\n    \"value\": 5,\n    // comment\n    \"value2\": \"hello\",\n    value3: true\n}",
			"{\n    \"value\": 5,\n    \"value2\": \"hello\",\n    value3: true\n}"},
		{"{\n    \"value\": 5,  // comment\n    \"value2\": \"hello\",\n    value3: true\n}",
			"{\n    \"value\": 5,\n    \"value2\": \"hello\",\n    value3: true\n}"},
	} {
		cst := buildCST(t, tc.json)
		for _, child := range cst.rootValue().children {
			if child.isComment() {
				child.remove()
				break
			}
		}
		if got := cst.String(); got != tc.want {
			t.Errorf("remove comment from %q:\ngot  %q\nwant %q", tc.json, got, tc.want)
		}
	}
}

func TestCSTObjectValueOrCreate(t *testing.T) {
	for text, want := range map[string]string{
		"":                         "{}\n",
		"// Copyright something":   "// Copyright something\n{}\n",
		"// Copyright something\n": "// Copyright something\n{}\n",
		`{ "value": 1 }`:           `{ "value": 1 }`,
	} {
		cst := buildCST(t, text)
		cst.rootObjectOrSet()
		if got := cst.String(); got != want {
			t.Errorf("object of %q: got %q, want %q", text, got, want)
		}
	}
	cst := buildCST(t, "")
	object := cst.rootObjectOrSet()
	if got := object.objectValueOrCreate("prop").String(); got != "{}" {
		t.Errorf("created %q", got)
	}
	object.appendProp("prop2", cstInput{kind: inputArray})
	if got, want := cst.String(), "{\n  \"prop\": {},\n  \"prop2\": []\n}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCSTEnsureMultiline(t *testing.T) {
	for text, want := range map[string]string{
		`[]`:                             "[\n]",
		`[   ]`:                          "[\n]",
		`[  /* test */  ]`:               "[\n  /* test */\n]",
		`[  1,   2, /* test */ 3  ]`:     "[\n  1,\n  2,\n  /* test */ 3\n]",
		"[  1,   2, /* test */ 3  ]\r\n": "[\r\n  1,\r\n  2,\r\n  /* test */ 3\r\n]\r\n",
		`{}`:                             "{\n}",
		`{   }`:                          "{\n}",
		`{  /* test */  }`:               "{\n  /* test */\n}",
		`{  prop: 1,   prop2: 2, /* test */ prop3: 3  }`: "{\n  prop: 1,\n  prop2: 2,\n  /* test */ prop3: 3\n}",
	} {
		cst := buildCST(t, text)
		ensureMultiline(cst.rootValue())
		if got := cst.String(); got != want {
			t.Errorf("multiline %q:\ngot  %q\nwant %q", text, got, want)
		}
	}
	for _, text := range []string{
		"{\n  \"prop\": {\n    \"value\": [  1,   2, /* test */ 3  ]\n  }\n}",
		"{\n  \"prop\": {\n    \"value\": {  prop: 1,   prop2: 2, /* test */ prop3: 3  }\n  }\n}",
	} {
		cst := buildCST(t, text)
		ensureMultiline(cst.rootValue().get("prop").propValue().get("value").propValue())
		want := "{\n  \"prop\": {\n    \"value\": [\n      1,\n      2,\n      /* test */ 3\n    ]\n  }\n}"
		if strings.Contains(text, "prop3") {
			want = "{\n  \"prop\": {\n    \"value\": {\n      prop: 1,\n      prop2: 2,\n      /* test */ prop3: 3\n    }\n  }\n}"
		}
		if got := cst.String(); got != want {
			t.Errorf("multiline deep:\ngot  %q\nwant %q", got, want)
		}
	}
}

func TestCSTEscapedStrings(t *testing.T) {
	for value, want := range map[string]string{
		`a\b`:        `"a\\b"`,
		"a\x01\x1f":  `"a\u0001\u001f"`,
		"tab\tnl\n":  `"tab\tnl\n"`,
		`say "hi" \`: `"say \"hi\" \\"`,
		"\u007f":     `"\u007f"`,
	} {
		if got := escapedString(value).text; got != want {
			t.Errorf("escape %q: got %s, want %s", value, got, want)
		}
	}
}

func TestCSTLoneCarriageReturnEndsLineComment(t *testing.T) {
	cst := buildCST(t, "{\r// a\r\"b\": 1}")
	if value, _ := cstValue(cst.rootValue()); Encode(value) != `{"b":1}` {
		t.Fatalf("value = %s", Encode(value))
	}
}

func TestCSTSetValues(t *testing.T) {
	cst := buildCST(t, "")
	object := cst.rootObjectOrSet()
	object.appendProp("test", cstInput{kind: inputArray})
	prop := object.get("test")
	prop.setPropValue(cstInput{kind: inputObject})
	if got, want := cst.String(), "{\n  \"test\": {}\n}\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	prop.setPropValue(cstInput{kind: inputArray})
	prop.propValue().appendElement(input(t, "1"))
	if got, want := cst.String(), "{\n  \"test\": [1]\n}\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	for value, want := range map[string]string{
		`1`:                             "1",
		`"test"`:                        `"test"`,
		`[1, 2, 3]`:                     "[1, 2, 3]",
		`{"value": [1,2,3]}`:            "{\n  \"value\": [1, 2, 3]\n}",
		`{"notQuoted": 1, "quoted": 2}`: "{\n  \"notQuoted\": 1,\n  \"quoted\": 2\n}",
	} {
		cst := buildCST(t, "")
		cst.setRootValue(input(t, value))
		if got := cst.String(); got != want+"\n" {
			t.Errorf("set %s: got %q, want %q", value, got, want+"\n")
		}
	}
	if _, err := parseJSONText("[1 2]", anySyntax, true); err != nil {
		t.Fatalf("missing commas: %v", err)
	}
}

func TestJSONNumbersReadAsSerdeJSONWritesThem(t *testing.T) {
	for text, want := range map[string]string{
		"10": "10", "-5": "-5", "1.0": "1.0", "1e3": "1000.0", "1.5e300": "1.5e300",
		"0.1": "0.1", "1e21": "1e21", "1e16": "1e16", "1e15": "1000000000000000.0",
		"0.00001": "0.00001", "0.000001": "1e-6", "-0": "-0.0", "0.0001": "0.0001", "123456789012345678901": "1.2345678901234568e20",
		"18446744073709551615": "18446744073709551615", "-0.0": "-0.0",
	} {
		got, ok := ParseNumber(text)
		if !ok || string(got) != want {
			t.Errorf("ParseNumber(%s) = %s, %v; want %s", text, got, ok, want)
		}
	}
	for _, text := range []string{"0x10", "+1", ".5", "5.", "Infinity", "NaN", "1e400", "01"} {
		if _, ok := ParseNumber(text); ok {
			t.Errorf("ParseNumber(%s) read a number", text)
		}
	}
	if got := Encode(map[string]any{"b": []any{Number("1"), "x <>"}, "a": nil}); got != "{\"a\":null,\"b\":[1,\"x <>\"]}" {
		t.Errorf("Encode = %s", got)
	}
}
