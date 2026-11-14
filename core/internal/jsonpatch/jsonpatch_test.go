package jsonpatch

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPatchesKeepOrderAndNumbers(t *testing.T) {
	document, err := Parse([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":1.50,"metadata":{"a":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	var operations []Operation
	_ = json.Unmarshal([]byte(`[
		{"op":"test","path":"/model","value":"m"},
		{"op":"add","path":"/store","value":false},
		{"op":"remove","path":"/metadata"},
		{"op":"replace","path":"/temperature","value":0.2},
		{"op":"add","path":"/messages/0","value":{"role":"system","content":"be brief"}},
		{"op":"copy","from":"/messages/1/content","path":"/user"},
		{"op":"move","from":"/user","path":"/messages/-"},
		{"op":"add","path":"/a~1b","value":1}
	]`), &operations)
	if err := document.Apply(operations); err != nil {
		t.Fatal(err)
	}
	got, _ := document.Bytes()
	want := `{"model":"m","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi"},"hi"],"temperature":0.2,"store":false,"a/b":1}`
	if string(got) != want {
		t.Fatalf("patched = %s\nwant      %s", got, want)
	}
	if value, ok := document.Get("/messages/1/role"); !ok || string(value) != `"user"` {
		t.Fatalf("Get = %s %v", value, ok)
	}
	// An untouched number keeps its spelling.
	untouched, _ := Parse([]byte(`{"a":1.50,"b":[1e3]}`))
	_ = untouched.Apply([]Operation{{Op: "add", Path: "/c", Value: json.RawMessage(`true`)}})
	if got, _ := untouched.Bytes(); string(got) != `{"a":1.50,"b":[1e3],"c":true}` {
		t.Fatalf("numbers = %s", got)
	}
}

func TestBadPatchesFail(t *testing.T) {
	for _, operation := range []Operation{
		{Op: "test", Path: "/model", Value: json.RawMessage(`"other"`)},
		{Op: "remove", Path: "/missing"},
		{Op: "replace", Path: "/missing", Value: json.RawMessage(`1`)},
		{Op: "add", Path: "/list/5", Value: json.RawMessage(`1`)},
		{Op: "add", Path: "/list/01", Value: json.RawMessage(`1`)},
		{Op: "add", Path: "missing-slash", Value: json.RawMessage(`1`)},
		{Op: "move", From: "/obj", Path: "/obj/inner"},
		{Op: "add", Path: "/model"},
		{Op: "patch", Path: "/model"},
	} {
		document, _ := Parse([]byte(`{"model":"m","list":[1],"obj":{}}`))
		err := document.Apply([]Operation{operation})
		if err == nil {
			t.Errorf("%+v applied", operation)
		}
		if operation.Op == "test" && !errors.Is(err, ErrTestFailed) {
			t.Errorf("a failed test = %v", err)
		}
	}
}
