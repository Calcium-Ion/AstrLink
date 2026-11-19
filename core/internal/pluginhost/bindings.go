package pluginhost

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// bind reads the session's bindings and applies what the turn's opening
// message says. A message that is one marker line binds or rebinds the
// session there and then, and so does an answer that names, word for word,
// one of the values a plugin offered the session. A session with no value
// stored for a marker's attribute, such as one whose client lost its
// session, works the value out from the history the client replayed; failing
// that, a new session takes the value its workspace was bound to. A stored
// value, none included, stops both: the history's markers were handled when
// they came, and replaying them would undo what the user changed since.
func (run *Run) bind(ctx context.Context, plugins []*plugin.Plugin) []contract.MetadataEvent {
	host := run.host
	if host.bindings == nil || host.metadata == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, bindingTimeout)
	defer cancel()
	values, err := host.bindings.MetadataValues(ctx, storage.MetadataTargetSession, run.input.Session)
	if err != nil {
		host.logf("read the session's metadata: %v", err)
		return nil
	}
	if values == nil {
		values = map[string]storage.MetadataValueRecord{}
	}
	run.values = values
	run.workspaceKeys = map[string]bool{}
	for _, candidate := range plugins {
		if candidate.Settings.WorkspaceBinding && !candidate.Settings.Excludes(run.input.Client) {
			for _, attribute := range candidate.Meta.Metadata {
				run.workspaceKeys[attribute.Key] = true
			}
		}
	}
	if run.turn.opened {
		if hit, ok := host.metadata.UserMarker(run.turn.own); ok {
			run.bindsOnly = true
			hit, written := run.canonical(ctx, hit)
			if !hit.Valid() {
				return []contract.MetadataEvent{ignored(hit, contract.MetadataSetByUserMarker)}
			}
			if event, ok := run.store(ctx, hit, contract.MetadataSetByUserMarker, run.input.Turn, written); ok {
				return []contract.MetadataEvent{event}
			}
			return nil
		}
	}
	var events []contract.MetadataEvent
	if hit, ok := run.answer(); ok {
		run.bindsOnly = true
		if event, ok := run.store(ctx, hit, contract.MetadataSetByUserAnswer, run.input.Turn, ""); ok {
			events = append(events, event)
		}
	}
	missing := false
	for _, key := range host.metadata.Markers() {
		if _, stored := values[key]; !stored {
			missing = true
		}
	}
	if missing {
		for key, hit := range host.metadata.Replay(run.turn.history) {
			if _, stored := values[key]; stored {
				continue
			}
			setBy := contract.MetadataSetByModelMarker
			if hit.User {
				setBy = contract.MetadataSetByUserMarker
			}
			// Which turn the line came in is not known from one body.
			if event, ok := run.store(ctx, hit, setBy, 0, ""); ok {
				events = append(events, event)
			}
		}
	}
	return append(events, run.bindWorkspace(ctx)...)
}

// answer reports a turn whose opening message is, word for word but for
// case, one of the values a plugin offered the session for an attribute it
// has no value of yet. A number or any other wording is the model's to read:
// "2" may answer another question.
func (run *Run) answer() (metadata.MarkerHit, bool) {
	text := strings.TrimSpace(run.turn.own)
	if !run.turn.opened || text == "" || len(text) > 256 || strings.ContainsAny(text, "\r\n") {
		return metadata.MarkerHit{}, false
	}
	host := run.host
	host.sessions.mutex.Lock()
	defer host.sessions.mutex.Unlock()
	session, found := host.sessions.lookup(run.input.Session)
	if !found {
		return metadata.MarkerHit{}, false
	}
	for _, key := range sortedKeys(session.plugins) {
		state := session.plugins[key]
		for _, attribute := range sortedKeys(state.choices) {
			if _, stored := run.values[attribute]; stored {
				continue
			}
			for _, choice := range state.choices[attribute] {
				if strings.EqualFold(choice, text) && host.metadata.Valid(attribute, choice) {
					return metadata.MarkerHit{Key: attribute, Value: choice, User: true}, true
				}
			}
		}
	}
	return metadata.MarkerHit{}, false
}

// bindWorkspace binds a session without a value to the one its workspace was
// bound to, for the attributes of the plugins that do so.
func (run *Run) bindWorkspace(ctx context.Context) []contract.MetadataEvent {
	host := run.host
	if !run.place.known() {
		return nil
	}
	wanted := false
	for key := range run.workspaceKeys {
		if _, stored := run.values[key]; !stored {
			wanted = true
		}
	}
	if !wanted {
		return nil
	}
	bound, err := host.bindings.WorkspaceValues(ctx, run.place.fingerprint)
	if err != nil {
		host.logf("read the workspace's metadata: %v", err)
		return nil
	}
	var events []contract.MetadataEvent
	for _, key := range sortedKeys(bound) {
		if _, stored := run.values[key]; stored || !run.workspaceKeys[key] || !host.metadata.Valid(key, bound[key].Value) {
			continue
		}
		hit := metadata.MarkerHit{Key: key, Value: bound[key].Value}
		if event, ok := run.store(ctx, hit, contract.MetadataSetByWorkspace, run.input.Turn, ""); ok {
			events = append(events, event)
		}
	}
	return events
}

// store keeps hit as the session's value and returns the event that says
// so; nothing when the value is already stored or the write failed. written
// is the marker's own spelling when canonical changed it. The first value a
// marker or answer binds also binds the session's workspace, for the
// sessions that start there later.
func (run *Run) store(ctx context.Context, hit metadata.MarkerHit, setBy string, turn int, written string) (contract.MetadataEvent, bool) {
	previous, had := run.values[hit.Key]
	if had && previous.Value == hit.Value {
		return contract.MetadataEvent{}, false
	}
	isNew := false
	if (setBy == contract.MetadataSetByModelMarker || setBy == contract.MetadataSetByUserMarker) && hit.Value != contract.MetadataNone {
		isNew = !slices.Contains(run.knownValues(ctx, hit.Key), hit.Value)
	}
	now := time.Now().UTC()
	record := storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: run.input.Session,
		Key: hit.Key, Value: hit.Value, SetBy: setBy, Turn: turn, CreatedAt: now, UpdatedAt: now,
	}
	if err := run.host.bindings.PutMetadataValue(ctx, record); err != nil {
		run.host.logf("bind the session's metadata: %v", err)
		return contract.MetadataEvent{}, false
	}
	run.values[hit.Key] = record
	if hit.Value == contract.MetadataNone {
		// A session left out has nothing to look up.
		run.host.lookups.revokeSession(run.input.Session)
	}
	event := contract.MetadataEvent{Key: hit.Key, Event: contract.MetadataEventBound, Value: hit.Value, SetBy: setBy, New: isNew, Normalized: written}
	if had {
		event.Event = contract.MetadataEventRebound
	}
	switch setBy {
	case contract.MetadataSetByModelMarker, contract.MetadataSetByUserMarker, contract.MetadataSetByUserAnswer:
		if run.place.known() && run.workspaceKeys[hit.Key] && hit.Value != contract.MetadataNone {
			_, err := run.host.bindings.PutWorkspaceValue(ctx, storage.MetadataWorkspaceRecord{
				Key: hit.Key, Workspace: run.place.fingerprint, Name: run.place.name, Value: hit.Value,
			}, false)
			if err != nil {
				run.host.logf("bind the workspace's metadata: %v", err)
			}
		}
	}
	return event, true
}

// maxKnownValues bounds the values of an attribute other sessions are bound
// to that a marker is compared with.
const maxKnownValues = 500

// knownValues returns the values of key a marker may mean: those plugins
// offered the session and those other sessions are bound to.
func (run *Run) knownValues(ctx context.Context, key string) []string {
	if values, read := run.known[key]; read {
		return values
	}
	host := run.host
	var values []string
	host.sessions.mutex.Lock()
	if session, found := host.sessions.lookup(run.input.Session); found {
		for _, state := range session.plugins {
			values = append(values, state.choices[key]...)
		}
	}
	host.sessions.mutex.Unlock()
	stats, err := host.bindings.MetadataValueStats(ctx, key, maxKnownValues)
	if err != nil {
		host.logf("read the values of %s: %v", key, err)
	}
	for _, stat := range stats {
		values = append(values, stat.Value)
	}
	if run.known == nil {
		run.known = map[string][]string{}
	}
	run.known[key] = values
	return values
}

// canonical maps a marker's value to a known one it differs from only in
// case, hyphens or underscores, so that "AstrLink" or "astr_link" joins the
// project astrlink instead of starting another. A value no known one matches
// keeps its spelling, lowercased when only that makes it valid. It returns
// the marker's own spelling when it took another.
func (run *Run) canonical(ctx context.Context, hit metadata.MarkerHit) (metadata.MarkerHit, string) {
	if (hit.Reason != "" && hit.Reason != contract.MetadataIgnoredInvalidValue) || hit.Value == "" || hit.Value == contract.MetadataNone {
		return hit, ""
	}
	registry := run.host.metadata
	want := foldValue(hit.Value)
	for _, known := range run.knownValues(ctx, hit.Key) {
		if known != hit.Value && foldValue(known) == want && registry.Valid(hit.Key, known) {
			written := hit.Value
			hit.Value, hit.Reason = known, ""
			return hit, written
		}
	}
	if hit.Reason != "" {
		if lower := strings.ToLower(hit.Value); registry.Valid(hit.Key, lower) {
			written := hit.Value
			hit.Value, hit.Reason = lower, ""
			return hit, written
		}
	}
	return hit, ""
}

// foldValue is a value without case, hyphens and underscores.
func foldValue(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return -1
		}
		return r
	}, strings.ToLower(value))
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ignored(hit metadata.MarkerHit, setBy string) contract.MetadataEvent {
	return contract.MetadataEvent{Key: hit.Key, Event: contract.MetadataEventIgnored, Value: hit.Value, SetBy: setBy, Reason: hit.Reason}
}

// NewMarkerScanner returns a scanner for the reply's text, or nil when no
// attribute has a marker or every plugin is paused.
func (run *Run) NewMarkerScanner() *metadata.MarkerScanner {
	if run == nil || run.host.metadata == nil || run.settings.Paused {
		return nil
	}
	return run.host.metadata.NewMarkerScanner()
}

// Finish binds the session to what the reply's marker lines say, when the
// request succeeded and the session has no value yet; a model cannot rebind
// a session. The captures that waited for the reply then run with the new
// value, and the next call of the turn settles the turn again and captures
// nothing more. Finish returns what the reply changed or tried to.
func (run *Run) Finish(ctx context.Context, hits []metadata.MarkerHit, succeeded bool) []contract.MetadataEvent {
	if run == nil {
		return nil
	}
	defer run.settleAwaited()
	if len(hits) == 0 {
		return nil
	}
	var events []contract.MetadataEvent
	bound := false
	for _, hit := range hits {
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingTimeout)
		hit, written := hit, ""
		if run.values != nil && succeeded {
			if _, stored := run.values[hit.Key]; !stored {
				hit, written = run.canonical(storeCtx, hit)
			}
		}
		if !hit.Valid() {
			cancel()
			events = append(events, ignored(hit, contract.MetadataSetByModelMarker))
			continue
		}
		if run.values == nil || !succeeded {
			cancel()
			continue
		}
		if _, stored := run.values[hit.Key]; stored {
			cancel()
			hit.Reason = contract.MetadataIgnoredAlreadyBound
			events = append(events, ignored(hit, contract.MetadataSetByModelMarker))
			continue
		}
		event, ok := run.store(storeCtx, hit, contract.MetadataSetByModelMarker, run.input.Turn, written)
		cancel()
		if ok {
			events = append(events, event)
			bound = true
		}
	}
	if bound {
		host := run.host
		host.sessions.mutex.Lock()
		for _, current := range run.parts {
			state := host.sessions.plugin(run.input.Session, current.plugin.Key)
			state.settled = ""
			state.bindingTurn = run.turnKey
		}
		host.sessions.mutex.Unlock()
	}
	return clampEvents(events)
}

// settleAwaited queues the captures that waited for the reply, with the
// values it bound. A plugin whose attributes the session still lacks has the
// capture held until the session has them. While the privacy check of the
// turn's text runs, the captures wait for it.
func (run *Run) settleAwaited() {
	if !run.checked() {
		go func() {
			<-run.ready
			run.settleAwaited()
		}()
		return
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	for _, current := range run.parts {
		if current.observe != observeAwaiting {
			continue
		}
		meta, complete := run.boundMeta(current)
		if !complete || current.awaited == nil {
			current.observe = observeSkippedUnbound
			if current.awaited != nil && run.host.held.hold(lookupKey{run.input.Session, current.plugin.Key}, *current.awaited) {
				current.observe = observeHeld
			}
			continue
		}
		view := *current.awaited
		view.Meta, current.info.Meta = meta, meta
		current.observe = run.queue(current, view)
	}
}

// boundMeta returns the plugin's view of the session's values with those
// the reply bound, and whether it now has a value of each of the plugin's
// attributes.
func (run *Run) boundMeta(current *part) (map[string]plugin.MetaValue, bool) {
	meta := maps.Clone(current.view.Meta)
	if meta == nil {
		meta = map[string]plugin.MetaValue{}
	}
	for _, attribute := range current.plugin.Meta.Metadata {
		if _, found := meta[attribute.Key]; found {
			continue
		}
		bound, found := run.values[attribute.Key]
		if !found {
			return nil, false
		}
		meta[attribute.Key] = plugin.MetaValue{Value: bound.Value, Source: contract.MetadataSourceSession}
	}
	return meta, true
}

// Captures returns what became of the turn's capture for each plugin that
// captures, by plugin key; for those that waited for the reply, what Finish
// decided.
func (run *Run) Captures() map[string]string {
	if run == nil {
		return nil
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	captures := map[string]string{}
	for _, current := range run.parts {
		if current.observe != "" && current.observe != "pending" {
			captures[current.plugin.Key] = current.observe
		}
	}
	return captures
}
