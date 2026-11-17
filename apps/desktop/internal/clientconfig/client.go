package clientconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Spec is what the shared code asks of every client. Each client is a file
// in clients/; what most clients share has a default, which a client
// changes by having the method of the same name (see Client).
type Spec interface {
	// ID is the id IPC and the record file name the client by.
	ID() string
	// Name is the product name, never translated.
	Name() string
	// Protocols are the gateway protocols the client can be written with,
	// preferred first.
	Protocols() []string
	// Dir is the directory that holds the client's config, and whose
	// absence means the client is not installed. target is one of
	// Targets, or "" for a client without.
	Dir(home agents.Home, target string) string
	// Files are the files the connection is written to, main file first.
	Files() []File
	// ConfigPaths is where each of Files is. record keeps a client that
	// reads one of several files on the one AstrLink wrote.
	ConfigPaths(home agents.Home, target string, record *Record) []string
	// ConnectionKeys decide where requests go and how they authenticate.
	// A model the client rewrote itself, like with Codex's or Pi's
	// `/model`, is not among them.
	ConnectionKeys() []string
	// CurrentModels are the models the config holds now.
	CurrentModels(config *Config) CurrentModels
	// DesiredKeys is every key AstrLink writes, with its value. details is
	// what an entry for each listed model says besides its id.
	DesiredKeys(connection *Connection, details Catalog) []Setting
	// BaseURLKey is the key holding the gateway address, which a sync
	// rewrites.
	BaseURLKey() string
}

// Setting is a key and the value AstrLink writes there.
type Setting struct {
	Key, Value string
}

// File is one file a client reads its connection from. Like Spec, it has
// defaults a file changes by having the method of the same name.
type File interface {
	// Format is how the client reads it.
	Format() format.Format
}

// Client is a client AstrLink writes config files for. IPC and the record
// file name it by its catalog id; an id no registered client has does not
// decode.
type Client struct{ spec Spec }

// New makes a client of its Spec.
func New(spec Spec) Client { return Client{spec} }

var registered, unlisted []Client

// Register lists clients, in the order the dialog lists them.
func Register(clients ...Client) { registered = append(registered, clients...) }

// RegisterUnlisted adds clients an id may name that the catalog does not
// list, for tests.
func RegisterUnlisted(clients ...Client) { unlisted = append(unlisted, clients...) }

// All is every registered client, in the order the dialog lists them.
func All() []Client { return slices.Clone(registered) }

func known() []Client { return append(slices.Clone(registered), unlisted...) }

// Find returns the client with this id.
func Find(id string) (Client, bool) {
	for _, client := range known() {
		if client.ID() == id {
			return client, true
		}
	}
	return Client{}, false
}

// rank is where the client sorts: the registry's order.
func (c Client) rank() int {
	for i, client := range known() {
		if client.ID() == c.ID() {
			return i
		}
	}
	return len(known())
}

// Compare orders clients as the dialog lists them.
func (c Client) Compare(other Client) int {
	if r, o := c.rank(), other.rank(); r != o {
		return r - o
	}
	return strings.Compare(c.ID(), other.ID())
}

// Valid reports whether c is a client, not the zero Client.
func (c Client) Valid() bool { return c.spec != nil }

func (c Client) Spec() Spec { return c.spec }

func (c Client) String() string { return c.ID() }

func (c Client) MarshalJSON() ([]byte, error) { return json.Marshal(c.ID()) }

func (c *Client) UnmarshalJSON(data []byte) error {
	var id string
	if err := json.Unmarshal(data, &id); err != nil {
		return err
	}
	client, ok := Find(id)
	if !ok {
		return fmt.Errorf("unknown client %s", id)
	}
	*c = client
	return nil
}

func (c Client) ID() string                                 { return c.spec.ID() }
func (c Client) Name() string                               { return c.spec.Name() }
func (c Client) Protocols() []string                        { return c.spec.Protocols() }
func (c Client) Dir(home agents.Home, target string) string { return c.spec.Dir(home, target) }
func (c Client) Files() []File                              { return c.spec.Files() }
func (c Client) ConnectionKeys() []string                   { return c.spec.ConnectionKeys() }
func (c Client) CurrentModels(config *Config) CurrentModels { return c.spec.CurrentModels(config) }
func (c Client) BaseURLKey() string                         { return c.spec.BaseURLKey() }
func (c Client) ConfigPaths(home agents.Home, target string, record *Record) []string {
	return c.spec.ConfigPaths(home, target, record)
}
func (c Client) DesiredKeys(connection *Connection, details Catalog) []Setting {
	return c.spec.DesiredKeys(connection, details)
}

// Kind is how AstrLink reaches the client: KindFile by default.
func (c Client) Kind() Kind {
	if s, ok := c.spec.(interface{ Kind() Kind }); ok {
		return s.Kind()
	}
	return KindFile
}

// Targets are the write targets of a client written in several places,
// such as two editions; none for one.
func (c Client) Targets() []string {
	if s, ok := c.spec.(interface{ Targets() []string }); ok {
		return s.Targets()
	}
	return nil
}

// ListsModels reports whether the client switches among the models
// AstrLink lists in it.
func (c Client) ListsModels() bool {
	if s, ok := c.spec.(interface{ ListsModels() bool }); ok {
		return s.ListsModels()
	}
	return false
}

// Detected reports whether the client is installed: by default, whether
// its Dir is.
func (c Client) Detected(home agents.Home, target string) bool {
	if s, ok := c.spec.(interface {
		Detected(agents.Home, string) bool
	}); ok {
		return s.Detected(home, target)
	}
	return IsDir(c.Dir(home, target))
}

// RecordedFile is the file name the record keeps for ConfigPaths.
func (c Client) RecordedFile(paths []string) (string, bool) {
	if s, ok := c.spec.(interface {
		RecordedFile([]string) (string, bool)
	}); ok {
		return s.RecordedFile(paths)
	}
	return "", false
}

// ModelFields are the Models fields the client writes, `model` first.
func (c Client) ModelFields() []string {
	if s, ok := c.spec.(interface{ ModelFields() []string }); ok {
		return s.ModelFields()
	}
	return []string{"model"}
}

// RequiresModel reports whether a write needs a default model.
func (c Client) RequiresModel() bool {
	if s, ok := c.spec.(interface{ RequiresModel() bool }); ok {
		return s.RequiresModel()
	}
	return true
}

// ReasoningEfforts are the reasoning efforts it takes; none when it takes
// none.
func (c Client) ReasoningEfforts() []string {
	if s, ok := c.spec.(interface{ ReasoningEfforts() []string }); ok {
		return s.ReasoningEfforts()
	}
	return nil
}

// TakesLimits reports whether it takes a context window and output limit
// for its default model.
func (c Client) TakesLimits() bool {
	if s, ok := c.spec.(interface{ TakesLimits() bool }); ok {
		return s.TakesLimits()
	}
	return false
}

// CheckModel rejects a default model the client cannot use.
func (c Client) CheckModel(model string) error {
	if s, ok := c.spec.(interface{ CheckModel(string) error }); ok {
		return s.CheckModel(model)
	}
	return nil
}

// BaseURL is the gateway address the client is given: `<origin>/v1` by
// default.
func (c Client) BaseURL(origin string) string {
	if s, ok := c.spec.(interface{ BaseURL(string) string }); ok {
		return s.BaseURL(origin)
	}
	return origin + "/v1"
}

// HoldsConnection reports whether a key AstrLink wrote carries an address
// or token of its own, like a model entry with its own URL and key, and so
// counts among the connection keys.
func (c Client) HoldsConnection(key string) bool {
	if s, ok := c.spec.(interface{ HoldsConnection(string) bool }); ok {
		return s.HoldsConnection(key)
	}
	return false
}

// StartFile is what a main file AstrLink creates starts with.
func (c Client) StartFile(config *Config) *FileError {
	if s, ok := c.spec.(interface{ StartFile(*Config) *FileError }); ok {
		return s.StartFile(config)
	}
	return nil
}

// SyncedKeys is each key a sync points at origin, with its new value:
// every place the client holds the gateway's address, in the form each
// needs. config is what the files hold now and record what AstrLink wrote.
func (c Client) SyncedKeys(config *Config, record *Record, origin string) []Setting {
	if s, ok := c.spec.(interface {
		SyncedKeys(*Config, *Record, string) []Setting
	}); ok {
		return s.SyncedKeys(config, record, origin)
	}
	return []Setting{{c.BaseURLKey(), c.BaseURL(origin)}}
}

// AddedKeys are keys newer versions write that configs from released
// builds lack. A sync adds each one once to a config nobody changed.
func (c Client) AddedKeys() []Setting {
	if s, ok := c.spec.(interface{ AddedKeys() []Setting }); ok {
		return s.AddedKeys()
	}
	return nil
}

// Notes are situations the dialog explains for a place, by id, such as
// `running` or `overridden`. config is nil when a file cannot be read.
func (c Client) Notes(home agents.Home, target string, config *Config) []string {
	if s, ok := c.spec.(interface {
		Notes(agents.Home, string, *Config) []string
	}); ok {
		return s.Notes(home, target, config)
	}
	return nil
}

// Running reports whether the client is open and writes its own copy of
// the files back on exit, so AstrLink leaves them alone until it quits.
func (c Client) Running(home agents.Home, target string) bool {
	if s, ok := c.spec.(interface {
		Running(agents.Home, string) bool
	}); ok {
		return s.Running(home, target)
	}
	return false
}

// KeepsTokenByAddress reports whether a client the user pastes the token
// into keeps it by address, so a new port needs another paste.
func (c Client) KeepsTokenByAddress() bool {
	if s, ok := c.spec.(interface{ KeepsTokenByAddress() bool }); ok {
		return s.KeepsTokenByAddress()
	}
	return false
}

// PasteInto are the names the token is pasted under in the client, as
// config holds them.
func (c Client) PasteInto(config *Config) []string {
	if s, ok := c.spec.(interface{ PasteInto(*Config) []string }); ok {
		return s.PasteInto(config)
	}
	return nil
}

// Place is where in a home a client is written: the client, and for a
// client with several write targets, which one. The record file keeps one
// record per place, keyed `<client>` or `<client>:<target>`.
type Place struct {
	Client Client
	// Target is one of the client's targets; "" for a client without.
	Target string
}

// NewPlace checks a target the dialog names: a client with targets needs
// one of its own, and a client without takes none.
func NewPlace(client Client, target *string) (Place, error) {
	targets := client.Targets()
	switch {
	case target == nil && len(targets) == 0:
		return Place{Client: client}, nil
	case target == nil:
		return Place{}, fmt.Errorf("%s needs a target", client.ID())
	case slices.Contains(targets, *target):
		return Place{client, *target}, nil
	}
	return Place{}, fmt.Errorf("%s has no target %s", client.ID(), *target)
}

// Places is every place of client, in its targets' order.
func Places(client Client) []Place {
	targets := client.Targets()
	if len(targets) == 0 {
		return []Place{{Client: client}}
	}
	places := make([]Place, len(targets))
	for i, target := range targets {
		places[i] = Place{client, target}
	}
	return places
}

// Key is the record file's key.
func (p Place) Key() string {
	if p.Target == "" {
		return p.Client.ID()
	}
	return p.Client.ID() + ":" + p.Target
}

// PlaceOf reads a record file's key.
func PlaceOf(key string) (Place, bool) {
	id, target, hasTarget := strings.Cut(key, ":")
	client, ok := Find(id)
	if !ok {
		return Place{}, false
	}
	var named *string
	if hasTarget {
		named = &target
	}
	place, err := NewPlace(client, named)
	return place, err == nil
}

// Compare orders places as the dialog lists them: by client, then by
// target.
func (p Place) Compare(other Place) int {
	if c := p.Client.Compare(other.Client); c != 0 {
		return c
	}
	rank := func(place Place) int {
		if place.Target == "" {
			return -1
		}
		if i := slices.Index(place.Client.Targets(), place.Target); i >= 0 {
			return i
		}
		return len(place.Client.Targets())
	}
	if r, o := rank(p), rank(other); r != o {
		return r - o
	}
	return strings.Compare(p.Target, other.Target)
}

// IsDir reports whether path is a directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// IsFile reports whether path is a regular file.
func IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
