package privacy

import (
	"context"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type StorePolicyProvider struct {
	store storage.PolicyStore
}

func NewStorePolicyProvider(store storage.PolicyStore) (*StorePolicyProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("privacy policy store is required")
	}
	return &StorePolicyProvider{store: store}, nil
}

func (provider *StorePolicyProvider) RequestPolicy(ctx context.Context, _ Scope) (Policy, error) {
	record, err := provider.store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		return Policy{}, err
	}
	return FromContractPolicy(record.Policy)
}

func FromContractPolicy(policy contract.Policy) (Policy, error) {
	if err := contract.ValidatePrivacyDefault(policy); err != nil {
		return Policy{}, err
	}
	result := Policy{
		Enabled:         policy.Enabled,
		ResponseRestore: policy.ResponseRestore,
	}
	if policy.LocalModelID != nil {
		result.LocalModelID = *policy.LocalModelID
	}
	switch policy.Detector {
	case contract.PolicyDetectorRegex:
		result.Mode = ModeRegex
	case contract.PolicyDetectorLocalModel:
		result.Mode = ModeLocalModel
	default:
		return Policy{}, fmt.Errorf("unsupported privacy detector")
	}
	switch policy.RequestAction {
	case contract.PolicyActionAllow:
		result.Action = ActionAllow
	case contract.PolicyActionWarn:
		result.Action = ActionWarn
	case contract.PolicyActionBlock:
		result.Action = ActionBlock
	case contract.PolicyActionRedact:
		result.Action = ActionRedact
	default:
		return Policy{}, fmt.Errorf("unsupported privacy request action")
	}
	return result, nil
}

var _ PolicyProvider = (*StorePolicyProvider)(nil)
