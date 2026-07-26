package secretstore

import "testing"

func TestParseRef(t *testing.T) {
	ref, err := ParseRef("local://endpoint/endpoint_01")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	if ref != Ref("local://endpoint/endpoint_01") {
		t.Fatalf("ref = %q", ref)
	}
}

func TestParseRefRetainsOptionalKeyringCompatibility(t *testing.T) {
	if _, err := ParseRef("keyring://endpoint/endpoint_01"); err != nil {
		t.Fatalf("ParseRef keyring compatibility: %v", err)
	}
}

func TestParseRefRejectsInsecureOrAmbiguousReferences(t *testing.T) {
	for _, value := range []string{
		"file:///tmp/secret",
		"keyring://endpoint",
		"keyring://endpoint/",
		"keyring:///endpoint_01",
		"keyring://endpoint/%65ndpoint_01",
		"keyring://endpoint/endpoint_01?secret=value",
		"local://other/endpoint_01",
		"local://endpoint/endpoint_01/child",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseRef(value); err == nil {
				t.Fatalf("ParseRef(%q) succeeded", value)
			}
		})
	}
}
