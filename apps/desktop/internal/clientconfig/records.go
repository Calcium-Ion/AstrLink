package clientconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// `~/.astrlink/client-configs.json` holds what AstrLink wrote in each place
// of a home, and the values it replaced there.

const (
	// recordsVersion 2 adds Gemini CLI, OpenCode, OpenClaw, write targets,
	// and the values AstrLink replaced. Version 1 files, from released
	// builds, read as keeping none.
	recordsVersion = 2
	recordsV1      = 1
)

// v1Keys are the places version 1 keys: released builds read a file with
// these and nothing else.
var v1Keys = []string{"claude", "codex", "pi"}

// Record is what AstrLink wrote in one place. Its own values are kept only
// as SHA-256 fingerprints, so the file never holds the token.
type Record struct {
	TokenID     string
	TokenSHA256 string
	BaseURL     string
	Keys        map[string]string
	CreatedFile bool
	// CreatedCompanions are the client's other files AstrLink created, by
	// their place in Files; each goes once a removal empties it. Version 1
	// has no room for them, so a version 1 file keeps such files.
	CreatedCompanions []int
	WrittenAtUnix     uint64
	// Backup holds the values AstrLink replaced, by key, put back when its
	// config is removed. They may be other providers' credentials, so the
	// record file is as private as the configs.
	Backup map[string]string
	// File is the file AstrLink wrote, for a client that reads one of
	// several; nil for none.
	File *string
	// PasteSinceUnix is, for a client the user pastes the token into, since
	// when its requests show the paste worked. A new token, or a new
	// address for a client that keeps the token by address, starts it
	// again.
	PasteSinceUnix *uint64
}

// Clone copies a record.
func (r *Record) Clone() *Record {
	out := *r
	out.Keys = maps.Clone(r.Keys)
	out.Backup = maps.Clone(r.Backup)
	out.CreatedCompanions = slices.Clone(r.CreatedCompanions)
	return &out
}

// object is the record as serde writes it, its fields in order.
func (r *Record) object() format.Object {
	strings := func(m map[string]string) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	object := format.Object{
		{Key: "token_id", Value: r.TokenID},
		{Key: "token_sha256", Value: r.TokenSHA256},
		{Key: "base_url", Value: r.BaseURL},
		{Key: "keys", Value: strings(r.Keys)},
		{Key: "created_file", Value: r.CreatedFile},
	}
	if len(r.CreatedCompanions) > 0 {
		items := make([]any, len(r.CreatedCompanions))
		for i, index := range r.CreatedCompanions {
			items[i] = format.Number(strconv.Itoa(index))
		}
		object = append(object, format.Member{Key: "created_companions", Value: items})
	}
	object = append(object, format.Member{Key: "written_at_unix", Value: format.Number(strconv.FormatUint(r.WrittenAtUnix, 10))})
	if len(r.Backup) > 0 {
		object = append(object, format.Member{Key: "backup", Value: strings(r.Backup)})
	}
	if r.File != nil {
		object = append(object, format.Member{Key: "file", Value: *r.File})
	}
	if r.PasteSinceUnix != nil {
		object = append(object, format.Member{Key: "paste_since_unix", Value: format.Number(strconv.FormatUint(*r.PasteSinceUnix, 10))})
	}
	return object
}

func (r *Record) MarshalJSON() ([]byte, error) { return []byte(format.Encode(r.object())), nil }

// UnmarshalJSON reads a record as serde does: every field it requires,
// and no unknown one.
func (r *Record) UnmarshalJSON(data []byte) error {
	var raw struct {
		TokenID           *string           `json:"token_id"`
		TokenSHA256       *string           `json:"token_sha256"`
		BaseURL           *string           `json:"base_url"`
		Keys              map[string]string `json:"keys"`
		CreatedFile       *bool             `json:"created_file"`
		CreatedCompanions []int             `json:"created_companions"`
		WrittenAtUnix     *uint64           `json:"written_at_unix"`
		Backup            map[string]string `json:"backup"`
		File              *string           `json:"file"`
		PasteSinceUnix    *uint64           `json:"paste_since_unix"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if raw.TokenID == nil || raw.TokenSHA256 == nil || raw.BaseURL == nil || raw.Keys == nil || raw.CreatedFile == nil || raw.WrittenAtUnix == nil {
		return fmt.Errorf("a record lacks a field")
	}
	for _, index := range raw.CreatedCompanions {
		if index < 0 {
			return fmt.Errorf("invalid created_companions")
		}
	}
	*r = Record{
		TokenID: *raw.TokenID, TokenSHA256: *raw.TokenSHA256, BaseURL: *raw.BaseURL, Keys: raw.Keys,
		CreatedFile: *raw.CreatedFile, CreatedCompanions: raw.CreatedCompanions, WrittenAtUnix: *raw.WrittenAtUnix,
		Backup: raw.Backup, File: raw.File, PasteSinceUnix: raw.PasteSinceUnix,
	}
	if r.Backup == nil {
		r.Backup = map[string]string{}
	}
	return nil
}

// Records is the record file: one record per place.
type Records struct {
	version int
	places  map[string]*Record
}

func (r *Records) needsV2() bool {
	for key, record := range r.places {
		if !slices.Contains(v1Keys, key) || len(record.Backup) > 0 || record.File != nil {
			return true
		}
	}
	return false
}

// Get returns the place's record, nil for none.
func (r *Records) Get(place Place) *Record { return r.places[place.Key()] }

// Set replaces the place's record, nil for none, returning the one it had.
func (r *Records) Set(place Place, record *Record) *Record {
	if r.places == nil {
		r.places = map[string]*Record{}
	}
	previous := r.places[place.Key()]
	if record == nil {
		delete(r.places, place.Key())
	} else {
		r.places[place.Key()] = record
	}
	return previous
}

// Places lists the places with a record, in the dialog's order.
func (r *Records) Places() []Place {
	var places []Place
	for key := range r.places {
		if place, ok := PlaceOf(key); ok {
			places = append(places, place)
		}
	}
	slices.SortFunc(places, Place.Compare)
	return places
}

// RecordsPath is the record file of a home.
func RecordsPath(home agents.Home) string {
	return filepath.Join(home.Dir(), ".astrlink", "client-configs.json")
}

// ReadRecords reads a home's record file; a missing one holds none.
func ReadRecords(home agents.Home) (*Records, error) {
	path := RecordsPath(home)
	text, exists, err := agents.ReadOptional(path)
	if err != nil || !exists {
		return &Records{places: map[string]*Record{}}, err
	}
	records, err := decodeRecords([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid record file", path)
	}
	if records.version != recordsV1 && records.version != recordsVersion {
		return nil, fmt.Errorf("%s has an unknown version", path)
	}
	return records, nil
}

func decodeRecords(data []byte) (*Records, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("not an object")
	}
	records := &Records{places: map[string]*Record{}}
	version, ok := raw["version"]
	if !ok {
		return nil, fmt.Errorf("no version")
	}
	var v uint32
	if err := json.Unmarshal(version, &v); err != nil {
		return nil, err
	}
	records.version = int(v)
	for key, value := range raw {
		// Clients 0.1 noted as moved to CC Switch. Read and dropped: a
		// config CC Switch wrote is another tool's, like any other.
		if key == "version" || key == "cc_switch" {
			continue
		}
		place, ok := PlaceOf(key)
		if !ok {
			return nil, fmt.Errorf("unknown client %s", key)
		}
		record := &Record{}
		if err := json.Unmarshal(value, record); err != nil {
			return nil, err
		}
		records.places[place.Key()] = record
	}
	return records, nil
}

// encode writes the record file as serde_json's to_string_pretty does.
func (r *Records) encode() string {
	object := format.Object{{Key: "version", Value: format.Number(strconv.Itoa(r.version))}}
	for _, place := range r.Places() {
		object = append(object, format.Member{Key: place.Key(), Value: r.places[place.Key()].object()})
	}
	return format.EncodePretty(object) + "\n"
}

// WriteRecords writes a home's record file, or removes it once it holds
// none.
func WriteRecords(home agents.Home, records *Records) error {
	path := RecordsPath(home)
	if len(records.places) == 0 {
		return agents.RemovePath(path)
	}
	// Released builds share this file and read version 1 only, so it stays
	// there until it holds something version 1 cannot.
	if records.needsV2() {
		records.version = recordsVersion
	} else {
		records.version = recordsV1
		for _, record := range records.places {
			record.CreatedCompanions = nil
		}
	}
	return agents.WriteText(path, records.encode(), agents.WriteOptions{Secret: true})
}

// SHA256Hex is a value's fingerprint.
func SHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// unixNow is now in seconds since the Unix epoch; replaced by tests.
var unixNow = func() uint64 { return uint64(max(time.Now().Unix(), 0)) }
