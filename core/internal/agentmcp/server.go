package agentmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpServerName      = "astrlink"
	mcpServerVersion   = "0.1.2"
)

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Server is the read-only stdio MCP process. Handshake and tools/list do not
// touch the Control API. Client is an optional pre-dialed handle for tests;
// production leaves it nil and sets Options so the first tools/call Dials.
type Server struct {
	Client  *Client
	Options DialOptions
}

func (server *Server) clientForCall() (*Client, error) {
	if server != nil && server.Client != nil {
		return server.Client, nil
	}
	var options DialOptions
	if server != nil {
		options = server.Options
	}
	client, err := Dial(options)
	if err != nil {
		return nil, err
	}
	if server != nil {
		server.Client = client
	}
	return client, nil
}

// ServeStdio runs a read-only MCP server on the given streams.
// server may be nil; initialize and tools/list still succeed.
func ServeStdio(ctx context.Context, server *Server, input io.Reader, output io.Writer) error {
	if server == nil {
		server = &Server{}
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
		result, rpcErr := dispatch(ctx, server, request)
		response, encodeErr := encodeResponse(request.ID, result, rpcErr)
		if encodeErr != nil {
			return encodeErr
		}
		if err := writeMCPMessage(output, response); err != nil {
			return err
		}
	}
}

func dispatch(ctx context.Context, server *Server, request rpcRequest) (any, *rpcError) {
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
		client, err := server.clientForCall()
		if err != nil {
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}, nil
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
