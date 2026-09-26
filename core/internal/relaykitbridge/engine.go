// Package relaykitbridge is the only place where a future RelayKit dependency
// may be adapted. Core contracts and transport code must not import RelayKit
// types directly.
package relaykitbridge

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

type ConvertRequestInput struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	ContentType   string
	Body          []byte
	PublicModel   string
	UpstreamModel string
	Streaming     bool
}

type ConvertRequestOutput struct {
	ContentType string
	Body        []byte
}

type ConvertResponseInput struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	StatusCode    int
	ContentType   string
	Body          []byte
	PublicModel   string
	UpstreamModel string
}

type ConvertResponseOutput struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type StreamOptions struct {
	From          contract.ProtocolID
	To            contract.ProtocolID
	PublicModel   string
	UpstreamModel string
	ID            string
	Created       int64
	IncludeUsage  bool
}

// ResponseEvent is an engine-neutral, stateful stream unit. Adapters decide
// how protocol-specific wire events map to and from this boundary.
type ResponseEvent struct {
	Type string
	Data []byte
}

type ResponseStream interface {
	Convert(context.Context, ResponseEvent) ([]ResponseEvent, error)
	Finalize(context.Context) ([]ResponseEvent, error)
	Close() error
}

// ConversionEngine is deliberately thin. Native and delegated plans bypass
// it completely; only an explicit relaykit execution plan may invoke it.
type ConversionEngine interface {
	Version() string
	Edges() []contract.ConversionEdge
	ConvertRequest(context.Context, ConvertRequestInput) (ConvertRequestOutput, error)
	ConvertResponse(context.Context, ConvertResponseInput) (ConvertResponseOutput, error)
	NewResponseStream(context.Context, StreamOptions) (ResponseStream, error)
}
