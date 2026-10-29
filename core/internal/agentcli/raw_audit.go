package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

const (
	maxRawReasonRunes = 500
	maxAgentNameLen   = 64
	// defaultRawWait matches how long the Control API keeps a request
	// pending; waiting longer cannot succeed.
	defaultRawWait = 10 * time.Minute
)

// rawPollInterval is how often a pending raw access request is checked.
var rawPollInterval = 2 * time.Second

var errRawAccessDisabled = errors.New("raw_access_disabled: the user turned off agent raw access requests in AstrLink; do not ask again")

func requiredString(arguments map[string]any, name string) (string, error) {
	value, ok := arguments[name].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return value, nil
}

// agentName bounds the agent's self-reported name.
func agentName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > maxAgentNameLen {
		name = strings.ToValidUTF8(name[:maxAgentNameLen], "")
	}
	return name
}

func (client *Client) progress(format string, args ...any) {
	if client.Progress != nil {
		_, _ = fmt.Fprintf(client.Progress, format+"\n", args...)
	}
}

// rawGrantView is the part of a grant's state the CLI reads. ExpiresAt is
// nil for a grant that lasts until the user locks.
type rawGrantView struct {
	GrantID   string     `json:"grant_id"`
	Status    string     `json:"status"`
	Decision  string     `json:"decision"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expires_at"`
}

const rawGrantUsage = "Use this same token for every raw read in this investigation, on any request: " +
	"`astrlink raw-audit <request_id> --grant <token>` reads without a new approval. " +
	"You may pass it to subagents working on this same investigation, only through their task prompt; they use --grant and never revoke. " +
	"Never write it into files, notes, code, or commits, and never carry it into an unrelated later task. " +
	"When the whole investigation ends (finished, unable to continue, or the user asked you to stop), " +
	"run `astrlink raw-revoke --grant <token>` exactly once, before giving your final result."

// requestRawAudit reads a request's raw parts. With --grant it reads through
// a lasting grant the user already approved. Otherwise it asks the user and
// waits for the decision; a lasting approval prints its token so the rest of
// the investigation reuses it, while a once approval's token stays in this
// process.
func requestRawAudit(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
	requestID, err := requiredID(arguments)
	if err != nil {
		return nil, err
	}
	auditPath := controlapi.RequestsPath + "/" + url.PathEscape(requestID) + "/audit"
	if token, _ := arguments["grant"].(string); strings.TrimSpace(token) != "" {
		return readRawAudit(ctx, client, auditPath, strings.TrimSpace(token), rawGrantView{})
	}
	reason, err := requiredString(arguments, "reason")
	if err != nil {
		return nil, fmt.Errorf("%w (or pass --grant with a token from an earlier lasting approval)", err)
	}
	if utf8.RuneCountInString(reason) > maxRawReasonRunes {
		return nil, fmt.Errorf("reason must be at most %d characters", maxRawReasonRunes)
	}
	wait := defaultRawWait
	if value, ok := arguments["wait"].(time.Duration); ok {
		if value <= 0 {
			return nil, fmt.Errorf("wait must be positive")
		}
		wait = min(value, defaultRawWait)
	}
	agent, _ := arguments["agent"].(string)
	if strings.TrimSpace(agent) == "" {
		agent = client.Agent
	}
	created, err := client.post(ctx, auditPath+"/raw-access", map[string]string{
		"reason": reason, "client_name": agentName(agent),
	})
	switch apiErrorCode(err) {
	case "":
		if err != nil {
			return nil, err
		}
	case "raw_access_disabled":
		return nil, errRawAccessDisabled
	case "raw_access_unavailable":
		return nil, fmt.Errorf("raw_access_unavailable: the user has not set a raw password in AstrLink, so raw content is not kept and cannot be approved; do not ask again")
	case "raw_access_limited":
		return nil, fmt.Errorf("raw_access_limited: too many raw access requests are awaiting the user's decision; wait for the user to decide")
	default:
		return nil, err
	}
	var grant struct {
		GrantID    string `json:"grant_id"`
		GrantToken string `json:"grant_token"`
	}
	if err := json.Unmarshal(created, &grant); err != nil || grant.GrantToken == "" {
		return nil, fmt.Errorf("control API returned no raw access grant")
	}
	client.progress("Waiting up to %s for the user to approve raw access request %s in the AstrLink desktop.", wait, grant.GrantID)

	deadline := time.Now().Add(wait)
	header := http.Header{controlapi.RawGrantHeader: {grant.GrantToken}}
	for {
		raw, err := client.do(ctx, http.MethodGet, controlapi.RawGrantPath, nil, nil, header)
		switch apiErrorCode(err) {
		case "":
			if err != nil {
				return nil, err
			}
			var state rawGrantView
			if err := json.Unmarshal(raw, &state); err != nil {
				return nil, fmt.Errorf("control API returned an unreadable raw grant: %w", err)
			}
			switch state.Status {
			case "pending":
			case "approved":
				return readRawAudit(ctx, client, auditPath, grant.GrantToken, state)
			case "denied":
				return nil, errRawAccessDenied
			default:
				return nil, errRawRequestLapsed
			}
		case "raw_access_disabled":
			return nil, errRawAccessDisabled
		case "raw_grant_invalid":
			return nil, errRawRequestLapsed
		default:
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("raw_access_pending: the user did not decide within %s; the request lapses on its own. Ask the user whether they still want to approve before requesting again", wait)
		}
		timer := time.NewTimer(min(rawPollInterval, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

var (
	errRawAccessDenied  = errors.New("raw_access_denied: the user denied raw access to this request; do not ask again unless the user asks you to")
	errRawRequestLapsed = errors.New("raw_grant_invalid: the raw access request expired, was revoked, or AstrLink restarted before it was read; ask the user before requesting again")
	errRawGrantInvalid  = errors.New("raw_grant_invalid: this raw grant token expired, was revoked, or is unknown; request raw access again with `astrlink raw-audit <id> --reason <why>`")
)

// readRawAudit reads one request's full audit through a grant. state is the
// grant just approved, or empty when the agent reuses a token.
func readRawAudit(ctx context.Context, client *Client, auditPath, token string, state rawGrantView) (json.RawMessage, error) {
	header := http.Header{controlapi.RawGrantHeader: {token}}
	raw, err := client.do(ctx, http.MethodGet, auditPath, url.Values{"view": {"full"}}, nil, header)
	switch apiErrorCode(err) {
	case "":
		if err != nil {
			return nil, err
		}
	case "raw_access_denied":
		return nil, errRawAccessDenied
	case "raw_access_disabled":
		return nil, errRawAccessDisabled
	case "raw_access_pending", "raw_grant_invalid":
		return nil, errRawGrantInvalid
	default:
		return nil, err
	}
	wrapped, err := annotateAuditPayload(raw, "raw")
	if err != nil {
		return nil, err
	}
	wrapped["status"] = "approved"
	if state.Scope == "all_requests" {
		grant := map[string]any{
			"grant_token": token,
			"decision":    state.Decision,
			"scope":       "all requests, including ones recorded while the grant runs",
			"expires_at":  state.ExpiresAt,
			"usage":       rawGrantUsage,
		}
		if state.ExpiresAt == nil {
			grant["ends"] = "when the user's screen locks, the computer sleeps, or AstrLink locks, quits, or restarts"
		}
		wrapped["raw_grant"] = grant
	}
	return json.Marshal(wrapped)
}

// revokeRawGrant gives up a lasting grant. Whoever holds the token may revoke
// it; revoking one that already ended reports its final state.
func revokeRawGrant(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
	token, err := requiredString(arguments, "grant")
	if err != nil {
		return nil, err
	}
	header := http.Header{controlapi.RawGrantHeader: {strings.TrimSpace(token)}}
	raw, err := client.do(ctx, http.MethodDelete, controlapi.RawGrantPath, nil, nil, header)
	switch apiErrorCode(err) {
	case "":
		if err != nil {
			return nil, err
		}
	case "raw_grant_invalid":
		return nil, fmt.Errorf("raw_grant_invalid: AstrLink does not know this grant token; it already ended when AstrLink restarted, or the token is wrong. Nothing is left to revoke")
	default:
		return nil, err
	}
	var state rawGrantView
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("control API returned an unreadable raw grant: %w", err)
	}
	return json.Marshal(map[string]string{"grant_id": state.GrantID, "status": state.Status})
}
