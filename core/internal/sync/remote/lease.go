package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrLocked means another device holds the remote lock and its lease has not
// expired. It is not a failure: the caller waits for the next run.
var ErrLocked = errors.New("another device is syncing to this remote")

// DefaultLease is how long a taken lock is held before another device may take
// it. It is longer than one sync takes, so a healthy device does not lose the
// lock mid-run, and short enough that a device that crashed frees it without
// manual repair.
const DefaultLease = 5 * time.Minute

// Lease is the remote write lock this process holds.
type Lease struct {
	remote  Remote
	owner   string
	nonce   string
	etag    string
	expires time.Time
	ttl     time.Duration
	now     func() time.Time
}

// Acquire takes the single remote write lock for owner. It returns ErrLocked
// when another device holds it and the lease has not expired. A lock whose
// lease has lapsed, or one this owner already holds, is taken over.
//
// The take-or-renew write is conditional on the entity tag read, so two
// devices that race both cannot win: one gets ErrPrecondition and reports
// ErrLocked.
func Acquire(ctx context.Context, target Remote, owner string, ttl time.Duration, now func() time.Time) (*Lease, error) {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = DefaultLease
	}
	if owner == "" {
		return nil, fmt.Errorf("a remote lock needs an owner")
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil, err
	}
	attempt := Lock{Owner: owner, Nonce: nonce, ExpiresAt: now().UTC().Add(ttl)}
	for tries := 0; tries < 3; tries++ {
		current, etag, exists, err := target.ReadLock(ctx)
		if err != nil {
			return nil, err
		}
		expected := ""
		if exists {
			if current.Owner != owner && current.ExpiresAt.After(now().UTC()) {
				return nil, ErrLocked
			}
			expected = etag
		}
		stored, err := target.WriteLock(ctx, attempt, expected)
		if errors.Is(err, ErrPrecondition) {
			// Another device changed the lock between the read and the write.
			continue
		}
		if err != nil {
			return nil, err
		}
		return &Lease{remote: target, owner: owner, nonce: nonce, etag: stored, expires: attempt.ExpiresAt, ttl: ttl, now: now}, nil
	}
	// The lock moved on every attempt, so another device is active.
	return nil, ErrLocked
}

// Renew extends the lease, so a long sync does not lose the lock. It fails
// with ErrLocked when the lease lapsed and another device took it.
func (lease *Lease) Renew(ctx context.Context) error {
	if lease == nil {
		return fmt.Errorf("no remote lock is held")
	}
	current, etag, exists, err := lease.remote.ReadLock(ctx)
	if err != nil {
		return err
	}
	if !exists || current.Nonce != lease.nonce {
		return ErrLocked
	}
	lease.expires = lease.now().UTC().Add(lease.ttl)
	next := Lock{Owner: lease.owner, Nonce: lease.nonce, ExpiresAt: lease.expires}
	stored, err := lease.remote.WriteLock(ctx, next, etag)
	if errors.Is(err, ErrPrecondition) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	lease.etag = stored
	return nil
}

// Release drops the lock. A lapsed lease is left alone, so a device that
// overran its lease cannot delete the lock a newer owner took.
func (lease *Lease) Release(ctx context.Context) error {
	if lease == nil {
		return nil
	}
	current, etag, exists, err := lease.remote.ReadLock(ctx)
	if err != nil {
		return err
	}
	if !exists || current.Nonce != lease.nonce {
		return nil
	}
	err = lease.remote.DeleteLock(ctx, etag)
	if errors.Is(err, ErrPrecondition) {
		return nil
	}
	return err
}

func randomNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate lock nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
