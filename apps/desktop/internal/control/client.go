package control

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNotRunning is returned while Core has no control address to call.
var ErrNotRunning = errors.New("astrlink-core is not running")

// Caller sends one request to Core's control API and returns the response
// body and its ETag. Package core implements it for the running Core; tests
// and other packages depend on the interface only.
type Caller interface {
	Call(ctx context.Context, req Request) (Response, error)
}

// Request is one control API call. Body is sent as JSON when it is not nil.
type Request struct {
	Method string
	// Path starts with /control/v1 and may carry a query string.
	Path string
	Body any
	// IfMatch is sent as the If-Match header when it is not empty.
	IfMatch string
}

// Response is what Core answered.
type Response struct {
	Status int
	ETag   string
	Body   json.RawMessage
}
