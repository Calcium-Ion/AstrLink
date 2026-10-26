package agentcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

// flagKind is the value type of a command flag.
type flagKind int

const (
	flagString flagKind = iota
	flagInt
	flagBool
	flagDuration
)

// commandFlag maps a command-line flag onto a Control API argument.
type commandFlag struct {
	Name  string
	Key   string
	Kind  flagKind
	Usage string
}

// command is one CLI command; raw-revoke only gives access up, the rest
// read. Arg names the positional argument ("" for none); it is stored under
// ArgKey.
type command struct {
	Name    string
	Arg     string
	ArgKey  string
	Summary string
	Detail  string
	Flags   []commandFlag
	Call    func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error)
}

var listFlags = []commandFlag{
	{Name: "limit", Key: "limit", Kind: flagInt, Usage: "maximum items to return (1-200)"},
	{Name: "cursor", Key: "cursor", Usage: "next_cursor from the previous page"},
	{Name: "from", Key: "from", Usage: "RFC3339 start timestamp"},
	{Name: "to", Key: "to", Usage: "RFC3339 end timestamp"},
	{Name: "protocol", Key: "protocol", Usage: "input protocol"},
	{Name: "service-id", Key: "service_id", Usage: "upstream service id"},
	{Name: "access-token-id", Key: "local_access_token_id", Usage: "local access token id"},
	{Name: "status", Key: "status", Usage: "request status, such as failed"},
}

func commandCatalog() []command {
	return []command{
		{
			Name:    "sessions",
			Summary: "List recent request sessions (grouped conversations).",
			Detail:  "List recent AstrLink request sessions (grouped conversations) with metadata only. turn_count is the number of user turns; call_count is the number of model calls, so an agent tool loop shows as 1 turn with many calls.",
			Flags:   listFlags,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.RequestSessionsPath, listQueryValues(arguments))
			},
		},
		{
			Name:    "session",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Show one session and its records.",
			Detail:  "Get one AstrLink request session and its records (metadata + trajectory events). Each record carries turn_index (1-based user turn shared by every call of one agent loop) and session_link (how it joined the session: explicit cursor, echoed id, or assistant-text fingerprint; null for the first record).",
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestSessionsPath+"/"+url.PathEscape(id), nil)
			},
		},
		{
			Name:    "requests",
			Summary: "List root request records.",
			Detail:  "List root AstrLink request records (metadata + trajectory events, no bodies).",
			Flags:   listFlags,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.RequestsPath, listQueryValues(arguments))
			},
		},
		{
			Name:    "search",
			Arg:     "text",
			ArgKey:  "q",
			Summary: "Find root request records by their input preview.",
			Detail:  "Search root AstrLink request records whose stored input preview contains text (case-insensitive literal text, not a pattern). Accepts the requests filters as well. Previews are short, so this finds requests by how they began, not by full body content.",
			Flags:   listFlags,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				q, ok := arguments["q"].(string)
				if !ok || strings.TrimSpace(q) == "" {
					return nil, fmt.Errorf("search text must be non-empty")
				}
				if utf8.RuneCountInString(q) > maxSearchRunes {
					return nil, fmt.Errorf("search text must be at most %d characters", maxSearchRunes)
				}
				query := listQueryValues(arguments)
				query.Set("q", q)
				return client.get(ctx, controlapi.RequestsPath, query)
			},
		},
		{
			Name:    "request",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Show one request record with routing and trajectory.",
			Detail:  "Get one AstrLink request record including events[] trajectory phases, routing_decision (why routing chose service_id: selected reason, and the higher-priority providers skipped with their reasons), turn_index, session_link, and cursors[] (the typed session cursors stored for linking; fingerprint values are keyed digests, never text).",
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id), nil)
			},
		},
		{
			Name:    "children",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "List failed retry attempts under a root request.",
			Detail:  "List failed retry attempts under a root AstrLink request record.",
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id)+"/children", nil)
			},
		},
		{
			Name:    "explain",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Summarize why one request ended the way it did.",
			Detail:  "Explain one AstrLink request: a summary (final status, HTTP status, service, attempt count, error, routing reason) plus the record, its failed retry attempts, and which audit parts were captured. It does not return bodies; run audit for those.",
			Call:    explainRequest,
		},
		{
			Name:    "audit",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Show the shareable request/response bodies of one request.",
			Detail:  "Get the shareable audit content for a request. Bodies are present only when the user enabled body capture in AstrLink. Parts the privacy policy did not clear (the client's original request, restored responses, uninspected parts) are withheld with a reason; every part carries content_view. privacy_findings lists what was found by kind and JSON path, never the values.",
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				raw, err := client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id)+"/audit",
					url.Values{"view": {"shareable"}})
				if err != nil {
					return nil, err
				}
				return annotateAudit(raw)
			},
		},
		{
			Name:    "raw-audit",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Read withheld raw parts after the user approves, or with a grant token.",
			Detail: "Ask the user to let you read the raw audit parts that audit withheld for one request. Use it only when a withheld part says raw_available: true and the shareable parts are not enough. " +
				"Raw content enters your context and is sent to the model provider you use, so first tell the user why you need it and pass that as --reason. " +
				"The command then waits while the user decides in the AstrLink desktop; never try to approve it yourself. " +
				"It prints the audit with content_view raw once approved, or exits with an error if the user denies it, raw access is unavailable, or the wait ends. " +
				"The user may approve one read, or approve until their screen locks or for 1 hour. A lasting approval covers every request and prints raw_grant.grant_token: " +
				"reuse it with --grant for the rest of the investigation, follow raw_grant.usage, and revoke it with raw-revoke when the investigation ends.",
			Flags: []commandFlag{
				{Name: "reason", Key: "reason", Usage: "why you need the raw parts, as you told the user; shown in the approval window (required without --grant)"},
				{Name: "grant", Key: "grant", Usage: "raw_grant.grant_token from an earlier lasting approval; reads without asking again"},
				{Name: "agent", Key: "agent", Usage: "your product name, shown to the user beside the request"},
				{Name: "wait", Key: "wait", Kind: flagDuration, Usage: "how long to wait for the decision (at most 10m)"},
			},
			Call: requestRawAudit,
		},
		{
			Name:    "raw-revoke",
			Summary: "Revoke a timed raw grant when the investigation ends.",
			Detail: "Revoke the timed raw grant whose token raw-audit printed, so it reads nothing more. " +
				"Only the agent that started the investigation runs it, exactly once, when the whole investigation ends and before giving its final result. " +
				"Subagents never revoke. Once approvals need no revoke.",
			Flags: []commandFlag{
				{Name: "grant", Key: "grant", Usage: "raw_grant.grant_token to revoke (required)"},
			},
			Call: revokeRawGrant,
		},
		{
			Name:    "audit-settings",
			Summary: "Show whether request/response bodies are being captured.",
			Detail:  "Read AstrLink audit settings to see whether request/response bodies are being captured.",
			Call: func(ctx context.Context, client *Client, _ map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.AuditSettingsPath, nil)
			},
		},
		{
			Name:    "routing",
			Summary: "Show model redirects, failover, retry, and identity settings.",
			Detail:  "Read AstrLink routing settings: model_redirects (client model → routed model rules; only enabled rules apply, exact case-sensitive match, one hop), failover and retry settings (default_failure_policy, allow_unmatched_failover, strategy, max_attempts), channel_stickiness, and the Codex/Claude/Grok subscription identity enforcement flags.",
			Call: func(ctx context.Context, client *Client, _ map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.RoutingSettingsPath, nil)
			},
		},
		{
			Name:    "services",
			Summary: "List configured upstream services without credentials.",
			Detail:  "List configured AstrLink upstream services: id, name, kind, enabled, models, capabilities, the base URL origin (scheme and host only), and subscription status. Credentials, credential references, proxy addresses, and provider account ids are never included.",
			Flags: []commandFlag{
				{Name: "enabled", Key: "enabled", Kind: flagBool, Usage: "only services with this enabled state (--enabled or --enabled=false)"},
			},
			Call: listServices,
		},
		{
			Name:    "service",
			Arg:     "id",
			ArgKey:  "id",
			Summary: "Show the health of one upstream service.",
			Detail:  "Get the health of one AstrLink service: its projected configuration, subscription token and risk state, recent risk events for subscription services, and a summary of its latest 20 request records (counts by status, last success, last failure with error code).",
			Call:    getServiceStatus,
		},
		{
			Name:    "privacy",
			Summary: "Show privacy policies without allowlist or pattern values.",
			Detail:  "Read AstrLink privacy policies: detector, enabled entity kinds, request/response actions, restore options, and match scope. Allowlist entries and custom regex patterns are reported as counts and types only; their values are never returned.",
			Call:    getPrivacyPolicy,
		},
	}
}

const maxSearchRunes = 200

func listQueryValues(arguments map[string]any) url.Values {
	query := url.Values{}
	for _, key := range []string{
		"limit", "cursor", "from", "to", "protocol", "service_id", "local_access_token_id", "status",
	} {
		value, ok := arguments[key]
		if !ok || value == nil {
			continue
		}
		query.Set(key, fmt.Sprint(value))
	}
	return query
}

func requiredID(arguments map[string]any) (string, error) {
	raw, ok := arguments["id"]
	if !ok {
		return "", fmt.Errorf("id is required")
	}
	id, ok := raw.(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("id must be a non-empty string")
	}
	return id, nil
}

var auditBodyParts = []string{"request_body", "response_content", "upstream_request_body", "upstream_response_content"}

// withheldReasonDetails explains each withheld reason to the agent.
var withheldReasonDetails = map[string]string{
	"privacy_redacted":  "The privacy policy redacted this before it went upstream; the upstream parts show what the model saw, with placeholders.",
	"privacy_blocked":   "The privacy policy blocked this request.",
	"privacy_restored":  "Placeholders in this response were restored to the original values.",
	"privacy_fail_open": "Privacy inspection failed and the request went upstream uninspected.",
	"privacy_pending":   "Privacy inspection had not finished when this was read.",
	"privacy_trusted":   "The request went upstream uninspected to a provider the user trusts, so it holds the original values.",
	"privacy_unknown":   "Captured before AstrLink recorded privacy decisions, or its inspection never finished.",
	"raw_locked":        "Raw reading is locked in the desktop.",
	"raw_not_kept":      "Captured before the user set a raw password in AstrLink, so the raw content was not kept.",
}

// annotateAudit wraps a shareable audit read for the agent.
func annotateAudit(raw json.RawMessage) (json.RawMessage, error) {
	wrapped, err := annotateAuditPayload(raw, "shareable")
	if err != nil {
		return raw, nil
	}
	return json.Marshal(wrapped)
}

// annotateAuditPayload labels every body part with content_view — the view
// its content came from, or "withheld" — and explains what was withheld.
func annotateAuditPayload(raw json.RawMessage, view string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	bodiesCaptured := false
	withheld := []string{}
	rawAvailable := false
	for _, name := range auditBodyParts {
		part, ok := payload[name].(map[string]any)
		if !ok {
			continue
		}
		bodiesCaptured = true
		if part["withheld"] == true {
			part["content_view"] = "withheld"
			if detail, ok := withheldReasonDetails[fmt.Sprint(part["reason"])]; ok {
				part["reason_detail"] = detail
			}
			withheld = append(withheld, name)
			rawAvailable = rawAvailable || part["raw_available"] == true
			continue
		}
		if part["exposure"] == "raw" {
			part["content_view"] = "raw"
		} else {
			part["content_view"] = "shareable"
		}
	}
	wrapped := map[string]any{
		"audit":           payload,
		"content_view":    view,
		"bodies_captured": bodiesCaptured,
	}
	switch {
	case !bodiesCaptured:
		wrapped["hint"] = "Request/response bodies were not captured for this request. Enable body audit in the AstrLink desktop (risk confirmation required). Metadata, trajectory events, and optional HTTP meta may still be present."
	case len(withheld) > 0 && view == "raw":
		wrapped["withheld_parts"] = withheld
	case len(withheld) > 0 && rawAvailable:
		wrapped["withheld_parts"] = withheld
		wrapped["hint"] = "Some parts are withheld. Work from the shareable parts and privacy_findings first. If you still need the raw parts, tell the user why, then run `astrlink raw-audit <id> --reason <why>`, or `astrlink raw-audit <id> --grant <token>` with a lasting grant from this same investigation; the user must approve new requests in the AstrLink desktop."
	case len(withheld) > 0:
		wrapped["withheld_parts"] = withheld
		wrapped["hint"] = "Some parts are withheld and raw access is not available: the user has not set a raw password, or has turned off agent raw access requests. Do not run raw-audit; work from the shareable parts and privacy_findings."
	}
	return wrapped, nil
}

func findCommand(name string) (command, bool) {
	for _, candidate := range commandCatalog() {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return command{}, false
}

func callCommand(ctx context.Context, client *Client, name string, arguments map[string]any) (json.RawMessage, error) {
	found, ok := findCommand(name)
	if !ok {
		return nil, fmt.Errorf("unknown command %q", name)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return found.Call(ctx, client, arguments)
}
