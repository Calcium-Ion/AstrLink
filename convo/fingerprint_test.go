package convo

import (
	"bytes"
	"strings"
	"testing"
)

func TestFingerprintFormat(t *testing.T) {
	fp := NewFingerprinter([]byte("secret"))
	digest := DigestText("some assistant text", false)
	value := fp.Fingerprint(digest)
	if !strings.HasPrefix(value, "fp1_") {
		t.Fatalf("fingerprint %q lacks version prefix", value)
	}
	if len(value) != len("fp1_")+32 {
		t.Fatalf("fingerprint %q has unexpected length %d", value, len(value))
	}
	if fp.Fingerprint(digest) != value {
		t.Fatal("fingerprint is not deterministic")
	}
	if fp.Fingerprint(nil) != "" {
		t.Fatal("nil digest must yield empty fingerprint")
	}
	if HasCursorEntropy(value) != true {
		t.Fatal("fingerprints must pass the entropy gate")
	}
}

func TestFingerprintDependsOnKey(t *testing.T) {
	digest := DigestText("same text", false)
	a := NewFingerprinter([]byte("key-a")).Fingerprint(digest)
	b := NewFingerprinter([]byte("key-b")).Fingerprint(digest)
	if a == b {
		t.Fatal("different keys must produce different fingerprints")
	}
}

func TestNilFingerprinter(t *testing.T) {
	if NewFingerprinter(nil) != nil {
		t.Fatal("empty key must return nil")
	}
	var fp *Fingerprinter
	if fp.Fingerprint([]byte{1, 2, 3}) != "" {
		t.Fatal("nil fingerprinter must return empty string")
	}
}

func TestDeriveFingerprintKey(t *testing.T) {
	master := bytes.Repeat([]byte{7}, 32)
	a := DeriveFingerprintKey(master, "app/fingerprint/v1")
	b := DeriveFingerprintKey(master, "app/fingerprint/v1")
	c := DeriveFingerprintKey(master, "app/fingerprint/v2")
	if len(a) != 32 || !bytes.Equal(a, b) {
		t.Fatal("derivation must be deterministic and 32 bytes")
	}
	if bytes.Equal(a, c) {
		t.Fatal("different info must derive different keys")
	}
	if bytes.Equal(a, master) {
		t.Fatal("derived key must not equal master")
	}
	if DeriveFingerprintKey(nil, "x") != nil {
		t.Fatal("empty master must derive nil")
	}
}
