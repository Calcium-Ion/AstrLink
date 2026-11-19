package pluginhost

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/imagecompress"
	"github.com/QuantumNous/astrlink/core/internal/jsonpatch"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// The request processing part of new-api plugins: the instances the user
// added check and change each request at its stages (ingress before
// routing, outbound and egress on each attempt), check the answer (reply)
// and hear of the finished request (report). A plugin the user does not
// trust reads the request as the privacy check leaves it, with placeholders
// for what it hides; its changes are made on that view, and the originals
// go back in place of the placeholders afterwards.

// requestHookTime bounds one call of a stage hook.
const requestHookTime = 10 * time.Second

// Bounds of a stage hook's result.
const (
	maxRequestEffects  = 32
	maxRequestFindings = 64
	maxRequestText     = 256
)

var requestCodePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

// RequestFormat is new-api's protocol name of a gateway protocol, as
// ctx.format names it.
func RequestFormat(protocol contract.ProtocolID) string {
	switch protocol {
	case contract.ProtocolOpenAIChat, contract.ProtocolOpenAICompletions:
		return "openai"
	case contract.ProtocolOpenAIResponses:
		return "openai_responses"
	case contract.ProtocolOpenAIResponsesCompact:
		return "openai_responses_compaction"
	case contract.ProtocolAnthropicMessages:
		return "claude"
	case contract.ProtocolGoogleGenerateContent:
		return "gemini"
	case contract.ProtocolOpenAIEmbeddings, contract.ProtocolGoogleEmbeddings:
		return "embedding"
	case contract.ProtocolRerank:
		return "rerank"
	case contract.ProtocolOpenAIImages:
		return "openai_image"
	case contract.ProtocolOpenAIAudio:
		return "openai_audio"
	case contract.ProtocolOpenAISearch:
		return "openai_alpha_search"
	case contract.ProtocolOpenAIVideos, contract.ProtocolPluginNative:
		return "task"
	}
	return string(protocol)
}

// requestInstance is an instance a request runs, with what its hooks read.
type requestInstance struct {
	instance contract.RequestPluginInstance
	plugin   *plugin.Plugin
	meta     *plugin.TaskMeta
	config   json.RawMessage
	keywords map[string][]string
	hosts    []string
	// skipped marks an optional instance that failed in the reply check.
	skipped bool
}

// RequestRefusal is how a request processing instance stopped a request:
// the status, error code and message the client gets.
type RequestRefusal struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RequestRun runs the request processing instances of one request. Its
// instances and their plugins are those of when the request started.
type RequestRun struct {
	host      *Host
	instances []*requestInstance
	model     string
	path      string
	language  string
	started   time.Time

	mutex   sync.Mutex
	records []contract.RequestPluginRecord
	// failures are the errors of the instances that failed, by stage, for
	// a dry run to show.
	failures []string
	// steps are the entries report hears of, in run order.
	steps []map[string]any
}

// RequestAttempt is what an attempt's stages read of it.
type RequestAttempt struct {
	Protocol      contract.ProtocolID
	UpstreamModel string
	Index         int
	// Summary is what the request stage reads of the client's request:
	// messageCount, toolCount, lastUserTextLength, totalTextLength,
	// hasImages and reasoningEffort.
	Summary map[string]any
	// Form marks the text fields of a form body, which take only replaces
	// of a field with a string.
	Form bool
}

// IngressForm runs the ingress hooks over the text fields of a form body:
// each field with its value, or its values for a repeated one. A hook may
// only replace a field's value, or one value of a repeated field, with a
// string; the files stay as they are.
func (run *RequestRun) IngressForm(ctx context.Context, protocol contract.ProtocolID, names []string, fields map[string][]string) (map[string][]string, *RequestRefusal) {
	if !run.Serves(contract.RequestStageIngress) {
		return fields, nil
	}
	object := map[string]any{}
	for _, name := range names {
		values := fields[name]
		if len(values) == 1 {
			object[name] = values[0]
		} else {
			object[name] = values
		}
	}
	body, _ := json.Marshal(object)
	changed, refusal := run.stage(ctx, contract.RequestStageIngress, protocol, RequestAttempt{Form: true}, body, nil, nil)
	if refusal != nil || bytes.Equal(changed, body) {
		return fields, refusal
	}
	var written map[string]any
	if json.Unmarshal(changed, &written) != nil {
		return fields, nil
	}
	result := map[string][]string{}
	for name, values := range fields {
		result[name] = slices.Clone(values)
	}
	for name, value := range written {
		switch typed := value.(type) {
		case string:
			result[name] = []string{typed}
		case []any:
			for index, item := range typed {
				if text, ok := item.(string); ok && index < len(result[name]) {
					result[name][index] = text
				}
			}
		}
	}
	return result, nil
}

// Reasoning is the effort the request stage's reasoning.set gave, and
// whether it replaces one the client set.
type Reasoning struct {
	Effort   string
	Override bool
}

// Request runs the request stage over the client's request, once per
// attempt, before it is converted: it returns the reasoning effort the
// instances set, or why the request stops.
func (run *RequestRun) Request(ctx context.Context, protocol contract.ProtocolID, body []byte, attempt RequestAttempt, view RequestView) (Reasoning, *RequestRefusal) {
	var reasoning Reasoning
	if !run.Serves(contract.RequestStageRequest) || !json.Valid(body) {
		return reasoning, nil
	}
	for _, current := range run.instances {
		if !current.meta.HasStage(contract.RequestStageRequest) {
			continue
		}
		started := time.Now()
		seen := BodyView{Body: body}
		if !current.plugin.Settings.Trusted && view != nil {
			var err error
			if seen, err = view(ctx, protocol, body); err != nil || seen.Blocked {
				run.note(current, contract.RequestStageRequest, contract.PluginRecordSkipped, "privacy", nil, started, nil)
				continue
			}
		}
		edit, result, err := run.call(ctx, current, contract.RequestStageRequest, protocol, attempt, seen.Body, nil)
		if err != nil {
			run.note(current, contract.RequestStageRequest, contract.PluginRecordFailed, "", nil, started, nil)
			run.failed(current, contract.RequestStageRequest, err)
			if current.meta.Optional {
				continue
			}
			return reasoning, &RequestRefusal{Status: http.StatusInternalServerError, Code: contract.RequestPluginFailedCode, Message: "A request plugin failed"}
		}
		if result.Action == "reject" {
			run.note(current, contract.RequestStageRequest, contract.PluginRecordOK, "", &result, started, []string{contract.PluginEffectDeny})
			message := result.Message
			if message == "" {
				message = "Request rejected by policy"
			}
			return reasoning, &RequestRefusal{Status: http.StatusBadRequest, Code: contract.RequestRejectedCode, Message: message + " (code: " + result.Code + ")"}
		}
		var effects []string
		for _, effect := range edit.effects {
			if effect.Type == "reasoning.set" {
				reasoning = Reasoning{Effort: effect.Effort, Override: effect.Override}
				effects = []string{contract.PluginEffectParams}
			}
		}
		run.note(current, contract.RequestStageRequest, contract.PluginRecordOK, "", &result, started, effects)
	}
	return reasoning, nil
}

// RequestPlugins starts the request processing of a request for model on
// path: nil when no enabled instance serves it, so the request runs no
// JavaScript.
func (host *Host) RequestPlugins(model, path string) *RequestRun {
	if host == nil || host.plugins == nil {
		return nil
	}
	settings := host.Settings()
	if settings.Paused || len(settings.RequestPlugins) == 0 {
		return nil
	}
	run := &RequestRun{host: host, model: model, path: path, language: settings.Language, started: time.Now()}
	generation := host.plugins.Current()
	for _, instance := range settings.RequestPlugins {
		if !instance.Enabled || !instance.Serves(model) {
			continue
		}
		target := generation.Plugin(instance.Plugin)
		if target == nil || !target.Enabled() || target.NewAPI() == nil || !target.NewAPI().HasRequestPart() {
			continue
		}
		meta := target.NewAPI()
		schema := meta.Config()
		config, keywords := schema.HookConfig(schema.Defaults(instance.Config))
		hosts := slices.Clone(meta.AllowedHosts)
		hosts = append(hosts, schema.URLHosts(schema.Defaults(instance.Config))...)
		run.instances = append(run.instances, &requestInstance{instance: instance, plugin: target, meta: meta, config: config, keywords: keywords, hosts: hosts})
	}
	if len(run.instances) == 0 {
		return nil
	}
	return run
}

// Serves reports whether an instance of the request exports the hook.
func (run *RequestRun) Serves(hook string) bool {
	if run == nil {
		return false
	}
	for _, current := range run.instances {
		if current.meta.HasStage(hook) && !current.skipped {
			return true
		}
	}
	return false
}

// Records are the request processing entries of the request record, in
// run order.
func (run *RequestRun) Records() []contract.RequestPluginRecord {
	if run == nil {
		return nil
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	return slices.Clone(run.records)
}

// RequestView shows a plugin a body as the privacy check leaves it.
type RequestView func(ctx context.Context, protocol contract.ProtocolID, body []byte) (BodyView, error)

// stageResult is what a stage hook returns.
type stageResult struct {
	Action   string            `json:"action"`
	Effects  []json.RawMessage `json:"effects"`
	Findings []requestFinding  `json:"findings"`
	Notice   json.RawMessage   `json:"notice"`
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	RuleID   string            `json:"ruleId"`
}

type requestFinding struct {
	Rule     string `json:"rule"`
	Category string `json:"category,omitempty"`
	Path     string `json:"path,omitempty"`
	Count    int    `json:"count"`
}

type stageEffect struct {
	Type     string                `json:"type"`
	Ops      []jsonpatch.Operation `json:"ops"`
	Set      map[string]string     `json:"set"`
	Remove   []string              `json:"remove"`
	Media    *int                  `json:"media"`
	Text     *string               `json:"text"`
	Effort   string                `json:"effort"`
	Override bool                  `json:"override"`
	Format   string                `json:"format"`
	Quality  *int                  `json:"quality"`
	MaxSide  *int                  `json:"maxSide"`
}

// requestEfforts are the efforts reasoning.set takes.
var requestEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// errInstanceFailed marks a result the host could not apply: a failure of
// the instance.
var errInstanceFailed = errors.New("the instance failed")

// Ingress runs the ingress hooks over the client's body, before routing.
// It returns the body the request goes on with, or why it stops.
func (run *RequestRun) Ingress(ctx context.Context, protocol contract.ProtocolID, body []byte, view RequestView) ([]byte, *RequestRefusal) {
	if !run.Serves(contract.RequestStageIngress) {
		return body, nil
	}
	return run.stage(ctx, contract.RequestStageIngress, protocol, RequestAttempt{}, body, nil, view)
}

// Outbound runs the outbound hooks over the body an attempt built for its
// provider, then the egress hooks over what it will send. header takes the
// headers the outbound hooks set.
func (run *RequestRun) Outbound(ctx context.Context, attempt RequestAttempt, body []byte, header http.Header, view RequestView) ([]byte, *RequestRefusal) {
	if run == nil {
		return body, nil
	}
	if run.Serves(contract.RequestStageOutbound) {
		var refusal *RequestRefusal
		body, refusal = run.stage(ctx, contract.RequestStageOutbound, attempt.Protocol, attempt, body, header, view)
		if refusal != nil {
			return body, refusal
		}
	}
	if run.Serves(contract.RequestStageEgress) {
		return run.stage(ctx, contract.RequestStageEgress, attempt.Protocol, attempt, body, header, view)
	}
	return body, nil
}

// Egress runs the egress hooks alone over a body, as a task submission's
// attempt does on the body its task plugin built.
func (run *RequestRun) Egress(ctx context.Context, protocol contract.ProtocolID, body []byte) *RequestRefusal {
	if !run.Serves(contract.RequestStageEgress) {
		return nil
	}
	_, refusal := run.stage(ctx, contract.RequestStageEgress, protocol, RequestAttempt{Protocol: protocol}, body, nil, nil)
	return refusal
}

// stage runs one stage's hooks in order, each on what the one before left.
func (run *RequestRun) stage(ctx context.Context, stage string, protocol contract.ProtocolID, attempt RequestAttempt, body []byte, header http.Header, view RequestView) ([]byte, *RequestRefusal) {
	setHeaders := map[string]string{}
	for _, current := range run.instances {
		if !current.meta.HasStage(stage) {
			continue
		}
		started := time.Now()
		if !json.Valid(body) {
			run.note(current, stage, contract.PluginRecordSkipped, "unchecked", nil, started, nil)
			continue
		}
		seen := BodyView{Body: body}
		var err error
		if !current.plugin.Settings.Trusted && view != nil {
			seen, err = view(ctx, protocol, body)
			if err != nil || seen.Blocked {
				// What the privacy check would not let out does not reach
				// the plugin either; the check stops the request itself.
				run.note(current, stage, contract.PluginRecordSkipped, "privacy", nil, started, nil)
				continue
			}
		}
		edited, result, err := run.call(ctx, current, stage, protocol, attempt, seen.Body, setHeaders)
		if err == nil && result.Action == "reject" {
			run.note(current, stage, contract.PluginRecordOK, "", &result, started, []string{contract.PluginEffectDeny})
			message := result.Message
			if message == "" {
				message = "Request rejected by policy"
			}
			return body, &RequestRefusal{Status: http.StatusBadRequest, Code: contract.RequestRejectedCode, Message: message + " (code: " + result.Code + ")"}
		}
		if err == nil {
			edited, err = run.applied(stage, seen, body, edited)
		}
		if err != nil {
			run.note(current, stage, contract.PluginRecordFailed, "", nil, started, nil)
			run.failed(current, stage, err)
			if current.meta.Optional {
				continue
			}
			return body, &RequestRefusal{Status: http.StatusInternalServerError, Code: contract.RequestPluginFailedCode, Message: "A request plugin failed"}
		}
		var effects []string
		if !bytes.Equal(edited.body, body) {
			effects = append(effects, contract.PluginEffectBody)
		}
		if len(edited.headers) > 0 {
			effects = append(effects, contract.PluginEffectHeaders)
			for name, value := range edited.headers {
				if value == "" {
					delete(setHeaders, name)
					if header != nil {
						header.Del(name)
					}
				} else {
					setHeaders[name] = value
					if header != nil {
						header.Set(name, value)
					}
				}
			}
		}
		body = edited.body
		run.note(current, stage, contract.PluginRecordOK, "", &result, started, effects)
	}
	return body, nil
}

// stageEdit is what a stage hook's effects make of the request.
type stageEdit struct {
	body []byte
	// headers set, "" for removed.
	headers map[string]string
	// effects are the effects to apply, on the body the hook saw.
	effects []stageEffect
	media   []requestMedia
	// reencoded counts the images media.reencode replaced.
	reencoded int
}

// call runs one instance's hook for a stage over seen, the body as the
// plugin may see it, and reads its result.
func (run *RequestRun) call(ctx context.Context, current *requestInstance, stage string, protocol contract.ProtocolID, attempt RequestAttempt, seen []byte, setHeaders map[string]string) (stageEdit, stageResult, error) {
	shown, media, err := hideMedia(seen)
	if err != nil {
		return stageEdit{}, stageResult{}, err
	}
	if stage == contract.RequestStageOutbound {
		measureImages(media)
	}
	view := map[string]any{
		"stage": stage, "format": RequestFormat(protocol), "path": run.path, "model": run.model, "attempt": attempt.Index,
		"instance": map[string]any{"id": current.instance.ID, "name": current.instance.Name},
		"config":   current.config,
	}
	if stage != contract.RequestStageIngress {
		view["upstreamModel"] = attempt.UpstreamModel
	}
	request := map[string]any{"body": json.RawMessage(shown), "media": mediaView(media)}
	if attempt.Form {
		request["form"] = true
	}
	if stage == contract.RequestStageRequest && attempt.Summary != nil {
		request["summary"] = attempt.Summary
	}
	if stage == contract.RequestStageOutbound || stage == contract.RequestStageEgress {
		headers := map[string]string{}
		for name, value := range setHeaders {
			headers[name] = value
		}
		request["headers"] = headers
	}
	raw, err := run.invoke(ctx, current, stage, media, view, request)
	if err != nil {
		return stageEdit{}, stageResult{}, err
	}
	var result stageResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return stageEdit{}, stageResult{}, errors.New("the result is not {action, ...}")
	}
	if err := result.check(stage); err != nil {
		return stageEdit{}, stageResult{}, err
	}
	edit := stageEdit{headers: map[string]string{}, media: media}
	operations := 0
	for index, raw := range result.Effects {
		var effect stageEffect
		if err := json.Unmarshal(raw, &effect); err != nil {
			return stageEdit{}, stageResult{}, fmt.Errorf("effect %d is malformed", index)
		}
		allowed := map[string][]string{
			contract.RequestStageIngress:  {"json.patch", "media.text"},
			contract.RequestStageRequest:  {"reasoning.set"},
			contract.RequestStageOutbound: {"json.patch", "header.patch", "media.reencode", "media.text"},
		}[stage]
		if !slices.Contains(allowed, effect.Type) {
			return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: %s does not take %q", index, stage, effect.Type)
		}
		if attempt.Form && effect.Type != "json.patch" && effect.Type != "media.text" {
			return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: a form body takes json.patch replaces alone", index)
		}
		switch effect.Type {
		case "json.patch":
			for _, operation := range effect.Ops {
				var text string
				if attempt.Form && (operation.Op != "replace" || json.Unmarshal(operation.Value, &text) != nil) {
					return stageEdit{}, stageResult{}, errors.New("a form body takes replaces of a text field with a string alone")
				}
			}
			operations += len(effect.Ops)
			if operations > maxRelayOperations {
				return stageEdit{}, stageResult{}, fmt.Errorf("the effects hold more than %d operations", maxRelayOperations)
			}
			for _, operation := range effect.Ops {
				if mediaRefPattern.Match(operation.Value) {
					return stageEdit{}, stageResult{}, errors.New("a json.patch value names a file")
				}
			}
		case "header.patch":
			if len(effect.Set)+len(effect.Remove) > maxRelayHeaders {
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d changes more than %d headers", index, maxRelayHeaders)
			}
			for _, name := range effect.Remove {
				if relayProtected(http.CanonicalHeaderKey(name), "") {
					return stageEdit{}, stageResult{}, fmt.Errorf("effect %d removes the protected header %s", index, name)
				}
				edit.headers[http.CanonicalHeaderKey(name)] = ""
			}
			for name, value := range effect.Set {
				if relayProtected(http.CanonicalHeaderKey(name), "") || !validHeader(name, value) || value == "" {
					return stageEdit{}, stageResult{}, fmt.Errorf("effect %d sets the protected or malformed header %s", index, name)
				}
				edit.headers[http.CanonicalHeaderKey(name)] = value
			}
		case "media.text":
			if effect.Media == nil || *effect.Media < 0 || *effect.Media >= len(media) || effect.Text == nil || len(*effect.Text) > 1<<20 {
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d names no file of the call, or its text is over 1 MiB", index)
			}
		case "media.reencode":
			switch {
			case effect.Media == nil || *effect.Media < 0 || *effect.Media >= len(media):
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d names no file of the call", index)
			case !slices.Contains([]string{"auto", "jpeg", "png", "webp"}, effect.Format):
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: the format %q is not auto, jpeg, png or webp", index, effect.Format)
			case effect.Quality != nil && (*effect.Quality < 1 || *effect.Quality > 100 || effect.Format == "webp"):
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: quality is 1 to 100, and webp takes none", index)
			case effect.MaxSide != nil && (*effect.MaxSide < 16 || *effect.MaxSide > 16384):
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: maxSide is 16 to 16384", index)
			}
		case "reasoning.set":
			if !slices.Contains(requestEfforts, effect.Effort) {
				return stageEdit{}, stageResult{}, fmt.Errorf("effect %d: the effort %q is not one of %s", index, effect.Effort, strings.Join(requestEfforts, ", "))
			}
		}
		edit.effects = append(edit.effects, effect)
	}
	return edit, result, nil
}

// applied applies an edit's effects to the body the hook saw and returns
// the body the request goes on with: the plugin's changes, with the
// originals back in place of what the privacy check hid. The guarded
// members of the stage must stay as they were.
func (run *RequestRun) applied(stage string, seen BodyView, body []byte, edit stageEdit) (stageEdit, error) {
	if len(edit.effects) == 0 {
		edit.body = body
		return edit, nil
	}
	document, err := jsonpatch.Parse(seen.Body)
	if err != nil {
		return edit, err
	}
	guarded := []string{"/model", "/stream"}
	if stage != contract.RequestStageIngress {
		guarded = relayBillingMembers
	}
	before := map[string]string{}
	for _, member := range guarded {
		if value, ok := document.Get(member); ok {
			before[member] = string(value)
		}
	}
	changed := false
	for _, effect := range edit.effects {
		switch effect.Type {
		case "json.patch":
			if err := document.Apply(effect.Ops); err != nil {
				return edit, err
			}
			changed = changed || len(effect.Ops) > 0
		case "media.text":
			item := edit.media[*effect.Media]
			if !item.rewritable {
				continue
			}
			encoded, _ := json.Marshal(item.prefix + base64.StdEncoding.EncodeToString([]byte(*effect.Text)))
			if err := document.Replace(item.path, encoded); err != nil {
				return edit, err
			}
			changed = true
		case "media.reencode":
			item := &edit.media[*effect.Media]
			if !strings.HasPrefix(item.mime, "image/") {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(item.data)
			if err != nil {
				continue
			}
			quality, maxSide := 0, 0
			if effect.Quality != nil {
				quality = *effect.Quality
			}
			if effect.MaxSide != nil {
				maxSide = *effect.MaxSide
			}
			reencoded, mime, ok := imagecompress.Reencode(data, effect.Format, quality, maxSide)
			if !ok {
				continue
			}
			value := base64.StdEncoding.EncodeToString(reencoded)
			if item.prefix != "" {
				value = "data:" + mime + ";base64," + value
			}
			encoded, _ := json.Marshal(value)
			if err := document.Replace(item.path, encoded); err != nil {
				return edit, err
			}
			if item.mimePath != "" {
				typed, _ := json.Marshal(mime)
				_ = document.Replace(item.mimePath, typed)
			}
			edit.reencoded++
			changed = true
		}
	}
	for _, member := range guarded {
		value, ok := document.Get(member)
		if was, had := before[member]; ok != had || (ok && string(value) != was) {
			return edit, fmt.Errorf("the effects change %s", strings.TrimPrefix(member, "/"))
		}
	}
	if !changed {
		edit.body = body
		return edit, nil
	}
	encoded, err := document.Bytes()
	if err != nil {
		return edit, err
	}
	if seen.Restore != nil {
		encoded = seen.Restore(encoded)
	}
	edit.body = encoded
	return edit, nil
}

// check refuses a result the stage does not take.
func (result stageResult) check(stage string) error {
	switch result.Action {
	case "continue":
	case "reject":
		if stage == contract.RequestStageReply {
			return errors.New("a reply check stops, it does not reject")
		}
		if !requestCodePattern.MatchString(result.Code) {
			return fmt.Errorf("the code %q is not 1 to 64 lowercase letters, digits, _, . or -", result.Code)
		}
	default:
		return fmt.Errorf("the action %q is not continue or reject", result.Action)
	}
	switch {
	case len(result.Findings) > maxRequestFindings:
		return fmt.Errorf("a result has at most %d findings", maxRequestFindings)
	case len(result.Effects) > maxRequestEffects:
		return fmt.Errorf("a result has at most %d effects", maxRequestEffects)
	case stage == contract.RequestStageEgress && len(result.Effects) > 0:
		return errors.New("egress only reads: it returns no effects")
	case utf8.RuneCountInString(result.Message) > maxRequestText || utf8.RuneCountInString(result.RuleID) > maxRequestText:
		return errors.New("the message and the rule id are at most 256 characters")
	}
	for _, finding := range result.Findings {
		if finding.Rule == "" || finding.Count < 0 || utf8.RuneCountInString(finding.Rule) > maxRequestText ||
			utf8.RuneCountInString(finding.Category) > maxRequestText || utf8.RuneCountInString(finding.Path) > maxRequestText {
			return errors.New("a finding names its rule, at most 256 characters, with a count of 0 or more")
		}
	}
	return nil
}

// invoke calls a request processing hook with what its utils read.
func (run *RequestRun) invoke(ctx context.Context, current *requestInstance, hook string, media []requestMedia, args ...any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, requestHookTime)
	defer cancel()
	options := plugin.CallOptions{RequestFiles: requestFiles(media, current.keywords)}
	if hook != contract.RequestStageRequest && hook != contract.RequestStageOutbound {
		hosts := current.hosts
		options.TaskFetch = &plugin.TaskFetch{
			Allowed: func(target *url.URL) bool { return slices.Contains(hosts, strings.ToLower(target.Host)) },
			Client:  &http.Client{},
			Post:    true,
			Refs: func(ref string) (string, bool) {
				for _, item := range media {
					if item.ref == ref {
						return "data:" + item.mime + ";base64," + item.data, true
					}
				}
				return "", false
			},
		}
		options.NetworkBudget = requestHookTime
	}
	result, _, err := current.plugin.CallTask(ctx, hook, args, options)
	return result, err
}

// failed logs an instance's failure, and keeps it for a dry run.
func (run *RequestRun) failed(current *requestInstance, stage string, err error) {
	run.host.logf("request plugin %s (%s) failed at %s: %v", current.instance.ID, current.plugin.Key, stage, err)
	run.mutex.Lock()
	run.failures = append(run.failures, stage+": "+truncateRunes(err.Error(), 1024))
	run.mutex.Unlock()
}

// note adds an instance's entry for a stage to the request record.
func (run *RequestRun) note(current *requestInstance, stage string, status contract.PluginRecordStatus, skipped string, result *stageResult, started time.Time, effects []string) {
	record := contract.RequestPluginRecord{
		Plugin: current.plugin.Key, PluginVersion: current.meta.Version, Hook: stage, Title: truncateRunes(current.instance.Name, 64),
		Status: status, SkippedReason: skipped, ElapsedMs: int(time.Since(started).Milliseconds()), Effects: effects,
	}
	step := map[string]any{"plugin": current.plugin.Key, "name": current.meta.Name, "stage": stage}
	if result != nil {
		record.Summary = run.summary(*result)
		if len(result.Findings) > 0 {
			findings := []map[string]any{}
			for _, finding := range result.Findings {
				entry := map[string]any{"count": finding.Count}
				if finding.Category != "" {
					entry["category"] = finding.Category
				}
				findings = append(findings, entry)
			}
			step["findings"] = findings
		}
		if result.Action == "reject" {
			step["rejected"] = map[string]any{"code": result.Code}
		}
		if len(result.Notice) > 0 {
			step["notice"] = result.Notice
		}
	}
	switch {
	case status == contract.PluginRecordFailed:
		record.Summary = "failed"
		step["rejected"] = map[string]any{"code": contract.RequestPluginFailedCode}
	case skipped != "":
		step["unchecked"] = true
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if len(run.records) < contract.MaxRequestPluginRecords {
		run.records = append(run.records, record)
	}
	if status != contract.PluginRecordOK || len(effects) > 0 || (result != nil && (len(result.Findings) > 0 || len(result.Notice) > 0)) {
		run.steps = append(run.steps, step)
	}
}

// summary describes a result for the request record: the plugin's notice
// in the user's language, else what it found or why it rejected.
func (run *RequestRun) summary(result stageResult) string {
	var parts []string
	if notice := noticeIn(result.Notice, run.language); notice != "" {
		parts = append(parts, notice)
	}
	if result.Action == "reject" {
		parts = append(parts, map[string]string{"zh-CN": "已拦截：", "ja": "拒否："}[run.language]+result.Code)
		if parts[len(parts)-1] == result.Code {
			parts[len(parts)-1] = "rejected: " + result.Code
		}
	}
	counts := map[string]int{}
	var order []string
	for _, finding := range result.Findings {
		name := finding.Category
		if name == "" {
			name = finding.Rule
		}
		if _, ok := counts[name]; !ok {
			order = append(order, name)
		}
		counts[name] += finding.Count
	}
	var found []string
	for _, name := range order {
		found = append(found, name+" ×"+strconv.Itoa(counts[name]))
	}
	if len(found) > 0 {
		parts = append(parts, strings.Join(found, ", "))
	}
	return truncateRunes(strings.Join(parts, " · "), 512)
}

// noticeIn reads a notice, a string or LocalizedText, in language.
func noticeIn(raw json.RawMessage, language string) string {
	if len(raw) == 0 {
		return ""
	}
	var text plugin.LocalizedText
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	if value := text.In(language); value != "" {
		return value
	}
	if strings.HasPrefix(language, "zh") {
		return text.In("zh")
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

// requestMedia is an inline file of a body, which hooks see as
// "[media:N]".
type requestMedia struct {
	ref    string
	path   string
	mime   string
	data   string
	prefix string
	bytes  int64
	// rewritable is a text file whose text media.text may replace.
	rewritable bool
	// mimePath is the member that names its media type beside it, for
	// a Claude or Gemini part.
	mimePath      string
	width, height int
}

// measureImages reads the size of each image of the call.
func measureImages(media []requestMedia) {
	for index := range media {
		item := &media[index]
		if !strings.HasPrefix(item.mime, "image/") {
			continue
		}
		if data, err := base64.StdEncoding.DecodeString(item.data); err == nil {
			item.width, item.height, _ = imagecompress.Size(data)
		}
	}
}

var mediaRefPattern = regexp.MustCompile(`\[media:\d+\]`)

// hideMedia replaces each inline file of body with "[media:N]": data
// URLs, the data of a Claude base64 source and of a Gemini inline part.
func hideMedia(body []byte) ([]byte, []requestMedia, error) {
	if !bytes.Contains(body, []byte("base64")) && !bytes.Contains(body, []byte("inlineData")) && !bytes.Contains(body, []byte("inline_data")) {
		return body, nil, nil
	}
	document, err := jsonpatch.Parse(body)
	if err != nil {
		return nil, nil, err
	}
	var media []requestMedia
	document.Strings(func(pointer string, holder *jsonpatch.Object, value string) (string, bool) {
		var mime, data, prefix, mimePath string
		switch {
		case strings.HasPrefix(value, "data:") && strings.Contains(value[:min(len(value), 256)], ";base64,"):
			header, payload, _ := strings.Cut(value, ",")
			mime = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
			data, prefix = payload, header+","
		case holder != nil && strings.HasSuffix(pointer, "/data") && memberText(holder, "type") == "base64":
			mime, data, mimePath = memberText(holder, "media_type"), value, strings.TrimSuffix(pointer, "/data")+"/media_type"
		case holder != nil && strings.HasSuffix(pointer, "/data") && (strings.Contains(pointer, "/inlineData/") || strings.Contains(pointer, "/inline_data/")):
			mime, data = memberText(holder, "mimeType")+memberText(holder, "mime_type"), value
			mimePath = strings.TrimSuffix(pointer, "/data") + "/mimeType"
			if memberText(holder, "mime_type") != "" {
				mimePath = strings.TrimSuffix(pointer, "/data") + "/mime_type"
			}
		default:
			return "", false
		}
		item := requestMedia{
			ref: "[media:" + strconv.Itoa(len(media)) + "]", path: pointer, mime: mime, data: data, prefix: prefix, mimePath: mimePath,
			bytes: int64(base64.StdEncoding.DecodedLen(len(strings.TrimRight(data, "=")))),
		}
		item.rewritable = strings.HasPrefix(mime, "text/") || mime == "application/json"
		media = append(media, item)
		return item.ref, true
	})
	if len(media) == 0 {
		return body, nil, nil
	}
	hidden, err := document.Bytes()
	return hidden, media, err
}

func memberText(object *jsonpatch.Object, key string) string {
	value, _ := object.Member(key)
	text, _ := value.(string)
	return text
}

func mediaView(media []requestMedia) []map[string]any {
	view := []map[string]any{}
	for index, item := range media {
		entry := map[string]any{"index": index, "path": item.path, "mimeType": item.mime, "bytes": item.bytes}
		if item.width > 0 {
			entry["width"], entry["height"] = item.width, item.height
		}
		view = append(view, entry)
	}
	return view
}

// mediaKind is the kind of file a media type names.
func mediaKind(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "text/"), mime == "application/pdf", mime == "application/json",
		strings.Contains(mime, "wordprocessingml"), mime == "application/msword":
		return "document"
	}
	return "other"
}

// requestFiles are the utils' view of a call's inline files.
func requestFiles(media []requestMedia, keywords map[string][]string) *plugin.RequestFiles {
	files := &plugin.RequestFiles{Keywords: keywords}
	for _, item := range media {
		size := item.bytes
		attachment := plugin.Attachment{Ref: item.ref, Kind: mediaKind(item.mime), Mime: item.mime, Size: &size, Path: item.path, Rewritable: item.rewritable}
		files.Attachments = append(files.Attachments, attachment)
	}
	files.Text = func(ref string, maxChars int) map[string]any {
		for _, item := range media {
			if item.ref != ref {
				continue
			}
			if !item.rewritable {
				return map[string]any{"unchecked": "unsupported"}
			}
			decoded, err := base64.StdEncoding.DecodeString(item.data)
			if err != nil || !utf8.Valid(decoded) {
				return map[string]any{"unchecked": "unreadable"}
			}
			units := utf16.Encode([]rune(string(decoded)))
			if len(units) <= maxChars {
				return map[string]any{"text": string(decoded), "truncated": false}
			}
			return map[string]any{"text": string(utf16.Decode(units[:maxChars])), "truncated": true}
		}
		return nil
	}
	return files
}
