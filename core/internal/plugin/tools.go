package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Limits of the queries a manifest declares.
const (
	maxTools               = 8
	maxToolParams          = 8
	maxToolDescription     = 300
	maxToolExample         = 100
	defaultToolStringRunes = 500
	maxToolStringRunes     = 2000
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Localized is a text in each interface language, by its code: en, zh-CN or
// ja. A manifest may give one plain string for every language.
type Localized map[string]string

func (text *Localized) UnmarshalJSON(data []byte) error {
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		*text = Localized{"": plain}
		return nil
	}
	var each map[string]string
	if err := json.Unmarshal(data, &each); err != nil {
		return errors.New("a localized text is a string or an object of strings by language")
	}
	*text = each
	return nil
}

// In returns the text in language: the language's own, else the English,
// else the plain string, else the first by code.
func (text Localized) In(language string) string {
	for _, code := range []string{language, contract.PluginLanguageEnglish, ""} {
		if value, found := text[code]; found {
			return value
		}
	}
	codes := make([]string, 0, len(text))
	for code := range text {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		return ""
	}
	return text[codes[0]]
}

func (text Localized) valid(max int) bool {
	if len(text) == 0 {
		return false
	}
	for code, value := range text {
		if code != "" && !contract.ValidPluginLanguage(code) {
			return false
		}
		if !displayText(value, max, "") {
			return false
		}
	}
	return true
}

// Tool is a query the plugin offers the model, or a report it takes from
// it. The model runs it through the gateway's lookup outlet with the
// session's own credential; the gateway writes how into the request, in the
// session's language, from what the tool declares here.
type Tool struct {
	Name string `json:"name"`
	// Description says what the query finds and when to run it.
	Description Localized   `json:"description"`
	Params      []ToolParam `json:"params,omitempty"`
	// Unbound marks a query a session may run before it has a value of
	// each of the plugin's attributes, such as listing the projects. The
	// others need the values.
	Unbound bool `json:"unbound,omitempty"`
	// Writes marks a report: the model hands the plugin something to keep
	// instead of reading. A report needs the session's values and has
	// limits of its own.
	Writes bool `json:"writes,omitempty"`
	// Setting names a switch of the plugin's settings that turns the tool
	// on and off; the gateway offers the tool only while it is on.
	Setting string `json:"setting,omitempty"`
}

// ToolParam is a parameter of a query: a string or a number.
type ToolParam struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	MaxRunes int      `json:"maxLength,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	// Example is what the usage shows the model to put there.
	Example Localized `json:"example,omitempty"`
}

// Types of a tool parameter.
const (
	ToolParamString = "string"
	ToolParamNumber = "number"
)

// validateTools checks the queries a manifest declares.
func validateTools(meta Meta) error {
	if len(meta.Tools) == 0 {
		return nil
	}
	if !slices.Contains(meta.Capabilities, CapabilityTools) {
		return errors.New("meta.tools needs the model.tools capability")
	}
	if len(meta.Tools) > maxTools {
		return fmt.Errorf("meta.tools has more than %d entries", maxTools)
	}
	for index, tool := range meta.Tools {
		where := fmt.Sprintf("meta.tools[%d]", index)
		if !toolNamePattern.MatchString(tool.Name) {
			return fmt.Errorf("%s.name %q must be a lowercase letter followed by up to 31 lowercase letters, digits or underscores", where, tool.Name)
		}
		if slices.ContainsFunc(meta.Tools[:index], func(other Tool) bool { return other.Name == tool.Name }) {
			return fmt.Errorf("%s: %q is declared twice", where, tool.Name)
		}
		if !tool.Description.valid(maxToolDescription) {
			return fmt.Errorf("%s.description must be 1 to %d characters in each language it gives", where, maxToolDescription)
		}
		if tool.Writes && tool.Unbound {
			return fmt.Errorf("%s: a report needs the session's values, so it cannot be unbound", where)
		}
		if tool.Setting != "" {
			setting, declared := meta.Setting(tool.Setting)
			if !declared || setting.Host() || setting.Type != SettingSwitch {
				return fmt.Errorf("%s.setting %q must name a switch in meta.settings", where, tool.Setting)
			}
		}
		if len(tool.Params) > maxToolParams {
			return fmt.Errorf("%s has more than %d params", where, maxToolParams)
		}
		for at, param := range tool.Params {
			if err := validateToolParam(param); err != nil {
				return fmt.Errorf("%s.params[%d]: %w", where, at, err)
			}
			if slices.ContainsFunc(tool.Params[:at], func(other ToolParam) bool { return other.Name == param.Name }) {
				return fmt.Errorf("%s.params[%d]: %q is declared twice", where, at, param.Name)
			}
		}
	}
	return nil
}

func validateToolParam(param ToolParam) error {
	if !toolNamePattern.MatchString(param.Name) {
		return fmt.Errorf("name %q must be a lowercase letter followed by up to 31 lowercase letters, digits or underscores", param.Name)
	}
	switch param.Type {
	case ToolParamString:
		if param.Min != nil || param.Max != nil {
			return errors.New("min and max are for numbers")
		}
		if param.MaxRunes < 0 || param.MaxRunes > maxToolStringRunes {
			return fmt.Errorf("maxLength must be 1 to %d", maxToolStringRunes)
		}
	case ToolParamNumber:
		if param.MaxRunes != 0 {
			return errors.New("maxLength is for strings")
		}
		for _, bound := range []*float64{param.Min, param.Max} {
			if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
				return errors.New("min and max must be finite")
			}
		}
		if param.Min != nil && param.Max != nil && *param.Min > *param.Max {
			return errors.New("min is above max")
		}
	default:
		return fmt.Errorf("type %q must be string or number", param.Type)
	}
	if param.Required && !param.Example.valid(maxToolExample) {
		return fmt.Errorf("a required parameter needs an example of 1 to %d characters", maxToolExample)
	}
	if !param.Required && len(param.Example) > 0 && !param.Example.valid(maxToolExample) {
		return fmt.Errorf("example must be 1 to %d characters", maxToolExample)
	}
	return nil
}

// Offers reports whether the plugin offers the model tool: always, or while
// the switch the tool names is on.
func (plugin *Plugin) Offers(tool Tool) bool {
	return tool.Setting == "" || plugin.Meta.SwitchOn(plugin.Settings.Config, tool.Setting)
}

// Tool returns the query of name.
func (meta Meta) Tool(name string) (Tool, bool) {
	for _, tool := range meta.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// ToolInputError says what a query's input gets wrong.
type ToolInputError struct{ Reason string }

func (failure *ToolInputError) Error() string { return failure.Reason }

// CheckInput reads a query's input, a JSON object, by the tool's
// parameters: unknown members, a wrong type, a string past its length or a
// number out of its range, and a required parameter missing are refused.
// It returns the input as the plugin reads it.
func (tool Tool) CheckInput(raw json.RawMessage) (map[string]any, error) {
	input := map[string]any{}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		if decoder.Decode(&input) != nil || input == nil {
			return nil, &ToolInputError{Reason: "the input is not a JSON object"}
		}
	}
	checked := map[string]any{}
	for key, value := range input {
		index := slices.IndexFunc(tool.Params, func(param ToolParam) bool { return param.Name == key })
		if index < 0 {
			return nil, &ToolInputError{Reason: fmt.Sprintf("%q is not a parameter of %s", key, tool.Name)}
		}
		param := tool.Params[index]
		switch param.Type {
		case ToolParamString:
			text, ok := value.(string)
			if !ok {
				// A number given for a string, as a form posts it, reads as
				// its text.
				number, isNumber := value.(json.Number)
				if !isNumber {
					return nil, &ToolInputError{Reason: fmt.Sprintf("%s must be text", key)}
				}
				text = number.String()
			}
			text = strings.TrimSpace(text)
			limit := param.MaxRunes
			if limit == 0 {
				limit = defaultToolStringRunes
			}
			if !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit {
				return nil, &ToolInputError{Reason: fmt.Sprintf("%s must be text of at most %d characters", key, limit)}
			}
			if text != "" {
				checked[key] = text
			}
		case ToolParamNumber:
			var number float64
			switch value := value.(type) {
			case json.Number:
				parsed, err := value.Float64()
				if err != nil {
					return nil, &ToolInputError{Reason: fmt.Sprintf("%s must be a number", key)}
				}
				number = parsed
			case string:
				var parsed json.Number = json.Number(strings.TrimSpace(value))
				float, err := parsed.Float64()
				if err != nil {
					return nil, &ToolInputError{Reason: fmt.Sprintf("%s must be a number", key)}
				}
				number = float
			default:
				return nil, &ToolInputError{Reason: fmt.Sprintf("%s must be a number", key)}
			}
			if math.IsNaN(number) || math.IsInf(number, 0) || (param.Min != nil && number < *param.Min) || (param.Max != nil && number > *param.Max) {
				return nil, &ToolInputError{Reason: fmt.Sprintf("%s is out of range", key)}
			}
			checked[key] = number
		}
	}
	for _, param := range tool.Params {
		if _, found := checked[param.Name]; param.Required && !found {
			return nil, &ToolInputError{Reason: fmt.Sprintf("%s is required", param.Name)}
		}
	}
	return checked, nil
}
