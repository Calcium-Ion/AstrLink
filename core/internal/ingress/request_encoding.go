package ingress

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// maxDecodedRequestBodyBytes bounds a compressed request body, on the wire and
// decoded, when no request size limit is set: a few kilobytes of gzip or zstd
// can expand to gigabytes.
const maxDecodedRequestBodyBytes = 256 << 20

var (
	errInvalidContentEncoding = errors.New("request body does not decode as its Content-Encoding says")
	errDecodedBodyTooLarge    = errors.New("decompressed request body exceeds the size limit")
)

// decodedBodyLimit is the bound decodeRequestBody applies and the error it
// reports past it: the configured request size limit, or
// maxDecodedRequestBodyBytes when there is none.
func (handler *Handler) decodedBodyLimit() (int64, error) {
	if handler.maxRequestBodyBytes > 0 {
		return handler.maxRequestBodyBytes, errMetadataTooLarge
	}
	return maxDecodedRequestBodyBytes, errDecodedBodyTooLarge
}

// decodeRequestBody decodes a body the client compressed in a coding that
// transport.DecodeBody knows: gzip, deflate, br or zstd. Routing, privacy
// inspection, rewrites and records then read the JSON, and the upstream gets
// the decoded body without the coding. It returns the body headers the client
// sent, for the request record. A body in any other coding is left as it is:
// routes that read the body reject it, and an opaque route forwards it.
func decodeRequestBody(request *http.Request, limit int64, tooLarge error) (http.Header, error) {
	values := request.Header.Values("Content-Encoding")
	encoding := strings.Join(values, ",")
	if !encodedBody(encoding) || !transport.DecodableContentEncoding(encoding) ||
		request.Body == nil || request.Body == http.NoBody {
		return nil, nil
	}
	raw, err := readRequestBody(request.Body, limit)
	if errors.Is(err, errMetadataTooLarge) {
		return nil, tooLarge
	}
	if err != nil {
		return nil, err
	}
	body, err := transport.DecodeBody(raw, encoding, limit)
	if errors.Is(err, transport.ErrResponseBodyTooLarge) {
		return nil, tooLarge
	}
	if err != nil {
		return nil, errInvalidContentEncoding
	}
	sent := http.Header{"Content-Encoding": values}
	if length := request.Header.Values("Content-Length"); len(length) > 0 {
		sent["Content-Length"] = length
	}
	replaceRecoveryRequestBody(request, body)
	request.Header.Del("Content-Encoding")
	return sent, nil
}

// encodedBody reports whether contentEncoding names a coding other than
// identity.
func encodedBody(contentEncoding string) bool {
	for _, coding := range strings.Split(contentEncoding, ",") {
		if coding = strings.ToLower(strings.TrimSpace(coding)); coding != "" && coding != "identity" {
			return true
		}
	}
	return false
}
