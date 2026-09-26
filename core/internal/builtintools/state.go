package builtintools

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const StateTTL = 24 * time.Hour
const StateLimit = 256 << 20

type State struct {
	UpstreamPreviousResponseID string              `json:"upstream_previous_response_id,omitempty"`
	ServiceID                  string              `json:"service_id,omitempty"`
	RoutingModel               string              `json:"routing_model,omitempty"`
	History                    []any               `json:"history"`
	Replacements               map[string][]any    `json:"replacements"`
	Images                     map[string][]string `json:"images"`
	Sources                    []Object            `json:"sources"`
	Model                      string              `json:"model"`
}
type stateKey struct{ Principal, ID string }
type stateEntry struct {
	Data []byte
	At   time.Time
}

// Store retains only volatile state; never use the opt-in audit store as a
// conversation cache. Indexed item references share the response byte budget.
type Store struct {
	mu      sync.Mutex
	entries map[stateKey]stateEntry
	items   map[stateKey]stateKey
	size    int
	Now     func() time.Time
	Limit   int
}

func (store *Store) now() time.Time {
	if store.Now != nil {
		return store.Now()
	}
	return time.Now()
}
func (store *Store) remove(key stateKey) {
	store.size -= len(store.entries[key].Data)
	delete(store.entries, key)
	for item, owner := range store.items {
		if owner == key {
			delete(store.items, item)
		}
	}
}
func (store *Store) expire() {
	for key, entry := range store.entries {
		if store.now().Sub(entry.At) >= StateTTL {
			store.remove(key)
		}
	}
}
func (store *Store) Put(principal, id string, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	limit := store.Limit
	if limit == 0 {
		limit = StateLimit
	}
	if len(data) > limit {
		return fmt.Errorf("tool conversation exceeds memory limit")
	}
	if store.entries == nil {
		store.entries = map[stateKey]stateEntry{}
		store.items = map[stateKey]stateKey{}
	}
	store.expire()
	key := stateKey{principal, id}
	if _, exists := store.entries[key]; exists {
		store.remove(key)
	}
	for store.size+len(data) > limit || len(store.entries) >= 10000 {
		var oldest stateKey
		var at time.Time
		for candidate, entry := range store.entries {
			if at.IsZero() || entry.At.Before(at) {
				oldest = candidate
				at = entry.At
			}
		}
		store.remove(oldest)
	}
	store.entries[key] = stateEntry{data, store.now()}
	store.size += len(data)
	for item := range state.Replacements {
		store.items[stateKey{principal, item}] = key
	}
	return nil
}
func (store *Store) Get(principal, id string) (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.expire()
	key := stateKey{principal, id}
	if owner, ok := store.items[key]; ok {
		key = owner
	}
	entry, ok := store.entries[key]
	if !ok {
		return State{}, fmt.Errorf("tool conversation state is unavailable or expired; resend the conversation and images")
	}
	var state State
	err := json.Unmarshal(entry.Data, &state)
	return state, err
}

func (store *Store) Prepare(principal string, body Object) (State, error) {
	state := State{UpstreamPreviousResponseID: String(body["previous_response_id"]), Replacements: map[string][]any{}, Images: map[string][]string{}, Model: String(body["model"])}
	if previous := String(body["previous_response_id"]); strings.HasPrefix(previous, "resp_tool_") {
		var err error
		state, err = store.Get(principal, previous)
		if err != nil {
			return state, err
		}
		if state.Model != String(body["model"]) {
			return state, fmt.Errorf("tool continuation must use its original model")
		}
		delete(body, "previous_response_id")
	}
	input := Array(body["input"])
	if text, ok := body["input"].(string); ok {
		input = []any{Object{"role": "user", "content": text}}
	}
	for _, raw := range input {
		item := Map(raw)
		id := String(item["id"])
		if strings.HasPrefix(id, "ws_tool_") || strings.HasPrefix(id, "ig_tool_") {
			owner, err := store.Get(principal, id)
			if err != nil {
				return state, err
			}
			if owner.Model != state.Model || (state.ServiceID != "" && owner.ServiceID != state.ServiceID) {
				return state, fmt.Errorf("tool replay must use its original model and provider")
			}
			state.ServiceID = owner.ServiceID
			state.RoutingModel = owner.RoutingModel
			state.Sources = owner.Sources
			replacement, ok := owner.Replacements[id]
			if !ok {
				return state, fmt.Errorf("unknown tool output item")
			}
			state.History = append(state.History, replacement...)
			for key, value := range owner.Replacements {
				state.Replacements[key] = value
			}
			for key, value := range owner.Images {
				state.Images[key] = value
			}
		} else {
			state.History = append(state.History, raw)
		}
	}
	if state.UpstreamPreviousResponseID != "" {
		body["previous_response_id"] = state.UpstreamPreviousResponseID
	} else {
		delete(body, "previous_response_id")
	}
	body["input"] = state.History
	return state, nil
}
