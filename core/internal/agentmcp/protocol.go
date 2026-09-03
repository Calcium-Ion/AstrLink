package agentmcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxMCPMessageBytes = 16 << 20

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type flusher interface {
	Flush() error
}

func writeMCPMessage(writer io.Writer, payload []byte) error {
	if _, err := writer.Write(payload); err != nil {
		return err
	}
	if _, err := writer.Write([]byte{'\n'}); err != nil {
		return err
	}
	if flush, ok := writer.(flusher); ok {
		return flush.Flush()
	}
	return nil
}

func readMCPMessage(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(strings.TrimSpace(line)) > 0 {
				return ndjsonPayload(strings.TrimRight(line, "\r\n"))
			}
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		body := strings.TrimLeft(trimmed, " \t")
		if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
			return ndjsonPayload(trimmed)
		}
		return readContentLengthBody(reader, trimmed)
	}
}

func ndjsonPayload(line string) ([]byte, error) {
	if len(line) > maxMCPMessageBytes {
		return nil, fmt.Errorf("message too large")
	}
	return []byte(line), nil
}

func readContentLengthBody(reader *bufio.Reader, firstLine string) ([]byte, error) {
	contentLength := -1
	line := firstLine
	for {
		if line != "" {
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "content-length:") {
				value := strings.TrimSpace(line[len("content-length:"):])
				length, err := strconv.Atoi(value)
				if err != nil || length < 0 || length > maxMCPMessageBytes {
					return nil, fmt.Errorf("invalid Content-Length")
				}
				contentLength = length
			}
		}
		next, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(next, "\r\n")
		if line == "" {
			break
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func encodeResponse(id json.RawMessage, result any, rpcErr *rpcError) ([]byte, error) {
	response := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}
	if rpcErr != nil {
		response.Result = nil
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

func notificationID(id json.RawMessage) bool {
	if len(id) == 0 || string(id) == "null" {
		return true
	}
	return false
}
