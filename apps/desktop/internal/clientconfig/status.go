package clientconfig

import (
	"iter"
	"maps"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
)

// Each place's config as the dialog shows it.

func mapKeys(m map[string]string) iter.Seq[string] { return maps.Keys(m) }

// ClientStatus is one place's config.
type ClientStatus struct {
	Client Client `json:"client"`
	// WSL is the WSL distribution the client runs in; nil for the desktop
	// user's.
	WSL *string `json:"wsl"`
	// Target is one of the client's targets; nil for a client without.
	Target   *string `json:"target"`
	Detected bool    `json:"detected"`
	// Home is the home the paths lie in, for showing them from `~`.
	Home string `json:"home"`
	// Dir is the client's config directory, where an install is looked
	// for.
	Dir   string   `json:"dir"`
	Paths []string `json:"paths"`
	// Exists reports whether each of Paths exists now.
	Exists  []bool  `json:"exists"`
	State   State   `json:"state"`
	TokenID *string `json:"token_id"`
	// Models are the models a config AstrLink wrote holds now.
	Models CurrentModels `json:"models"`
	// Notes are situations the dialog explains for this place, by id.
	Notes []string `json:"notes"`
	// PasteSinceUnix is, for a written client the token is pasted into,
	// since when a request from it with the token shows the paste worked;
	// PasteInto the names it is pasted under.
	PasteSinceUnix *uint64  `json:"paste_since_unix"`
	PasteInto      []string `json:"paste_into"`
}

// ClientOverview is client configs across the desktop user's home and WSL
// homes.
type ClientOverview struct {
	Clients []ClientStatus `json:"clients"`
	// WSLUnchecked are WSL distributions whose clients were not checked.
	WSLUnchecked []string `json:"wsl_unchecked"`
	// WSLLocalhost reports whether WSL clients reach the gateway at
	// `localhost`.
	WSLLocalhost bool `json:"wsl_localhost"`
	// Installed are the link clients installed on this computer.
	Installed []string `json:"installed"`
}

// Status is every place of each client AstrLink writes files for, in the
// dialog's order. inferenceURL is "" when the gateway is not running, and
// tokens nil when unknown.
func Status(home agents.Home, inferenceURL string, tokens Tokens) ([]ClientStatus, error) {
	return StatusOf(home, All(), inferenceURL, tokens)
}

// StatusOf is Status for clients.
func StatusOf(home agents.Home, clients []Client, inferenceURL string, tokens Tokens) ([]ClientStatus, error) {
	origin := ""
	if inferenceURL != "" {
		var err error
		if origin, err = LocalOrigin(inferenceURL); err != nil {
			return nil, err
		}
	}
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return nil, err
	}
	statuses := []ClientStatus{}
	for _, client := range clients {
		for _, place := range Places(client) {
			record := records.Get(place)
			paths := client.ConfigPaths(home, place.Target, record)
			existing, err := ReadFiles(paths)
			if err != nil {
				return nil, err
			}
			state := PlaceState(records, place, existing, origin, tokens)
			status := ClientStatus{
				Client:    client,
				WSL:       optional(home.Distribution()),
				Target:    optional(place.Target),
				Detected:  client.Detected(home, place.Target),
				Home:      home.Dir(),
				Dir:       client.Dir(home, place.Target),
				Paths:     paths,
				State:     state,
				PasteInto: []string{},
			}
			for _, file := range existing {
				status.Exists = append(status.Exists, file != nil)
			}
			// Only a config AstrLink wrote names its token and models.
			if record != nil && state != NotConfigured {
				status.TokenID = Some(record.TokenID)
				status.PasteSinceUnix = record.PasteSinceUnix
				status.Models = CurrentModelsOf(client, existing)
			}
			config, fileErr := ParseConfig(client, existing)
			if fileErr != nil {
				config = nil
			}
			if config != nil && status.PasteSinceUnix != nil {
				status.PasteInto = client.PasteInto(config)
			}
			status.Notes = client.Notes(home, place.Target, config)
			if client.Running(home, place.Target) {
				status.Notes = append(status.Notes, "running")
			}
			if status.Notes == nil {
				status.Notes = []string{}
			}
			statuses = append(statuses, status)
		}
	}
	return statuses, nil
}

// ConfiguredToken is the token AstrLink last set up place with in home,
// whatever the config holds now; nil for none.
func ConfiguredToken(home agents.Home, place Place) (*string, error) {
	defer agents.LockHostFiles()()
	records, err := ReadRecords(home)
	if err != nil {
		return nil, err
	}
	if record := records.Get(place); record != nil {
		return Some(record.TokenID), nil
	}
	return nil, nil
}
