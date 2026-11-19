package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
)

// A new-api plugin's meta.configSchema describes the config of an instance
// of its request processing part, and meta.channelConfig the settings of
// a provider of its provider part. The host checks a schema when the plugin
// loads, checks a config against it when it is saved, and fills in its
// defaults when a hook reads it.

// ConfigSchema is {type: "object", properties, required?}.
type ConfigSchema struct {
	Type       string           `json:"type"`
	Properties ConfigProperties `json:"properties"`
	Required   []string         `json:"required,omitempty"`
}

// ConfigProperties are an object's fields in the order the schema declares
// them.
type ConfigProperties []ConfigProperty

// ConfigProperty is one named field.
type ConfigProperty struct {
	Name  string
	Field *ConfigField
}

// ConfigField is one field of a schema.
type ConfigField struct {
	Type                 string                     `json:"type"`
	Title                LocalizedText              `json:"title"`
	Description          LocalizedText              `json:"description,omitempty"`
	Default              json.RawMessage            `json:"default,omitempty"`
	Examples             []json.RawMessage          `json:"examples,omitempty"`
	Enum                 []json.RawMessage          `json:"enum,omitempty"`
	EnumTitles           []LocalizedText            `json:"enumTitles,omitempty"`
	Minimum              *float64                   `json:"minimum,omitempty"`
	Maximum              *float64                   `json:"maximum,omitempty"`
	Unit                 string                     `json:"unit,omitempty"`
	MinLength            *int                       `json:"minLength,omitempty"`
	MaxLength            *int                       `json:"maxLength,omitempty"`
	Pattern              string                     `json:"pattern,omitempty"`
	Format               string                     `json:"format,omitempty"`
	Items                *ConfigField               `json:"items,omitempty"`
	MinItems             *int                       `json:"minItems,omitempty"`
	MaxItems             *int                       `json:"maxItems,omitempty"`
	UniqueItems          bool                       `json:"uniqueItems,omitempty"`
	Properties           ConfigProperties           `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	VisibleWhen          map[string]json.RawMessage `json:"visibleWhen,omitempty"`
	AdditionalProperties *ConfigField               `json:"additionalProperties,omitempty"`

	pattern *regexp.Regexp
}

// UnmarshalJSON reads properties in their declared order, refusing members
// a field does not have.
func (properties *ConfigProperties) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("properties is an object of fields")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, _ := token.(string)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		field := &ConfigField{}
		if err := strictJSON(raw, field); err != nil {
			return fmt.Errorf("%s: %s", name, strings.TrimPrefix(err.Error(), "json: "))
		}
		for _, have := range *properties {
			if have.Name == name {
				return fmt.Errorf("%s is declared twice", name)
			}
		}
		*properties = append(*properties, ConfigProperty{Name: name, Field: field})
	}
	_, err = decoder.Token()
	return err
}

// MarshalJSON writes properties in their declared order.
func (properties ConfigProperties) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, property := range properties {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, _ := json.Marshal(property.Name)
		buffer.Write(name)
		buffer.WriteByte(':')
		field, err := json.Marshal(property.Field)
		if err != nil {
			return nil, err
		}
		buffer.Write(field)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func (properties ConfigProperties) field(name string) *ConfigField {
	for _, property := range properties {
		if property.Name == name {
			return property.Field
		}
	}
	return nil
}

// configFormats are the formats a string field may take; json is the one
// of an object field.
var configFormats = []string{"textarea", "regex", "url", "password", "keywords"}

// ParseConfigSchema reads and checks a schema; channel says it is a
// provider's channelConfig, whose object fields may hold a value per model.
func ParseConfigSchema(raw json.RawMessage, channel bool) (*ConfigSchema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	schema := &ConfigSchema{}
	if err := strictJSON(raw, schema); err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	if schema.Type != "object" {
		return nil, errors.New(`the schema is {type: "object", properties, required?}`)
	}
	if err := checkFields(schema.Properties, schema.Required, 1, channel, true); err != nil {
		return nil, err
	}
	return schema, nil
}

func checkFields(properties ConfigProperties, required []string, level int, channel, top bool) error {
	for _, name := range required {
		if properties.field(name) == nil {
			return fmt.Errorf("required names %q, which properties does not declare", name)
		}
	}
	for _, property := range properties {
		if err := checkField(property.Field, level, channel, top); err != nil {
			return fmt.Errorf("%s: %w", property.Name, err)
		}
		for sibling, values := range property.Field.VisibleWhen {
			other := properties.field(sibling)
			if other == nil || sibling == property.Name || !slices.Contains([]string{"string", "number", "integer", "boolean"}, other.Type) {
				return fmt.Errorf("%s: visibleWhen names %q, which is not a string, number, integer or boolean sibling", property.Name, sibling)
			}
			list := []json.RawMessage{values}
			if trimmed := bytes.TrimSpace(values); len(trimmed) > 0 && trimmed[0] == '[' {
				list = nil
				if json.Unmarshal(values, &list) != nil || len(list) == 0 {
					return fmt.Errorf("%s: visibleWhen.%s is a value or a list of values", property.Name, sibling)
				}
			}
			for _, value := range list {
				if _, err := other.check(value, false); err != nil {
					return fmt.Errorf("%s: visibleWhen.%s: %w", property.Name, sibling, err)
				}
			}
		}
	}
	return nil
}

func checkField(field *ConfigField, level int, channel, top bool) error {
	if level > 4 {
		return errors.New("the schema nests deeper than 4 levels")
	}
	if len(field.Title) == 0 {
		return errors.New("title is required")
	}
	if err := validateLocalized(field.Title, 256, "title"); err != nil {
		return err
	}
	if err := validateLocalized(field.Description, 512, "description"); err != nil {
		return err
	}
	numeric := field.Type == "number" || field.Type == "integer"
	switch field.Type {
	case "string", "number", "integer", "boolean", "array", "object":
	default:
		return fmt.Errorf("type %q is not string, number, integer, boolean, array or object", field.Type)
	}
	switch {
	case (field.Minimum != nil || field.Maximum != nil || field.Unit != "") && !numeric:
		return errors.New("minimum, maximum and unit are for a number or an integer")
	case (field.MinLength != nil || field.MaxLength != nil || field.Pattern != "") && field.Type != "string":
		return errors.New("minLength, maxLength and pattern are for a string")
	case (field.Items != nil || field.MinItems != nil || field.MaxItems != nil || field.UniqueItems) && field.Type != "array":
		return errors.New("items, minItems, maxItems and uniqueItems are for an array")
	case field.Type == "array" && field.Items == nil:
		return errors.New("an array declares items")
	case (len(field.Properties) > 0 || len(field.Required) > 0) && field.Type != "object":
		return errors.New("properties and required are for an object")
	case len(field.Enum) > 0 && field.Type != "string" && !numeric:
		return errors.New("enum is for a string, number or integer")
	case len(field.EnumTitles) > 0 && len(field.EnumTitles) != len(field.Enum):
		return errors.New("enumTitles has one title per enum value")
	case field.Unit != "" && !slices.Contains([]string{"byte", "pixel", "character", "second"}, field.Unit):
		return fmt.Errorf("unit %q is not byte, pixel, character or second", field.Unit)
	case field.AdditionalProperties != nil && (!channel || field.Type != "object" || len(field.Properties) > 0):
		return errors.New("additionalProperties holds a value per model, on an object field of channelConfig without properties")
	case len(field.VisibleWhen) > 0 && level > 1 && !top:
		return errors.New("visibleWhen is for a property of the schema")
	}
	switch field.Format {
	case "":
	case "json":
		if field.Type != "object" {
			return errors.New("format json is for an object")
		}
	case "keywords":
		if field.Type != "string" || !top {
			return errors.New("format keywords is for a string property of the schema itself")
		}
	default:
		if field.Type != "string" || !slices.Contains(configFormats, field.Format) {
			return fmt.Errorf("format %q is not one of textarea, regex, url, password, keywords and, for an object, json", field.Format)
		}
	}
	if field.Pattern != "" {
		compiled, err := regexp.Compile(field.Pattern)
		if err != nil {
			return fmt.Errorf("pattern %q is not a regular expression", field.Pattern)
		}
		field.pattern = compiled
	}
	seen := map[string]bool{}
	for _, value := range field.Enum {
		var probe any
		if json.Unmarshal(value, &probe) != nil {
			return errors.New("enum holds JSON values")
		}
		if _, isText := probe.(string); isText != (field.Type == "string") {
			return errors.New("enum values are strings for a string field and numbers for a number")
		}
		key := string(bytes.TrimSpace(value))
		if seen[key] {
			return errors.New("enum repeats a value")
		}
		seen[key] = true
	}
	for _, title := range field.EnumTitles {
		if err := validateLocalized(title, 256, "enumTitles"); err != nil {
			return err
		}
	}
	if field.Items != nil {
		if err := checkField(field.Items, level+1, channel, false); err != nil {
			return fmt.Errorf("items: %w", err)
		}
	}
	if field.AdditionalProperties != nil {
		value := field.AdditionalProperties
		if !slices.Contains([]string{"string", "number", "integer", "boolean"}, value.Type) || len(value.Default) > 0 || len(value.VisibleWhen) > 0 {
			return errors.New("additionalProperties is a string, number, integer or boolean field without default or visibleWhen")
		}
		if err := checkField(value, level+1, channel, false); err != nil {
			return fmt.Errorf("additionalProperties: %w", err)
		}
	}
	if len(field.Properties) > 0 {
		if err := checkFields(field.Properties, field.Required, level+1, channel, false); err != nil {
			return err
		}
	}
	if len(field.Default) > 0 {
		if _, err := field.check(field.Default, false); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	for _, example := range field.Examples {
		if _, err := field.check(example, false); err != nil {
			return fmt.Errorf("examples: %w", err)
		}
	}
	return nil
}

// CheckConfig checks config, an object, against the schema and returns it
// with the default of every absent property filled in. A nil schema takes
// only {}.
func (schema *ConfigSchema) CheckConfig(config json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage("{}")
	}
	if schema == nil {
		var members map[string]json.RawMessage
		if json.Unmarshal(config, &members) != nil || len(members) > 0 {
			return nil, errors.New("the plugin takes no config")
		}
		return json.RawMessage("{}"), nil
	}
	object := &ConfigField{Type: "object", Properties: schema.Properties, Required: schema.Required}
	return object.check(config, true)
}

// Defaults is the config with every default filled in, without checking
// it: a stored config the plugin's new version rejects reaches its hooks as
// stored.
func (schema *ConfigSchema) Defaults(config json.RawMessage) json.RawMessage {
	if checked, err := schema.CheckConfig(config); err == nil {
		return checked
	}
	if schema == nil {
		return json.RawMessage("{}")
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(config, &members) != nil || members == nil {
		members = map[string]json.RawMessage{}
	}
	ordered := []byte("{")
	first := true
	write := func(name string, value json.RawMessage) {
		if !first {
			ordered = append(ordered, ',')
		}
		first = false
		key, _ := json.Marshal(name)
		ordered = append(append(append(ordered, key...), ':'), value...)
	}
	for _, property := range schema.Properties {
		if value, ok := members[property.Name]; ok {
			write(property.Name, value)
		} else if len(property.Field.Default) > 0 {
			write(property.Name, property.Field.Default)
		}
	}
	return append(ordered, '}')
}

// check checks value against the field and returns it with defaults
// filled in below it when fill is set.
func (field *ConfigField) check(value json.RawMessage, fill bool) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return nil, errors.New("a value is missing")
	}
	switch field.Type {
	case "string":
		var text string
		if json.Unmarshal(trimmed, &text) != nil {
			return nil, errors.New("must be a string")
		}
		length := len(utf16.Encode([]rune(text)))
		switch {
		case field.MinLength != nil && length < *field.MinLength:
			return nil, fmt.Errorf("must be at least %d characters", *field.MinLength)
		case field.MaxLength != nil && length > *field.MaxLength:
			return nil, fmt.Errorf("must be at most %d characters", *field.MaxLength)
		case field.pattern != nil && !field.pattern.MatchString(text):
			return nil, errors.New("does not match the pattern")
		case field.Format == "url" && text != "" && !configURL(text):
			return nil, errors.New("must be an http or https address without credentials or fragment")
		}
	case "number", "integer":
		var number float64
		if json.Unmarshal(trimmed, &number) != nil || trimmed[0] == '"' {
			return nil, errors.New("must be a number")
		}
		switch {
		case field.Type == "integer" && (number != math.Trunc(number) || math.Abs(number) > 1<<53):
			return nil, errors.New("must be an integer")
		case field.Minimum != nil && number < *field.Minimum:
			return nil, fmt.Errorf("must be at least %v", *field.Minimum)
		case field.Maximum != nil && number > *field.Maximum:
			return nil, fmt.Errorf("must be at most %v", *field.Maximum)
		}
	case "boolean":
		if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
			return nil, errors.New("must be true or false")
		}
	case "array":
		var items []json.RawMessage
		if trimmed[0] != '[' || json.Unmarshal(trimmed, &items) != nil {
			return nil, errors.New("must be an array")
		}
		switch {
		case field.MinItems != nil && len(items) < *field.MinItems:
			return nil, fmt.Errorf("must hold at least %d items", *field.MinItems)
		case field.MaxItems != nil && len(items) > *field.MaxItems:
			return nil, fmt.Errorf("must hold at most %d items", *field.MaxItems)
		}
		seen := map[string]bool{}
		for index, item := range items {
			checked, err := field.Items.check(item, fill)
			if err != nil {
				return nil, fmt.Errorf("item %d %w", index, err)
			}
			if field.UniqueItems {
				key := canonicalJSON(checked)
				if seen[key] {
					return nil, errors.New("must not repeat an item")
				}
				seen[key] = true
			}
			items[index] = checked
		}
		if fill {
			encoded, _ := json.Marshal(items)
			return encoded, nil
		}
	case "object":
		if trimmed[0] != '{' {
			return nil, errors.New("must be an object")
		}
		var members map[string]json.RawMessage
		if json.Unmarshal(trimmed, &members) != nil {
			return nil, errors.New("must be an object")
		}
		if field.AdditionalProperties != nil {
			for name, member := range members {
				if _, err := field.AdditionalProperties.check(member, false); err != nil {
					return nil, fmt.Errorf("%s %w", name, err)
				}
			}
			break
		}
		if len(field.Properties) == 0 {
			break
		}
		for name := range members {
			if field.Properties.field(name) == nil {
				return nil, fmt.Errorf("has %q, which the schema does not declare", name)
			}
		}
		for _, name := range field.Required {
			// A required property the config leaves out takes its default.
			if _, ok := members[name]; !ok && !(fill && len(field.Properties.field(name).Default) > 0) {
				return nil, fmt.Errorf("needs %s", name)
			}
		}
		ordered := []byte("{")
		count := 0
		for _, property := range field.Properties {
			member, present := members[property.Name]
			switch {
			case present:
				checked, err := property.Field.check(member, fill)
				if err != nil {
					return nil, fmt.Errorf("%s %w", property.Name, err)
				}
				member = checked
			case fill && len(property.Field.Default) > 0:
				member = property.Field.Default
			default:
				continue
			}
			if count > 0 {
				ordered = append(ordered, ',')
			}
			count++
			key, _ := json.Marshal(property.Name)
			ordered = append(append(append(ordered, key...), ':'), member...)
		}
		if fill {
			return append(ordered, '}'), nil
		}
	}
	if len(field.Enum) > 0 {
		key := canonicalJSON(trimmed)
		if !slices.ContainsFunc(field.Enum, func(option json.RawMessage) bool { return canonicalJSON(option) == key }) {
			return nil, errors.New("must be one of the listed values")
		}
	}
	return trimmed, nil
}

// canonicalJSON spells a value the same way whatever its spacing and
// number spelling.
func canonicalJSON(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func configURL(text string) bool {
	parsed, err := url.Parse(text)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

// KeywordFields are the names of the schema's keyword lists.
func (schema *ConfigSchema) KeywordFields() []string {
	if schema == nil {
		return nil
	}
	var names []string
	for _, property := range schema.Properties {
		if property.Field.Format == "keywords" {
			names = append(names, property.Name)
		}
	}
	return names
}

// URLHosts are the hosts of the non-empty url values of a config, which a
// request processing hook may fetch from.
func (schema *ConfigSchema) URLHosts(config json.RawMessage) []string {
	if schema == nil {
		return nil
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal(config, &members)
	var hosts []string
	for _, property := range schema.Properties {
		if property.Field.Format != "url" {
			continue
		}
		var text string
		if json.Unmarshal(members[property.Name], &text) != nil || text == "" {
			continue
		}
		if parsed, err := url.Parse(text); err == nil && parsed.Host != "" {
			hosts = append(hosts, strings.ToLower(parsed.Host))
		}
	}
	return hosts
}

// HookConfig is a config as the hooks read it: its keyword lists as
// {count, longest}, the lists themselves returned by name for
// utils.keywords.
func (schema *ConfigSchema) HookConfig(config json.RawMessage) (json.RawMessage, map[string][]string) {
	names := schema.KeywordFields()
	if len(names) == 0 {
		return config, nil
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(config, &members) != nil {
		return config, nil
	}
	lists := map[string][]string{}
	for _, name := range names {
		var text string
		_ = json.Unmarshal(members[name], &text)
		keywords := Keywords(text)
		lists[name] = keywords
		longest := 0
		for _, keyword := range keywords {
			longest = max(longest, len(utf16.Encode([]rune(keyword))))
		}
		members[name], _ = json.Marshal(map[string]int{"count": len(keywords), "longest": longest})
	}
	ordered := []byte("{")
	count := 0
	for _, property := range schema.Properties {
		member, ok := members[property.Name]
		if !ok {
			continue
		}
		if count > 0 {
			ordered = append(ordered, ',')
		}
		count++
		key, _ := json.Marshal(property.Name)
		ordered = append(append(append(ordered, key...), ':'), member...)
	}
	return append(ordered, '}'), lists
}

// Keywords are the distinct non-empty trimmed lines of a keyword list.
func Keywords(text string) []string {
	var keywords []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		keyword := strings.TrimSpace(line)
		if keyword != "" && !seen[keyword] {
			seen[keyword] = true
			keywords = append(keywords, keyword)
		}
	}
	return keywords
}
