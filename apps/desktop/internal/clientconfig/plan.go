package clientconfig

import (
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Planning a write, a removal, and a port sync over a client's files, and
// reading the state they leave. The client decides the keys; the plans
// decide what to replace, back up, put back, and leave alone.

// State is a place's config as the dialog shows it.
type State string

const (
	NotConfigured State = "not_configured"
	Configured    State = "configured"
	// Outdated is still AstrLink's, pointing at a port the gateway no
	// longer uses.
	Outdated State = "outdated"
	// Elsewhere is another AstrLink's, pointing at its own address.
	Elsewhere State = "elsewhere"
	// Modified has a connection key that changed since AstrLink wrote it.
	Modified State = "modified"
	// Invalid has a config file that cannot be parsed.
	Invalid State = "invalid"
	// Manual was set up by hand from AstrLink's manual setup: AstrLink did
	// not write it, but it connects to this gateway with one of its
	// tokens. Only the status reports it; writes, removals and port syncs
	// treat the place as not configured.
	Manual State = "manual"
)

// Connection is what a write connects a client with.
type Connection struct {
	// Target is the place's write target, for a client that names what it
	// writes after it; "" for a client without.
	Target  string
	TokenID string
	Token   string
	Origin  string
	Models  []Field
	// List holds the models a client that switches models lists; the
	// default model alone when empty.
	List []string
	// Protocols is the gateway protocol chosen for each model; see
	// Protocol.
	Protocols map[string]string
}

// Model is the model in a Models field, by its camelCase name.
func (c *Connection) Model(field string) (string, bool) {
	for _, f := range c.Models {
		if f.Name == field {
			return f.Value, true
		}
	}
	return "", false
}

// ModelList is the models listed: List, or the default model alone.
func (c *Connection) ModelList() []string {
	if len(c.List) > 0 {
		return slices.Clone(c.List)
	}
	if model, ok := c.Model("model"); ok {
		return []string{model}
	}
	return nil
}

// Protocol is the protocol model is written with: the one chosen for it,
// else the client's first.
func (c *Connection) Protocol(client Client, model string) string {
	if protocol, ok := c.Protocols[model]; ok {
		return protocol
	}
	if protocols := client.Protocols(); len(protocols) > 0 {
		return protocols[0]
	}
	return ""
}

// connectionKept reports whether every key deciding where client's
// requests go still holds what AstrLink wrote: its fixed connection keys,
// and the keys AstrLink wrote that carry their own connection.
func connectionKept(client Client, config *Config, record *Record) bool {
	keys := slices.Clone(client.ConnectionKeys())
	for _, key := range slices.Sorted(maps.Keys(record.Keys)) {
		if client.HoldsConnection(key) {
			keys = append(keys, key)
		}
	}
	for _, path := range keys {
		value, ok, _ := config.Get(path)
		if !WrittenByUs(record, path, value, ok) {
			return false
		}
	}
	return true
}

// WrittenByUs reports whether value, the string at path (ok false for
// none), is what record says AstrLink wrote there.
func WrittenByUs(record *Record, path, value string, ok bool) bool {
	if record == nil || !ok {
		return false
	}
	fingerprint, written := record.Keys[path]
	return written && fingerprint == SHA256Hex(value)
}

// WritePlan is a write's result.
type WritePlan struct {
	// Contents holds new contents for each of the client's files, in Files
	// order; a file the plan leaves alone keeps its text, and an empty one
	// goes.
	Contents []string
	// Conflicts are keys holding values AstrLink did not write, which the
	// plan replaces or removes. Their values go only to the record's
	// backup.
	Conflicts []string
	Record    *Record
}

// PlanWrite plans writing connection; existing holds each of the client's
// files, in Files order, nil for a missing one. details is what a list
// entry for each model says besides its id.
func PlanWrite(client Client, existing []*string, connection *Connection, details Catalog, previous *Record) (*WritePlan, *FileError) {
	config, err := ParseConfig(client, existing)
	if err != nil {
		return nil, err
	}
	desired := client.DesiredKeys(connection, details)
	wanted := func(path string) (string, bool) {
		for _, setting := range desired {
			if setting.Key == path {
				return setting.Value, true
			}
		}
		return "", false
	}
	var conflicts []string
	backup := map[string]string{}
	if previous != nil {
		backup = maps.Clone(previous.Backup)
		if backup == nil {
			backup = map[string]string{}
		}
	}
	// Each value AstrLink did not write is kept until its config goes. One
	// that differs from what AstrLink writes is a conflict; one that does
	// not, like an allowlisted model, stays once AstrLink's config goes.
	found := func(path, value string, ok bool, want string, wants bool) {
		if WrittenByUs(previous, path, value, ok) {
			return
		}
		switch {
		case ok && wants && want == value:
			if _, kept := backup[path]; !kept {
				backup[path] = value
			}
		case ok:
			conflicts = append(conflicts, path)
			backup[path] = value
		default:
			conflicts = append(conflicts, path)
			delete(backup, path)
		}
	}
	newFile := existing[0] == nil || strings.TrimSpace(*existing[0]) == ""
	if newFile {
		if err := client.StartFile(config); err != nil {
			return nil, err
		}
	}
	group := config.Group()
	inGroup := func(path string) bool {
		return slices.ContainsFunc(group, func(kv format.KeyValue) bool { return kv.Key == path })
	}
	for _, kv := range group {
		want, wants := wanted(kv.Key)
		found(kv.Key, kv.Value, kv.Present, want, wants)
		if !wants {
			config.Remove(kv.Key)
		}
	}
	// Settings outside the group, like Codex's reasoning effort or Gemini
	// CLI's login.
	for _, setting := range desired {
		if inGroup(setting.Key) {
			continue
		}
		if value, ok, present := config.Get(setting.Key); present {
			found(setting.Key, value, ok, setting.Value, true)
		}
	}
	// What AstrLink wrote before and writes no more, like a cleared effort,
	// goes back to the value it replaced. A connection key's old value
	// waits for the whole config to go.
	if previous != nil {
		for _, path := range slices.Sorted(maps.Keys(previous.Keys)) {
			if _, wants := wanted(path); wants || inGroup(path) {
				continue
			}
			value, ok, _ := config.Get(path)
			if !WrittenByUs(previous, path, value, ok) {
				continue
			}
			if original, kept := backup[path]; kept {
				delete(backup, path)
				if err := config.Set(path, original); err != nil {
					return nil, err
				}
			} else {
				config.Remove(path)
			}
		}
	}
	for _, setting := range desired {
		if err := config.Set(setting.Key, setting.Value); err != nil {
			return nil, err
		}
	}
	contents := config.Render(existing)
	createdCompanion := func(index int) bool {
		return existing[index] == nil || previous != nil && slices.Contains(previous.CreatedCompanions, index)
	}
	// A file AstrLink added beside the main one goes once a write empties
	// it, as a removal would take it; an empty provider file breaks Goose.
	for index := 1; index < len(contents); index++ {
		if createdCompanion(index) && config.Changed[index] && FileIsEmpty(config.Files[index], config.Documents[index]) {
			contents[index] = ""
		}
	}
	keys := make(map[string]string, len(desired))
	for _, setting := range desired {
		keys[setting.Key] = SHA256Hex(setting.Value)
	}
	record := &Record{
		TokenID:       connection.TokenID,
		TokenSHA256:   SHA256Hex(connection.Token),
		BaseURL:       client.BaseURL(connection.Origin),
		Keys:          keys,
		CreatedFile:   existing[0] == nil || previous != nil && previous.CreatedFile,
		WrittenAtUnix: unixNow(),
		Backup:        backup,
	}
	for index := 1; index < len(existing); index++ {
		if createdCompanion(index) {
			record.CreatedCompanions = append(record.CreatedCompanions, index)
		}
	}
	record.PasteSinceUnix = pasteSince(client, previous, connection)
	return &WritePlan{Contents: contents, Conflicts: conflicts, Record: record}, nil
}

// Removal is what a removal does to one file.
type Removal struct {
	// Kind is RemovalUnchanged, RemovalWrite with Contents, or
	// RemovalDelete.
	Kind     RemovalKind
	Contents string
}

type RemovalKind int

const (
	RemovalUnchanged RemovalKind = iota
	RemovalWrite
	RemovalDelete
)

// PlanRemove plans one removal per file, in Files order.
func PlanRemove(client Client, existing []*string, record *Record) ([]Removal, *FileError) {
	removals, _, err := PlanRestore(client, existing, record)
	return removals, err
}

// PlanRestore is PlanRemove, and how many replaced values come back. A
// key goes back to its old value only while it holds what AstrLink wrote,
// or while it is still gone; a value changed since stays.
func PlanRestore(client Client, existing []*string, record *Record) ([]Removal, int, *FileError) {
	config, err := ParseConfig(client, existing)
	if err != nil {
		return nil, 0, err
	}
	// A connection another tool or the user replaced is theirs now, with
	// the config around it, so removal only forgets AstrLink's record.
	if !connectionKept(client, config, record) {
		return make([]Removal, len(existing)), 0, nil
	}
	restored := 0
	for _, path := range slices.Sorted(maps.Keys(record.Keys)) {
		value, ok, _ := config.Get(path)
		if !WrittenByUs(record, path, value, ok) {
			continue
		}
		original, kept := record.Backup[path]
		switch {
		case kept && ok && value == original:
		case kept:
			if err := config.Set(path, original); err != nil {
				return nil, 0, err
			}
			restored++
		default:
			config.Remove(path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(record.Backup)) {
		if _, written := record.Keys[path]; !written && !config.Has(path) {
			if err := config.Set(path, record.Backup[path]); err != nil {
				return nil, 0, err
			}
			restored++
		}
	}
	removals := make([]Removal, len(config.Documents))
	for index, document := range config.Documents {
		if !config.Changed[index] {
			continue
		}
		file := config.Files[index]
		FileTidy(file, document)
		created := record.CreatedFile
		if index > 0 {
			created = slices.Contains(record.CreatedCompanions, index)
		}
		if created && FileIsEmpty(file, document) {
			removals[index] = Removal{Kind: RemovalDelete}
		} else {
			removals[index] = Removal{Kind: RemovalWrite, Contents: document.Render()}
		}
	}
	return removals, restored, nil
}

// Inspect is the state of a client's files; origin is "" when the
// gateway's address is unknown.
func Inspect(client Client, existing []*string, record *Record, origin string) State {
	config, err := ParseConfig(client, existing)
	if err != nil {
		return Invalid
	}
	if record == nil {
		return NotConfigured
	}
	if !connectionKept(client, config, record) {
		return Modified
	}
	if origin != "" && record.BaseURL != client.BaseURL(origin) {
		return Outdated
	}
	return Configured
}

// PlaceState is a place's state. Without the gateway's tokens, another
// AstrLink's config reads as outdated.
func PlaceState(records *Records, place Place, existing []*string, origin string, tokens Tokens) State {
	record := records.Get(place)
	state := Inspect(place.Client, existing, record, origin)
	if state == Outdated && tokens != nil && record != nil && !tokens.Own(record) {
		return Elsewhere
	}
	return state
}

// CurrentModelsOf is the models a client's config holds now. The client
// may have picked them itself since, like with Codex's `/model`. Names
// AstrLink could not write back are left out.
func CurrentModelsOf(client Client, existing []*string) CurrentModels {
	config, err := ParseConfig(client, existing)
	if err != nil {
		return CurrentModels{}
	}
	models := client.CurrentModels(config)
	// The default model leads the list, as AstrLink wrote it.
	if models.Model != nil && models.Models != nil {
		if at := slices.Index(models.Models, *models.Model); at >= 0 {
			id := models.Models[at]
			models.Models = append([]string{id}, slices.Delete(models.Models, at, at+1)...)
		}
	}
	return models
}

// pasteSince is when the token pasted into a client starts to count. It
// stays while the token does, and for a client that keeps it by address,
// the address.
func pasteSince(client Client, previous *Record, connection *Connection) *uint64 {
	if client.Kind() != KindPaste {
		return nil
	}
	if previous != nil && previous.TokenSHA256 == SHA256Hex(connection.Token) &&
		(!client.KeepsTokenByAddress() || previous.BaseURL == client.BaseURL(connection.Origin)) &&
		previous.PasteSinceUnix != nil {
		return Some(*previous.PasteSinceUnix)
	}
	return Some(unixNow())
}

// PlanSync points an untouched config at the gateway's new port, and adds
// the keys a released build did not write yet. The token stays as
// written, so the sync never needs to reveal it. It returns nil when
// nothing changes.
func PlanSync(client Client, existing []*string, record *Record, origin string) ([]string, *Record, *FileError) {
	state := Inspect(client, existing, record, origin)
	if state != Outdated && state != Configured {
		return nil, nil, nil
	}
	config, err := ParseConfig(client, existing)
	if err != nil {
		return nil, nil, err
	}
	next := record.Clone()
	changed := false
	if state == Outdated {
		for _, setting := range client.SyncedKeys(config, record, origin) {
			if err := config.Set(setting.Key, setting.Value); err != nil {
				return nil, nil, err
			}
			next.Keys[setting.Key] = SHA256Hex(setting.Value)
		}
		next.BaseURL = client.BaseURL(origin)
		if next.PasteSinceUnix != nil && client.KeepsTokenByAddress() {
			next.PasteSinceUnix = Some(unixNow())
		}
		changed = true
	}
	for _, setting := range client.AddedKeys() {
		// A key AstrLink wrote before and the user removed stays removed,
		// and a value the user set stays theirs.
		if _, written := record.Keys[setting.Key]; written || config.Has(setting.Key) {
			continue
		}
		if err := config.Set(setting.Key, setting.Value); err != nil {
			return nil, nil, err
		}
		next.Keys[setting.Key] = SHA256Hex(setting.Value)
		changed = true
	}
	if !changed {
		return nil, nil, nil
	}
	next.WrittenAtUnix = unixNow()
	return config.Render(existing), next, nil
}
