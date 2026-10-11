// Package remote stores one sealed sync package on an untrusted store and
// holds the single write lock that keeps two devices from writing at once.
//
// Two transports are supported: WebDAV and an S3-compatible object store. Both
// expose the same conditional writes, so the engine's compare-and-swap is
// written once. The lock is one small object next to the package: it carries
// the owner, an expiry, and a nonce, and every write to it uses a condition so
// an expired holder cannot take it back.
package remote

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrPrecondition means the object changed since it was read, so the
	// caller has to read it again and retry. It is how a lost write is
	// reported rather than silently overwriting another device.
	ErrPrecondition = errors.New("remote object changed since it was read")
	// ErrUnreachable means the store could not be contacted.
	ErrUnreachable = errors.New("remote store is unreachable")
	// ErrUnauthorized means the credentials were refused.
	ErrUnauthorized = errors.New("remote store refused the credentials")
	// ErrUnsupported means the store does not offer a feature the sync needs,
	// such as a conditional write.
	ErrUnsupported = errors.New("remote store does not support the required operation")
)

// Lock is the single write lock on a remote. ExpiresAt lets a device that
// crashed release it by waiting, and the nonce is compared on release so a
// device whose lease lapsed cannot delete the lock a newer owner took.
type Lock struct {
	Owner     string    `json:"owner"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Remote is one configured store.
type Remote interface {
	// Identity returns a stable name for the store, written on first use. The
	// engine pins the value it first saw, so a package that turns out to live
	// somewhere else is refused.
	Identity(ctx context.Context) (string, error)
	// Read returns the stored package. exists is false when none is stored.
	Read(ctx context.Context) (payload []byte, etag string, exists bool, err error)
	// Write stores the package. An empty expectedETag creates the object only
	// if it is absent; otherwise the write applies only if the object still
	// carries that tag, so a concurrent writer's change is not overwritten.
	Write(ctx context.Context, payload []byte, expectedETag string) (etag string, err error)
	// ReadLock returns the current lock. exists is false when none is held.
	ReadLock(ctx context.Context) (lock Lock, etag string, exists bool, err error)
	// WriteLock takes or renews the lock. An empty expectedETag creates it only
	// if absent; otherwise it must still carry that tag.
	WriteLock(ctx context.Context, lock Lock, expectedETag string) (etag string, err error)
	// DeleteLock releases the lock, only if it still carries that tag.
	DeleteLock(ctx context.Context, expectedETag string) error
	// Describe names the store for a status message, without secrets.
	Describe() string
}
