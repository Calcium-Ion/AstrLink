package privacy

import (
	"bytes"
	"testing"
)

func TestRestorePlaceholdersExactAndSkipUnsafeJSON(t *testing.T) {
	redactions := []Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: KindEmail, Value: "alice@example.com"},
		{Placeholder: "<PRIVATE_PERSON_0>", Kind: KindPerson, Value: `Ann "Quote"`},
		{Placeholder: "<PRIVATE_PHONE_0>", Kind: KindPhone, Value: "+14155550001"},
		{Placeholder: "<PRIVATE_PHONE_1>", Kind: KindPhone, Value: "+14155550002"},
	}
	input := []byte(`{"text":"mail <PRIVATE_EMAIL> phones <PRIVATE_PHONE_0> and <PRIVATE_PHONE_1> person <PRIVATE_PERSON_0>"}`)
	got := RestorePlaceholders(input, redactions)
	if !bytes.Contains(got, []byte("alice@example.com")) ||
		!bytes.Contains(got, []byte("+14155550001")) ||
		!bytes.Contains(got, []byte("+14155550002")) {
		t.Fatalf("restored=%s", got)
	}
	if !bytes.Contains(got, []byte("<PRIVATE_PERSON_0>")) {
		t.Fatalf("unsafe person must remain unrestored: %s", got)
	}
}

func TestCarryRestorerRestoresSplitAcrossChunks(t *testing.T) {
	restorer := NewCarryRestorer([]Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: KindEmail, Value: "alice@example.com"},
	})
	var out []byte
	out = append(out, restorer.Push([]byte(`prefix <PRIV`))...)
	out = append(out, restorer.Push([]byte(`ATE_EMAIL> suffix`))...)
	out = append(out, restorer.Flush()...)
	if !bytes.Equal(out, []byte(`prefix alice@example.com suffix`)) {
		t.Fatalf("carry restore = %q", out)
	}
}

func TestCarryRestorerDoesNotInventAcrossUnrelatedFlush(t *testing.T) {
	restorer := NewCarryRestorer([]Redaction{
		{Placeholder: "<PRIVATE_EMAIL>", Kind: KindEmail, Value: "alice@example.com"},
	})
	// Simulates v1 limitation: flushing carry between independent SSE/delta
	// events drops the cross-event split rather than inventing a restore.
	first := restorer.Push([]byte(`event1 <PRIV`))
	second := restorer.Flush()
	third := restorer.Push([]byte(`ATE_EMAIL> event2`))
	combined := append(append(first, second...), third...)
	if bytes.Contains(combined, []byte("alice@example.com")) {
		t.Fatalf("cross-event split should remain unrestored in v1: %q", combined)
	}
}

func TestRestorePlaceholdersNestedToolArgsJSONEscape(t *testing.T) {
	redactions := []Redaction{
		{Placeholder: "<PRIVATE_PHONE_0>", Kind: KindPhone, Value: "+14155550001"},
	}
	input := []byte(`{"arguments":"{\"phone\":\"<PRIVATE_PHONE_0>\"}"}`)
	got := RestorePlaceholders(input, redactions)
	want := []byte(`{"arguments":"{\"phone\":\"+14155550001\"}"}`)
	if !bytes.Equal(got, want) {
		t.Fatalf("got=%s want=%s", got, want)
	}
}

func TestShouldRestoreContentType(t *testing.T) {
	cases := map[string]bool{
		"application/json":                         true,
		"application/json; charset=utf-8":          true,
		"text/event-stream":                        true,
		"text/plain":                               true,
		"application/json-seq":                     true,
		"application/vnd.google.api+json":          true,
		"application/octet-stream":                 false,
		"image/png":                                false,
	}
	for contentType, want := range cases {
		if got := ShouldRestoreContentType(contentType); got != want {
			t.Fatalf("%q: got %v want %v", contentType, got, want)
		}
	}
}
