package contract

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Metadata attributes are request attributes the user defines, or a plugin
// registers when it is enabled: the project a memory belongs to, an
// environment, and so on. A request takes each value from its headers, its
// session's binding, or its access token's default, in that order.
const (
	MetadataOwnerUser = "user"
	// MetadataNone is the value that leaves a request or a session out, for
	// every attribute.
	MetadataNone = "none"
	// MetadataHeaderPrefix starts the request header of an attribute. The
	// gateway strips the X-AstrLink- namespace before forwarding.
	MetadataHeaderPrefix = "X-AstrLink-Meta-"

	MaxMetadataLabelRunes       = 32
	MaxMetadataDescriptionRunes = 200
	MaxMetadataPatternBytes     = 256
	MaxMetadataValueRunes       = 128
	MaxMetadataAttributes       = 32
)

// Where a request's metadata value came from.
const (
	MetadataSourceHeader  = "header"
	MetadataSourceSession = "session"
	MetadataSourceToken   = "token"
)

// How a stored value was set: a token's default, a marker in a model reply,
// a marker the user sent, the user's answer naming one of the values a plugin
// offered, the value the session's workspace was bound to, or the session
// details.
const (
	MetadataSetByTokenDefault = "token_default"
	MetadataSetByModelMarker  = "model_marker"
	MetadataSetByUserMarker   = "user_marker"
	MetadataSetByUserAnswer   = "user_answer"
	MetadataSetByWorkspace    = "workspace"
	MetadataSetByManual       = "manual"
)

var (
	metadataKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	metadataMarkerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// ValidMetadataKey reports whether key can name an attribute: a lowercase
// letter, then up to 31 lowercase letters, digits and underscores.
func ValidMetadataKey(key string) bool { return metadataKeyPattern.MatchString(key) }

// ValidMetadataMarker reports whether marker can name the line a model
// reply carries for an attribute. Markers reach the upstream model, so they
// must not name the gateway.
func ValidMetadataMarker(marker string) bool {
	return metadataMarkerPattern.MatchString(marker) && !strings.Contains(marker, "astrlink")
}

// MetadataHeader is the request header that sets key: the prefix and the key
// with underscores as hyphens, in canonical form.
func MetadataHeader(key string) string {
	return http.CanonicalHeaderKey(MetadataHeaderPrefix + strings.ReplaceAll(key, "_", "-"))
}

// MetadataAttribute is a request attribute.
type MetadataAttribute struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Pattern validates every value; it is anchored with ^ and $.
	Pattern string `json:"pattern"`
	// Marker, when set, names the line a model reply or the user can send
	// to bind the value for a session.
	Marker string `json:"marker,omitempty"`
	// Owner is user or the key of the plugin that registered the attribute.
	// Only the user's own attributes can be edited or deleted.
	Owner     string    `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (attribute MetadataAttribute) Validate() error {
	if !ValidMetadataKey(attribute.Key) {
		return fmt.Errorf("key must be a lowercase letter followed by up to 31 lowercase letters, digits or underscores")
	}
	if err := validateBoundedText("label", attribute.Label, MaxMetadataLabelRunes, false); err != nil {
		return err
	}
	if err := validateBoundedText("description", attribute.Description, MaxMetadataDescriptionRunes, true); err != nil {
		return err
	}
	if _, err := CompileMetadataPattern(attribute.Pattern); err != nil {
		return err
	}
	if attribute.Marker != "" && !ValidMetadataMarker(attribute.Marker) {
		return fmt.Errorf("marker must be a lowercase letter followed by up to 31 lowercase letters, digits or hyphens, and must not name the gateway")
	}
	if attribute.Owner != MetadataOwnerUser && !ValidPluginKey(attribute.Owner) {
		return fmt.Errorf("owner must be user or a plugin key")
	}
	return nil
}

// CompileMetadataPattern compiles an attribute's pattern, which must be
// anchored so that it validates the whole value.
func CompileMetadataPattern(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > MaxMetadataPatternBytes || !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return nil, fmt.Errorf("pattern must start with ^, end with $ and be at most %d bytes", MaxMetadataPatternBytes)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern does not compile: %v", err)
	}
	return compiled, nil
}

// ValidMetadataValue reports whether value suits an attribute whose pattern
// compiled to pattern: the reserved none, or a short line the pattern
// accepts. Every source of a value goes through it.
func ValidMetadataValue(pattern *regexp.Regexp, value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxMetadataValueRunes {
		return false
	}
	for _, r := range value {
		if r < 32 || r == 0x7f {
			return false
		}
	}
	return value == MetadataNone || pattern.MatchString(value)
}

// MetadataAttributeInput creates or changes one of the user's attributes.
type MetadataAttributeInput struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Pattern     string `json:"pattern"`
	Marker      string `json:"marker"`
}

// MetadataAttributeView is an attribute as the settings page lists it.
type MetadataAttributeView struct {
	MetadataAttribute
	// Header is the request header that sets the attribute.
	Header string `json:"header"`
	// OwnerMissing marks an attribute whose plugin is gone; the user may
	// then delete it.
	OwnerMissing bool `json:"owner_missing,omitempty"`
}

// AccessTokenMetadata holds an access token's default values by attribute
// key, used when a request's headers and session set none.
type AccessTokenMetadata struct {
	Values map[string]string `json:"values"`
}

// SessionMetadata is what a session is bound to, by attribute key.
type SessionMetadata struct {
	Values map[string]SessionMetadataValue `json:"values"`
	// Workspace is the working directory the session's last request named
	// and what it is bound to; nil when the gateway knows none, as after a
	// restart until the session's next request.
	Workspace *SessionWorkspace `json:"workspace,omitempty"`
	// Choices are the values plugins offered the session, by attribute key.
	Choices map[string][]string `json:"choices,omitempty"`
}

// SessionWorkspace is a session's working directory, named by its last
// element, and the value of each attribute it is bound to.
type SessionWorkspace struct {
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

// SessionMetadataValue is one binding of a session: its value, how it was
// set, the turn it was set in (0 when not known) and when.
type SessionMetadataValue struct {
	Value     string    `json:"value"`
	SetBy     string    `json:"set_by"`
	Turn      int       `json:"turn,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionMetadataInput sets one binding of a session by hand. Workspace
// also binds the session's workspace to the value, for the sessions that
// start there later.
type SessionMetadataInput struct {
	Value     string `json:"value"`
	Workspace bool   `json:"workspace,omitempty"`
}

// RequestMetadata is the metadata of one request, kept with its record.
type RequestMetadata struct {
	// Values holds the value of each attribute the request has, by key.
	Values map[string]MetadataValue `json:"values,omitempty"`
	// Events lists what the request changed, or tried to: bindings and
	// values the gateway ignored.
	Events []MetadataEvent `json:"events,omitempty"`
}

// MetadataValue is one resolved value and where it came from.
type MetadataValue struct {
	Value string `json:"value"`
	// Source is header, session or token.
	Source string `json:"source"`
	// SetBy and Turn tell how and when a session binding was set.
	SetBy string `json:"set_by,omitempty"`
	Turn  int    `json:"turn,omitempty"`
}

// Metadata events.
const (
	MetadataEventBound   = "bound"
	MetadataEventRebound = "rebound"
	MetadataEventCleared = "cleared"
	MetadataEventIgnored = "ignored"
)

// Reasons a value was ignored.
const (
	MetadataIgnoredInvalidValue = "invalid_value"
	MetadataIgnoredAlreadyBound = "already_bound"
	MetadataIgnoredInCodeBlock  = "in_code_block"
)

// MetadataEvent is one change, or ignored attempt, to a request's metadata.
type MetadataEvent struct {
	Key   string `json:"key"`
	Event string `json:"event"`
	Value string `json:"value,omitempty"`
	// SetBy is where the value came from: header, or how a binding was set.
	SetBy  string `json:"set_by,omitempty"`
	Reason string `json:"reason,omitempty"`
	// New marks a binding to a value no session had and none of the
	// choices a plugin offered the session named: a new project.
	New bool `json:"new,omitempty"`
	// Normalized is the value as the marker wrote it when the gateway took
	// a known value that differs from it only in case, hyphens or
	// underscores.
	Normalized string `json:"normalized,omitempty"`
}

// MaxRequestMetadataEvents bounds the events kept with one request.
const MaxRequestMetadataEvents = 16

// Empty reports whether the request has no metadata to keep.
func (metadata RequestMetadata) Empty() bool {
	return len(metadata.Values) == 0 && len(metadata.Events) == 0
}

func (metadata RequestMetadata) Validate() error {
	if len(metadata.Values) > MaxMetadataAttributes {
		return fmt.Errorf("a request has at most %d metadata values", MaxMetadataAttributes)
	}
	for key, value := range metadata.Values {
		if !ValidMetadataKey(key) {
			return fmt.Errorf("invalid metadata key %q", key)
		}
		if err := validateBoundedText("metadata value", value.Value, MaxMetadataValueRunes, false); err != nil {
			return err
		}
		switch value.Source {
		case MetadataSourceHeader, MetadataSourceSession, MetadataSourceToken:
		default:
			return fmt.Errorf("unknown metadata source %q", value.Source)
		}
	}
	if len(metadata.Events) > MaxRequestMetadataEvents {
		return fmt.Errorf("a request keeps at most %d metadata events", MaxRequestMetadataEvents)
	}
	for _, event := range metadata.Events {
		if !ValidMetadataKey(event.Key) {
			return fmt.Errorf("invalid metadata key %q", event.Key)
		}
		switch event.Event {
		case MetadataEventBound, MetadataEventRebound, MetadataEventCleared, MetadataEventIgnored:
		default:
			return fmt.Errorf("unknown metadata event %q", event.Event)
		}
		if err := validateBoundedText("metadata event value", event.Value, MaxMetadataValueRunes, true); err != nil {
			return err
		}
	}
	return nil
}
