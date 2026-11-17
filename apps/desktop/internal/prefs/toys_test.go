package prefs

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDesktopToysKeepOnlyHandFlippedSwitchesAndSaveOnTheirOwn(t *testing.T) {
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
	if !reflect.DeepEqual(toys, DefaultDesktopToys()) || !toys.On().Pet || toys.Level != ToyNormal ||
		!toys.Orb || !toys.Status || !toys.Milestone || !toys.Pet.Grab || !toys.Pet.Reply || !toys.Pet.Failover ||
		!toys.Pet.Rate || toys.Pet.Size != ToySizeNormal || toys.Pet.Character != PetAuto {
		t.Fatalf("%+v", toys)
	}
	var fixed DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"pet":{"character":"deepseek_harness"}}`), &fixed); err != nil ||
		fixed.Pet.Character != PetDeepseekHarness || !fixed.Pet.Grab || !fixed.Pet.Rate {
		t.Fatal(fixed, err)
	}
	var gemini DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"pet":{"character":"gemini_cli"}}`), &gemini); err != nil || gemini.Pet.Character != PetGeminiCLI {
		t.Fatal(gemini, err)
	}
	for _, bad := range []string{`{"pet":{"character":"pi"}}`, `{"pet":{"size":"huge"}}`, `{"pet":{"egg":1}}`, `{"level":"wild"}`} {
		var v DesktopToysPreferences
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Fatal(bad)
		}
	}

	// A partial block keeps the defaults it leaves out.
	var partial DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"level":"lively","orb":false}`), &partial); err != nil ||
		!partial.On().Pet || !partial.On().Any() || partial.Level != ToyLively || !partial.Pet.Grab || !partial.Status || partial.Orb {
		t.Fatal(partial, err)
	}

	// An earlier build saved every switch, defaults included: its "on" is
	// not a choice, so the pet follows her default.
	var earlier DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"on":{"pet":false},"level":"calm"}`), &earlier); err != nil ||
		!earlier.On().Pet || earlier.Switched.Pet != nil || earlier.Level != ToyCalm {
		t.Fatal(earlier, err)
	}

	// A switch flipped by hand sticks, either way.
	var off, on DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"switched":{"pet":false}}`), &off); err != nil || off.On().Pet || off.On().Any() {
		t.Fatal(off, err)
	}
	if err := json.Unmarshal([]byte(`{"switched":{"pet":true}}`), &on); err != nil || !on.On().Pet {
		t.Fatal(on, err)
	}

	// The toys save alone; a stale general save does not undo them.
	staleGeneral := store.Values()
	saved := DefaultDesktopToys()
	petOff := false
	saved.Switched.Pet, saved.Level = &petOff, ToyCalm
	if _, err := store.ReplaceDesktopToys(saved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(staleGeneral); err != nil {
		t.Fatal(err)
	}
	if got := load(directory).Values().DesktopToys; !reflect.DeepEqual(got, saved) || got.On().Pet {
		t.Fatalf("%+v", got)
	}

	// Toys this build does not have are left out, not refused.
	var more DesktopToysPreferences
	if err := json.Unmarshal([]byte(`{"switched":{"pet":false,"roam":true},"roam":{"size":"large"},"pet":{"size":"small"}}`), &more); err != nil ||
		more.On().Pet || more.Pet.Size != ToySmall {
		t.Fatal(more, err)
	}
	// Nothing flipped, nothing saved: the defaults stay free to change.
	encoded, err := json.Marshal(Default(DefaultInferencePort))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		t.Fatal(err)
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(top["desktop_toys"], &block); err != nil || string(block["level"]) != `"normal"` ||
		string(block["switched"]) != "{}" || block["on"] != nil {
		t.Fatalf("%s %v", top["desktop_toys"], err)
	}
}
