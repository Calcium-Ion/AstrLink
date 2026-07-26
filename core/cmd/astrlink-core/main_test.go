package main

import (
	"strings"
	"testing"
)

func TestReadControlTokenAcceptsOneBoundedBase64URLLine(t *testing.T) {
	control := strings.Repeat("aB3_", 8)
	got, err := readControlToken(strings.NewReader(control + "\ntrailing pipe data"))
	if err != nil {
		t.Fatalf("readControlToken: %v", err)
	}
	if got != control {
		t.Fatalf("token = %q", got)
	}
}

func TestReadControlTokenRejectsMissingDelimiterLengthAndUnsafeValues(t *testing.T) {
	valid := strings.Repeat("a", 32)
	for _, value := range []string{
		valid,
		"short\n",
		strings.Repeat("a", 129) + "\n",
		strings.Repeat("a", 31) + "+\n",
		strings.Repeat("a", 31) + " \n",
	} {
		if token, err := readControlToken(strings.NewReader(value)); err == nil {
			t.Fatalf("readControlToken(%q) = %q", value, token)
		}
	}
}
