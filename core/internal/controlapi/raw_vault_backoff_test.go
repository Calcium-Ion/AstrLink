package controlapi

import (
	"context"
	"errors"
	"testing"
	"time"
)

// wrongProofDelays records the wait each wrong password starts, moving the
// clock past every wait so the next guess is checked.
func wrongProofDelays(t *testing.T, vault *Vault, clock *rawTestClock, attempts int) []time.Duration {
	t.Helper()
	ctx := context.Background()
	delays := make([]time.Duration, 0, attempts)
	for range attempts {
		if _, err := vault.Verify(ctx, passwordProof("wrong password")); !errors.Is(err, ErrRawPasswordInvalid) {
			t.Fatalf("wrong password = %v", err)
		}
		status, err := vault.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		delays = append(delays, status.RetryAfter)
		clock.Advance(status.RetryAfter)
	}
	return delays
}

func wantDelays(t *testing.T, got []time.Duration, want ...time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("delays = %v, want %v", got, want)
		}
	}
}

func TestRawVaultBackoffKeepsItsThirtySecondCapByDefault(t *testing.T) {
	store := newRawAccessFixture(t, nil).store
	clock := newRawTestClock()
	vault := NewRawVault(store, RawVaultOptions{KDF: rawVaultTestKDF, Now: clock.Now})
	mustSetRawPassword(t, vault, rawTestPassword, RawProof{})
	wantDelays(t, wrongProofDelays(t, vault, clock, 9),
		0, 0, time.Second, 2*time.Second, 4*time.Second, 8*time.Second, 16*time.Second, 30*time.Second, 30*time.Second)
}

func TestRawVaultBackoffCapCanBeRaised(t *testing.T) {
	store := newRawAccessFixture(t, nil).store
	clock := newRawTestClock()
	vault := NewRawVault(store, RawVaultOptions{KDF: rawVaultTestKDF, Now: clock.Now, BackoffCap: 15 * time.Minute})
	mustSetRawPassword(t, vault, rawTestPassword, RawProof{})
	// The same two free failures and doubling, only stopping later.
	wantDelays(t, wrongProofDelays(t, vault, clock, 14),
		0, 0, time.Second, 2*time.Second, 4*time.Second, 8*time.Second, 16*time.Second, 32*time.Second,
		64*time.Second, 128*time.Second, 256*time.Second, 512*time.Second, 15*time.Minute, 15*time.Minute)

	// While blocked even the right password waits.
	if _, err := vault.Verify(context.Background(), passwordProof("wrong password")); !errors.Is(err, ErrRawPasswordInvalid) {
		t.Fatalf("wrong password = %v", err)
	}
	var backoff *RawBackoffError
	if _, err := vault.Verify(context.Background(), passwordProof(rawTestPassword)); !errors.As(err, &backoff) || backoff.Remaining != 15*time.Minute {
		t.Fatalf("right password while blocked = %v", err)
	}
	// The backoff lives in memory: a restarted Core over the same store
	// checks the password at once.
	restarted := NewRawVault(store, RawVaultOptions{KDF: rawVaultTestKDF, Now: clock.Now, BackoffCap: 15 * time.Minute})
	status, err := restarted.Status(context.Background())
	if err != nil || status.RetryAfter != 0 {
		t.Fatalf("restarted vault status = %+v, %v", status, err)
	}
	if _, err := restarted.Verify(context.Background(), passwordProof(rawTestPassword)); err != nil {
		t.Fatalf("right password after restart = %v", err)
	}
}
