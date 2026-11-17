package format

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// yamlEditCase is a file and the edits made to it, with what the Tauri
// shell's yaml-edit editor wrote after each one (testdata/yaml_edits.json).
type yamlEditCase struct {
	Name    string            `json:"name"`
	Root    string            `json:"root"`
	Text    string            `json:"text"`
	Ops     []json.RawMessage `json:"ops"`
	Results []struct {
		Text  *string `json:"text"`
		Error *string `json:"error"`
		Line  *int    `json:"line"`
	} `json:"results"`
}

type yamlEditOp struct {
	Op     string          `json:"op"`
	Parent []any           `json:"parent"`
	Path   []any           `json:"path"`
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value"`
	Rank   string          `json:"rank"`
}

func yamlSteps(t *testing.T, raw []any) []YAMLStep {
	t.Helper()
	steps := make([]YAMLStep, len(raw))
	for i, step := range raw {
		switch s := step.(type) {
		case string:
			steps[i] = YAMLKey(s)
		case float64:
			steps[i] = YAMLIndex(int(s))
		default:
			t.Fatalf("step %v", step)
		}
	}
	return steps
}

func yamlRank(name string) Rank {
	switch name {
	case "id_config":
		return func(key string) int {
			switch key {
			case "id":
				return 0
			case "config":
				return 1
			}
			return NoRank(key)
		}
	case "zero":
		return func(string) int { return 0 }
	}
	return NoRank
}

func TestYAMLEditsMatchTheTauriShell(t *testing.T) {
	data, err := os.ReadFile("testdata/yaml_edits.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []yamlEditCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			root := YAMLMapping
			if tc.Root == "sequence" {
				root = YAMLSequence
			}
			doc, err := ParseYAML(tc.Text, root)
			if err != nil {
				want := tc.Results[0]
				inv, ok := err.(*Invalid)
				if want.Error == nil || !ok || inv.Message != *want.Error {
					t.Fatalf("parse: %v, want %+v", err, want)
				}
				// go-yaml stops at another line than libyaml for some errors;
				// both name one, or neither.
				if (want.Line != nil) != (inv.Line > 0) {
					t.Errorf("parse error at line %d, want %v", inv.Line, want.Line)
				}
				return
			}
			if len(tc.Ops) == 0 && len(tc.Results) > 0 {
				t.Fatalf("parsed, want %+v", tc.Results[0])
			}
			for i, raw := range tc.Ops {
				var op yamlEditOp
				if err := json.Unmarshal(raw, &op); err != nil {
					t.Fatal(err)
				}
				var value any
				if len(op.Value) > 0 {
					if value, err = DecodeJSON(string(op.Value)); err != nil {
						t.Fatal(err)
					}
				}
				switch op.Op {
				case "set":
					err = doc.Set(yamlSteps(t, op.Parent), op.Key, value, yamlRank(op.Rank))
				case "push":
					err = doc.Push(yamlSteps(t, op.Parent), value, yamlRank(op.Rank))
				case "remove":
					err = doc.Remove(yamlSteps(t, op.Path)...)
				case "set_key":
					err = doc.SetKey(op.Key, value.(string))
				case "set_key_json":
					err = doc.SetKeyJSON(op.Key, value)
				case "remove_key":
					err = doc.RemoveKey(op.Key)
				default:
					t.Fatalf("op %q", op.Op)
				}
				want := tc.Results[i]
				if want.Error != nil {
					if err == nil || err.Error() != *want.Error {
						t.Fatalf("op %d %s: %v, want error %q; text %q", i, raw, err, *want.Error, doc.Render())
					}
					continue
				}
				if err != nil {
					t.Fatalf("op %d %s: %v", i, raw, err)
				}
				if got := doc.Render(); got != *want.Text {
					t.Fatalf("op %d %s:\ngot  %q\nwant %q", i, raw, got, *want.Text)
				}
			}
		})
	}
}

func TestYAMLEditsKeepCommentsTagsAndLineEndings(t *testing.T) {
	original := "# mine\r\n- id: tools\r\n  config:\r\n    mode: !!js process.env.MODE\r\n- id: llm-pi-ai\r\n  config:\r\n    providers:\r\n      gw: {api: openai-completions}  # hand-written\r\n"
	doc, err := ParseYAML(original, YAMLSequence)
	if err != nil {
		t.Fatal(err)
	}
	providers := []YAMLStep{YAMLIndex(1), YAMLKey("config"), YAMLKey("providers")}
	route := map[string]any{"api": "anthropic-messages", "baseURL": "http://127.0.0.1:8317"}
	if err := doc.Set(providers, "astrlink-anthropic", route, yamlRank("id_config")); err != nil {
		t.Fatal(err)
	}
	text := doc.Render()
	if bareLineFeed(text) || !strings.Contains(text, "# hand-written") || !strings.Contains(text, "!!js process.env.MODE") {
		t.Fatalf("written %q", text)
	}
	got, ok := doc.GetJSON(append(providers, YAMLKey("astrlink-anthropic"))...)
	if !ok || Encode(got) != Encode(route) {
		t.Fatalf("read %v", got)
	}
	if err := doc.Remove(append(providers, YAMLKey("astrlink-anthropic"))...); err != nil {
		t.Fatal(err)
	}
	if doc.Render() != original {
		t.Fatalf("removed %q", doc.Render())
	}
}

func TestYAMLDottedKeys(t *testing.T) {
	doc, err := ParseYAML("version: 1\n# my keys\nrecords:\n    a: {kind: grant}\n", YAMLMapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetKey("refs.ASTRLINK_WEB_API_KEY", "astr_x"); err != nil {
		t.Fatal(err)
	}
	if value, ok, _ := doc.GetKey("refs.ASTRLINK_WEB_API_KEY"); !ok || value != "astr_x" {
		t.Fatalf("read %q", value)
	}
	if err := doc.RemoveKey("refs.ASTRLINK_WEB_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, _, present := doc.GetKey("refs.ASTRLINK_WEB_API_KEY"); present {
		t.Fatal("still there")
	}
	if refs, ok := doc.Get(YAMLKey("refs")); !ok || Encode(refs) != "{}" {
		t.Fatalf("refs %v", refs)
	}
}

func TestYAMLReadsAsYAMLSerde(t *testing.T) {
	doc, err := ParseYAML("a: yes\nb: 0x1F\nc: 017\nd: 1e3\ne: +1\nf: 1.0\ng: ~\nh: 'true'\ni: !!str 1\nj: &x {k: 1}\nl: *x\nm: |\n  x\nn: 18446744073709551615\no: -0o17\np:\n", YAMLMapping)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := doc.GetJSON()
	want := `{"a":"yes","b":31,"c":"017","d":1000.0,"e":1,"f":1.0,"g":null,"h":"true","i":"1","j":{"k":1},"l":{"k":1},"m":"x\n","n":18446744073709551615,"o":-15,"p":null}`
	if Encode(got) != want {
		t.Fatalf("got  %s\nwant %s", Encode(got), want)
	}
}

func TestYAMLInvalidReportsALine(t *testing.T) {
	for _, tc := range []struct {
		text string
		line int
	}{
		{"a: 1\nb: 'astr_x\n", 2},
		{"a: 1\na: 2\n", 2},
	} {
		_, err := ParseYAML(tc.text, YAMLMapping)
		inv, ok := err.(*Invalid)
		if !ok || inv.Message != "is not valid YAML" || inv.Line != tc.line {
			t.Errorf("%q: %v, want line %d", tc.text, err, tc.line)
		}
		if ok && strings.Contains(inv.Error(), "astr_x") {
			t.Errorf("%q: the error shows the value", tc.text)
		}
	}
}
