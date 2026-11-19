package pluginhost

import (
	"context"
	"encoding/json"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// UICall answers a call of the plugin's interface code with its onUiCall
// hook, within the hook's limit: { name, input, language }. The interface
// code reaches the network only this way, under the plugin's grants.
func (host *Host) UICall(ctx context.Context, target *plugin.Plugin, name string, input json.RawMessage) (json.RawMessage, error) {
	limit := target.Limit(plugin.HookOnUICall)
	ctx, cancel := context.WithTimeoutCause(ctx, limit, errHookLimit)
	defer cancel()
	options := plugin.CallOptions{}
	if target.Can(plugin.CapabilityNetwork) {
		options.NetworkBudget = limit
	}
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	arg := map[string]any{"name": name, "input": input, "language": host.Settings().Language}
	result, _, err := target.Call(ctx, plugin.HookOnUICall, arg, options)
	return result, err
}
