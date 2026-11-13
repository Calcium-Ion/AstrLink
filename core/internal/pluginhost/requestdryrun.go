package pluginhost

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/QuantumNous/astrlink/core/contract"
)

// RequestDryRun is what a dry run of a request processing instance did
// with a request: the body ingress left, why it stopped, the reply text the
// reply check left, each step's record and the errors of the hooks that
// failed.
type RequestDryRun struct {
	Body     json.RawMessage                `json:"body,omitempty"`
	Stopped  *RequestRefusal                `json:"stopped,omitempty"`
	Reply    *string                        `json:"reply,omitempty"`
	Records  []contract.RequestPluginRecord `json:"records"`
	Failures []string                       `json:"failures"`
}

// DryRunRequestPlugin runs an instance, saved or not, over a request in
// protocol: its ingress and egress, then its reply check on reply as on a
// whole answer when one is given. The instance runs whatever models it
// lists, and its hooks may fetch as they would on a request.
func (host *Host) DryRunRequestPlugin(ctx context.Context, instance contract.RequestPluginInstance, protocol contract.ProtocolID, body json.RawMessage, reply *string) (RequestDryRun, error) {
	if host == nil || host.plugins == nil {
		return RequestDryRun{}, errors.New("plugins are unavailable")
	}
	target := host.plugins.Current().Plugin(instance.Plugin)
	if target == nil || !target.Loaded() || target.NewAPI() == nil || !target.NewAPI().HasRequestPart() {
		return RequestDryRun{}, errors.New("the plugin has no request processing part")
	}
	meta := target.NewAPI()
	schema := meta.Config()
	config, keywords := schema.HookConfig(schema.Defaults(instance.Config))
	model := ""
	var named struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &named) == nil {
		model = named.Model
	}
	run := &RequestRun{
		host: host, model: model, path: dryRunPaths[protocol], language: host.Settings().Language,
		instances: []*requestInstance{{instance: instance, plugin: target, meta: meta, config: config, keywords: keywords,
			hosts: append(append([]string{}, meta.AllowedHosts...), schema.URLHosts(schema.Defaults(instance.Config))...)}},
	}
	result := RequestDryRun{Body: body}
	changed, refusal := run.Ingress(ctx, protocol, body, nil)
	result.Body, result.Stopped = changed, refusal
	if refusal == nil && run.Serves(contract.RequestStageEgress) {
		result.Stopped = run.Egress(ctx, protocol, changed)
	}
	if result.Stopped == nil && reply != nil {
		check := run.Reply(ctx, protocol, false, RequestAttempt{Protocol: protocol, UpstreamModel: model})
		released, stop := check.Feed([]ReplyPiece{{Index: 0, Text: *reply, Final: true}}, false)
		check.Finish()
		if stop != nil {
			result.Stopped = stop
		} else {
			text := released[0]
			result.Reply = &text
		}
	}
	result.Records = run.Records()
	run.mutex.Lock()
	result.Failures = append([]string{}, run.failures...)
	run.mutex.Unlock()
	return result, nil
}

// dryRunPaths are the client paths a dry run's request stands for.
var dryRunPaths = map[contract.ProtocolID]string{
	contract.ProtocolOpenAIChat:            "/v1/chat/completions",
	contract.ProtocolOpenAIResponses:       "/v1/responses",
	contract.ProtocolAnthropicMessages:     "/v1/messages",
	contract.ProtocolGoogleGenerateContent: "/v1beta/models/gemini:generateContent",
	contract.ProtocolPluginNative:          "/v1/tasks",
}

// RequestProtocol is the gateway protocol of a new-api protocol name.
func RequestProtocol(format string) (contract.ProtocolID, bool) {
	switch format {
	case "openai":
		return contract.ProtocolOpenAIChat, true
	case "openai_responses":
		return contract.ProtocolOpenAIResponses, true
	case "claude":
		return contract.ProtocolAnthropicMessages, true
	case "gemini":
		return contract.ProtocolGoogleGenerateContent, true
	case "task":
		return contract.ProtocolPluginNative, true
	}
	return "", false
}
