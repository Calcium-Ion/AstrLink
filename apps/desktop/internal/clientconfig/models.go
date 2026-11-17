package clientconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The models a client is written with, as the dialog sends them and as a
// config holds them, and what the official catalog says about each.

const (
	// MaxListedModels is the most models one switchable list takes.
	MaxListedModels = 500
	// MaxPiLimit is the largest limit a model entry takes, in tokens.
	MaxPiLimit = 100_000_000
)

// Models is what the dialog writes a client with.
type Models struct {
	Model       *string `json:"model,omitempty"`
	HaikuModel  *string `json:"haikuModel,omitempty"`
	SonnetModel *string `json:"sonnetModel,omitempty"`
	OpusModel   *string `json:"opusModel,omitempty"`
	FableModel  *string `json:"fableModel,omitempty"`
	// SubagentModel is Claude Code's only: the model its subagents use.
	SubagentModel *string `json:"subagentModel,omitempty"`
	// ReasoningEffort is Codex's only: one of its reasoning efforts.
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
	// Models are, for clients that switch models, the models the client
	// can switch among.
	Models []string `json:"models,omitempty"`
	// ContextWindow and MaxTokens are Pi's only: the context window and
	// output limit its entry for the default model names.
	ContextWindow *uint64 `json:"contextWindow,omitempty"`
	MaxTokens     *uint64 `json:"maxTokens,omitempty"`
	// ModelProtocols is the gateway protocol each model is written with,
	// one of the client's; a model left out takes the client's first.
	ModelProtocols map[string]string `json:"modelProtocols,omitempty"`
}

// UnmarshalJSON takes the fields Models has and nothing else.
func (m *Models) UnmarshalJSON(data []byte) error {
	type plain Models
	var out plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*m = Models(out)
	return nil
}

// field is a model field by its camelCase name.
func (m *Models) field(name string) *string {
	switch name {
	case "model":
		return m.Model
	case "haikuModel":
		return m.HaikuModel
	case "sonnetModel":
		return m.SonnetModel
	case "opusModel":
		return m.OpusModel
	case "fableModel":
		return m.FableModel
	case "subagentModel":
		return m.SubagentModel
	}
	return nil
}

// Limits are Pi's context window and output limit; nil for none.
type Limits struct {
	ContextWindow, MaxTokens *uint64
}

func (l Limits) set() bool { return l.ContextWindow != nil || l.MaxTokens != nil }

// piLimits are the context window and output limit for Pi's entry.
func (m *Models) piLimits(client Client) (Limits, error) {
	limits := Limits{m.ContextWindow, m.MaxTokens}
	if !client.TakesLimits() {
		if limits.set() {
			return Limits{}, errors.New("context and output limits apply to Pi only")
		}
		return limits, nil
	}
	for _, limit := range []struct {
		name  string
		value *uint64
	}{{"contextWindow", limits.ContextWindow}, {"maxTokens", limits.MaxTokens}} {
		if limit.value != nil && (*limit.value == 0 || *limit.value > MaxPiLimit) {
			return Limits{}, fmt.Errorf("invalid %s", limit.name)
		}
	}
	return limits, nil
}

// Field is a model field, named by its camelCase name, and its value.
type Field struct {
	Name, Value string
}

// Fields are the filled-in models, trimmed and named by their camelCase
// field. Each client takes its own fields and ignores the rest.
func (m *Models) Fields(client Client) ([]Field, error) {
	var values []Field
	for _, key := range client.ModelFields() {
		value := m.field(key)
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		trimmed := strings.TrimSpace(*value)
		if !ValidModel(trimmed) {
			return nil, fmt.Errorf("invalid %s", key)
		}
		values = append(values, Field{key, trimmed})
	}
	if client.RequiresModel() && len(values) == 0 {
		return nil, errors.New("model is required for this client")
	}
	if efforts := client.ReasoningEfforts(); len(efforts) > 0 && m.ReasoningEffort != nil {
		if !slices.Contains(efforts, *m.ReasoningEffort) {
			return nil, errors.New("invalid reasoningEffort")
		}
		values = append(values, Field{"reasoningEffort", *m.ReasoningEffort})
	}
	return values, nil
}

// list is the models a client that switches models lists, each once, with
// the default model added when the list lacks it.
func (m *Models) list(client Client, model string) ([]string, error) {
	if m.Models == nil {
		if model != "" && client.ListsModels() {
			return []string{model}, nil
		}
		return nil, nil
	}
	if !client.ListsModels() {
		return nil, errors.New("a model list applies to OpenCode, OpenClaw, and Pi only")
	}
	if len(m.Models) > MaxListedModels {
		return nil, errors.New("too many models")
	}
	var list []string
	for _, value := range m.Models {
		value = strings.TrimSpace(value)
		if value == "" || !ValidModel(value) {
			return nil, errors.New("invalid models")
		}
		if !slices.Contains(list, value) {
			list = append(list, value)
		}
	}
	if model != "" && !slices.Contains(list, model) {
		list = append([]string{model}, list...)
	}
	return list, nil
}

// protocols is the protocol chosen for each model, each one of the
// client's.
func (m *Models) protocols(client Client) (map[string]string, error) {
	chosen := map[string]string{}
	if m.ModelProtocols == nil {
		return chosen, nil
	}
	// A full list and the default model it may lack.
	if len(m.ModelProtocols) > MaxListedModels+1 {
		return nil, errors.New("too many modelProtocols")
	}
	for model, protocol := range m.ModelProtocols {
		model = strings.TrimSpace(model)
		if !slices.Contains(client.Protocols(), protocol) || model == "" || !ValidModel(model) {
			return nil, errors.New("invalid modelProtocols")
		}
		chosen[model] = protocol
	}
	return chosen, nil
}

// Selection is what one client is written with, checked.
type Selection struct {
	Fields []Field
	List   []string
	// Limits are Pi's limits for the default model.
	Limits Limits
	// Protocols is the protocol chosen for each model; see
	// Connection.Protocol.
	Protocols map[string]string
}

// Select checks models for client.
func Select(models *Models, client Client) (*Selection, error) {
	limits, err := models.piLimits(client)
	if err != nil {
		return nil, err
	}
	fields, err := models.Fields(client)
	if err != nil {
		return nil, err
	}
	selection := &Selection{Fields: fields, Limits: limits}
	model, _ := selection.Model()
	if model != "" {
		if err := client.CheckModel(model); err != nil {
			return nil, err
		}
	}
	if selection.List, err = models.list(client, model); err != nil {
		return nil, err
	}
	if selection.Protocols, err = models.protocols(client); err != nil {
		return nil, err
	}
	return selection, nil
}

// Model is the default model, if there is one.
func (s *Selection) Model() (string, bool) {
	for _, field := range s.Fields {
		if field.Name == "model" {
			return field.Value, true
		}
	}
	return "", false
}

// Connection is the connection that writes this selection with token.
func (s *Selection) Connection(target, tokenID, token, origin string) *Connection {
	return &Connection{
		Target:    target,
		TokenID:   tokenID,
		Token:     token,
		Origin:    origin,
		Models:    s.Fields,
		List:      s.List,
		Protocols: s.Protocols,
	}
}

// ValidModel reports whether a trimmed, non-empty model name fits in a
// client config.
func ValidModel(value string) bool {
	return utf8.RuneCountInString(value) <= 256 && strings.IndexFunc(value, unicode.IsControl) < 0
}

// CleanModel is a model a config holds, trimmed, when AstrLink could write
// it back.
func CleanModel(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && ValidModel(value)
}

// CurrentModels are the models a client's config holds now.
type CurrentModels struct {
	Model           *string  `json:"model,omitempty"`
	HaikuModel      *string  `json:"haikuModel,omitempty"`
	SonnetModel     *string  `json:"sonnetModel,omitempty"`
	OpusModel       *string  `json:"opusModel,omitempty"`
	FableModel      *string  `json:"fableModel,omitempty"`
	SubagentModel   *string  `json:"subagentModel,omitempty"`
	ReasoningEffort *string  `json:"reasoningEffort,omitempty"`
	Models          []string `json:"models,omitempty"`
	ContextWindow   *uint64  `json:"contextWindow,omitempty"`
	MaxTokens       *uint64  `json:"maxTokens,omitempty"`
}

// Set sets a model field by its camelCase name; another name sets the
// reasoning effort.
func (c *CurrentModels) Set(field, value string) {
	slot := &c.ReasoningEffort
	switch field {
	case "model":
		slot = &c.Model
	case "haikuModel":
		slot = &c.HaikuModel
	case "sonnetModel":
		slot = &c.SonnetModel
	case "opusModel":
		slot = &c.OpusModel
	case "fableModel":
		slot = &c.FableModel
	case "subagentModel":
		slot = &c.SubagentModel
	}
	*slot = &value
}

// Some is a pointer to value, for an optional field.
func Some[T any](value T) *T { return &value }

// ModelDetails is what AstrLink writes into a client's entry for a model
// besides its id.
type ModelDetails struct {
	// Reasoning holds the official reasoning levels; nil when the catalog
	// lists no reasoning for the model. A model with no levels to choose
	// keeps Pi's default range.
	Reasoning     []string
	HasReasoning  bool
	ContextWindow *uint64
	MaxTokens     *uint64
}

// Catalog is entry details, by model.
type Catalog map[string]ModelDetails

// officialDetails adds the official reasoning levels and limits of each
// model the catalog lacks; a model it cannot answer for gets none.
func officialDetails(ctx context.Context, gateway Gateway, models []string, known Catalog) {
	for _, model := range models {
		if _, ok := known[model]; ok {
			continue
		}
		var details ModelDetails
		metadata, err := gateway.ModelMetadata(ctx, model)
		if err != nil {
			fmt.Fprintf(os.Stderr, "official details for %s are unavailable: %v\n", model, err)
		} else {
			details = detailsOf(metadata)
		}
		known[model] = details
	}
}

// detailsOf reads the model metadata Core answers.
func detailsOf(metadata json.RawMessage) ModelDetails {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(metadata, &fields)
	tokens := func(key string) *uint64 {
		var n uint64
		if json.Unmarshal(fields[key], &n) != nil || n < 1 || n > MaxPiLimit {
			return nil
		}
		return &n
	}
	var details ModelDetails
	var reasoning bool
	if json.Unmarshal(fields["reasoning"], &reasoning) == nil && reasoning {
		details.HasReasoning = true
		details.Reasoning = []string{}
		var levels []json.RawMessage
		_ = json.Unmarshal(fields["reasoning_levels"], &levels)
		for _, raw := range levels {
			var level string
			if json.Unmarshal(raw, &level) == nil {
				details.Reasoning = append(details.Reasoning, level)
			}
		}
	}
	details.ContextWindow = tokens("context_tokens")
	details.MaxTokens = tokens("output_tokens")
	return details
}

// catalogFor is the catalog's details for each listed model, with the
// limits the user set for Pi's default one.
func catalogFor(selection *Selection, known Catalog) Catalog {
	catalog := Catalog{}
	for _, model := range selection.List {
		catalog[model] = known[model]
	}
	if model, ok := selection.Model(); ok && selection.Limits.set() {
		entry := catalog[model]
		entry.ContextWindow, entry.MaxTokens = selection.Limits.ContextWindow, selection.Limits.MaxTokens
		catalog[model] = entry
	}
	return catalog
}
