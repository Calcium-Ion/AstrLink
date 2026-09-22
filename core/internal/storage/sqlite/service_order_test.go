package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestServiceOrderPersistenceConflictsAndResolver(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "order.db")
	store := openTestStore(t, path)
	for _, id := range []contract.ServiceID{"service_z", "service_a", "service_b"} {
		service := pathTestService(id)
		if id == "service_z" {
			service.Capabilities[0].Mode = contract.CapabilityModeDelegated
		}
		if _, err := store.CreateService(ctx, service, storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	original, err := store.GetServiceOrder(ctx)
	if err != nil || !reflect.DeepEqual(original.Order.ServiceIDs, []contract.ServiceID{"service_z", "service_a", "service_b"}) {
		t.Fatalf("creation order: %+v %v", original, err)
	}
	for _, ids := range [][]contract.ServiceID{nil, {"service_z"}, {"service_z", "service_z", "service_a"}, {"service_z", "service_a", "missing"}} {
		if _, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: ids}, original.ETag); !errors.Is(err, storage.ErrInvalidArgument) {
			t.Fatalf("invalid order accepted %v: %v", ids, err)
		}
	}
	settings, _ := store.GetRoutingSettings(ctx)
	if settings.Strategy != contract.FailoverOnly || !settings.AllowUnmatchedFailover {
		t.Fatalf("defaults: %+v", settings)
	}
	settings.AllowUnmatchedFailover = true
	if err := store.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	resolver, _ := endpoint.NewStoreResolver(store)
	request := endpoint.ResolveRequest{Protocol: contract.ProtocolOpenAIResponses, Model: "public", Streaming: true}
	snapshot, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || len(snapshot) != 3 || snapshot[0].CanonicalService().ID != "service_z" {
		t.Fatalf("priority must outrank native mode: %v %v", snapshot, err)
	}
	reordered, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: []contract.ServiceID{"service_b", "service_z", "service_a"}}, original.ETag)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateServiceOrder(ctx, original.Order, original.ETag); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale write: %v", err)
	}
	fresh, err := resolver.ResolveCandidates(ctx, request)
	if err != nil || fresh[0].CanonicalService().ID != "service_b" || snapshot[0].CanonicalService().ID != "service_z" {
		t.Fatal("new order did not isolate request snapshots")
	}
	// Disabled services retain their position and resume it when enabled again.
	record, _ := store.GetService(ctx, "service_b")
	record.Service.Enabled = false
	record, err = store.UpdateService(ctx, record.Service, storage.CredentialMutation{}, record.ETag)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = resolver.ResolveCandidates(ctx, request)
	if err != nil || len(fresh) != 2 || fresh[0].CanonicalService().ID != "service_z" {
		t.Fatalf("disabled: %v %v", fresh, err)
	}
	record.Service.Enabled = true
	if _, err := store.UpdateService(ctx, record.Service, storage.CredentialMutation{}, record.ETag); err != nil {
		t.Fatal(err)
	}
	// Concurrent membership changes invalidate the list ETag.
	if _, err := store.CreateService(ctx, pathTestService("service_new"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateServiceOrder(ctx, reordered.Order, reordered.ETag); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("membership conflict: %v", err)
	}
	beforeClose, _ := store.GetServiceOrder(ctx)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, path)
	defer reopened.Close()
	afterClose, err := reopened.GetServiceOrder(ctx)
	if err != nil || !reflect.DeepEqual(beforeClose, afterClose) {
		t.Fatalf("restart: %+v %v", afterClose, err)
	}
}

func TestConcurrentServiceReordersAreAtomic(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "order.db"))
	defer store.Close()
	ctx := context.Background()
	for _, id := range []contract.ServiceID{"service_a", "service_b", "service_c"} {
		if _, err := store.CreateService(ctx, pathTestService(id), storage.CredentialMutation{}); err != nil {
			t.Fatal(err)
		}
	}
	original, _ := store.GetServiceOrder(ctx)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, ids := range [][]contract.ServiceID{{"service_b", "service_a", "service_c"}, {"service_c", "service_b", "service_a"}} {
		wg.Add(1)
		go func(ids []contract.ServiceID) {
			defer wg.Done()
			<-start
			_, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: ids}, original.ETag)
			results <- err
		}(ids)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, storage.ErrPrecondition) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestSubscriptionUpsertsAppendOnceAndRetainOrder(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "subscriptions.db"))
	defer store.Close()
	ctx := context.Background()
	if _, err := store.CreateService(ctx, pathTestService("service_z"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	account := contract.SubscriptionAccount{ID: "service_a", DisplayName: "Subscription", Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusDisconnected, Capabilities: contract.DefaultOpenAICodexCapabilities(), CreatedAt: now, UpdatedAt: now}
	if err := store.PutSubscriptionAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	order, _ := store.GetServiceOrder(ctx)
	if !reflect.DeepEqual(order.Order.ServiceIDs, []contract.ServiceID{"service_z", "service_a"}) {
		t.Fatal(order)
	}
	if _, err := store.UpdateServiceOrder(ctx, contract.ServiceOrder{ServiceIDs: []contract.ServiceID{"service_a", "service_z"}}, order.ETag); err != nil {
		t.Fatal(err)
	}
	account.DisplayName = "Renamed"
	if err := store.PutSubscriptionAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	order, _ = store.GetServiceOrder(ctx)
	if !reflect.DeepEqual(order.Order.ServiceIDs, []contract.ServiceID{"service_a", "service_z"}) {
		t.Fatal(order)
	}
}
