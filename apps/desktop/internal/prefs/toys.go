package prefs

import "encoding/json"

// ToyLevel is how much the desktop pet moves.
type ToyLevel string

const (
	ToyCalm   ToyLevel = "calm"
	ToyNormal ToyLevel = "normal"
	ToyLively ToyLevel = "lively"
)

func (l *ToyLevel) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(l), "toy level", ToyCalm, ToyNormal, ToyLively)
}

// ToySize is how big the pet draws.
type ToySize string

const (
	ToySmall      ToySize = "small"
	ToySizeNormal ToySize = "normal"
	ToyLarge      ToySize = "large"
)

func (s *ToySize) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(s), "toy size", ToySmall, ToySizeNormal, ToyLarge)
}

// PetCharacter is who the big pet is: one of the drawn girls, or whoever
// has the most conversations going.
type PetCharacter string

const (
	PetAuto            PetCharacter = "auto"
	PetClaudeCode      PetCharacter = "claude_code"
	PetCodex           PetCharacter = "codex"
	PetDeepseekHarness PetCharacter = "deepseek_harness"
)

func (c *PetCharacter) UnmarshalJSON(data []byte) error {
	return unmarshalChoice(data, (*string)(c), "pet character", PetAuto, PetClaudeCode, PetCodex, PetDeepseekHarness)
}

// DesktopToysOn is which desktop toys are out. Missing ones are off; only
// a new macOS install starts with the big pet out.
type DesktopToysOn struct {
	// Pet is one big girl for every conversation, to pat, poke and throw.
	Pet bool `json:"pet"`
}

// Any reports whether a toy is out.
func (o DesktopToysOn) Any() bool { return o.Pet }

// UnmarshalJSON keeps unknown toys of other builds out without failing,
// like the block it sits in.
func (o *DesktopToysOn) UnmarshalJSON(data []byte) error {
	type plain DesktopToysOn
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*o = DesktopToysOn(v)
	return nil
}

// DesktopToyPet is the big pet floating where she was left.
type DesktopToyPet struct {
	Size      ToySize      `json:"size"`
	Character PetCharacter `json:"character"`
	// Grab lets her be picked up and thrown with the mouse.
	Grab bool `json:"grab"`
	// Reply shows a bubble when a reply is written.
	Reply bool `json:"reply"`
	// Failover shows a bubble when a call switches route.
	Failover bool `json:"failover"`
}

func defaultToyPet() DesktopToyPet {
	return DesktopToyPet{Size: ToySizeNormal, Character: PetAuto, Grab: true, Reply: true, Failover: true}
}

func (p *DesktopToyPet) UnmarshalJSON(data []byte) error {
	type plain DesktopToyPet
	v := plain(defaultToyPet())
	if err := decodeStrict(data, &v); err != nil {
		return err
	}
	*p = DesktopToyPet(v)
	return nil
}

// DesktopToysPreferences are the desktop toys outside the main window, for
// now the big pet, saved apart from the general settings.
type DesktopToysPreferences struct {
	Level ToyLevel `json:"level"`
	// Orb floats each conversation's model beside the pet as a winged orb.
	Orb bool `json:"orb"`
	// Status shows what she is doing over her head: the tool, tok/s, a
	// thought cloud, a snore.
	Status bool `json:"status"`
	// Milestone has her celebrate the day's token milestones.
	Milestone bool          `json:"milestone"`
	On        DesktopToysOn `json:"on"`
	Pet       DesktopToyPet `json:"pet"`
}

// DefaultDesktopToys are the toy settings of a file that has none: every
// toy off.
func DefaultDesktopToys() DesktopToysPreferences {
	return DesktopToysPreferences{Level: ToyNormal, Orb: true, Status: true, Milestone: true, Pet: defaultToyPet()}
}

// UnmarshalJSON fills what a block leaves out with the defaults, and keeps
// unknown fields out without failing: a file saved by a build with other
// toys keeps its pet instead of resetting every preference.
func (d *DesktopToysPreferences) UnmarshalJSON(data []byte) error {
	type plain DesktopToysPreferences
	v := plain(DefaultDesktopToys())
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*d = DesktopToysPreferences(v)
	return nil
}
