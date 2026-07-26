package privacy

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type fakePolicyStore struct {
	record storage.PolicyRecord
	err    error
}

func (store *fakePolicyStore) GetPolicy(context.Context, contract.PolicyID) (storage.PolicyRecord, error) {
	return store.record, store.err
}

func (store *fakePolicyStore) ListPolicies(context.Context) (storage.PolicyPage, error) {
	return storage.PolicyPage{}, nil
}

func (store *fakePolicyStore) UpdatePolicy(context.Context, contract.Policy, string) (storage.PolicyRecord, error) {
	return storage.PolicyRecord{}, nil
}

func TestStorePolicyProviderMapsFrozenContract(t *testing.T) {
	policy := contract.DefaultPrivacyPolicy()
	policy.Enabled = true
	policy.Detector = contract.PolicyDetectorOpenAIPrivacyFilter
	modelID := contract.LegacyOpenAIPrivacyFilterInstallationID
	policy.LocalModelID = &modelID
	policy.RequestAction = contract.PolicyActionWarn
	provider, err := NewStorePolicyProvider(&fakePolicyStore{
		record: storage.PolicyRecord{Policy: policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := provider.RequestPolicy(context.Background(), Scope{
		Protocol: contract.ProtocolOpenAIResponses,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Enabled || resolved.Mode != ModeOpenAIPrivacyFilter ||
		resolved.LocalModelID != modelID || resolved.Action != ActionWarn ||
		!resolved.ResponseRestore {
		t.Fatalf("resolved policy = %#v", resolved)
	}
}

func TestStorePolicyProviderPropagatesFailureForEngineSanitization(t *testing.T) {
	private := errors.New("database contained alice@example.com")
	provider, err := NewStorePolicyProvider(&fakePolicyStore{err: private})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.RequestPolicy(context.Background(), Scope{}); !errors.Is(err, private) {
		t.Fatalf("RequestPolicy error = %v", err)
	}
	engine, err := New(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ResolvePolicy(context.Background(), Scope{}); !errors.Is(err, ErrPolicyUnavailable) {
		t.Fatalf("ResolvePolicy error = %v", err)
	}
}

func TestContractAllowActionResolvesToDisabledExecution(t *testing.T) {
	policy := contract.DefaultPrivacyPolicy()
	policy.Enabled = true
	policy.RequestAction = contract.PolicyActionAllow
	provider, err := NewStorePolicyProvider(&fakePolicyStore{
		record: storage.PolicyRecord{Policy: policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolvePolicy(context.Background(), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Enabled {
		t.Fatalf("allow policy should bypass request inspection: %#v", resolved)
	}
}

func TestNewStorePolicyProviderRequiresStore(t *testing.T) {
	if _, err := NewStorePolicyProvider(nil); err == nil {
		t.Fatal("NewStorePolicyProvider accepted nil store")
	}
}
