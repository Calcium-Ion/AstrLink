// Package imports adds providers the user already has elsewhere: from an
// astrlink:// link (provider import), and from the agent tools' own config
// files on this computer (local import). A key read from either never
// reaches the webview: the page sees a hint and confirms by id, and the key
// is attached to the request to Core here.
package imports

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

const (
	// maxLinkBytes is also bounded by the Windows command line that carries
	// the link.
	maxLinkBytes        = 32 * 1024
	maxBaseURLBytes     = 2_048
	maxNameChars        = 128
	maxModels           = 2_000
	maxModelChars       = 256
	maxProtocols        = 32
	maxHeaderNameBytes  = 128
	maxSecretBytes      = 16_384
	providerImportPath  = "/providers/import"
	providerImportHost  = "v1"
	unknownProviderLink = "this link is no longer waiting; open it again"
)

var authSchemes = []string{"none", "bearer", "anthropic_api_key", "google_api_key", "custom_header"}

var parameters = []string{"kind", "name", "base_url", "api_key", "auth", "auth_header", "protocols", "models"}

// Provider is the link's provider as the confirmation dialog shows it.
// Empty protocol and model lists fall back to the kind's defaults.
type Provider struct {
	Kind       string   `json:"kind"`
	Name       *string  `json:"name"`
	BaseURL    string   `json:"base_url"`
	Auth       *string  `json:"auth"`
	AuthHeader *string  `json:"auth_header"`
	Protocols  []string `json:"protocols"`
	Models     []string `json:"models"`
	HasAPIKey  bool     `json:"has_api_key"`
	APIKeyHint *string  `json:"api_key_hint"`
}

// Reason is why a link was rejected.
type Reason string

const (
	ReasonMalformed          Reason = "malformed"
	ReasonUnsupported        Reason = "unsupported"
	ReasonUnknownParameter   Reason = "unknown_parameter"
	ReasonDuplicateParameter Reason = "duplicate_parameter"
	ReasonMissingParameter   Reason = "missing_parameter"
	ReasonInvalidParameter   Reason = "invalid_parameter"
)

type rejection struct {
	reason Reason
	field  string
}

func reject(reason Reason, field string) *rejection { return &rejection{reason, field} }

// NoticeStatus tells a link waiting for confirmation from one rejected.
type NoticeStatus string

const (
	NoticePending NoticeStatus = "pending"
	NoticeInvalid NoticeStatus = "invalid"
)

// Notice is a link the main window shows: pending with its provider, or
// invalid with the reason and the parameter at fault.
type Notice struct {
	Status   NoticeStatus `json:"status"`
	ID       string       `json:"id"`
	Provider *Provider    `json:"provider,omitempty"`
	Reason   *Reason      `json:"reason,omitempty"`
	Field    *string      `json:"field,omitempty"`
}

// MarshalJSON writes an invalid notice's field even when it is null, as
// the Tauri shell did.
func (n Notice) MarshalJSON() ([]byte, error) {
	if n.Status == NoticePending {
		return json.Marshal(struct {
			Status   NoticeStatus `json:"status"`
			ID       string       `json:"id"`
			Provider *Provider    `json:"provider"`
		}{n.Status, n.ID, n.Provider})
	}
	return json.Marshal(struct {
		Status NoticeStatus `json:"status"`
		ID     string       `json:"id"`
		Reason *Reason      `json:"reason"`
		Field  *string      `json:"field"`
	}{n.Status, n.ID, n.Reason, n.Field})
}

type link struct {
	provider Provider
	apiKey   *string
}

type providerEntry struct {
	digest [32]byte
	notice Notice
	apiKey *string
}

// ProviderImports holds the latest link waiting for the user; a newer link
// replaces it.
type ProviderImports struct {
	mu     sync.Mutex
	entry  *providerEntry
	scheme string
}

// NewProviderImports takes links of scheme (astrlink, or astrlink-isolated
// in development).
func NewProviderImports(scheme string) *ProviderImports {
	return &ProviderImports{scheme: scheme}
}

// Accept reads a link the OS opened and keeps it until the user confirms
// or dismisses it.
func (p *ProviderImports) Accept(raw string) (Notice, error) {
	digest := sha256.Sum256([]byte(raw))
	p.mu.Lock()
	defer p.mu.Unlock()
	// The same click can arrive both at launch and as an open event.
	if p.entry != nil && p.entry.digest == digest {
		return p.entry.notice, nil
	}
	id, err := NewID()
	if err != nil {
		return Notice{}, errors.New("unable to generate provider import id: " + err.Error())
	}
	entry := &providerEntry{digest: digest}
	if parsed, rejected := parseLink(raw, p.scheme); rejected != nil {
		reason := rejected.reason
		entry.notice = Notice{Status: NoticeInvalid, ID: id, Reason: &reason}
		if rejected.field != "" {
			field := rejected.field
			entry.notice.Field = &field
		}
	} else {
		provider := parsed.provider
		entry.notice = Notice{Status: NoticePending, ID: id, Provider: &provider}
		entry.apiKey = parsed.apiKey
	}
	p.entry = entry
	return entry.notice, nil
}

// Pending is the link waiting for the user, nil for none.
func (p *ProviderImports) Pending() *Notice {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entry == nil {
		return nil
	}
	notice := p.entry.notice
	return &notice
}

// Dismiss drops the link with id.
func (p *ProviderImports) Dismiss(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entry != nil && p.entry.notice.ID == id {
		p.entry = nil
	}
}

// take removes the entry while it is being created so a second confirm
// cannot add the provider twice.
func (p *ProviderImports) take(id string) *providerEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entry == nil || p.entry.notice.ID != id || p.entry.notice.Status != NoticePending {
		return nil
	}
	entry := p.entry
	p.entry = nil
	return entry
}

func (p *ProviderImports) restore(entry *providerEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entry == nil {
		p.entry = entry
	}
}

// ServiceCreator is Core's control API for the services an import adds.
type ServiceCreator interface {
	CreateService(ctx context.Context, input json.RawMessage) (control.ServiceRecord, error)
}

// Confirm creates the linked provider from what the dialog filled in,
// attaching the link's key. A failure keeps the link waiting.
func (p *ProviderImports) Confirm(ctx context.Context, core ServiceCreator, id string, input json.RawMessage) (control.ServiceRecord, error) {
	entry := p.take(id)
	if entry == nil {
		return control.ServiceRecord{}, errors.New(unknownProviderLink)
	}
	authorized, err := authorize(entry, input)
	var record control.ServiceRecord
	if err == nil {
		record, err = core.CreateService(ctx, authorized)
	}
	if err != nil {
		p.restore(entry)
		return control.ServiceRecord{}, errors.New(redact(err.Error(), entry.apiKey))
	}
	return record, nil
}

// NewID is a random import id: 32 hexadecimal digits.
func NewID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// parseLink reads an astrlink://v1/providers/import link of scheme.
func parseLink(raw, scheme string) (*link, *rejection) {
	if len(raw) > maxLinkBytes {
		return nil, reject(ReasonMalformed, "")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return nil, reject(ReasonMalformed, "")
	}
	if u.Scheme != scheme || u.Hostname() != providerImportHost || u.EscapedPath() != providerImportPath ||
		u.Port() != "" || u.User != nil || strings.Contains(raw, "#") {
		return nil, reject(ReasonUnsupported, "")
	}

	seen := map[int]bool{}
	values := make([]*string, len(parameters))
	for _, pair := range formPairs(u.RawQuery) {
		index := slices.Index(parameters, pair.key)
		if index < 0 {
			// Echo only a plain name; the rest of the link is untrusted.
			echo := ""
			if len(pair.key) <= 64 && plainName(pair.key) {
				echo = pair.key
			}
			return nil, reject(ReasonUnknownParameter, echo)
		}
		if seen[index] {
			return nil, reject(ReasonDuplicateParameter, parameters[index])
		}
		seen[index] = true
		// Empty values count as absent: link builders often emit every
		// field.
		if trim(pair.value) != "" {
			value := pair.value
			values[index] = &value
		}
	}
	kindValue, nameValue, baseURLValue, apiKey, authValue, authHeaderValue, protocolsValue, modelsValue :=
		values[0], values[1], values[2], values[3], values[4], values[5], values[6], values[7]

	if kindValue == nil {
		return nil, reject(ReasonMissingParameter, "kind")
	}
	kind := trim(*kindValue)
	if len(kind) > 64 || !validKind(kind) {
		return nil, reject(ReasonInvalidParameter, "kind")
	}
	// Subscriptions sign in through their own flow and never take an API
	// key.
	if strings.HasSuffix(kind, "_subscription") {
		return nil, reject(ReasonUnsupported, "kind")
	}

	if baseURLValue == nil {
		return nil, reject(ReasonMissingParameter, "base_url")
	}
	baseURL := trim(*baseURLValue)
	if !ValidBaseURL(baseURL) {
		return nil, reject(ReasonInvalidParameter, "base_url")
	}

	var name *string
	if nameValue != nil {
		trimmed := trim(*nameValue)
		if utf8.RuneCountInString(trimmed) > maxNameChars || !PlainText(trimmed) {
			return nil, reject(ReasonInvalidParameter, "name")
		}
		name = &trimmed
	}

	var auth *string
	if authValue != nil {
		trimmed := trim(*authValue)
		if !slices.Contains(authSchemes, trimmed) {
			return nil, reject(ReasonInvalidParameter, "auth")
		}
		auth = &trimmed
	}
	customHeader := auth != nil && *auth == "custom_header"
	var authHeader *string
	if authHeaderValue != nil {
		trimmed := trim(*authHeaderValue)
		authHeader = &trimmed
	}
	switch {
	case authHeader == nil && customHeader:
		return nil, reject(ReasonMissingParameter, "auth_header")
	case authHeader != nil && (!customHeader || !validHeaderName(*authHeader)):
		return nil, reject(ReasonInvalidParameter, "auth_header")
	}

	if apiKey != nil && (len(*apiKey) > maxSecretBytes || !PlainText(*apiKey) || auth != nil && *auth == "none") {
		return nil, reject(ReasonInvalidParameter, "api_key")
	}

	protocols, ok := list(protocolsValue, maxProtocols, validProtocolID)
	if !ok {
		return nil, reject(ReasonInvalidParameter, "protocols")
	}
	models, ok := list(modelsValue, maxModels, func(model string) bool {
		return utf8.RuneCountInString(model) <= maxModelChars && PlainText(model)
	})
	if !ok {
		return nil, reject(ReasonInvalidParameter, "models")
	}

	provider := Provider{
		Kind:       kind,
		Name:       name,
		BaseURL:    baseURL,
		Auth:       auth,
		AuthHeader: authHeader,
		Protocols:  protocols,
		Models:     models,
		HasAPIKey:  apiKey != nil,
	}
	if apiKey != nil {
		provider.APIKeyHint = KeyHint(*apiKey)
	}
	return &link{provider: provider, apiKey: apiKey}, nil
}

type formPair struct{ key, value string }

// formPairs decodes a query as application/x-www-form-urlencoded, the way
// the url crate's query_pairs does: empty sequences are skipped, `+` is a
// space, a bad escape stays as it is, and invalid UTF-8 becomes U+FFFD.
func formPairs(query string) []formPair {
	var pairs []formPair
	for _, sequence := range strings.Split(query, "&") {
		if sequence == "" {
			continue
		}
		key, value, _ := strings.Cut(sequence, "=")
		pairs = append(pairs, formPair{formDecode(key), formDecode(value)})
	}
	return pairs
}

func formDecode(text string) string {
	text = strings.ReplaceAll(text, "+", " ")
	var out []byte
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) && isHex(text[i+1]) && isHex(text[i+2]) {
			out = append(out, unhex(text[i+1])<<4|unhex(text[i+2]))
			i += 2
			continue
		}
		out = append(out, text[i])
	}
	return strings.ToValidUTF8(string(out), "�")
}

func isHex(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

func plainName(key string) bool {
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(isASCIIAlnum(c) || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func isASCIIAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func validKind(kind string) bool {
	if kind == "" || kind[0] < 'a' || kind[0] > 'z' {
		return false
	}
	for i := 0; i < len(kind); i++ {
		c := kind[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// trim is Rust's str::trim: Unicode whitespace on both ends.
func trim(value string) string { return strings.TrimFunc(value, isRustWhitespace) }

func isRustWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// PlainText reports text without control characters or U+FFFD, which marks
// bytes that were not UTF-8.
func PlainText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// ValidBaseURL reports an http(s) address with a host and without
// credentials, a query or a fragment.
func ValidBaseURL(value string) bool {
	if len(value) > maxBaseURLBytes {
		return false
	}
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Opaque == "" && u.Hostname() != "" &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && !strings.Contains(value, "#")
}

func validHeaderName(value string) bool {
	if value == "" || len(value) > maxHeaderNameBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; !isASCIIAlnum(c) && !strings.ContainsRune("!#$%&'*+.^_`|~-", rune(c)) {
			return false
		}
	}
	return true
}

// validProtocolID matches Core's protocol ids such as `openai.chat` or
// `openai.responses.compact`.
func validProtocolID(value string) bool {
	if len(value) > 64 || value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, part := range splitAny(value, "._-") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			if c := part[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

// splitAny splits at every separator, keeping empty parts.
func splitAny(value, separators string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(value); i++ {
		if strings.IndexByte(separators, value[i]) >= 0 {
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	return append(parts, value[start:])
}

// list reads a comma-separated list; blank entries are skipped and
// duplicates rejected.
func list(value *string, limit int, valid func(string) bool) ([]string, bool) {
	items := []string{}
	if value == nil {
		return items, true
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(*value, ",") {
		item = trim(item)
		if item == "" {
			continue
		}
		if len(items) == limit || !valid(item) || seen[item] {
			return nil, false
		}
		seen[item] = true
		items = append(items, item)
	}
	return items, true
}

// KeyHint is Core's credential hint: the last four characters of a key of
// twelve or more.
func KeyHint(key string) *string {
	runes := []rune(key)
	if len(runes) < 12 {
		return nil
	}
	hint := "…" + string(runes[len(runes)-4:])
	return &hint
}

// authorize checks that the dialog creates what the link described, then
// attaches the link's key. Kind and address must match so the key only
// reaches that host.
func authorize(entry *providerEntry, input json.RawMessage) (json.RawMessage, error) {
	if entry.notice.Status != NoticePending || entry.notice.Provider == nil {
		return nil, errors.New("this link cannot add a service")
	}
	provider := entry.notice.Provider
	value, err := format.DecodeJSON(string(input))
	if err != nil {
		return nil, errors.New("invalid service input")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("invalid service input")
	}
	if kind, _ := object["kind"].(string); kind != provider.Kind {
		return nil, errors.New("the service type does not match the link")
	}
	httpObject, ok := object["http"].(map[string]any)
	if !ok {
		return nil, errors.New("invalid service input")
	}
	if baseURL, _ := httpObject["base_url"].(string); baseURL != provider.BaseURL {
		return nil, errors.New("the service address does not match the link")
	}
	auth, ok := httpObject["auth"]
	if !ok {
		return nil, errors.New("invalid service input")
	}
	authObject, _ := auth.(map[string]any)
	scheme, ok := authObject["scheme"].(string)
	if !ok {
		return nil, errors.New("invalid service input")
	}
	if provider.Auth != nil {
		header, hasHeader := authObject["header_name"].(string)
		wantHeader := provider.AuthHeader != nil
		if scheme != *provider.Auth || hasHeader != wantHeader || wantHeader && header != *provider.AuthHeader {
			return nil, errors.New("the sign-in method does not match the link")
		}
	}
	if entry.apiKey != nil {
		if _, ok := httpObject["credential"]; scheme == "none" || ok {
			return nil, errors.New("the link's API key cannot be replaced")
		}
		httpObject["credential"] = map[string]any{"secret": *entry.apiKey}
	}
	return json.RawMessage(format.Encode(value)), nil
}

// redact keeps the link's key out of an error Core may have echoed.
func redact(message string, apiKey *string) string {
	if apiKey != nil && len(*apiKey) >= 8 && strings.Contains(message, *apiKey) {
		return "unable to add the service"
	}
	return message
}
