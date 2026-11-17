package clientconfig

import (
	"iter"
	"maps"
	"os"
	"slices"
	"strings"

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
			// A config that cannot be read is invalid, not a failed list.
			existing, err := ReadFiles(client, paths)
			unreadable := err != nil
			if unreadable {
				existing = make([]*string, len(paths))
			}
			state := Invalid
			if !unreadable {
				state = PlaceState(records, place, existing, origin, tokens)
			}
			config, fileErr := ParseConfig(client, existing)
			if fileErr != nil {
				config = nil
			}
			manual := ""
			if config != nil && origin != "" && tokens != nil && record == nil &&
				state == NotConfigured && slices.ContainsFunc(existing, func(file *string) bool { return file != nil }) {
				manual, _ = manualToken(place, config, origin, tokens)
			}
			if manual != "" {
				state = Manual
			}
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
			for _, path := range paths {
				_, err := os.Stat(path)
				status.Exists = append(status.Exists, err == nil)
			}
			// Only a config AstrLink wrote or the user set up from its manual
			// setup names its token and models.
			switch {
			case record != nil && state != NotConfigured:
				status.TokenID = Some(record.TokenID)
				status.PasteSinceUnix = record.PasteSinceUnix
				status.Models = CurrentModelsOf(client, existing)
			case manual != "":
				status.TokenID = Some(manual)
				status.Models = CurrentModelsOf(client, existing)
			}
			if config != nil && status.PasteSinceUnix != nil {
				status.PasteInto = client.PasteInto(config)
			}
			status.Notes = client.Notes(home, place.Target, config)
			if unreadable {
				status.Notes = append(status.Notes, "unreadable")
			}
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

// manualSentinel is a stand-in token that marks where a client's keys hold
// the token. Letters, digits and underscores only, so no format escapes
// it.
const manualSentinel = "ASTRLINK_MANUAL_SETUP_TOKEN_0"

// manualToken is the id of the gateway token a config AstrLink did not
// record holds, when the config is what AstrLink's manual setup shows for
// this gateway: every key holding the token, the connection keys and the
// address key hold what a write with that token would. The token never
// leaves this function; only its hint is compared. A client whose keys
// this cannot tell apart, like one that lists its token per model, is
// never manual.
func manualToken(place Place, config *Config, origin string, tokens Tokens) (string, bool) {
	client := place.Client
	render := func(token string) []Setting {
		return client.DesiredKeys(&Connection{
			Target:    place.Target,
			Token:     token,
			Origin:    origin,
			Protocols: map[string]string{},
		}, Catalog{})
	}
	held := func(key string) (string, bool) {
		value, ok, present := config.Get(key)
		return value, ok && present
	}
	var tokenKeys []string
	candidate := ""
	for _, setting := range render(manualSentinel) {
		if strings.Count(setting.Value, manualSentinel) != 1 {
			continue
		}
		before, after, _ := strings.Cut(setting.Value, manualSentinel)
		value, ok := held(setting.Key)
		if !ok || !strings.HasPrefix(value, before) || !strings.HasSuffix(value[len(before):], after) {
			return "", false
		}
		token := value[len(before) : len(value)-len(after)]
		if candidate != "" && candidate != token {
			return "", false
		}
		candidate = token
		tokenKeys = append(tokenKeys, setting.Key)
	}
	if !strings.HasPrefix(candidate, "astr_") || !isASCII(candidate) {
		return "", false
	}
	tokenID, ok := tokens.Matching(candidate)
	if !ok {
		return "", false
	}
	desired := render(candidate)
	keys := append(tokenKeys, client.ConnectionKeys()...)
	if key := client.BaseURLKey(); key != "" {
		keys = append(keys, key)
	}
	addressed := false
	for _, key := range keys {
		at := slices.IndexFunc(desired, func(setting Setting) bool { return setting.Key == key })
		if at < 0 {
			return "", false
		}
		want := desired[at].Value
		if value, ok := held(key); !ok || value != want {
			return "", false
		}
		addressed = addressed || strings.Contains(want, origin)
	}
	if !addressed {
		return "", false
	}
	return tokenID, true
}

func isASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			return false
		}
	}
	return true
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
