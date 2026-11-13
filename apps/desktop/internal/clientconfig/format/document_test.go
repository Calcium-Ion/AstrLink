package format

import "testing"

func TestDocumentsEditByKey(t *testing.T) {
	for _, tc := range []struct {
		format       Format
		text, key    string
		set, removed string
	}{
		{JSONFormat{Syntax: &JSONC}, "{\n  // mine\n  \"a\": 1\n}\n", "env.KEY", "{\n  // mine\n  \"a\": 1,\n  \"env\": {\n    \"KEY\": \"v\"\n  }\n}\n", "{\n  // mine\n  \"a\": 1\n}\n"},
		{TOMLFormat{}, "a = 1\n", "model", "a = 1\nmodel = \"v\"\n", "a = 1\n"},
		{EnvFormat{}, "A=1\n", "KEY", "A=1\nKEY=v\n", "A=1\n"},
		{YAMLFormat{Root: YAMLMapping}, "# c\na: 1\n", "x.KEY", "# c\na: 1\nx:\n  KEY: v\n", "# c\na: 1\nx: {}\n"},
	} {
		doc, err := ParseDocument(tc.format, tc.text)
		if err != nil {
			t.Fatalf("%T: %v", tc.format, err)
		}
		if _, _, present := doc.Get(tc.key); present {
			t.Fatalf("%T: %s is there", tc.format, tc.key)
		}
		if err := doc.Set(tc.key, "v"); err != nil {
			t.Fatalf("%T: %v", tc.format, err)
		}
		if got := doc.Render(); got != tc.set {
			t.Fatalf("%T: set %q, want %q", tc.format, got, tc.set)
		}
		if value, ok, present := doc.Get(tc.key); value != "v" || !ok || !present {
			t.Fatalf("%T: read %q %v %v", tc.format, value, ok, present)
		}
		doc.Remove(tc.key)
		doc.DropEmpty([]string{"env"})
		if got := doc.Render(); got != tc.removed {
			t.Fatalf("%T: removed %q, want %q", tc.format, got, tc.removed)
		}
	}
	doc, _ := ParseDocument(EnvFormat{}, "")
	if AsEnv(doc) == nil || AsYAML(doc) != nil {
		t.Fatal("downcast")
	}
	if _, err := ParseDocument(YAMLFormat{Root: YAMLSequence}, "a: 1\n"); err == nil {
		t.Fatal("a mapping is not a list")
	}
}
