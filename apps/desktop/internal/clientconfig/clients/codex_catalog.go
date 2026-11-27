package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// Codex's desktop model picker is driven by the model catalog, not by the
// provider's /models endpoint alone. A custom provider therefore needs both a
// model_catalog_json pointer and a catalog file with one entry per model.

type codexCatalogFile struct{}

type codexCatalog struct {
	Models []codexCatalogModel `json:"models"`
}

type codexCatalogModel struct {
	Slug                          string                `json:"slug"`
	DisplayName                   string                `json:"display_name"`
	Description                   string                `json:"description"`
	BaseInstructions              string                `json:"base_instructions"`
	DefaultReasoningLevel         string                `json:"default_reasoning_level"`
	SupportedReasoningLevels      []codexReasoningLevel `json:"supported_reasoning_levels"`
	ShellType                     string                `json:"shell_type"`
	Visibility                    string                `json:"visibility"`
	SupportedInAPI                bool                  `json:"supported_in_api"`
	Priority                      int                   `json:"priority"`
	AdditionalSpeedTiers          []string              `json:"additional_speed_tiers"`
	ServiceTiers                  []any                 `json:"service_tiers"`
	AvailabilityNux               any                   `json:"availability_nux"`
	Upgrade                       any                   `json:"upgrade"`
	SupportsReasoningSummaries    bool                  `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary       string                `json:"default_reasoning_summary"`
	SupportVerbosity              bool                  `json:"support_verbosity"`
	TruncationPolicy              codexTruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls     bool                  `json:"supports_parallel_tool_calls"`
	SupportsImageDetailOriginal   bool                  `json:"supports_image_detail_original"`
	ContextWindow                 uint64                `json:"context_window"`
	MaxContextWindow              uint64                `json:"max_context_window"`
	AutoCompactTokenLimit         uint64                `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent uint64                `json:"effective_context_window_percent"`
	ExperimentalSupportedTools    []string              `json:"experimental_supported_tools"`
	InputModalities               []string              `json:"input_modalities"`
	SupportsSearchTool            bool                  `json:"supports_search_tool"`
}

type codexReasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

type codexTruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit uint64 `json:"limit"`
}

func (codexCatalogFile) Format() format.Format { return format.JSONFormat{} }

func (codexCatalogFile) Generated(connection *cc.Connection, details cc.Catalog) (string, error) {
	models := connection.ModelList()
	if len(models) == 0 {
		return "", errors.New("model is required")
	}
	defaultModel, _ := connection.Model("model")
	entries := make([]codexCatalogModel, 0, len(models))
	for index, model := range models {
		modelDetails := details[model]
		contextWindow := uint64(128_000)
		if modelDetails.ContextWindow != nil && *modelDetails.ContextWindow > 0 {
			contextWindow = *modelDetails.ContextWindow
		}
		levels := codexCatalogReasoningLevels(connection, modelDetails)
		entries = append(entries, codexCatalogModel{
			Slug:                          model,
			DisplayName:                   model,
			Description:                   "Model served by AstrLink",
			BaseInstructions:              "You are Codex, a coding agent. You and the user share the same workspace and collaborate to achieve the user's goals.",
			DefaultReasoningLevel:         codexCatalogDefaultReasoning(connection, model, defaultModel, levels),
			SupportedReasoningLevels:      levels,
			ShellType:                     "shell_command",
			Visibility:                    "list",
			SupportedInAPI:                true,
			Priority:                      index,
			AdditionalSpeedTiers:          []string{},
			ServiceTiers:                  []any{},
			SupportsReasoningSummaries:    len(levels) > 0,
			DefaultReasoningSummary:       "none",
			SupportVerbosity:              false,
			TruncationPolicy:              codexTruncationPolicy{Mode: "bytes", Limit: 10_000},
			SupportsParallelToolCalls:     false,
			SupportsImageDetailOriginal:   false,
			ContextWindow:                 contextWindow,
			MaxContextWindow:              contextWindow,
			AutoCompactTokenLimit:         contextWindow * 9 / 10,
			EffectiveContextWindowPercent: 95,
			ExperimentalSupportedTools:    []string{},
			InputModalities:               []string{"text", "image"},
			SupportsSearchTool:            true,
		})
	}
	raw, err := json.MarshalIndent(codexCatalog{Models: entries}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode Codex model catalog: %w", err)
	}
	return string(raw) + "\n", nil
}

func codexCatalogReasoningLevels(connection *cc.Connection, details cc.ModelDetails) []codexReasoningLevel {
	var efforts []string
	if details.HasReasoning && len(details.Reasoning) > 0 {
		efforts = details.Reasoning
	} else {
		efforts = []string{"low", "medium", "high"}
		if effort, ok := connection.Model("reasoningEffort"); ok {
			efforts = append(efforts, effort)
		}
	}
	seen := map[string]bool{}
	levels := make([]codexReasoningLevel, 0, len(efforts))
	for _, effort := range efforts {
		if effort == "" || seen[effort] {
			continue
		}
		seen[effort] = true
		levels = append(levels, codexReasoningLevel{Effort: effort, Description: codexReasoningDescription(effort)})
	}
	return levels
}

func codexCatalogDefaultReasoning(connection *cc.Connection, model, defaultModel string, levels []codexReasoningLevel) string {
	if model == defaultModel {
		if effort, ok := connection.Model("reasoningEffort"); ok && slices.ContainsFunc(levels, func(level codexReasoningLevel) bool {
			return level.Effort == effort
		}) {
			return effort
		}
	}
	for _, preferred := range []string{"medium", "high", "low"} {
		if slices.ContainsFunc(levels, func(level codexReasoningLevel) bool { return level.Effort == preferred }) {
			return preferred
		}
	}
	if len(levels) > 0 {
		return levels[0].Effort
	}
	return "high"
}

func codexReasoningDescription(effort string) string {
	switch effort {
	case "none":
		return "Disable Thinking"
	case "minimal":
		return "Fast responses with minimal reasoning"
	case "low":
		return "Fast responses with lighter reasoning"
	case "medium":
		return "Balances speed and reasoning depth for everyday tasks"
	case "high":
		return "Greater reasoning depth for complex problems"
	case "xhigh":
		return "Extra high reasoning depth for complex problems"
	case "max":
		return "Maximum reasoning depth for the hardest problems"
	case "ultra":
		return "Maximum reasoning with automatic task delegation"
	case "persistent":
		return "Keep reasoning enabled across turns"
	default:
		return "Model-defined reasoning effort"
	}
}
