// Package plugin runs gateway plugins: JavaScript or TypeScript modules that
// the host compiles once and calls in sandboxed moejs runtimes.
//
// A plugin decides policy: what to look up, what to keep, how to lay it out,
// what to show. The host owns the mechanism: the sandbox, the budgets, the
// network, the storage, and where a plugin's results go. A plugin reaches
// nothing but the astrlink.* functions the host installs: it has no files,
// timers, environment or network of its own, and never sees a credential.
package plugin

import (
	"errors"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// APIVersion is the newest plugin contract this host implements; a plugin
// states the one it was written for in meta.apiVersion. Version 1 plugins
// are synchronous; version 2 hooks may be async, and the host functions
// that wait on the host return promises.
const APIVersion = 2

// Capabilities a plugin asks for in meta.capabilities. None is in effect
// until the user grants it when enabling the plugin.
const (
	// CapabilityInject lets prepareSession and prepareTurn add blocks to
	// the request.
	CapabilityInject = "context.inject"
	// CapabilityRequest gives the hooks the turn's text and metadata, and
	// astrlink.request.info.
	CapabilityRequest = "request.read"
	// CapabilityNetwork gives astrlink.http.fetch, limited to the
	// addresses the user allows.
	CapabilityNetwork = "network"
	// CapabilityStorage gives astrlink.store, the plugin's own key-value
	// store in the gateway settings.
	CapabilityStorage = "storage"
	// CapabilityViews lets the hooks return views for request records and
	// the plugin page.
	CapabilityViews = "ui.views"
	// CapabilityTools lets the model run the read-only queries the plugin
	// declares in meta.tools, through the gateway's lookup outlet.
	CapabilityTools = "model.tools"

	// The capabilities of apiVersion 2.

	// CapabilityRequestWrite lets beforeForward change the request in the
	// structured ways: the system prompt, the turn, parameters and tools.
	// It covers what context.inject lets the prepare hooks do.
	CapabilityRequestWrite = "request.write"
	// CapabilityRequestWriteRaw lets beforeForward replace the client's
	// request body, and beforeUpstream the body an attempt sends.
	CapabilityRequestWriteRaw = "request.write.raw"
	// CapabilityRequestGate lets onRequest refuse a request.
	CapabilityRequestGate = "request.gate"
	// CapabilityRouting lets route reorder and exclude the providers a
	// request may go to, or change its model.
	CapabilityRouting = "routing"
	// CapabilityResponseRead gives onResponse the response once it ended.
	CapabilityResponseRead = "response.read"
	// CapabilityResponseWrite lets rewriteResponse change a whole response
	// and onStreamEvent a stream's events.
	CapabilityResponseWrite = "response.write"
	// CapabilityEvents gives onEvent what happened in the gateway, without
	// any request or response content.
	CapabilityEvents = "events"
	// CapabilitySchedule runs onSchedule at the intervals of
	// meta.schedules.
	CapabilitySchedule = "schedule"
	// CapabilityHTTP serves /plugins/<key>/… on the inference listener
	// through onHttp.
	CapabilityHTTP = "http.serve"
	// CapabilityProvider offers the providers of meta.providers.
	CapabilityProvider = "provider"
	// CapabilityUICode loads the plugin's interface code into the desktop,
	// where it can do whatever the desktop can.
	CapabilityUICode = "ui.code"
)

var knownCapabilities = []string{
	CapabilityInject,
	CapabilityRequest,
	CapabilityNetwork,
	CapabilityStorage,
	CapabilityViews,
	CapabilityTools,
	CapabilityRequestWrite,
	CapabilityRequestWriteRaw,
	CapabilityRequestGate,
	CapabilityRouting,
	CapabilityResponseRead,
	CapabilityResponseWrite,
	CapabilityEvents,
	CapabilitySchedule,
	CapabilityHTTP,
	CapabilityProvider,
	CapabilityUICode,
}

// versionOneCapabilities are those an apiVersion 1 plugin may ask for.
var versionOneCapabilities = knownCapabilities[:6]

// HighRiskCapabilities are confirmed on their own, in red: interface code
// can do whatever the desktop can.
var HighRiskCapabilities = []string{CapabilityUICode}

// Hooks a plugin may export. Each is optional; whether a plugin exports one
// is settled once, when it loads.
const (
	HookPrepareSession = "prepareSession"
	HookPrepareTurn    = "prepareTurn"
	HookObserveTurn    = "observeTurn"
	HookDescribeStatus = "describeStatus"
	HookTestConnection = "testConnection"
	// HookOnAction handles a click on a button of one of the plugin's
	// records, outside any request.
	HookOnAction = "onAction"
	// HookCallTool runs a query of meta.tools the model sent to the lookup
	// outlet, outside any request.
	HookCallTool = "callTool"
)

// The hooks of apiVersion 2's extension points.
const (
	// HookOnRequest lets a request in, tags it, or refuses it, before
	// routing.
	HookOnRequest = "onRequest"
	// HookRoute reorders and excludes the request's candidate providers,
	// or changes its model.
	HookRoute = "route"
	// HookBeforeForward changes the request once, before every attempt.
	HookBeforeForward = "beforeForward"
	// HookBeforeUpstream changes what one attempt sends to its provider,
	// in the provider's protocol.
	HookBeforeUpstream = "beforeUpstream"
	// HookRewriteResponse changes a whole response before the client gets
	// it.
	HookRewriteResponse = "rewriteResponse"
	// HookOnStreamEvent changes one event of a streamed response, and
	// HookOnStreamEnd adds the last events once the stream ended.
	HookOnStreamEvent = "onStreamEvent"
	HookOnStreamEnd   = "onStreamEnd"
	// HookOnResponse sees the response once it ended, outside the request.
	HookOnResponse = "onResponse"
	// HookOnEvent sees what happened in the gateway.
	HookOnEvent = "onEvent"
	// HookOnSchedule runs at the intervals of meta.schedules.
	HookOnSchedule = "onSchedule"
	// HookOnHTTP answers /plugins/<key>/… on the inference listener.
	HookOnHTTP = "onHttp"
	// HookOnUICall answers the calls of the plugin's interface code.
	HookOnUICall = "onUiCall"
)

var knownHooks = []string{
	HookPrepareSession,
	HookPrepareTurn,
	HookObserveTurn,
	HookDescribeStatus,
	HookTestConnection,
	HookOnAction,
	HookCallTool,
	HookOnRequest,
	HookRoute,
	HookBeforeForward,
	HookBeforeUpstream,
	HookRewriteResponse,
	HookOnStreamEvent,
	HookOnStreamEnd,
	HookOnResponse,
	HookOnEvent,
	HookOnSchedule,
	HookOnHTTP,
	HookOnUICall,
	HookBuildRequest,
	HookParseResponse,
	HookParseStreamEvent,
	HookFinishStream,
	HookParseError,
}

// versionOneHooks are those an apiVersion 1 plugin may export.
var versionOneHooks = knownHooks[:7]

// Limits bound one hook call.
type Limits struct {
	// ScriptTime is the JavaScript time a call may take. Waiting on the host
	// (http.fetch, the store) does not count.
	ScriptTime time.Duration
	// Memory is what a runtime's JavaScript may keep: what it retains, as
	// the runtime last measured it, plus what it allocated since. Garbage
	// it drops does not count once the runtime measures.
	Memory int64
	// ResultBytes and ResultNodes bound a hook's result as JSON text: its
	// length, and the number of values in it.
	ResultBytes int
	ResultNodes int
	// Concurrency is how many calls of one plugin may run JavaScript at the
	// same time. A call waiting on the host gives up its place meanwhile.
	Concurrency int
	// DataBytes is what a plugin's own data directory may hold.
	DataBytes int64
}

// DefaultLimits are the limits of the design: 200 ms of JavaScript, 32 MiB,
// and a result of at most 64 KiB and 4096 values.
func DefaultLimits() Limits {
	return Limits{
		ScriptTime:  200 * time.Millisecond,
		Memory:      32 << 20,
		ResultBytes: 64 << 10,
		ResultNodes: 4096,
		Concurrency: runtime.GOMAXPROCS(0),
		DataBytes:   DefaultDataBytes,
	}
}

func (limits Limits) withDefaults() Limits {
	defaults := DefaultLimits()
	if limits.ScriptTime <= 0 {
		limits.ScriptTime = defaults.ScriptTime
	}
	if limits.Memory <= 0 {
		limits.Memory = defaults.Memory
	}
	if limits.ResultBytes <= 0 {
		limits.ResultBytes = defaults.ResultBytes
	}
	if limits.ResultNodes <= 0 {
		limits.ResultNodes = defaults.ResultNodes
	}
	if limits.Concurrency <= 0 {
		limits.Concurrency = defaults.Concurrency
	}
	if limits.DataBytes <= 0 {
		limits.DataBytes = defaults.DataBytes
	}
	return limits
}

// Reasons a hook call failed. Request records and the plugin page show them;
// the request itself goes on without the plugin's result.
const (
	ReasonTimeout       = "timeout"
	ReasonMemory        = "memory_limit"
	ReasonPluginError   = "plugin_error"
	ReasonInvalidResult = "invalid_result"
	ReasonCanceled      = "canceled"
	ReasonUnavailable   = "unavailable"
)

// CallError is a hook call that produced no result.
type CallError struct {
	Reason string
	// Message is safe to show: control characters removed and at most
	// maxMessageRunes long.
	Message string
	// Thrown is the message of what the plugin threw, cleaned as Message,
	// without its name or position: what a task plugin tells the client.
	Thrown string
	Err    error
}

func (e *CallError) Error() string { return e.Reason + ": " + e.Message }

func (e *CallError) Unwrap() error { return e.Err }

// errScriptTime interrupts a call that used up Limits.ScriptTime.
var errScriptTime = errors.New("plugin script time limit exceeded")

// maxMessageRunes bounds the messages a plugin can put in front of the user
// through an error.
const maxMessageRunes = 512

// cleanText removes control characters other than those keep allows and cuts
// the text to max runes. Text from a plugin passes through it before the
// host shows or logs it.
func cleanText(text string, max int, keep string) string {
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�")
	}
	var builder strings.Builder
	count := 0
	for _, r := range text {
		if unicode.IsControl(r) && !strings.ContainsRune(keep, r) {
			continue
		}
		if count == max {
			builder.WriteString("…")
			break
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}
