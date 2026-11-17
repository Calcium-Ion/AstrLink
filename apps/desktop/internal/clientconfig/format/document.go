package format

// Format is how a client reads one of its files: a JSONFormat, TOML, an
// EnvFormat, or a YAMLFormat.
type Format interface{ isFormat() }

// TOMLFormat is a TOML file.
type TOMLFormat struct{}

// EnvFormat is `NAME=value` lines; values of the SingleLine names may not
// span lines.
type EnvFormat struct {
	SingleLine []string
}

// YAMLFormat is YAML whose root is Root, or nothing yet.
type YAMLFormat struct {
	Root YAMLRoot
}

func (JSONFormat) isFormat() {}
func (TOMLFormat) isFormat() {}
func (EnvFormat) isFormat()  {}
func (YAMLFormat) isFormat() {}

// Document is a config file opened for in-place editing: a *JSONDocument,
// *TOMLDocument, *EnvFile, or *YAMLDocument.
type Document interface {
	// Get returns the string at key: present is false when there is
	// nothing there, and ok false for another value.
	Get(key string) (value string, ok, present bool)
	Set(key, value string) error
	// Remove drops key. A YAML key that cannot be removed in place stays;
	// the check after the removal reports the file.
	Remove(key string)
	// DropEmpty drops each of the objects containers names, innermost
	// first, that a removal left empty. Only JSON has any.
	DropEmpty(containers []string)
	IsEmpty() bool
	Render() string
}

// ParseDocument opens text, the file's content or "" for none, as format.
func ParseDocument(format Format, text string) (Document, error) {
	switch f := format.(type) {
	case JSONFormat:
		return ParseJSON(text, f)
	case TOMLFormat:
		return ParseTOML(text)
	case EnvFormat:
		env, err := ParseEnv(text, f.SingleLine)
		if err != nil {
			return nil, err
		}
		return envKeys{env}, nil
	case YAMLFormat:
		doc, err := ParseYAML(text, f.Root)
		if err != nil {
			return nil, err
		}
		return yamlKeys{doc}, nil
	}
	panic("format: unknown format")
}

// AsYAML returns the YAML file a Document opened, or nil.
func AsYAML(doc Document) *YAMLDocument {
	if keys, ok := doc.(yamlKeys); ok {
		return keys.YAMLDocument
	}
	return nil
}

// yamlKeys is a YAML file edited by dotted keys.
type yamlKeys struct{ *YAMLDocument }

func (y yamlKeys) Get(key string) (string, bool, bool) { return y.GetKey(key) }
func (y yamlKeys) Set(key, value string) error         { return y.SetKey(key, value) }
func (y yamlKeys) Remove(key string)                   { _ = y.RemoveKey(key) }
func (y yamlKeys) DropEmpty([]string)                  {}

// AsEnv returns the `.env` file a Document opened, or nil.
func AsEnv(doc Document) *EnvFile {
	if keys, ok := doc.(envKeys); ok {
		return keys.EnvFile
	}
	return nil
}

// envKeys is a `.env` file edited by name.
type envKeys struct{ *EnvFile }

func (e envKeys) Get(name string) (string, bool, bool) {
	value, ok := e.EnvFile.Get(name)
	return value, ok, ok
}
func (envKeys) DropEmpty([]string) {}

// DropEmpty does nothing: TOML clients drop their own tables.
func (*TOMLDocument) DropEmpty([]string) {}
