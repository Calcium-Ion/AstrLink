package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// actionTimeout bounds onAction: its run and its network wait.
const actionTimeout = 10 * time.Second

// maxActionMessageRunes bounds the message a plugin answers a click with.
const maxActionMessageRunes = 256

// Settings returns the settings of every plugin, as the requests that start
// now read them.
func (host *Host) Settings() contract.PluginHostSettings {
	return *host.settings.Load()
}

// LoadSettings reads the settings of every plugin from storage. Until it
// succeeds, requests read the defaults.
func (host *Host) LoadSettings(ctx context.Context) error {
	if host.store == nil {
		return nil
	}
	settings, err := host.store.PluginHostSettings(ctx)
	if err != nil {
		return err
	}
	host.settings.Store(&settings)
	return nil
}

// UpdateSettings changes the settings of every plugin in storage and then
// for the requests that start from now on. A session keeps the language it
// started with.
func (host *Host) UpdateSettings(ctx context.Context, update contract.PluginHostSettingsUpdate) (contract.PluginHostSettings, error) {
	settings := host.Settings()
	if update.Paused != nil {
		settings.Paused = *update.Paused
	}
	if update.InjectionBudgetBytes != nil {
		settings.InjectionBudgetBytes = *update.InjectionBudgetBytes
	}
	if update.Language != nil {
		settings.Language = *update.Language
	}
	if update.Order != nil {
		settings.Order = slices.Clone(*update.Order)
	}
	if update.WebUI != nil {
		settings.WebUI = *update.WebUI
	}
	if update.NpmRegistry != nil {
		settings.NpmRegistry = strings.TrimRight(*update.NpmRegistry, "/")
	}
	if update.RequestPlugins != nil {
		settings.RequestPlugins = slices.Clone(*update.RequestPlugins)
	}
	if err := settings.Validate(); err != nil {
		return host.Settings(), errors.Join(storage.ErrInvalidArgument, err)
	}
	if host.store != nil {
		if err := host.store.PutPluginHostSettings(ctx, settings); err != nil {
			return host.Settings(), err
		}
	}
	host.settings.Store(&settings)
	return settings, nil
}

// SessionPlugins returns the part each enabled plugin that injects or offers
// lookups has in session: whether the user paused it there, the turn it
// started in, and the resident block and usage the session carries, which
// live in memory only.
func (host *Host) SessionPlugins(ctx context.Context, session string) ([]contract.SessionPlugin, error) {
	var plugins []*plugin.Plugin
	for _, candidate := range host.plugins.Current().Plugins() {
		loads := candidate.Has(plugin.HookPrepareSession) && candidate.Can(plugin.CapabilityInject)
		looks := candidate.Has(plugin.HookCallTool) && candidate.Can(plugin.CapabilityTools) && len(candidate.Meta.Tools) > 0
		if candidate.Enabled() && (loads || looks) {
			plugins = append(plugins, candidate)
		}
	}
	parts := make([]contract.SessionPlugin, 0, len(plugins))
	for _, candidate := range plugins {
		part := contract.SessionPlugin{Plugin: candidate.Key, Name: candidate.Meta.Name}
		if host.store != nil {
			record, found, err := host.store.PluginSession(ctx, candidate.Key, session)
			if err != nil {
				return nil, err
			}
			if found {
				part.Paused, part.FirstTurn = record.Paused, record.FirstTurn
			}
		}
		host.sessions.mutex.Lock()
		if state, found := host.sessions.lookup(session); found {
			if entry, found := state.plugins[candidate.Key]; found {
				if entry.computed && entry.resident != "" {
					part.Block = &contract.SessionPluginBlock{
						Text: entry.resident, Bytes: len(entry.resident), Trigger: entry.record.trigger,
						Language: entry.record.language, ComputedAt: entry.record.at.UTC(),
					}
				}
				part.RefreshPending = entry.refresh != ""
				part.Lookup = maskLookup(entry.usage)
			}
		}
		host.sessions.mutex.Unlock()
		parts = append(parts, part)
	}
	return parts, nil
}

// PauseSession pauses or resumes a plugin in a session. A paused plugin
// injects nothing there, binds nothing and captures nothing; what it put in
// earlier requests stays where the client keeps it. turn is where a session
// the plugin never took part in starts for it.
func (host *Host) PauseSession(ctx context.Context, key, session string, paused bool, turn int) error {
	if host.store == nil {
		return errors.New("the plugin host keeps no sessions")
	}
	record, err := host.store.PausePluginSession(ctx, key, session, paused, turn)
	if err != nil {
		return err
	}
	host.sessions.mutex.Lock()
	state := host.sessions.plugin(session, key)
	state.loaded, state.paused, state.firstTurn = true, record.Paused, record.FirstTurn
	host.sessions.mutex.Unlock()
	// A paused session carries no lookups: what was issued stops now, and
	// resuming issues a new credential.
	if record.Paused {
		host.lookups.revoke(lookupKey{session, key})
	}
	return nil
}

// Refresh makes the session's next request compute the plugin's resident
// block again. A session the host does not keep computes it anyway.
func (host *Host) Refresh(key, session string) {
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	state, found := host.sessions.lookup(session)
	if !found {
		return
	}
	if entry, found := state.plugins[key]; found && entry.computed {
		entry.refresh = contract.PluginTriggerManual
	}
}

// SessionChoices returns the values plugins offered the session, by
// attribute key.
func (host *Host) SessionChoices(session string) map[string][]string {
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	state, found := host.sessions.lookup(session)
	if !found {
		return nil
	}
	var choices map[string][]string
	for _, key := range sortedKeys(state.plugins) {
		for attribute, values := range state.plugins[key].choices {
			if choices == nil {
				choices = map[string][]string{}
			}
			for _, value := range values {
				if !slices.Contains(choices[attribute], value) {
					choices[attribute] = append(choices[attribute], value)
				}
			}
		}
	}
	return choices
}

// SessionWorkspace returns the last element of the workspace the session's
// last request named; ok is false when the host knows none.
func (host *Host) SessionWorkspace(session string) (name string, ok bool) {
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	state, found := host.sessions.lookup(session)
	if !found || !state.workspace.known() {
		return "", false
	}
	return state.workspace.name, true
}

// WorkspaceValue returns the value of key the session's workspace is bound
// to; ok is false when the host knows no workspace for the session or the
// workspace has no value.
func (host *Host) WorkspaceValue(ctx context.Context, session, key string) (value string, ok bool, err error) {
	place := host.sessionWorkspace(session)
	if !place.known() || host.bindings == nil {
		return "", false, nil
	}
	values, err := host.bindings.WorkspaceValues(ctx, place.fingerprint)
	if err != nil {
		return "", false, err
	}
	record, found := values[key]
	return record.Value, found, nil
}

// BindWorkspace binds the session's workspace to value, when the plugin
// that registered key binds workspaces: a value bound before stays unless
// replace. It reports whether the workspace is bound to value now.
func (host *Host) BindWorkspace(ctx context.Context, session, key, value string, replace bool) (bool, error) {
	place := host.sessionWorkspace(session)
	if !place.known() || host.bindings == nil || value == contract.MetadataNone || !host.bindsWorkspace(key) {
		return false, nil
	}
	return host.bindings.PutWorkspaceValue(ctx, storage.MetadataWorkspaceRecord{Key: key, Workspace: place.fingerprint, Name: place.name, Value: value}, replace)
}

func (host *Host) sessionWorkspace(session string) workspace {
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	if state, found := host.sessions.lookup(session); found {
		return state.workspace
	}
	return workspace{}
}

// bindsWorkspace reports whether an enabled plugin that registered key binds
// workspaces.
func (host *Host) bindsWorkspace(key string) bool {
	for _, candidate := range host.plugins.Current().Plugins() {
		if !candidate.Enabled() || !candidate.Settings.WorkspaceBinding {
			continue
		}
		if slices.ContainsFunc(candidate.Meta.Metadata, func(attribute plugin.MetadataAttribute) bool { return attribute.Key == key }) {
			return true
		}
	}
	return false
}

// ActionView is what onAction reads: the button clicked, the record's
// session with its bindings, and the interface language.
type ActionView struct {
	Action   string                      `json:"action"`
	Session  plugin.SessionRef           `json:"session"`
	Meta     map[string]plugin.MetaValue `json:"meta"`
	Language string                      `json:"language"`
}

// Action runs the plugin's onAction hook for a click on a button of one of
// its records. When the plugin answers refresh, the session's next request
// computes the plugin's resident block again.
func (host *Host) Action(ctx context.Context, target *plugin.Plugin, input contract.PluginActionInput) contract.PluginActionResult {
	view := ActionView{Action: input.Action, Session: plugin.SessionRef{ID: input.Session}, Meta: map[string]plugin.MetaValue{}, Language: host.Settings().Language}
	host.sessions.mutex.Lock()
	if state, found := host.sessions.lookup(input.Session); found && state.language != "" {
		view.Language = state.language
	}
	host.sessions.mutex.Unlock()
	if target.Can(plugin.CapabilityRequest) && host.bindings != nil {
		values, err := host.bindings.MetadataValues(ctx, storage.MetadataTargetSession, input.Session)
		if err == nil {
			for key, value := range values {
				view.Meta[key] = plugin.MetaValue{Value: value.Value, Source: contract.MetadataSourceSession}
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	raw, _, err := target.Call(ctx, plugin.HookOnAction, view, plugin.CallOptions{NetworkBudget: actionTimeout})
	var failure *plugin.CallError
	switch {
	case errors.As(err, &failure):
		return contract.PluginActionResult{Status: contract.PluginTestFailed, Message: failure.Message, Reason: failure.Reason}
	case err != nil:
		return contract.PluginActionResult{Status: contract.PluginTestFailed, Message: "the action did not run", Reason: plugin.ReasonUnavailable}
	}
	var answer struct {
		Refresh bool   `json:"refresh"`
		Message string `json:"message"`
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && json.Unmarshal(trimmed, &answer) != nil {
		return contract.PluginActionResult{Status: contract.PluginTestFailed, Message: "the action returned no result", Reason: plugin.ReasonInvalidResult}
	}
	result := contract.PluginActionResult{Status: contract.PluginTestOK, Refresh: answer.Refresh, Message: cleanMessage(answer.Message)}
	if answer.Refresh {
		host.Refresh(target.Key, input.Session)
	}
	return result
}

// cleanMessage keeps a plugin's message as one line of plain text.
func cleanMessage(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, "�"))
	return contract.ClampRunes(text, maxActionMessageRunes)
}

// maskLookup hides the credential in a usage the session details show.
func maskLookup(usage string) string {
	var masked strings.Builder
	rest := usage
	for {
		start := strings.Index(rest, "Bearer "+lookupPrefix)
		if start < 0 {
			masked.WriteString(rest)
			return masked.String()
		}
		token := start + len("Bearer ") + len(lookupPrefix)
		end := token
		for end < len(rest) && strings.ContainsRune("0123456789abcdef", rune(rest[end])) {
			end++
		}
		masked.WriteString(rest[:token] + "…")
		rest = rest[end:]
	}
}
