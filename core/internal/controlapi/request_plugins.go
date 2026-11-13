package controlapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
)

// validateConfigTime bounds a plugin's validateConfig.
const validateConfigTime = 5 * time.Second

// checkRequestPlugins readies the request processing instances a save
// gives: a new one gets an id, and each must name a plugin with a request
// processing part whose schema and validateConfig take its config.
func (handler *Handler) checkRequestPlugins(ctx context.Context, instances []contract.RequestPluginInstance) error {
	for index := range instances {
		instance := &instances[index]
		if instance.ID == "" {
			instance.ID = newRequestPluginInstanceID()
		}
		instance.Name = strings.TrimSpace(instance.Name)
		if instance.Models == nil {
			instance.Models = []string{}
		}
		for at, model := range instance.Models {
			instance.Models[at] = strings.TrimSpace(model)
		}
		if err := instance.Validate(); err != nil {
			return err
		}
		if handler.plugins == nil {
			return fmt.Errorf("plugins are unavailable")
		}
		target := handler.plugins.Current().Plugin(instance.Plugin)
		if target == nil || target.NewAPI() == nil || !target.NewAPI().HasRequestPart() {
			return fmt.Errorf("%s: %s is not an installed plugin with a request processing part", instance.Name, instance.Plugin)
		}
		meta := target.NewAPI()
		checked, err := meta.Config().CheckConfig(instance.Config)
		if err != nil {
			return fmt.Errorf("%s: the config %v", instance.Name, err)
		}
		if target.HasTaskHook("validateConfig") {
			config, _ := meta.Config().HookConfig(checked)
			call, cancel := context.WithTimeout(ctx, validateConfigTime)
			_, _, err := target.CallTask(call, "validateConfig", []any{json.RawMessage(config)}, plugin.CallOptions{})
			cancel()
			if err != nil {
				return fmt.Errorf("%s: %s", instance.Name, validateConfigMessage(err))
			}
		}
	}
	return nil
}

// validateConfigMessage is what a throw of validateConfig says, cut to 256
// characters.
func validateConfigMessage(err error) string {
	message := err.Error()
	if callErr, ok := err.(*plugin.CallError); ok {
		if callErr.Thrown != "" {
			message = callErr.Thrown
		} else if callErr.Message != "" {
			message = callErr.Message
		}
	}
	if at := strings.Index(message, " (at "); at > 0 {
		message = message[:at]
	}
	message = strings.TrimPrefix(message, "threw Error: ")
	if runes := []rune(message); len(runes) > 256 {
		message = string(runes[:256])
	}
	return message
}

func newRequestPluginInstanceID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var random [16]byte
	_, _ = rand.Read(random[:])
	id := make([]byte, len(random))
	for index, value := range random {
		id[index] = alphabet[int(value)%len(alphabet)]
	}
	return "rpi_" + string(id)
}

// requestDryRunInput is a dry run of a request processing instance: its
// config, the request in a protocol, and a reply text to check.
type requestDryRunInput struct {
	Config json.RawMessage `json:"config"`
	Format string          `json:"format"`
	Body   json.RawMessage `json:"body"`
	Reply  *string         `json:"reply"`
}

// requestPluginDryRun runs an instance of the plugin, saved or not, over
// a request without sending it.
func (handler *Handler) requestPluginDryRun(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	var input requestDryRunInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	protocol, ok := pluginhost.RequestProtocol(input.Format)
	if !ok || !json.Valid(input.Body) || len(input.Body) == 0 || input.Body[0] != '{' {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_dry_run", "format is openai, openai_responses, claude, gemini or task, and body a JSON object")
		return
	}
	instances := []contract.RequestPluginInstance{{Name: target.Key, Plugin: target.Key, Enabled: true, Config: input.Config}}
	if err := handler.checkRequestPlugins(request.Context(), instances); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request_plugin", err.Error())
		return
	}
	result, err := handler.pluginHost.DryRunRequestPlugin(request.Context(), instances[0], protocol, input.Body, input.Reply)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request_plugin", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
