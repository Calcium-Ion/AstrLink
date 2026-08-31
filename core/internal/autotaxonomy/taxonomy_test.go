package autotaxonomy

import "testing"

func TestFrozenLabelsAndIndexes(t *testing.T) {
	if ID != "astrlink-text-v1" {
		t.Fatalf("taxonomy id = %q", ID)
	}
	if SHA256 != "83990d9d763df0feac9b06aef36ef14417a662494f53004e68c3f18248fd5a2f" {
		t.Fatalf("taxonomy sha256 drifted")
	}
	if got, err := LabelAt(2); err != nil || got != "coding" {
		t.Fatalf("label 2 = %q err=%v", got, err)
	}
	if _, err := IndexOf("code"); err == nil {
		t.Fatal("legacy code id must not resolve")
	}
	if err := EqualID2Label(map[string]string{
		"0": "general", "1": "research", "2": "coding", "3": "architect",
	}); err != nil {
		t.Fatal(err)
	}
	if err := EqualID2Label(map[string]string{
		"0": "general", "1": "research", "2": "code", "3": "architect",
	}); err == nil {
		t.Fatal("accepted legacy code label")
	}
}
