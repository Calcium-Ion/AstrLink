package subscription

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	forbiddenEscalationWindow    = 180 * time.Minute
	forbiddenEscalationThreshold = 3
	forbiddenCooldown            = 10 * time.Minute
	// Repeated identical signals within this window update the current risk
	// without adding history, so concurrent in-flight failures log once.
	riskEventDedupWindow = time.Minute
	// A 401 on a credential refreshed within this window needs a new login.
	unauthorizedRepeatWindow    = 2 * time.Minute
	ErrCodeUpstreamUnauthorized = "upstream_unauthorized"
)

var forbiddenRiskCodes = []string{contract.RiskCodeForbidden, contract.RiskCodeClientIdentityRejected}

var errRiskUnchanged = errors.New("subscription risk unchanged")

// RiskEventStore persists the bounded risk history of each account.
type RiskEventStore interface {
	AppendSubscriptionRiskEvent(context.Context, contract.SubscriptionRiskEvent) error
	ListSubscriptionRiskEvents(context.Context, contract.ServiceID, int) ([]contract.SubscriptionRiskEvent, error)
	CountSubscriptionRiskEvents(context.Context, contract.ServiceID, []string, time.Time) (int, error)
}

// SetRiskEventStore is configured before the manager starts serving requests.
func (manager *Manager) SetRiskEventStore(store RiskEventStore) {
	if store != nil {
		manager.riskEvents = store
	}
}

// ReportRisk records one classified upstream signal. Suspended risks pause
// scheduling until ClearRisk; cooling risks pause until PausedUntil.
func (manager *Manager) ReportRisk(ctx context.Context, id contract.ServiceID, observation contract.SubscriptionRiskObservation) error {
	now := manager.now().UTC()
	risk := contract.SubscriptionRisk{
		State:      observation.State,
		Code:       observation.Code,
		Message:    contract.SanitizeSubscriptionRiskMessage(observation.Message),
		HTTPStatus: observation.HTTPStatus,
		ObservedAt: now,
	}
	if observation.PausedUntil != nil {
		until := observation.PausedUntil.UTC()
		risk.PausedUntil = &until
	}
	if observation.Forbidden {
		count, err := manager.riskEvents.CountSubscriptionRiskEvents(ctx, id, forbiddenRiskCodes, now.Add(-forbiddenEscalationWindow))
		if err != nil {
			return err
		}
		if count+1 >= forbiddenEscalationThreshold {
			risk.State, risk.Code, risk.PausedUntil = contract.SubscriptionRiskSuspended, contract.RiskCodeRepeatedForbidden, nil
		} else {
			until := now.Add(forbiddenCooldown)
			risk.State, risk.PausedUntil = contract.SubscriptionRiskCooling, &until
		}
	}
	if err := risk.Validate(); err != nil {
		return fmt.Errorf("invalid subscription risk observation: %w", err)
	}
	record := true
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if account.Status != contract.SubscriptionStatusConnected || manager.lifecycleTransitions[id] {
			return errRiskUnchanged
		}
		current := account.Risk
		if current != nil && current.Blocks(now) {
			if current.State == contract.SubscriptionRiskSuspended && risk.State == contract.SubscriptionRiskCooling {
				return errRiskUnchanged
			}
			if current.State == risk.State && current.Code == risk.Code {
				risk.Occurrences = current.Occurrences + 1
				record = now.Sub(current.ObservedAt) >= riskEventDedupWindow
			}
			if risk.State == contract.SubscriptionRiskCooling && current.PausedUntil != nil && current.PausedUntil.After(*risk.PausedUntil) {
				risk.PausedUntil = current.PausedUntil
			}
		}
		if risk.Occurrences == 0 {
			risk.Occurrences = 1
		}
		account.Risk = &risk
		delete(manager.usageCache, id)
		account.UpdatedAt = now
		return nil
	})
	if errors.Is(err, errRiskUnchanged) || errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || !record {
		return err
	}
	kind := contract.SubscriptionRiskEventCooling
	if risk.State == contract.SubscriptionRiskSuspended {
		kind = contract.SubscriptionRiskEventSuspended
	}
	return manager.riskEvents.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
		ServiceID: id, Kind: kind, Code: risk.Code, Message: risk.Message,
		HTTPStatus: risk.HTTPStatus, ObservedAt: now, PausedUntil: risk.PausedUntil,
	})
}

// ClearRisk restores scheduling for an account paused by an upstream signal.
func (manager *Manager) ClearRisk(ctx context.Context, id contract.ServiceID) (contract.SubscriptionAccount, error) {
	now := manager.now().UTC()
	cleared := false
	account, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if account.Risk == nil {
			return errRiskUnchanged
		}
		account.Risk = nil
		delete(manager.usageCache, id)
		account.UpdatedAt = now
		cleared = true
		return nil
	})
	if errors.Is(err, errRiskUnchanged) {
		account, err = manager.Get(ctx, id)
	}
	if err != nil {
		return contract.SubscriptionAccount{}, err
	}
	if cleared {
		if err := manager.riskEvents.AppendSubscriptionRiskEvent(ctx, contract.SubscriptionRiskEvent{
			ServiceID: id, Kind: contract.SubscriptionRiskEventCleared, ObservedAt: now,
		}); err != nil {
			return contract.SubscriptionAccount{}, err
		}
	}
	return manager.publicAccount(account), nil
}

// ClearExpiredRisk drops a cooling risk whose pause window has passed.
func (manager *Manager) ClearExpiredRisk(ctx context.Context, id contract.ServiceID) error {
	now := manager.now().UTC()
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if !account.Risk.Expired(now) {
			return errRiskUnchanged
		}
		account.Risk = nil
		account.UpdatedAt = now
		return nil
	})
	if errors.Is(err, errRiskUnchanged) || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// RiskEvents lists an account's most recent risk history, newest first.
func (manager *Manager) RiskEvents(ctx context.Context, id contract.ServiceID, limit int) ([]contract.SubscriptionRiskEvent, error) {
	if _, err := manager.Get(ctx, id); err != nil {
		return nil, err
	}
	return manager.riskEvents.ListSubscriptionRiskEvents(ctx, id, limit)
}

// HandleUnauthorized refreshes a credential the provider rejected with 401.
// A second rejection shortly after a forced refresh requires a new login.
func (manager *Manager) HandleUnauthorized(ctx context.Context, id contract.ServiceID, rejectedAccessToken string) error {
	account, err := manager.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if account.Status != contract.SubscriptionStatusConnected || rejectedAccessToken == "" {
		return nil
	}
	now := manager.now()
	manager.mu.Lock()
	last := manager.forcedRefreshAt[id]
	manager.mu.Unlock()
	if !last.IsZero() && now.Sub(last) < unauthorizedRepeatWindow {
		current, err := manager.credentials.Get(ctx, id)
		if err != nil || current.AccessToken != rejectedAccessToken {
			return nil
		}
		return manager.markUnauthorized(ctx, id)
	}
	refreshed, err := manager.tokensFor(account.Provider).RefreshRejected(ctx, id, rejectedAccessToken)
	if refreshed {
		manager.mu.Lock()
		manager.forcedRefreshAt[id] = now
		manager.mu.Unlock()
	}
	return err
}

func (manager *Manager) markUnauthorized(ctx context.Context, id contract.ServiceID) error {
	_, err := manager.mutateAccount(ctx, id, func(account *contract.SubscriptionAccount) error {
		if account.Status != contract.SubscriptionStatusConnected || manager.lifecycleTransitions[id] {
			return errRiskUnchanged
		}
		account.Status = contract.SubscriptionStatusNeedsReauth
		delete(manager.usageCache, id)
		delete(manager.forcedRefreshAt, id)
		account.LastError = &contract.SubscriptionError{
			Code:    ErrCodeUpstreamUnauthorized,
			Message: "provider rejected the refreshed credential; sign in again",
		}
		account.UpdatedAt = manager.now().UTC()
		return nil
	})
	if errors.Is(err, errRiskUnchanged) {
		return nil
	}
	return err
}

// MemoryRiskEventStore keeps risk history in process for tests and headless wiring.
type MemoryRiskEventStore struct {
	mu     sync.Mutex
	nextID int64
	events []contract.SubscriptionRiskEvent
}

func NewMemoryRiskEventStore() *MemoryRiskEventStore { return &MemoryRiskEventStore{} }

func (store *MemoryRiskEventStore) AppendSubscriptionRiskEvent(_ context.Context, event contract.SubscriptionRiskEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.nextID++
	event.ID = store.nextID
	store.events = append(store.events, event)
	return nil
}

func (store *MemoryRiskEventStore) ListSubscriptionRiskEvents(_ context.Context, id contract.ServiceID, limit int) ([]contract.SubscriptionRiskEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	events := []contract.SubscriptionRiskEvent{}
	for _, event := range store.events {
		if event.ServiceID == id {
			events = append(events, event)
		}
	}
	sort.Slice(events, func(left, right int) bool { return events[left].ID > events[right].ID })
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

func (store *MemoryRiskEventStore) CountSubscriptionRiskEvents(_ context.Context, id contract.ServiceID, codes []string, since time.Time) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, event := range store.events {
		if event.ServiceID != id || event.ObservedAt.Before(since) {
			continue
		}
		for _, code := range codes {
			if event.Code == code {
				count++
				break
			}
		}
	}
	return count, nil
}
