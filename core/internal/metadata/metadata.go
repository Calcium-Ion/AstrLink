// Package metadata resolves the attributes of each request, the user's own
// and those plugins register: the project a memory belongs to, an
// environment, and so on. A request takes each value from its headers, then
// its session's binding, then its access token's default.
package metadata

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Registry keeps the attributes and the access tokens' defaults in memory,
// so that a request resolves its metadata without reading the database.
// Whoever changes them calls Refresh.
type Registry struct {
	store   storage.MetadataStore
	mutex   sync.Mutex
	current atomic.Pointer[snapshot]
}

type snapshot struct {
	attributes []contract.MetadataAttribute
	patterns   map[string]*regexp.Regexp
	tokens     map[string]map[string]string
}

// NewRegistry returns a Registry with nothing in it until its first Refresh.
func NewRegistry(store storage.MetadataStore) *Registry {
	registry := &Registry{store: store}
	registry.current.Store(&snapshot{patterns: map[string]*regexp.Regexp{}, tokens: map[string]map[string]string{}})
	return registry
}

// Refresh reads the attributes and the tokens' defaults again. When they
// cannot be read, the registry keeps what it had.
func (registry *Registry) Refresh(ctx context.Context) error {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	attributes, err := registry.store.ListMetadataAttributes(ctx)
	if err != nil {
		return fmt.Errorf("read metadata attributes: %w", err)
	}
	defaults, err := registry.store.ListMetadataValues(ctx, storage.MetadataTargetToken)
	if err != nil {
		return fmt.Errorf("read token metadata: %w", err)
	}
	next := &snapshot{patterns: make(map[string]*regexp.Regexp, len(attributes)), tokens: map[string]map[string]string{}}
	for _, attribute := range attributes {
		pattern, err := contract.CompileMetadataPattern(attribute.Pattern)
		if err != nil {
			// A stored pattern passed this check when it was saved.
			continue
		}
		next.attributes = append(next.attributes, attribute)
		next.patterns[attribute.Key] = pattern
	}
	for _, value := range defaults {
		if next.tokens[value.TargetID] == nil {
			next.tokens[value.TargetID] = map[string]string{}
		}
		next.tokens[value.TargetID][value.Key] = value.Value
	}
	registry.current.Store(next)
	return nil
}

// Attributes returns the attributes in the order they were created.
func (registry *Registry) Attributes() []contract.MetadataAttribute {
	return slices.Clone(registry.current.Load().attributes)
}

// Attribute returns the attribute with key.
func (registry *Registry) Attribute(key string) (contract.MetadataAttribute, bool) {
	for _, attribute := range registry.current.Load().attributes {
		if attribute.Key == key {
			return attribute, true
		}
	}
	return contract.MetadataAttribute{}, false
}

// Valid reports whether value suits the attribute key: the one check every
// source of a value goes through.
func (registry *Registry) Valid(key, value string) bool {
	pattern, known := registry.current.Load().patterns[key]
	return known && contract.ValidMetadataValue(pattern, value)
}

// TokenDefaults returns the defaults of an access token by key.
func (registry *Registry) TokenDefaults(token contract.AccessTokenID) map[string]string {
	defaults := registry.current.Load().tokens[string(token)]
	values := make(map[string]string, len(defaults))
	for key, value := range defaults {
		values[key] = value
	}
	return values
}

// Resolve returns the metadata of a request from its headers, its session's
// bindings and its access token's defaults. A header with a value the
// attribute does not accept counts as absent and is recorded as ignored.
func (registry *Registry) Resolve(header http.Header, token contract.AccessTokenID, session map[string]storage.MetadataValueRecord) contract.RequestMetadata {
	current := registry.current.Load()
	var metadata contract.RequestMetadata
	set := func(key string, value contract.MetadataValue) {
		if metadata.Values == nil {
			metadata.Values = map[string]contract.MetadataValue{}
		}
		metadata.Values[key] = value
	}
	for _, attribute := range current.attributes {
		pattern := current.patterns[attribute.Key]
		if values := header.Values(contract.MetadataHeader(attribute.Key)); len(values) > 0 {
			value := strings.TrimSpace(values[0])
			if contract.ValidMetadataValue(pattern, value) {
				set(attribute.Key, contract.MetadataValue{Value: value, Source: contract.MetadataSourceHeader})
				continue
			}
			if len(metadata.Events) < contract.MaxRequestMetadataEvents {
				metadata.Events = append(metadata.Events, contract.MetadataEvent{
					Key: attribute.Key, Event: contract.MetadataEventIgnored, Value: printable(value),
					SetBy: contract.MetadataSourceHeader, Reason: contract.MetadataIgnoredInvalidValue,
				})
			}
		}
		if bound, found := session[attribute.Key]; found && contract.ValidMetadataValue(pattern, bound.Value) {
			set(attribute.Key, contract.MetadataValue{Value: bound.Value, Source: contract.MetadataSourceSession, SetBy: bound.SetBy, Turn: bound.Turn})
			continue
		}
		if value, found := current.tokens[string(token)][attribute.Key]; found && token != "" && contract.ValidMetadataValue(pattern, value) {
			set(attribute.Key, contract.MetadataValue{Value: value, Source: contract.MetadataSourceToken, SetBy: contract.MetadataSetByTokenDefault})
		}
	}
	return metadata
}

// printable keeps what a request record may show of a value it ignored.
func printable(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "�"))
	return contract.ClampRunes(value, 64)
}

// RegisterPlugin registers the attributes plugin declares, with plugin as
// their owner, and refreshes the registry. A key or marker another owner
// holds is storage.ErrConflict, and then nothing is registered.
func (registry *Registry) RegisterPlugin(ctx context.Context, plugin string, attributes []contract.MetadataAttribute) error {
	if len(attributes) == 0 {
		return nil
	}
	if err := registry.store.RegisterPluginAttributes(ctx, plugin, attributes); err != nil {
		return err
	}
	return registry.Refresh(ctx)
}
