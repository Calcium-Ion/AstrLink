package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// The plugin confirmation window (plugin platform v2, §2.4). A change that
// grants a plugin more takes effect only from this small window: enabling
// it, capabilities it did not have, addresses it could not reach, plain
// HTTP, the privacy check skipped, refusing requests it cannot decide, and
// new code: an install from source, an update, a rollback. The main
// window's change stops at Core with approval_required; the host keeps it
// here and opens the window without taking focus; only a decision made in
// that window sends it again, confirmed. Plugin interface code runs in the
// main window, which can neither decide there nor click for the user.

// PluginApprovalLabel is the confirmation window's label
// (PLUGIN_APPROVAL_LABEL in the frontend).
const PluginApprovalLabel = "plugin-approval"

// PluginApprovalPendingError is what the main window gets for a change that
// waits in the confirmation window.
const PluginApprovalPendingError = `{"error":{"code":"approval_pending","message":"the change waits for the user's confirmation in the plugin confirmation window"}}`

// PluginApprovalWindow is the confirmation window (package ui).
type PluginApprovalWindow interface {
	// Open shows the window for the change waiting, or tells an open one
	// that the change was replaced. One opened for a new change does not
	// take focus; one the user asks for does.
	Open(focus bool) error
	// Decided tells every window that the change was decided and closes
	// the confirmation window.
	Decided(finished PluginApprovalFinished)
}

// PendingPluginApproval is a change waiting for the user: the plugin, what
// Core said it grants, and the change to send again once confirmed.
type PendingPluginApproval struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	// Approval is Core's description of what the change grants.
	Approval json.RawMessage `json:"approval"`
	change   pendingPluginChange
}

// pendingPluginChange is what a confirmed change sends again: the plugin's
// settings when action is empty, or a source_install or source_rollback
// with its input.
type pendingPluginChange struct {
	action string
	input  json.RawMessage
}

// PluginApprovalFinished is what every window hears once the user decided.
type PluginApprovalFinished struct {
	ID       string  `json:"id"`
	Key      string  `json:"key"`
	Approved bool    `json:"approved"`
	Error    *string `json:"error,omitempty"`
}

// PluginApprovals holds the one change waiting for confirmation; a newer
// one replaces it.
type PluginApprovals struct {
	mu      sync.Mutex
	pending *PendingPluginApproval
}

func (a *PluginApprovals) hold(key string, approval json.RawMessage, change pendingPluginChange) string {
	id := newApprovalID()
	a.mu.Lock()
	a.pending = &PendingPluginApproval{ID: id, Key: key, Approval: approval, change: change}
	a.mu.Unlock()
	return id
}

// Current is the change waiting, nil for none.
func (a *PluginApprovals) Current() *PendingPluginApproval {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		return nil
	}
	held := *a.pending
	return &held
}

// take hands over the change id names, so it is decided once.
func (a *PluginApprovals) take(id string) (PendingPluginApproval, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil || a.pending.ID != id {
		return PendingPluginApproval{}, false
	}
	held := *a.pending
	a.pending = nil
	return held, true
}

func newApprovalID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "approval-" + hex.EncodeToString(b[:])
}

// narrowGrants keeps, of the capabilities the change adds, only those the
// user left ticked: granted_capabilities loses the others.
func narrowGrants(input json.RawMessage, added, kept []string) json.RawMessage {
	value, err := decodeValue(input)
	if err != nil {
		return input
	}
	settings, ok := asObject(value)
	if !ok {
		return input
	}
	grants, ok := asArray(settings["granted_capabilities"])
	if !ok {
		return input
	}
	contains := func(list []string, name string) bool {
		for _, item := range list {
			if item == name {
				return true
			}
		}
		return false
	}
	narrowed := []any{}
	for _, grant := range grants {
		name, ok := asString(grant)
		if ok && (!contains(added, name) || contains(kept, name)) {
			narrowed = append(narrowed, grant)
		}
	}
	settings["granted_capabilities"] = narrowed
	return encodeValue(settings)
}

// addedCapabilities lists approval's added_capabilities.
func addedCapabilities(approval json.RawMessage) []string {
	var parsed struct {
		Added []any `json:"added_capabilities"`
	}
	_ = json.Unmarshal(approval, &parsed)
	names := []string{}
	for _, item := range parsed.Added {
		if name, ok := item.(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// ---- Plugins ----

// Plugins is the plugin page: list, rescan, settings, credentials, tests,
// status, installs from source, the marketplace, and the plugins' part in
// a session; and the confirmation window's change.
type Plugins struct {
	*API
	Approvals *PluginApprovals
	// Owner names the window a call came from.
	Owner func(ctx context.Context) string
	// Window is the confirmation window.
	Window PluginApprovalWindow
}

// pluginOutcome is what a change of a plugin came to: Core's answer, or,
// for a change that grants the plugin more and was not confirmed, what it
// grants.
type pluginOutcome struct {
	saved    json.RawMessage
	approval json.RawMessage
}

// PluginAction runs one plugin page action. A change of settings that
// grants the plugin more, and an install or rollback from source, wait
// for the user in the confirmation window; the rest is sent at once.
func (p Plugins) PluginAction(ctx context.Context, action, key, name string, input json.RawMessage) (json.RawMessage, error) {
	if action == "update" {
		if key == "" {
			return nil, errors.New("invalid plugin")
		}
		if !present(input) {
			return nil, errors.New("plugin settings are required")
		}
		outcome, err := p.updatePlugin(ctx, key, input, false)
		if err != nil {
			return nil, err
		}
		if outcome.approval == nil {
			return outcome.saved, nil
		}
		return nil, p.hold(key, outcome.approval, pendingPluginChange{input: input})
	}
	if strings.HasPrefix(action, "source_") {
		outcome, err := p.pluginChange(ctx, action, key, input, false)
		if err != nil {
			return nil, err
		}
		if outcome.approval == nil {
			return outcome.saved, nil
		}
		var named struct {
			Plugin string `json:"plugin"`
		}
		_ = json.Unmarshal(outcome.approval, &named)
		if named.Plugin != "" {
			key = named.Plugin
		}
		if key == "" {
			return nil, errors.New("invalid plugin")
		}
		return nil, p.hold(key, outcome.approval, pendingPluginChange{action: action, input: input})
	}
	value, err := optional(input)
	if err != nil {
		return nil, err
	}
	req, err := pluginRequest(action, key, name, value, present(input))
	if err != nil {
		return nil, err
	}
	return p.value(ctx, req.Method, req.Path, req.Body, "plugins")
}

// hold keeps a change for the confirmation window and opens it; the main
// window hears that the change waits there.
func (p Plugins) hold(key string, approval json.RawMessage, change pendingPluginChange) error {
	p.Approvals.hold(key, approval, change)
	if err := p.Window.Open(false); err != nil {
		return err
	}
	return errors.New(PluginApprovalPendingError)
}

// PluginApprovalPending is the change waiting in the confirmation window,
// for that window alone.
func (p Plugins) PluginApprovalPending(ctx context.Context) (*PendingPluginApproval, error) {
	if p.Owner(ctx) != PluginApprovalLabel {
		return nil, errors.New("only the plugin confirmation window reads the change it confirms")
	}
	return p.Approvals.Current(), nil
}

// PluginApprovalDecide decides the change waiting for confirmation. Only
// the confirmation window may: what runs in the main window cannot confirm
// for the user. kept are the added capabilities the user left ticked; nil
// keeps them all.
func (p Plugins) PluginApprovalDecide(ctx context.Context, id string, approve bool, kept *[]string) error {
	if p.Owner(ctx) != PluginApprovalLabel {
		return errors.New("only the plugin confirmation window confirms a plugin change")
	}
	pending, ok := p.Approvals.take(id)
	if !ok {
		return errors.New("the change was already decided or replaced")
	}
	finished := PluginApprovalFinished{ID: pending.ID, Key: pending.Key, Approved: approve}
	if approve {
		var outcome pluginOutcome
		var err error
		if pending.change.action == "" {
			input := pending.change.input
			if kept != nil {
				input = narrowGrants(input, addedCapabilities(pending.Approval), *kept)
			}
			outcome, err = p.updatePlugin(ctx, pending.Key, input, true)
		} else {
			outcome, err = p.pluginChange(ctx, pending.change.action, pending.Key, pending.change.input, true)
		}
		switch {
		case err != nil:
			message := err.Error()
			finished.Error = &message
		case outcome.approval != nil:
			message := "Core asked for the confirmation again"
			finished.Error = &message
		}
	}
	p.Window.Decided(finished)
	return nil
}

// OpenPluginApproval brings the confirmation window back, for a change
// still waiting; false when none waits.
func (p Plugins) OpenPluginApproval() (bool, error) {
	if p.Approvals.Current() == nil {
		return false, nil
	}
	if err := p.Window.Open(true); err != nil {
		return false, err
	}
	return true, nil
}

// SessionPlugins reads the plugins' part in a session, pauses or resumes a
// plugin there, or makes its next request load the plugin's block again.
func (p Plugins) SessionPlugins(ctx context.Context, sessionID, key string, input json.RawMessage, refresh bool) (json.RawMessage, error) {
	if err := ValidateResourceID(sessionID); err != nil {
		return nil, err
	}
	req, err := sessionPluginsRequest(sessionID, key, input, refresh)
	if err != nil {
		return nil, err
	}
	return p.value(ctx, req.Method, req.Path, req.Body, "session plugins")
}

// updatePlugin saves a plugin's settings. A change that grants the plugin
// more and is not approved comes back with what it grants.
func (p Plugins) updatePlugin(ctx context.Context, key string, input json.RawMessage, approved bool) (pluginOutcome, error) {
	if !pluginKeyOK(key) {
		return pluginOutcome{}, errors.New("invalid plugin")
	}
	req := Request{Method: http.MethodPatch, Path: pluginsPath + "/" + key, Body: input, PluginApproved: approved}
	response, err := p.Caller.Call(ctx, req)
	if err != nil {
		return pluginOutcome{}, err
	}
	if approval := approvalRequired(response); approval != nil {
		return pluginOutcome{approval: approval}, nil
	}
	if !response.OK() {
		return pluginOutcome{}, StatusError(req.Method, req.Path, response.Status, response.Body)
	}
	saved, err := checkedJSON(response.Body, "plugins")
	return pluginOutcome{saved: saved}, err
}

// pluginChange runs an install from source: the review of a GitHub link,
// the install it stages, the update check, the rollback and the uninstall.
// An install or a rollback that is not approved comes back with what it
// grants, as a change of settings does. Why an install cannot go on comes
// back whole, as Core's JSON, for the page to explain.
func (p Plugins) pluginChange(ctx context.Context, action, key string, input json.RawMessage, approved bool) (pluginOutcome, error) {
	value, err := optional(input)
	if err != nil {
		return pluginOutcome{}, err
	}
	req, err := pluginRequest(action, key, "", value, present(input))
	if err != nil {
		return pluginOutcome{}, err
	}
	req.PluginApproved = approved
	response, err := p.Caller.Call(ctx, req)
	if err != nil {
		return pluginOutcome{}, err
	}
	if response.OK() {
		saved, err := checkedJSON(response.Body, "plugins")
		return pluginOutcome{saved: saved}, err
	}
	if approval := approvalRequired(response); approval != nil {
		return pluginOutcome{approval: approval}, nil
	}
	var refused map[string]json.RawMessage
	if json.Unmarshal(response.Body, &refused) == nil {
		if failure, ok := refused["failure"]; ok {
			reason := refused["error"]
			if reason == nil {
				reason = json.RawMessage("null")
			}
			text, _ := json.Marshal(map[string]json.RawMessage{"error": reason, "failure": failure})
			return pluginOutcome{}, errors.New(string(text))
		}
	}
	return pluginOutcome{}, StatusError(req.Method, req.Path, response.Status, response.Body)
}

// approvalRequired is what a change grants when Core refused it for want
// of the user's confirmation, nil otherwise.
func approvalRequired(response Response) json.RawMessage {
	if response.Status != http.StatusConflict {
		return nil
	}
	var refused struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Approval json.RawMessage `json:"approval"`
	}
	if json.Unmarshal(response.Body, &refused) != nil || refused.Error.Code != "approval_required" {
		return nil
	}
	if refused.Approval == nil {
		return json.RawMessage("null")
	}
	return refused.Approval
}

// ---- Metadata ----

// Metadata is the request attributes, an access token's default metadata
// and a session's bindings.
type Metadata struct{ *API }

// MetadataAction lists, creates, updates or deletes a request attribute,
// or reads the values one took.
func (m Metadata) MetadataAction(ctx context.Context, action, key string, input json.RawMessage) (json.RawMessage, error) {
	value, err := optional(input)
	if err != nil {
		return nil, err
	}
	req, err := metadataRequest(action, key, value, present(input))
	if err != nil {
		return nil, err
	}
	response, err := m.send(ctx, req.Method, req.Path, req.Body, "")
	if err != nil {
		return nil, err
	}
	if len(response.Body) == 0 {
		return json.RawMessage("null"), nil
	}
	return checkedJSON(response.Body, "metadata")
}

// AccessTokenMetadata reads an access token's default metadata, or
// replaces it with input.
func (m Metadata) AccessTokenMetadata(ctx context.Context, tokenID string, input json.RawMessage) (json.RawMessage, error) {
	if err := ValidateResourceID(tokenID); err != nil {
		return nil, err
	}
	path := "/control/v1/access-tokens/" + tokenID + "/metadata"
	if present(input) {
		return m.value(ctx, http.MethodPut, path, input, "token metadata")
	}
	return m.value(ctx, http.MethodGet, path, nil, "token metadata")
}

// SessionMetadata reads a session's bindings, sets one by hand, or unbinds
// one, which Core stores as none.
func (m Metadata) SessionMetadata(ctx context.Context, sessionID, key string, input json.RawMessage, unbind bool) (json.RawMessage, error) {
	if err := ValidateResourceID(sessionID); err != nil {
		return nil, err
	}
	req, err := sessionMetadataRequest(sessionID, key, input, unbind)
	if err != nil {
		return nil, err
	}
	return m.value(ctx, req.Method, req.Path, req.Body, "session metadata")
}

// ---- Request mappings ----

const pluginsPath = "/control/v1/plugins"

// pluginKeyOK reports whether value can name a plugin, as Core's
// ValidPluginKey: a lowercase letter or digit, then up to 63 lowercase
// letters, digits, hyphens and underscores, as New API's community keys
// need.
func pluginKeyOK(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '-' || c == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

// pluginNameOK accepts a lowercase letter, then up to 31 lowercase letters,
// digits or extra.
func pluginNameOK(value string, extra byte) bool {
	if value == "" || len(value) > 32 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for i := 1; i < len(value); i++ {
		c := value[i]
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != extra {
			return false
		}
	}
	return true
}

// pluginRequest translates a plugin page action into the control request it
// makes. It is kept apart from the request so the mapping is testable
// without a running Core.
func pluginRequest(action, key, name string, input any, hasInput bool) (Request, error) {
	body := func(missing string) (json.RawMessage, error) {
		if !hasInput {
			return nil, errors.New(missing)
		}
		return encodeValue(input), nil
	}
	with := func(method, path, missing string) (Request, error) {
		b, err := body(missing)
		if err != nil {
			return Request{}, err
		}
		return Request{Method: method, Path: path, Body: b}, nil
	}
	settings, _ := asObject(input)
	text := func(name string) string {
		value, _ := stringField(settings, name)
		return value
	}
	switch action {
	case "list":
		return Request{Method: http.MethodGet, Path: pluginsPath}, nil
	case "reload":
		return Request{Method: http.MethodPost, Path: pluginsPath + "/reload"}, nil
	// The settings of every plugin at once: the pause, the injection budget
	// and the interface language.
	case "host":
		return Request{Method: http.MethodGet, Path: "/control/v1/plugin-host"}, nil
	case "update_host":
		return with(http.MethodPatch, "/control/v1/plugin-host", "plugin host settings are required")
	// A GitHub link fetched, built and reviewed; the install of what the
	// review staged.
	case "source_preview":
		return with(http.MethodPost, pluginsPath+"/sources/preview", "the link is required")
	case "source_install":
		return with(http.MethodPost, pluginsPath+"/sources/install", "the review is required")
	// What a plugin marketplace offers, the official one unless the input
	// names another index; with a code, the one plugin of that code.
	case "market":
		if code := text("code"); code != "" {
			if len(code) != 8 || strings.Trim(code, "0123456789") != "" {
				return Request{}, errors.New("invalid plugin code")
			}
			return Request{Method: http.MethodGet, Path: pluginsPath + "/market?code=" + code}, nil
		}
		if index := text("index"); index != "" {
			return Request{Method: http.MethodGet, Path: pluginsPath + "/market?index=" + PercentEncodeQuery(index)}, nil
		}
		return Request{Method: http.MethodGet, Path: pluginsPath + "/market"}, nil
	// A marketplace version's usage guide or release notes.
	case "market_doc":
		if !hasInput {
			return Request{}, errors.New("the plugin is required")
		}
		key, doc := text("key"), text("doc")
		if !pluginKeyOK(key) || (doc != "readme" && doc != "changelog") {
			return Request{}, errors.New("invalid plugin document")
		}
		path := pluginsPath + "/market/doc?key=" + key + "&doc=" + doc
		for _, name := range []string{"index", "version"} {
			if value := text(name); value != "" {
				path += "&" + name + "=" + PercentEncodeQuery(value)
			}
		}
		return Request{Method: http.MethodGet, Path: path}, nil
	}
	if !pluginKeyOK(key) {
		return Request{}, errors.New("invalid plugin")
	}
	plugin := pluginsPath + "/" + key
	switch action {
	case "get":
		return Request{Method: http.MethodGet, Path: plugin}, nil
	case "update":
		return with(http.MethodPatch, plugin, "plugin settings are required")
	case "test":
		return Request{Method: http.MethodPost, Path: plugin + "/test"}, nil
	case "status":
		return Request{Method: http.MethodGet, Path: plugin + "/status"}, nil
	// The GitHub stars of the projects the plugin credits.
	case "credits":
		return Request{Method: http.MethodGet, Path: plugin + "/credits"}, nil
	// The development tab: the log lines after a sequence number, and a
	// hook run by hand without the network.
	case "journal":
		since, _ := asU64(settings["since"])
		return Request{Method: http.MethodGet, Path: plugin + "/journal?since=" + strconv.FormatUint(since, 10)}, nil
	case "dry_run":
		return with(http.MethodPost, plugin+"/dry-run", "the dry run is required")
	// The login of a provider of the plugin: start, state, submit, cancel,
	// or the status of the credential it keeps.
	case "login":
		return with(http.MethodPost, plugin+"/login", "the login is required")
	// The balance of a provider of the plugin, and whether it rests.
	case "provider_state":
		return with(http.MethodPost, plugin+"/provider-state", "the provider is required")
	// A request processing instance run over a request, not sent.
	case "request_dry_run":
		return with(http.MethodPost, plugin+"/request-dry-run", "the dry run is required")
	// The plugin's account over its last 1 to 30 days.
	case "stats":
		days, ok := asU64(settings["days"])
		if !ok || days < 1 || days > 30 {
			return Request{}, errors.New("the days of the statistics are 1 to 30")
		}
		return Request{Method: http.MethodGet, Path: plugin + "/stats?days=" + strconv.FormatUint(days, 10)}, nil
	case "action":
		return with(http.MethodPost, plugin+"/actions", "the action is required")
	case "source_check":
		return Request{Method: http.MethodPost, Path: plugin + "/source/check"}, nil
	// A call of the plugin's interface code to its backend part.
	case "ui_call":
		return with(http.MethodPost, plugin+"/ui-call", "the call is required")
	case "source_rollback":
		return with(http.MethodPost, plugin+"/source/rollback", "the version is required")
	case "source_uninstall":
		return Request{Method: http.MethodDelete, Path: plugin + "/source"}, nil
	case "clear_workspaces":
		return Request{Method: http.MethodDelete, Path: plugin + "/workspaces"}, nil
	case "clear_values":
		return Request{Method: http.MethodDelete, Path: plugin + "/values"}, nil
	case "clear_files":
		return Request{Method: http.MethodDelete, Path: plugin + "/files"}, nil
	// A task plugin's tasks, the newest first, a page after a cursor; and
	// clearing those that finished.
	case "tasks":
		if cursor := text("cursor"); cursor != "" {
			return Request{Method: http.MethodGet, Path: plugin + "/tasks?cursor=" + PercentEncodeQuery(cursor)}, nil
		}
		return Request{Method: http.MethodGet, Path: plugin + "/tasks"}, nil
	case "clear_tasks":
		return Request{Method: http.MethodDelete, Path: plugin + "/tasks"}, nil
	// Voids the credentials the model runs the plugin's lookups with.
	case "revoke_lookups":
		return Request{Method: http.MethodDelete, Path: plugin + "/lookup-credentials"}, nil
	case "save_credential", "delete_credential":
		if !pluginNameOK(name, '_') {
			return Request{}, errors.New("invalid plugin credential")
		}
		path := plugin + "/credentials/" + name
		if action == "save_credential" {
			return with(http.MethodPut, path, "the credential is required")
		}
		return Request{Method: http.MethodDelete, Path: path}, nil
	}
	return Request{}, errors.New("unsupported plugin operation")
}

// metadataRequest translates a metadata settings action into the control
// request it makes.
func metadataRequest(action, key string, input any, hasInput bool) (Request, error) {
	const base = "/control/v1/metadata/attributes"
	required := func(method, path string) (Request, error) {
		if !hasInput {
			return Request{}, errors.New("the attribute is required")
		}
		return Request{Method: method, Path: path, Body: encodeValue(input)}, nil
	}
	switch action {
	case "list":
		return Request{Method: http.MethodGet, Path: base}, nil
	case "create":
		return required(http.MethodPost, base)
	}
	if !pluginNameOK(key, '_') {
		return Request{}, errors.New("invalid metadata attribute")
	}
	switch action {
	case "update":
		return required(http.MethodPatch, base+"/"+key)
	case "delete":
		return Request{Method: http.MethodDelete, Path: base + "/" + key}, nil
	case "values":
		return Request{Method: http.MethodGet, Path: base + "/" + key + "/values"}, nil
	}
	return Request{}, errors.New("unsupported metadata operation")
}

// sessionMetadataRequest translates a session metadata operation into the
// control request it makes.
func sessionMetadataRequest(sessionID, key string, input json.RawMessage, unbind bool) (Request, error) {
	base := "/control/v1/request-sessions/" + sessionID + "/metadata"
	if key == "" {
		return Request{Method: http.MethodGet, Path: base}, nil
	}
	if !pluginNameOK(key, '_') {
		return Request{}, errors.New("invalid metadata attribute")
	}
	if unbind {
		return Request{Method: http.MethodDelete, Path: base + "/" + key}, nil
	}
	if !present(input) {
		return Request{}, errors.New("the value is required")
	}
	return Request{Method: http.MethodPut, Path: base + "/" + key, Body: input}, nil
}

// sessionPluginsRequest translates an operation on the plugins' part in a
// session into the control request it makes: read them all, pause or
// resume one, or refresh one.
func sessionPluginsRequest(sessionID, key string, input json.RawMessage, refresh bool) (Request, error) {
	base := "/control/v1/request-sessions/" + sessionID + "/plugins"
	if key == "" {
		return Request{Method: http.MethodGet, Path: base}, nil
	}
	if !pluginKeyOK(key) {
		return Request{}, errors.New("invalid plugin")
	}
	if refresh {
		return Request{Method: http.MethodPost, Path: base + "/" + key + "/refresh"}, nil
	}
	if !present(input) {
		return Request{}, errors.New("the pause is required")
	}
	return Request{Method: http.MethodPut, Path: base + "/" + key, Body: input}, nil
}

// ---- Plugin interface code ----

// PluginScheme serves a plugin's interface code to the main window
// (PLUGIN_SCHEME in the frontend): astrlink-plugin://localhost/<key>/client.js,
// or http://astrlink-plugin.localhost/<key>/client.js on Windows.
const PluginScheme = "astrlink-plugin"

// pluginClientFile is the key and file a request names: the code and its
// source map, nothing else.
func pluginClientFile(path string) (key, file string, ok bool) {
	key, file, ok = strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || (file != "client.js" && file != "client.js.map") {
		return "", "", false
	}
	// Any key Core accepts, as new-api plugin keys with underscores.
	if !pluginKeyOK(key) {
		return "", "", false
	}
	return key, file, true
}

// PluginClientHandler serves the plugin interface protocol (plugin platform
// v2, §6). It reads a plugin's code from Core with the control token: Core
// serves it only while the plugin is enabled with ui.code granted and its
// interface is on. Only the main window may load from it; owner names the
// window a request came from.
func PluginClientHandler(caller Caller, owner func(ctx context.Context) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if owner(r.Context()) != "main" || r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		key, file, ok := pluginClientFile(r.URL.Path)
		if !ok {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		response, err := caller.Call(r.Context(), Request{Method: http.MethodGet, Path: pluginsPath + "/" + key + "/" + file})
		switch {
		case err != nil:
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
		case !response.OK():
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(response.Status)
		default:
			kind := "application/json"
			if file == "client.js" {
				kind = "text/javascript; charset=utf-8"
			}
			w.Header().Set("Content-Type", kind)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(response.Body)
		}
	})
}
