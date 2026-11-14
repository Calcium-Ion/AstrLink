package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// RequestPluginInstance is one configured use of a new-api plugin's
// request processing part: its hooks check and change every request whose
// model it lists, all when it lists none, with config as ctx.config. The
// instances run in the order the settings list them.
type RequestPluginInstance struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Plugin  string          `json:"plugin"`
	Enabled bool            `json:"enabled"`
	Models  []string        `json:"models"`
	Config  json.RawMessage `json:"config,omitempty"`
}

// Bounds of the request processing instances.
const (
	MaxRequestPluginInstances   = 64
	MaxRequestPluginModels      = 100
	MaxRequestPluginModelRunes  = 128
	MaxRequestPluginNameRunes   = 64
	MaxRequestPluginConfigBytes = 256 << 10
)

var requestPluginInstanceIDPattern = regexp.MustCompile(`^rpi_[a-z0-9]{8,32}$`)

// ValidRequestPluginInstanceID reports whether id can name an instance.
func ValidRequestPluginInstanceID(id string) bool {
	return requestPluginInstanceIDPattern.MatchString(id)
}

// Validate checks an instance's shape; whether its plugin has a request
// processing part and takes its config is the host's to check.
func (instance RequestPluginInstance) Validate() error {
	switch {
	case !ValidRequestPluginInstanceID(instance.ID):
		return fmt.Errorf("request plugin instance id %q is not rpi_ and 8 to 32 lowercase letters or digits", instance.ID)
	case strings.TrimSpace(instance.Name) == "" || utf8.RuneCountInString(instance.Name) > MaxRequestPluginNameRunes:
		return fmt.Errorf("request plugin instance %s: the name is 1 to %d characters", instance.ID, MaxRequestPluginNameRunes)
	case !ValidPluginKey(instance.Plugin):
		return fmt.Errorf("request plugin instance %s: plugin %q is not a plugin key", instance.ID, instance.Plugin)
	case len(instance.Models) > MaxRequestPluginModels:
		return fmt.Errorf("request plugin instance %s lists more than %d models", instance.ID, MaxRequestPluginModels)
	case len(instance.Config) > MaxRequestPluginConfigBytes:
		return fmt.Errorf("request plugin instance %s: the config is larger than %d KiB", instance.ID, MaxRequestPluginConfigBytes>>10)
	case len(instance.Config) > 0 && (!json.Valid(instance.Config) || strings.TrimSpace(string(instance.Config))[0] != '{'):
		return fmt.Errorf("request plugin instance %s: the config is a JSON object", instance.ID)
	}
	seen := map[string]bool{}
	for _, model := range instance.Models {
		if model == "" || model != strings.TrimSpace(model) || utf8.RuneCountInString(model) > MaxRequestPluginModelRunes || seen[model] {
			return fmt.Errorf("request plugin instance %s: models are distinct names of 1 to %d characters", instance.ID, MaxRequestPluginModelRunes)
		}
		seen[model] = true
	}
	return nil
}

// Serves reports whether the instance runs for model, the model the client
// asked for without its @ modifiers.
func (instance RequestPluginInstance) Serves(model string) bool {
	if len(instance.Models) == 0 {
		return true
	}
	base, _, _ := strings.Cut(model, "@")
	for _, listed := range instance.Models {
		if listed == base {
			return true
		}
	}
	return false
}

// PluginRequestPart is what a plugin's request processing part offers: the
// hooks it exports, the schema of an instance's config as the plugin wrote
// it, whether a request may go on without an instance that fails, and its
// description in the page's language.
type PluginRequestPart struct {
	Stages       []string        `json:"stages"`
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
	Optional     bool            `json:"optional,omitempty"`
	Description  string          `json:"description,omitempty"`
	// Samples are the requests the plugin offers a dry run, as it wrote
	// them: name, format, body and reply.
	Samples json.RawMessage `json:"samples,omitempty"`
}

// PluginKindRequest is a new-api plugin with a request processing part and
// neither a task nor a provider part.
const PluginKindRequest = "request"

// Stages of a request processing plugin, as its hooks and records name
// them.
const (
	RequestStageIngress  = "ingress"
	RequestStageRequest  = "request"
	RequestStageOutbound = "outbound"
	RequestStageEgress   = "egress"
	RequestStageReply    = "reply"
	RequestStageReport   = "report"
)

// The error codes a request processing plugin answers a client with.
const (
	RequestRejectedCode     = "request_rejected"
	RequestPluginFailedCode = "request_plugin_failed"
	ResponseRejectedCode    = "response_rejected"
)
