package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type resolverStore struct {
	endpoints []contract.Endpoint
}

func (store resolverStore) ListEndpoints(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error) {
	items := make([]storage.EndpointRecord, 0, len(store.endpoints))
	for _, candidate := range store.endpoints {
		items = append(items, storage.EndpointRecord{Endpoint: candidate})
	}
	return storage.EndpointPage{Items: items}, nil
}

type endpointReaderFunc func(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error)

func (function endpointReaderFunc) ListEndpoints(ctx context.Context, options storage.EndpointListOptions) (storage.EndpointPage, error) {
	return function(ctx, options)
}

func TestStoreResolverSelectsServiceBeforeCapabilityMode(t *testing.T) {
	reader := endpointReaderFunc(func(_ context.Context, options storage.EndpointListOptions) (storage.EndpointPage, error) {
		if options.Enabled == nil || !*options.Enabled || options.Limit != 200 {
			t.Fatalf("list options = %#v", options)
		}
		return storage.EndpointPage{Items: []storage.EndpointRecord{
			{Endpoint: resolverEndpoint("endpoint_01", contract.CapabilityModeDelegated, true, nil)},
			{Endpoint: resolverEndpoint("endpoint_02", contract.CapabilityModeNative, false, nil)},
			{Endpoint: resolverEndpoint("endpoint_03", contract.CapabilityModeNative, true, []string{"gpt-5"})},
			{Endpoint: resolverEndpoint("endpoint_04", contract.CapabilityModeNative, true, nil)},
		}}, nil
	})
	resolver, err := NewStoreResolver(reader)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Endpoint.ID != "endpoint_01" || resolved.Mode != contract.CapabilityModeDelegated {
		t.Fatalf("resolved endpoint = %q, want endpoint_01", resolved.Endpoint.ID)
	}
	resolved, err = resolver.Resolve(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "other", Streaming: true,
	})
	if err != nil || resolved.Endpoint.ID != "endpoint_01" {
		t.Fatalf("unrestricted resolution = %#v, %v", resolved, err)
	}
}

func TestStoreResolverUsesDelegatedOnlyWhenNoNativePathExists(t *testing.T) {
	reader := endpointReaderFunc(func(_ context.Context, _ storage.EndpointListOptions) (storage.EndpointPage, error) {
		return storage.EndpointPage{Items: []storage.EndpointRecord{
			{Endpoint: resolverEndpoint("endpoint_01", contract.CapabilityModeDelegated, true, nil)},
			{Endpoint: resolverEndpoint("endpoint_02", contract.CapabilityModeDelegated, true, nil)},
		}}, nil
	})
	resolver, err := NewStoreResolver(reader)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses, Model: "gpt-5", Streaming: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Endpoint.ID != "endpoint_01" || resolved.Mode != contract.CapabilityModeDelegated {
		t.Fatalf("delegated resolution = %#v", resolved)
	}
}

func TestStoreResolverTreatsEmptyServiceModelListAsNoAvailableModels(t *testing.T) {
	reader := endpointReaderFunc(func(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error) {
		return storage.EndpointPage{Items: []storage.EndpointRecord{{
			Endpoint: resolverEndpoint(
				"endpoint_empty_models",
				contract.CapabilityModeNative,
				true,
				[]string{},
			),
		}}}, nil
	})
	resolver, err := NewStoreResolver(reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), ResolveRequest{
		Protocol: contract.ProtocolOpenAIResponses,
		Model:    "gpt-5",
	})
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("Resolve() error = %v, want ErrNoEndpoint", err)
	}
	var capabilityErr *CapabilityUnavailableError
	if !errors.As(err, &capabilityErr) || capabilityErr.Model != "gpt-5" {
		t.Fatalf("Resolve() error = %#v, want requested model detail", err)
	}
}

func TestStoreResolverPaginatesAndFailsClosed(t *testing.T) {
	calls := 0
	resolver, err := NewStoreResolver(endpointReaderFunc(func(_ context.Context, options storage.EndpointListOptions) (storage.EndpointPage, error) {
		calls++
		if calls == 1 {
			return storage.EndpointPage{NextCursor: "next"}, nil
		}
		if options.Cursor != "next" {
			t.Fatalf("cursor = %q", options.Cursor)
		}
		return storage.EndpointPage{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), ResolveRequest{Protocol: contract.ProtocolOpenAIResponses}); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("Resolve error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("list calls = %d", calls)
	}

	privateErr := errors.New("private database detail")
	resolver, _ = NewStoreResolver(endpointReaderFunc(func(context.Context, storage.EndpointListOptions) (storage.EndpointPage, error) {
		return storage.EndpointPage{}, privateErr
	}))
	if _, err := resolver.Resolve(context.Background(), ResolveRequest{Protocol: contract.ProtocolOpenAIResponses}); !errors.Is(err, privateErr) {
		t.Fatalf("storage error = %v", err)
	}
	if _, err := NewStoreResolver(nil); err == nil {
		t.Fatal("nil endpoint reader was accepted")
	}
}

func TestStoreResolverResolveServiceFindsOnlyTheEnabledProvider(t *testing.T) {
	disabled := resolverEndpoint("endpoint_disabled", contract.CapabilityModeNative, true, nil)
	disabled.Enabled = false
	// Images API calls are outside protocol routing, so the model list and
	// Responses capability do not decide eligibility.
	images := resolverEndpoint("endpoint_images", contract.CapabilityModeDelegated, false, []string{"gpt-5"})
	resolver, err := NewStoreResolver(resolverStore{endpoints: []contract.Endpoint{disabled, images}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.ResolveService(context.Background(), "endpoint_images")
	if err != nil || resolved.Service.ID != "endpoint_images" || resolved.EffectiveBaseURL() != "https://api.example/v1" {
		t.Fatalf("ResolveService = %#v, %v", resolved, err)
	}
	if _, err := resolved.AuthorizationEndpoint(); err != nil {
		t.Fatalf("AuthorizationEndpoint: %v", err)
	}
	for _, id := range []contract.ServiceID{"endpoint_disabled", "endpoint_missing"} {
		if _, err := resolver.ResolveService(context.Background(), id); !errors.Is(err, ErrNoEndpoint) {
			t.Fatalf("ResolveService(%s) error = %v", id, err)
		}
	}
}

func resolverEndpoint(id contract.ServiceID, mode contract.CapabilityMode, streaming bool, models []string) contract.Endpoint {
	if models == nil {
		models = []string{"gpt-5", "other"}
	}
	return contract.Endpoint{
		ID: id, Name: string(id), Kind: contract.EndpointKindOpenAI,
		BaseURL: "https://api.example/v1", Auth: contract.EndpointAuth{Scheme: contract.AuthSchemeNone}, Enabled: true,
		Models: models,
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIResponses, Mode: mode, Streaming: streaming,
		}},
	}
}
