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
)

// Types of a setting a plugin declares.
const (
	SettingServer = "server"
	SettingText   = "text"
	SettingNumber = "number"
	SettingSwitch = "switch"
	SettingChoice = "choice"
)

// The host's own settings, which a plugin may place in its form: how long a
// request waits for it, whether its text skips the privacy check, the
// addresses it may reach beside its servers, the clients it is off for,
// whether a new session takes the value its workspace had, and whether the
// host swaps a session's resident block for newer data. A form that does
// not place them shows them on its own.
const (
	HostSettingWait      = "$wait"
	HostSettingTrusted   = "$trusted"
	HostSettingOrigins   = "$origins"
	HostSettingClients   = "$clients"
	HostSettingWorkspace = "$workspace"
	HostSettingRefresh   = "$refresh"
	HostSettingLookup    = "$lookup"
)

// Limits of the settings and credits a manifest declares.
const (
	maxSettings        = 64
	maxSettingOptions  = 32
	maxSettingText     = 1000
	maxSettingAddress  = 2048
	maxCredits         = 8
	maxCreditURLLength = 512
)

// Setting is a field of the plugin's settings form. Key is where its value
// sits in the configuration, a dot stepping into an object, or one of the
// host settings, which take only Group and Advanced. The fields of one Row
// sit on one line under the row's name.
type Setting struct {
	Key         string          `json:"key"`
	Label       string          `json:"label,omitempty"`
	Type        string          `json:"type,omitempty"`
	Group       string          `json:"group,omitempty"`
	Row         string          `json:"row,omitempty"`
	Help        string          `json:"help,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Advanced    bool            `json:"advanced,omitempty"`
	Min         *float64        `json:"min,omitempty"`
	Max         *float64        `json:"max,omitempty"`
	Step        *float64        `json:"step,omitempty"`
	Unit        string          `json:"unit,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
	Options     []SettingOption `json:"options,omitempty"`
	// Credential names the credential slot the host sends to a server.
	Credential string `json:"credential,omitempty"`
}

// SettingOption is one answer of a choice.
type SettingOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Credit names an open-source project the plugin builds on or talks to,
// for the plugin page to credit.
type Credit struct {
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
	// License is an SPDX identifier such as Apache-2.0.
	License string `json:"license,omitempty"`
	// URL is the project's https address.
	URL string `json:"url"`
}

var (
	settingKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}(\.[A-Za-z0-9_-]{1,64}){0,7}$`)
	spdxPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$`)
)

// Host reports whether the setting is one of the host's own.
func (setting Setting) Host() bool { return strings.HasPrefix(setting.Key, "$") }

// path is where the setting's value sits in the configuration.
func (setting Setting) path() []string { return strings.Split(setting.Key, ".") }

// validateSettings checks the settings of a manifest whose other fields
// passed.
func validateSettings(meta Meta) error {
	if len(meta.Settings) > maxSettings {
		return fmt.Errorf("meta.settings has more than %d entries", maxSettings)
	}
	bound := map[string]bool{}
	for index, setting := range meta.Settings {
		where := fmt.Sprintf("meta.settings[%d]", index)
		if err := validateSetting(meta, setting); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		for _, other := range meta.Settings[:index] {
			if other.Key == setting.Key {
				return fmt.Errorf("%s: %q is declared twice", where, setting.Key)
			}
			if !setting.Host() && !other.Host() && (strings.HasPrefix(setting.Key, other.Key+".") || strings.HasPrefix(other.Key, setting.Key+".")) {
				return fmt.Errorf("%s: %q lies inside %q", where, setting.Key, other.Key)
			}
		}
		if setting.Credential != "" {
			if bound[setting.Credential] {
				return fmt.Errorf("%s: the credential %q goes to two servers", where, setting.Credential)
			}
			bound[setting.Credential] = true
		}
		if setting.Row == "" {
			continue
		}
		// A row's fields follow one another in one place of the form.
		if index > 0 && meta.Settings[index-1].Row == setting.Row {
			previous := meta.Settings[index-1]
			if previous.Group != setting.Group || previous.Advanced != setting.Advanced {
				return fmt.Errorf("%s: the fields of the row %q are not in one group", where, setting.Row)
			}
		} else if slices.ContainsFunc(meta.Settings[:index], func(other Setting) bool { return other.Row == setting.Row }) {
			return fmt.Errorf("%s: the fields of the row %q are not together", where, setting.Row)
		}
	}
	return nil
}

func validateSetting(meta Meta, setting Setting) error {
	if setting.Group != "" && !displayText(setting.Group, 16, "") {
		return errors.New("group must be at most 16 characters")
	}
	if setting.Host() {
		switch setting.Key {
		case HostSettingWait, HostSettingTrusted, HostSettingClients:
		case HostSettingOrigins:
			if !meta.NeedsNetwork() {
				return errors.New("$origins needs the network capability")
			}
		case HostSettingWorkspace:
			if len(meta.Metadata) == 0 {
				return errors.New("$workspace needs an attribute in meta.metadata")
			}
		case HostSettingRefresh:
			if !slices.Contains(meta.Capabilities, CapabilityInject) {
				return errors.New("$refresh needs the context.inject capability")
			}
		case HostSettingLookup:
			if len(meta.Tools) == 0 {
				return errors.New("$lookup needs a query in meta.tools")
			}
		default:
			return fmt.Errorf("%q is not a host setting; the host has $wait, $trusted, $origins, $clients, $workspace, $refresh and $lookup", setting.Key)
		}
		if setting.Label != "" || setting.Type != "" || setting.Row != "" || setting.Help != "" || len(setting.Default) > 0 ||
			setting.Required || setting.Min != nil || setting.Max != nil || setting.Step != nil || setting.Unit != "" ||
			setting.Placeholder != "" || len(setting.Options) > 0 || setting.Credential != "" {
			return fmt.Errorf("%s takes only group and advanced", setting.Key)
		}
		return nil
	}
	if !settingKeyPattern.MatchString(setting.Key) {
		return fmt.Errorf("key %q must be words of letters, digits, _ and - joined by dots, starting with a letter", setting.Key)
	}
	switch {
	case !displayText(setting.Label, 32, ""):
		return errors.New("label must be 1 to 32 characters")
	case setting.Row != "" && !displayText(setting.Row, 16, ""):
		return errors.New("row must be at most 16 characters")
	case setting.Help != "" && !displayText(setting.Help, 500, "\n"):
		return errors.New("help must be at most 500 characters")
	case setting.Placeholder != "" && !displayText(setting.Placeholder, 32, ""):
		return errors.New("placeholder must be at most 32 characters")
	case setting.Unit != "" && !displayText(setting.Unit, 8, ""):
		return errors.New("unit must be at most 8 characters")
	}
	switch setting.Type {
	case SettingServer:
		switch {
		case !meta.NeedsNetwork():
			return errors.New("a server needs the network capability")
		case len(setting.Default) > 0:
			return errors.New("a server has no default: only the user names the address")
		case setting.Row != "":
			return errors.New("a server takes a line of its own")
		}
		if setting.Credential != "" {
			if _, declared := meta.Credential(setting.Credential); !declared {
				return fmt.Errorf("credential %q is not declared", setting.Credential)
			}
		}
	case SettingText, SettingNumber, SettingSwitch, SettingChoice:
		if setting.Credential != "" {
			return errors.New("only a server sends a credential")
		}
	default:
		return fmt.Errorf("type %q must be server, text, number, switch or choice", setting.Type)
	}
	if setting.Type != SettingNumber && (setting.Min != nil || setting.Max != nil || setting.Step != nil || setting.Unit != "") {
		return errors.New("min, max, step and unit are for numbers")
	}
	if (len(setting.Options) > 0) != (setting.Type == SettingChoice) {
		return errors.New("a choice takes options, and only a choice")
	}
	if setting.Type == SettingSwitch && (setting.Required || setting.Placeholder != "") {
		return errors.New("a switch is never empty, so it takes neither required nor placeholder")
	}
	if setting.Type == SettingNumber {
		for _, bound := range []*float64{setting.Min, setting.Max, setting.Step} {
			if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
				return errors.New("min, max and step must be finite")
			}
		}
		if setting.Min != nil && setting.Max != nil && *setting.Min > *setting.Max {
			return errors.New("min is above max")
		}
		if setting.Step != nil && *setting.Step <= 0 {
			return errors.New("step must be above 0")
		}
	}
	if setting.Type == SettingChoice {
		if len(setting.Options) > maxSettingOptions {
			return fmt.Errorf("a choice has at most %d options", maxSettingOptions)
		}
		for index, option := range setting.Options {
			if !displayText(option.Value, 64, "") || !displayText(option.Label, 32, "") {
				return fmt.Errorf("options[%d] needs a value of 1 to 64 and a label of 1 to 32 characters", index)
			}
			if slices.ContainsFunc(setting.Options[:index], func(other SettingOption) bool { return other.Value == option.Value }) {
				return fmt.Errorf("options lists %q twice", option.Value)
			}
		}
	}
	if len(setting.Default) > 0 {
		if err := setting.Check(setting.Default); err != nil {
			return fmt.Errorf("default %w", err)
		}
	}
	return nil
}

// Check reports whether value suits the setting.
func (setting Setting) Check(value json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return errors.New("is not JSON")
	}
	switch setting.Type {
	case SettingServer:
		text, ok := decoded.(string)
		if !ok || len(text) > maxSettingAddress {
			return errors.New("must be an address")
		}
		if _, err := NormalizeOrigin(text); err != nil {
			return errors.New("must be an http or https address with a host")
		}
	case SettingText:
		text, ok := decoded.(string)
		if !ok || len([]rune(text)) > maxSettingText {
			return fmt.Errorf("must be text of at most %d characters", maxSettingText)
		}
	case SettingNumber:
		number, ok := decoded.(json.Number)
		if !ok {
			return errors.New("must be a number")
		}
		parsed, err := number.Float64()
		if err != nil || math.IsInf(parsed, 0) {
			return errors.New("must be a number")
		}
		if (setting.Min != nil && parsed < *setting.Min) || (setting.Max != nil && parsed > *setting.Max) {
			return fmt.Errorf("must be %s", setting.bounds())
		}
	case SettingSwitch:
		if _, ok := decoded.(bool); !ok {
			return errors.New("must be true or false")
		}
	case SettingChoice:
		text, ok := decoded.(string)
		if !ok || !slices.ContainsFunc(setting.Options, func(option SettingOption) bool { return option.Value == text }) {
			return errors.New("must be one of the options")
		}
	}
	return nil
}

func (setting Setting) bounds() string {
	switch {
	case setting.Min != nil && setting.Max != nil:
		return fmt.Sprintf("from %g to %g", *setting.Min, *setting.Max)
	case setting.Min != nil:
		return fmt.Sprintf("at least %g", *setting.Min)
	default:
		return fmt.Sprintf("at most %g", *setting.Max)
	}
}

// validateCredits checks the projects a manifest credits.
func validateCredits(credits []Credit) error {
	if len(credits) > maxCredits {
		return fmt.Errorf("meta.credits has more than %d entries", maxCredits)
	}
	for index, credit := range credits {
		where := fmt.Sprintf("meta.credits[%d]", index)
		switch {
		case !displayText(credit.Name, 64, ""):
			return fmt.Errorf("%s.name must be 1 to 64 characters", where)
		case credit.Owner != "" && !displayText(credit.Owner, 64, ""):
			return fmt.Errorf("%s.owner must be at most 64 characters", where)
		case credit.License != "" && !spdxPattern.MatchString(credit.License):
			return fmt.Errorf("%s.license must be an SPDX identifier such as Apache-2.0", where)
		}
		parsed, err := url.Parse(credit.URL)
		if err != nil || len(credit.URL) > maxCreditURLLength || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("%s.url must be an https address of at most %d characters", where, maxCreditURLLength)
		}
	}
	return nil
}

// SettingError says which setting a configuration gets wrong.
type SettingError struct {
	Setting Setting
	Reason  string
}

func (failure *SettingError) Error() string {
	return fmt.Sprintf("the setting %s (%s) %s", failure.Setting.Key, failure.Setting.Label, failure.Reason)
}

// CheckConfig reports the first setting whose value in config does not suit
// it, or that is required and missing. Keys no setting declares are the
// plugin's to read, as they are.
func (meta Meta) CheckConfig(config json.RawMessage) error {
	object, ok := decodeObject(config)
	if !ok {
		return errors.New("the configuration is not a JSON object")
	}
	for _, setting := range meta.Settings {
		if setting.Host() {
			continue
		}
		value, found := lookup(object, setting.path())
		if !found || value == nil {
			if setting.Required {
				return &SettingError{Setting: setting, Reason: "is required"}
			}
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return &SettingError{Setting: setting, Reason: "is not JSON"}
		}
		if err := setting.Check(raw); err != nil {
			return &SettingError{Setting: setting, Reason: err.Error()}
		}
	}
	return nil
}

// Servers returns the server settings and the addresses config gives them;
// a server without one is left out.
func (meta Meta) Servers(config json.RawMessage) map[string]string {
	object, ok := decodeObject(config)
	servers := map[string]string{}
	if !ok {
		return servers
	}
	for _, setting := range meta.Settings {
		if setting.Type != SettingServer {
			continue
		}
		if value, found := lookup(object, setting.path()); found {
			if address, ok := value.(string); ok && address != "" {
				servers[setting.Key] = address
			}
		}
	}
	return servers
}

// Setting returns the setting of key.
func (meta Meta) Setting(key string) (Setting, bool) {
	for _, setting := range meta.Settings {
		if setting.Key == key {
			return setting, true
		}
	}
	return Setting{}, false
}

// SwitchOn reports whether the switch of key is on in config: its value
// there, else its default, else off.
func (meta Meta) SwitchOn(config json.RawMessage, key string) bool {
	setting, declared := meta.Setting(key)
	if !declared || setting.Host() || setting.Type != SettingSwitch {
		return false
	}
	if object, ok := decodeObject(config); ok {
		if value, found := lookup(object, setting.path()); found && value != nil {
			on, isSwitch := value.(bool)
			return isSwitch && on
		}
	}
	var on bool
	return json.Unmarshal(setting.Default, &on) == nil && on
}

// WithDefaults returns config with the defaults of the settings it has no
// value for. A value in the way of a default's place keeps it out.
func (meta Meta) WithDefaults(config json.RawMessage) json.RawMessage {
	if !slices.ContainsFunc(meta.Settings, func(setting Setting) bool { return len(setting.Default) > 0 }) {
		return config
	}
	object, ok := decodeObject(config)
	if !ok {
		return config
	}
	for _, setting := range meta.Settings {
		if len(setting.Default) == 0 || setting.Host() {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(setting.Default))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			continue
		}
		place(object, setting.path(), value)
	}
	filled, err := json.Marshal(object)
	if err != nil {
		return config
	}
	return filled
}

func decodeObject(config json.RawMessage) (map[string]any, bool) {
	if len(bytes.TrimSpace(config)) == 0 {
		return map[string]any{}, true
	}
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, false
	}
	return object, true
}

func lookup(object map[string]any, path []string) (any, bool) {
	for index, part := range path {
		value, found := object[part]
		if !found {
			return nil, false
		}
		if index == len(path)-1 {
			return value, true
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		object = next
	}
	return nil, false
}

// place puts value at path unless something is there already.
func place(object map[string]any, path []string, value any) {
	for _, part := range path[:len(path)-1] {
		next, found := object[part]
		if !found {
			child := map[string]any{}
			object[part] = child
			object = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return
		}
		object = child
	}
	last := path[len(path)-1]
	if _, found := object[last]; !found {
		object[last] = value
	}
}
