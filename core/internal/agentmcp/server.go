package agentmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpServerName      = "astrlink"
	mcpServerVersion   = "0.1.0"
)

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ServeStdio runs a read-only MCP server on the given streams.
func ServeStdio(ctx context.Context, client *Client, input io.Reader, output io.Writer) error {
	if client == nil {
		return fmt.Errorf("control client is required")
	}
	reader := bufio.NewReader(input)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		payload, err := readMCPMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var request rpcRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			response, encodeErr := encodeResponse(nil, nil, &rpcError{Code: -32700, Message: "parse error"})
			if encodeErr != nil {
				return encodeErr
			}
			if err := writeMCPMessage(output, response); err != nil {
				return err
			}
			continue
		}
		if request.JSONRPC != "2.0" || request.Method == "" {
			if notificationID(request.ID) {
				continue
			}
			response, encodeErr := encodeResponse(request.ID, nil, &rpcError{Code: -32600, Message: "invalid request"})
			if encodeErr != nil {
				return encodeErr
			}
			if err := writeMCPMessage(output, response); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(request.Method, "notifications/") || notificationID(request.ID) {
			continue
		}
		result, rpcErr := dispatch(ctx, client, request)
		response, encodeErr := encodeResponse(request.ID, result, rpcErr)
		if encodeErr != nil {
			return encodeErr
		}
		if err := writeMCPMessage(output, response); err != nil {
			return err
		}
	}
}

func dispatch(ctx context.Context, client *Client, request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		version := mcpProtocolVersion
		var params initializeParams
		if len(request.Params) > 0 {
			_ = json.Unmarshal(request.Params, &params)
			if params.ProtocolVersion != "" {
				version = params.ProtocolVersion
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    mcpServerName,
				"version": mcpServerVersion,
			},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolsListPayload()}, nil
	case "tools/call":
		var params toolsCallParams
		if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "invalid tools/call params"}
		}
		payload, err := callTool(ctx, client, params.Name, params.Arguments)
		if err != nil {
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}, nil
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(payload)}},
			"isError": false,
		}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}
