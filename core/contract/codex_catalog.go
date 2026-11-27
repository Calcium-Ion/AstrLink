package contract

import (
	"encoding/json"
	"slices"
	"strings"
)

// CodexUpstreamCatalogHeader asks the local model-discovery endpoint for
// upstream declarations only, so an explicit empty list is distinguishable
// from a synthesized entry. The transport strips this reserved header.
const CodexUpstreamCatalogHeader = "X-AstrLink-Upstream-Codex-Catalog"

type CodexEffort struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

// CodexReasoningEfforts are the values accepted by this client's config.
func CodexReasoningEfforts() []string {
	return []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra", "persistent"}
}

// CodexReasoning describes a model's chooser, independent of the user's
// model_reasoning_effort preference.
type CodexReasoning struct {
	Supported []CodexEffort `json:"supported_reasoning_levels"`
	Default   *string       `json:"default_reasoning_level"`
}

// ModelReasoning prefers a native capability declaration, including an
// explicitly empty list. Official metadata supplies levels only when absent.
func ModelReasoning(native json.RawMessage, fallback []string) CodexReasoning {
	var declared CodexReasoning
	_ = json.Unmarshal(native, &declared)
	result := CodexReasoning{Supported: []CodexEffort{}}
	levels := declared.Supported
	if levels == nil {
		for _, e := range fallback {
			levels = append(levels, CodexEffort{Effort: e, Description: e})
		}
	}
	known := CodexReasoningEfforts()
	seen := map[string]bool{}
	for _, level := range levels {
		if !slices.Contains(known, level.Effort) || seen[level.Effort] {
			continue
		}
		seen[level.Effort] = true
		if level.Description == "" {
			level.Description = level.Effort
		}
		result.Supported = append(result.Supported, level)
	}
	if declared.Default != nil && seen[*declared.Default] {
		result.Default = declared.Default
	}
	return result
}

// CodexCatalogEntry builds the ModelInfo used by both the gateway and the
// desktop startup catalog. Official metadata supplements ID-only providers.
func CodexCatalogEntry(id string, native json.RawMessage, reasoning *CodexReasoning, contextWindow uint64) (json.RawMessage, error) {
	// Required ModelInfo fields for Codex 0.158.0. Unknown providers advertise
	// text and ordinary shell tools only, with no guessed context limit,
	// reasoning levels, image support, custom tools, or parallel tool calls.
	model := map[string]any{
		"slug":                         id,
		"display_name":                 id,
		"description":                  "",
		"visibility":                   "list",
		"supported_in_api":             true,
		"priority":                     0,
		"supported_reasoning_levels":   []CodexEffort{},
		"default_reasoning_level":      nil,
		"shell_type":                   "shell_command",
		"base_instructions":            "You are a coding assistant.",
		"supports_reasoning_summaries": false,
		"support_verbosity":            false,
		"default_verbosity":            nil,
		"apply_patch_tool_type":        nil,
		"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
		"supports_parallel_tool_calls": false,
		"input_modalities":             []string{"text"},
		"experimental_supported_tools": []any{},
	}
	if id == "codex-auto-review" {
		// Subscription discovery adds this internal model for routing, not for
		// the user's picker. An explicit upstream visibility still wins below.
		model["visibility"] = "hide"
	}
	if contextWindow > 0 {
		model["context_window"] = contextWindow
	}
	if len(native) != 0 {
		var upstream map[string]json.RawMessage
		if err := json.Unmarshal(native, &upstream); err != nil {
			return nil, err
		}
		for key, value := range upstream {
			model[key] = value
		}
		var slug string
		_ = json.Unmarshal(upstream["slug"], &slug)
		if strings.TrimSpace(slug) != id {
			// Redirect sources inherit their target's capabilities, while their
			// public name must remain selectable independently of the target.
			model["display_name"] = id
		}
	}
	if reasoning != nil {
		model["supported_reasoning_levels"] = reasoning.Supported
		model["default_reasoning_level"] = reasoning.Default
		// In a local startup catalog each entry was selected for this API
		// provider. The upstream picker may otherwise hide it for API-key users.
		model["visibility"] = "list"
		model["supported_in_api"] = true
	}
	model["slug"] = id
	return json.Marshal(model)
}
