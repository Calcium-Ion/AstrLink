package relaykitbridge

import (
	"context"
	"errors"

	"github.com/QuantumNous/astrlink/core/contract"
)

var ErrConversionUnavailable = errors.New("local protocol conversion is unavailable")

// NoopEngine is used before RelayKit is integrated. It never performs
// pass-through or best-effort conversion; native/delegated execution bypasses
// the engine instead.
type NoopEngine struct{}

var _ ConversionEngine = NoopEngine{}

func (NoopEngine) Version() string {
	return ""
}

func (NoopEngine) Edges() []contract.ConversionEdge {
	return []contract.ConversionEdge{}
}

func (NoopEngine) ConvertRequest(context.Context, ConvertRequestInput) (ConvertRequestOutput, error) {
	return ConvertRequestOutput{}, ErrConversionUnavailable
}

func (NoopEngine) ConvertResponse(context.Context, ConvertResponseInput) (ConvertResponseOutput, error) {
	return ConvertResponseOutput{}, ErrConversionUnavailable
}

func (NoopEngine) NewResponseStream(context.Context, StreamOptions) (ResponseStream, error) {
	return nil, ErrConversionUnavailable
}
