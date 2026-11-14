package prefs

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDesktopToysDefaultOffForExistingFilesAndSaveOnTheirOwn(t *testing.T) {
	directory := t.TempDir()
	// A preferences file written before the desktop toys existed.
	writeFile(t, filepath.Join(directory, fileName),
		`{"close_behavior":"hide_to_tray","inference_port":8317,"tray":{"menubar_text":"tokens"}}`)
	store := load(directory)
	snapshot := store.Snapshot()
	if snapshot.LoadWarning != nil {
		t.Fatal(*snapshot.LoadWarning)
	}
	toys := snapshot.Values.DesktopToys
	if !reflect.DeepEqual(toys, DefaultDesktopToys()) || toys.On.Any() || toys.Level != ToyNormal ||
		!toys.Orb || !toys.Status || !toys.Milestone || !toys.Pet.Grab || !toys.Pet.Reply || !toys.Pet.Failover ||
		toys.Pet.Size != ToySizeNormal || toys.Pet.Character != PetAuto {
		t.Fatalf("%+v", toys)
	}
	var fixed DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"pet":{"character":"deepseek_harness"}}`), &fixed); err != nil ||
		fixed.Pet.Character != PetDeepseekHarness || !fixed.Pet.Grab {
		t.Fatal(fixed, err)
	}
	for _, bad := range []string{`{"pet":{"character":"pi"}}`, `{"pet":{"size":"huge"}}`, `{"pet":{"egg":1}}`, `{"level":"wild"}`} {
		var v DesktopToysPreferences
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Fatal(bad)
		}
	}

	// A partial block keeps the defaults it leaves out.
	var partial DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"on":{"pet":true},"level":"lively","orb":false}`), &partial); err != nil ||
		!partial.On.Pet || partial.Level != ToyLively || !partial.Pet.Grab || !partial.Status || partial.Orb {
		t.Fatal(partial, err)
	}

	// The toys save alone; a stale general save does not undo them.
	staleGeneral := store.Values()
	saved := DefaultDesktopToys()
	saved.On.Pet, saved.Level = true, ToyCalm
	if _, err := store.ReplaceDesktopToys(saved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(staleGeneral); err != nil {
		t.Fatal(err)
	}
	if got := load(directory).Values().DesktopToys; !reflect.DeepEqual(got, saved) {
		t.Fatalf("%+v", got)
	}

	// Toys this build does not have are left out, not refused.
	var more DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"on":{"pet":true,"roam":true},"roam":{"size":"large"},"pet":{"size":"small"}}`), &more); err != nil ||
		!more.On.Pet || more.Pet.Size != ToySmall {
		t.Fatal(more, err)
	}
	encoded, err := json.Marshal(Default(DefaultInferencePort))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		t.Fatal(err)
	}
	var block struct {
		Level string          `json:"level"`
		On    map[string]bool `json:"on"`
	}
	if err := json.Unmarshal(top["desktop_toys"], &block); err != nil || block.Level != "normal" || block.On["pet"] {
		t.Fatalf("%s %v", top["desktop_toys"], err)
	}
}
