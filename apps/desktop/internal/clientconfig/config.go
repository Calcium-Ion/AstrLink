package clientconfig

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// FileError is why one of a client's config files cannot be edited, by the
// file's position in Files.
type FileError struct {
	File    int
	Message string
	// Line is the 1-based line the file stops parsing at; 0 for none.
	Line int
}

func (e *FileError) Error() string { return e.Message }

// At names the file at fault among paths.
func (e *FileError) At(paths []string) *ItemError {
	path := paths[e.File]
	out := &ItemError{Message: path + " " + e.Message, Path: &path}
	if e.Line > 0 {
		line := e.Line
		out.Line = &line
	}
	return out
}

// ItemError is why writing or removing one client's config failed. It
// never holds a value from the files.
type ItemError struct {
	Message string `json:"message"`
	// Path is the file at fault, when one is.
	Path *string `json:"path"`
	Line *int    `json:"line"`
}

func (e *ItemError) Error() string { return e.Message }

// itemError is err as an ItemError.
func itemError(err error) *ItemError {
	if e, ok := err.(*ItemError); ok {
		return e
	}
	return &ItemError{Message: err.Error()}
}

// The defaults of a File, which a file changes by having the method.

// FileSecret reports whether the file holds credentials and nothing a
// preview may show. Every file a write changes becomes owner-only, since
// most hold the token; a secret file also stays owner-only when a removal
// rewrites it, and its preview masks every value, whatever the key's name.
func FileSecret(f File) bool {
	if s, ok := f.(interface{ Secret() bool }); ok {
		return s.Secret()
	}
	return false
}

// FileRead reads the settings the file represents, nil for a missing one.
// A database client returns the rows it manages as JSON.
func FileRead(f File, path string) (*string, error) {
	if s, ok := f.(interface {
		Read(string) (*string, error)
	}); ok {
		return s.Read(path)
	}
	text, exists, err := agents.ReadOptional(path)
	if err != nil || !exists {
		return nil, err
	}
	return &text, nil
}

// FileWrite commits after, or removes the file for nil, once what it holds
// still is before.
func FileWrite(f File, path string, before, after *string, secret bool) error {
	if s, ok := f.(interface {
		Write(path string, before, after *string, secret bool) error
	}); ok {
		return s.Write(path, before, after, secret)
	}
	if after != nil {
		return agents.WriteText(path, *after, agents.WriteOptions{Secret: secret, Expected: expected(before)})
	}
	current, err := FileRead(f, path)
	if err != nil {
		return err
	}
	if !sameText(current, before) {
		return fmt.Errorf("unable to remove %s: the file changed; try again", path)
	}
	return agents.RemovePath(path)
}

// FilePreview hides values whose privacy depends on their position, before
// the preview masks credential-named keys.
func FilePreview(f File, text string) string {
	if s, ok := f.(interface{ Preview(string) string }); ok {
		return s.Preview(text)
	}
	return text
}

// FileHolds reports whether this file of its client holds the key.
func FileHolds(f File, key string) bool {
	if s, ok := f.(interface{ Holds(string) bool }); ok {
		return s.Holds(key)
	}
	return true
}

// FileUnit is what a change counts in a preview: `line` for `.env`, else
// `key`.
func FileUnit(f File) string {
	if _, ok := f.Format().(format.EnvFormat); ok {
		return "line"
	}
	return "key"
}

// FileGroup is every key in the group AstrLink takes over, with its
// current value.
func FileGroup(f File, document format.Document) []format.KeyValue {
	if s, ok := f.(interface {
		Group(format.Document) []format.KeyValue
	}); ok {
		return s.Group(document)
	}
	return nil
}

// FileGet reads key: present is false when there is nothing there, and ok
// false for a value that is not a string.
func FileGet(f File, document format.Document, key string) (value string, ok, present bool) {
	if s, has := f.(interface {
		Get(format.Document, string) (string, bool, bool)
	}); has {
		return s.Get(document, key)
	}
	return document.Get(key)
}

func FileSet(f File, document format.Document, key, value string) error {
	if s, ok := f.(interface {
		Set(format.Document, string, string) error
	}); ok {
		return s.Set(document, key, value)
	}
	return document.Set(key, value)
}

func FileRemove(f File, document format.Document, key string) {
	if s, ok := f.(interface{ Remove(format.Document, string) }); ok {
		s.Remove(document, key)
		return
	}
	document.Remove(key)
}

// FileEmptied are objects a removal drops once they are empty, innermost
// first.
func FileEmptied(f File) []string {
	if s, ok := f.(interface{ Emptied() []string }); ok {
		return s.Emptied()
	}
	return nil
}

// FileTidy drops the containers a removal left empty.
func FileTidy(f File, document format.Document) {
	if s, ok := f.(interface{ Tidy(format.Document) }); ok {
		s.Tidy(document)
		return
	}
	document.DropEmpty(FileEmptied(f))
}

// FileIsEmpty reports whether nothing is left, so a file AstrLink created
// can go.
func FileIsEmpty(f File, document format.Document) bool {
	if s, ok := f.(interface{ IsEmpty(format.Document) bool }); ok {
		return s.IsEmpty(document)
	}
	return document.IsEmpty()
}

// Config is every config file of one client, opened together. Each key
// lives in exactly one of them.
type Config struct {
	Files     []File
	Documents []format.Document
	Changed   []bool
}

// ParseConfig opens a client's files; existing holds each one's text, nil
// for a missing file.
func ParseConfig(client Client, existing []*string) (*Config, *FileError) {
	files := client.Files()
	if len(existing) != len(files) {
		return nil, &FileError{Message: "was read without its companion files"}
	}
	config := &Config{Files: files, Changed: make([]bool, len(files))}
	for i, file := range files {
		text := ""
		if existing[i] != nil {
			text = *existing[i]
		}
		document, err := format.ParseDocument(file.Format(), text)
		if err != nil {
			out := &FileError{File: i, Message: err.Error()}
			if invalid, ok := err.(*format.Invalid); ok {
				out.Message, out.Line = invalid.Message, invalid.Line
			}
			return nil, out
		}
		config.Documents = append(config.Documents, document)
	}
	return config, nil
}

// Index is the file that holds path.
func (c *Config) Index(path string) int {
	for i, file := range c.Files {
		if FileHolds(file, path) {
			return i
		}
	}
	return 0
}

// Get reads path: present is false when there is nothing there, and ok
// false for a value that is not a string.
func (c *Config) Get(path string) (value string, ok, present bool) {
	i := c.Index(path)
	return FileGet(c.Files[i], c.Documents[i], path)
}

// String is the string at path, if there is one.
func (c *Config) String(path string) (string, bool) {
	value, ok, _ := c.Get(path)
	return value, ok
}

// Has reports whether anything is at path.
func (c *Config) Has(path string) bool {
	_, _, present := c.Get(path)
	return present
}

// Group is every key in the groups AstrLink takes over.
func (c *Config) Group() []format.KeyValue {
	var keys []format.KeyValue
	for i, file := range c.Files {
		keys = append(keys, FileGroup(file, c.Documents[i])...)
	}
	return keys
}

func (c *Config) Set(path, value string) *FileError {
	i := c.Index(path)
	c.Changed[i] = true
	if err := FileSet(c.Files[i], c.Documents[i], path, value); err != nil {
		return &FileError{File: i, Message: err.Error()}
	}
	return nil
}

func (c *Config) Remove(path string) {
	i := c.Index(path)
	c.Changed[i] = true
	FileRemove(c.Files[i], c.Documents[i], path)
}

// Render returns new contents per file; a file left alone keeps its text,
// and a missing one stays empty.
func (c *Config) Render(existing []*string) []string {
	contents := make([]string, len(c.Documents))
	for i, document := range c.Documents {
		switch {
		case c.Changed[i]:
			contents[i] = document.Render()
		case existing[i] != nil:
			contents[i] = *existing[i]
		}
	}
	return contents
}

// Model is the model at path, when AstrLink could write it back.
func (c *Config) Model(path string) (string, bool) {
	value, ok := c.String(path)
	if !ok {
		return "", false
	}
	return CleanModel(value)
}

// ProviderModel is AstrLink's model in a client that names the provider
// first.
func (c *Config) ProviderModel(path string) (string, bool) {
	value, ok := c.Model(path)
	if !ok {
		return "", false
	}
	value, ok = strings.CutPrefix(value, ProviderID+"/")
	if !ok {
		return "", false
	}
	return CleanModel(value)
}

// ReadJSON reads a JSON config file as the client reads it, for importing
// the providers it configures. Nothing here writes.
func ReadJSON(file File, text string) (any, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]any{}, nil
	}
	document, err := format.ParseDocument(file.Format(), text)
	if err != nil {
		if invalid, ok := err.(*format.Invalid); ok {
			return nil, &ItemError{Message: invalid.Message}
		}
		return nil, err
	}
	json, ok := document.(*format.JSONDocument)
	if !ok {
		return nil, &ItemError{Message: "is not valid JSON"}
	}
	value, ok := json.Value()
	if !ok {
		return nil, &ItemError{Message: "is not valid JSON"}
	}
	return value, nil
}
