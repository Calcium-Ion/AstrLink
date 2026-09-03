package agentmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

type toolDef struct {
	Name        string
	Description string
	Schema      map[string]any
	Call        func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error)
}

func toolCatalog() []toolDef {
	listQuery := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit":                 map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
			"cursor":                map[string]any{"type": "string"},
			"from":                  map[string]any{"type": "string", "description": "RFC3339 start timestamp"},
			"to":                    map[string]any{"type": "string", "description": "RFC3339 end timestamp"},
			"protocol":              map[string]any{"type": "string"},
			"service_id":            map[string]any{"type": "string"},
			"local_access_token_id": map[string]any{"type": "string"},
			"status":                map[string]any{"type": "string"},
		},
	}
	idQuery := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"id": map[string]any{"type": "string"}},
		"required":             []string{"id"},
		"additionalProperties": false,
	}
	return []toolDef{
		{
			Name:        "list_request_sessions",
			Description: "List recent AstrLink request sessions (grouped conversations) with metadata only. turn_count is the number of user turns; call_count is the number of model calls, so an agent tool loop shows as 1 turn with many calls.",
			Schema:      listQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.RequestSessionsPath, listQueryValues(arguments))
			},
		},
		{
			Name:        "get_request_session",
			Description: "Get one AstrLink request session and its records (metadata + trajectory events). Each record carries turn_index (1-based user turn shared by every call of one agent loop) and session_link (how it joined the session: explicit cursor, echoed id, or assistant-text fingerprint; null for the first record).",
			Schema:      idQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestSessionsPath+"/"+url.PathEscape(id), nil)
			},
		},
		{
			Name:        "list_request_records",
			Description: "List root AstrLink request records (metadata + trajectory events, no bodies).",
			Schema:      listQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.RequestsPath, listQueryValues(arguments))
			},
		},
		{
			Name:        "get_request_record",
			Description: "Get one AstrLink request record including events[] trajectory phases, turn_index, session_link, and cursors[] (the typed session cursors stored for linking; fingerprint values are keyed digests, never text).",
			Schema:      idQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id), nil)
			},
		},
		{
			Name:        "get_request_children",
			Description: "List failed retry attempts under a root AstrLink request record.",
			Schema:      idQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				return client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id)+"/children", nil)
			},
		},
		{
			Name:        "get_request_audit",
			Description: "Get captured audit content for a request. Bodies are present only when the user enabled body capture in AstrLink.",
			Schema:      idQuery,
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				id, err := requiredID(arguments)
				if err != nil {
					return nil, err
				}
				raw, err := client.get(ctx, controlapi.RequestsPath+"/"+url.PathEscape(id)+"/audit", nil)
				if err != nil {
					return nil, err
				}
				return annotateAudit(raw)
			},
		},
		{
			Name:        "get_audit_settings",
			Description: "Read AstrLink audit settings to see whether request/response bodies are being captured.",
			Schema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			Call: func(ctx context.Context, client *Client, arguments map[string]any) (json.RawMessage, error) {
				return client.get(ctx, controlapi.AuditSettingsPath, nil)
			},
		},
	}
}

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

func annotateAudit(raw json.RawMessage) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw, nil
	}
	bodiesCaptured := payload["request_body"] != nil || payload["response_content"] != nil ||
		payload["upstream_request_body"] != nil || payload["upstream_response_content"] != nil
	wrapped := map[string]any{
		"audit":           payload,
		"bodies_captured": bodiesCaptured,
	}
	if !bodiesCaptured {
		wrapped["hint"] = "Request/response bodies were not captured for this request. Enable body audit in the AstrLink desktop (risk confirmation required). Metadata, trajectory events, and optional HTTP meta may still be present."
	}
	return json.Marshal(wrapped)
}

func toolsListPayload() []map[string]any {
	tools := toolCatalog()
	items := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		items = append(items, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.Schema,
		})
	}
	return items
}

func callTool(ctx context.Context, client *Client, name string, arguments map[string]any) (json.RawMessage, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	for _, tool := range toolCatalog() {
		if tool.Name == name {
			return tool.Call(ctx, client, arguments)
		}
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}
