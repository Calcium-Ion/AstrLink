package ingress

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/QuantumNous/astrlink/core/contract"
)

func adaptRelayKitRequest(
	request *http.Request,
	protocol contract.ProtocolID,
	streaming bool,
	model string,
	body []byte,
) error {
	if model == "" {
		return fmt.Errorf("relaykit upstream model is required")
	}
	path := ""
	query := url.Values{}
	switch protocol {
	case contract.ProtocolOpenAIChat:
		path = "/v1/chat/completions"
	case contract.ProtocolOpenAIResponses:
		path = "/v1/responses"
	case contract.ProtocolAnthropicMessages:
		path = "/v1/messages"
		if request.Header.Get("anthropic-version") == "" {
			request.Header.Set("anthropic-version", "2023-06-01")
		}
	case contract.ProtocolGoogleGenerateContent:
		suffix := ":generateContent"
		if streaming {
			suffix = ":streamGenerateContent"
			query.Set("alt", "sse")
		}
		path = "/v1beta/models/" + url.PathEscape(model) + suffix
	default:
		return fmt.Errorf("unsupported relaykit upstream protocol %q", protocol)
	}
	request.URL.Path, request.URL.RawPath, request.URL.RawQuery = path, "", query.Encode()
	request.Header.Del("Accept-Encoding")
	request.Header.Del("Content-Length")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	request.ContentLength = int64(len(body))
	request.TransferEncoding = nil
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return nil
}
