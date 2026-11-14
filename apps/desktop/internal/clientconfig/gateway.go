package clientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// LocalOrigin returns `http://127.0.0.1:<port>` or `http://localhost:<port>`
// for the gateway's root inference URL; Core announces localhost only while
// it serves `[::1]`.
func LocalOrigin(inferenceURL string) (string, error) {
	u, err := url.Parse(inferenceURL)
	if err != nil {
		return "", errors.New("invalid inference URL")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.ToLower(u.Scheme) != "http" || host != "127.0.0.1" && host != "localhost" ||
		u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(inferenceURL, "#") || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid local inference URL")
	}
	origin := "http://" + host
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", errors.New("invalid inference URL")
		}
		if n != 80 {
			origin += ":" + strconv.FormatUint(n, 10)
		}
	}
	return origin, nil
}

// Gateway is the running gateway client configs are written for. The
// desktop host provides it from Core and its control API.
type Gateway interface {
	// Session is the gateway's current session: ok only while Core is
	// ready. Identity changes whenever Core starts again or announces
	// another address.
	Session() (inferenceURL, identity string, ok bool)
	// ListAccessTokens is the control API's access token page.
	ListAccessTokens(ctx context.Context) (json.RawMessage, error)
	// RevealAccessToken is a token's secret.
	RevealAccessToken(ctx context.Context, tokenID string) (string, error)
	// ModelMetadata is the official catalog's entry for a model, `null`
	// when it has none.
	ModelMetadata(ctx context.Context, model string) (json.RawMessage, error)
}

var errSessionChanged = errors.New("gateway session changed; reopen the dialog")

// RevealAccessToken reveals a token for the gateway session the dialog was
// opened against, so a restart in between cannot pair the token with a
// stale address.
func RevealAccessToken(ctx context.Context, gateway Gateway, tokenID, inferenceURL string) (string, error) {
	url, before, ok := gateway.Session()
	if !ok || url != inferenceURL {
		return "", errSessionChanged
	}
	secret, err := gateway.RevealAccessToken(ctx, tokenID)
	if err != nil {
		return "", err
	}
	if _, after, ok := gateway.Session(); !ok || after != before {
		return "", errSessionChanged
	}
	if secret == "" {
		return "", errors.New("access token is unavailable")
	}
	return secret, nil
}

// accessTokens reads the items of the access token page; false when the
// page has no list of them.
func accessTokens(ctx context.Context, gateway Gateway) ([]map[string]any, bool, error) {
	page, err := gateway.ListAccessTokens(ctx)
	if err != nil {
		return nil, false, err
	}
	var fields struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal(page, &fields) != nil || fields.Items == nil {
		return nil, false, nil
	}
	items := make([]map[string]any, 0, len(fields.Items))
	for _, raw := range fields.Items {
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		items = append(items, item)
	}
	return items, true, nil
}

// TokenSummary is a token's list entry: its display name and non-secret
// hint.
func TokenSummary(ctx context.Context, gateway Gateway, tokenID string) (name, hint string, err error) {
	items, _, err := accessTokens(ctx, gateway)
	if err != nil {
		return "", "", err
	}
	for _, item := range items {
		id, _ := item["id"].(string)
		name, nameOK := item["name"].(string)
		hint, hintOK := item["hint"].(string)
		if id == tokenID && nameOK && hintOK {
			return name, hint, nil
		}
	}
	return "", "", errors.New("access token not found")
}

// Tokens are the gateway's access tokens: each id with its non-secret
// hint. Every AstrLink on this computer writes the same client configs,
// and one written with a token the gateway lacks belongs to another
// AstrLink, such as the instance `make dev` runs beside an installed one.
// Token ids are random, so no two gateways share one.
type Tokens map[string]string

// TokensOf lists the gateway's tokens.
func TokensOf(ctx context.Context, gateway Gateway) (Tokens, error) {
	items, ok, err := accessTokens(ctx, gateway)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("access token list is invalid")
	}
	tokens := Tokens{}
	for _, item := range items {
		if id, ok := item["id"].(string); ok {
			hint, _ := item["hint"].(string)
			tokens[id] = hint
		}
	}
	return tokens, nil
}

// Own reports whether this gateway wrote record.
func (t Tokens) Own(record *Record) bool {
	_, ok := t[record.TokenID]
	return ok
}

// TokenHint is the hint the gateway lists for token.
func TokenHint(token string) string {
	if len(token) < 6 {
		return ""
	}
	return "astr_…" + token[len(token)-6:]
}

// Matching is the id of the token whose hint ends in the last characters
// of token, when exactly one does.
func (t Tokens) Matching(token string) (string, bool) {
	hint := TokenHint(token)
	if hint == "" {
		return "", false
	}
	found := ""
	for id, known := range t {
		if known != hint {
			continue
		}
		if found != "" {
			return "", false
		}
		found = id
	}
	return found, found != ""
}
