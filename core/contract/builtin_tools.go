package contract

import (
	"encoding/json"
	"fmt"
	"net/url"
)

func (settings *BuiltinTools) UnmarshalJSON(data []byte) error {
	type plain BuiltinTools
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != 2 || fields["web_search"] == nil || fields["image_generation"] == nil {
		return fmt.Errorf("both builtin tool configurations are required")
	}
	var value plain
	if err := decodeStrictContractJSON(data, &value); err != nil {
		return err
	}
	*settings = BuiltinTools(value)
	return nil
}

func (config *BuiltinTool) UnmarshalJSON(data []byte) error {
	type plain BuiltinTool
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields["enabled"] == nil || fields["backend"] == nil || string(fields["enabled"]) == "null" || string(fields["backend"]) == "null" {
		return fmt.Errorf("tool enabled and backend fields are required")
	}
	var value plain
	if err := decodeStrictContractJSON(data, &value); err != nil {
		return err
	}
	*config = BuiltinTool(value)
	return nil
}

// BuiltinTools configures gateway-owned execution of Responses hosted tools.
// Credentials are deliberately absent from the routing document.
type BuiltinTools struct {
	WebSearch       BuiltinTool `json:"web_search"`
	ImageGeneration BuiltinTool `json:"image_generation"`
}

type BuiltinTool struct {
	Enabled   bool      `json:"enabled"`
	Backend   string    `json:"backend"`
	ServiceID ServiceID `json:"service_id,omitempty"`
	Model     string    `json:"model,omitempty"`
	BaseURL   string    `json:"base_url,omitempty"`
}

func (settings BuiltinTools) Validate() error {
	for kind, config := range map[string]BuiltinTool{"web_search": settings.WebSearch, "image_generation": settings.ImageGeneration} {
		if err := config.Validate(kind); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
	}
	return nil
}

func (config BuiltinTool) Validate(kind string) error {
	if !BuiltinToolKind(kind) {
		return fmt.Errorf("unknown builtin tool")
	}
	if config.Backend != "" && config.Backend != "upstream" && config.Backend != "external" && config.Backend != "service_images" {
		return fmt.Errorf("invalid backend")
	}
	if config.Backend == "service_images" && kind != "image_generation" {
		return fmt.Errorf("provider Images API is only available for image generation")
	}
	if config.Enabled && config.Backend == "" {
		return fmt.Errorf("backend is required")
	}
	if config.Model != "" {
		if err := validateBoundedText("model", config.Model, 256, false); err != nil {
			return err
		}
	}
	if config.ServiceID != "" {
		if err := config.ServiceID.Validate(); err != nil {
			return err
		}
	}
	if config.BaseURL != "" {
		u, err := url.Parse(config.BaseURL)
		if err != nil || len(config.BaseURL) > 2048 || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid API base URL")
		}
	}
	if config.Enabled {
		if config.Backend == "upstream" && (config.ServiceID == "" || config.Model == "") {
			return fmt.Errorf("provider and model are required")
		}
		if config.Backend == "external" && (config.BaseURL == "" || (kind == "image_generation" && config.Model == "")) {
			return fmt.Errorf("API URL and image model are required")
		}
		if config.Backend == "service_images" && (config.ServiceID == "" || config.Model == "") {
			return fmt.Errorf("provider and image model are required")
		}
	}
	return nil
}

func BuiltinToolKind(kind string) bool { return kind == "web_search" || kind == "image_generation" }

// BuiltinImagesServiceKind reports provider kinds whose API root serves the
// OpenAI Images endpoints with the provider's ordinary credential.
func BuiltinImagesServiceKind(kind ServiceKind) bool {
	return kind == ServiceKindNewAPI || kind == ServiceKindOpenAI || kind == ServiceKindOpenAICompatible
}

func (settings BuiltinTools) For(kind string) BuiltinTool {
	if kind == "web_search" {
		return settings.WebSearch
	}
	return settings.ImageGeneration
}
