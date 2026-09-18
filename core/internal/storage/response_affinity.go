package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type ResponseAffinity struct {
	ServiceID        contract.ServiceID
	UpstreamModel    string
	UpstreamProtocol contract.ProtocolID
	PlanType         contract.PlanType
}

// ResponseAffinityStore uses only an exact response id under the same local
// principal. Conversation fingerprints must never be used to route requests.
type ResponseAffinityStore interface {
	GetResponseAffinity(context.Context, string, string) (ResponseAffinity, bool, error)
	PutResponseAffinity(context.Context, string, string, ResponseAffinity) error
}
