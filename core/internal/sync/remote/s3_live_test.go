package remote

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// liveS3 describes a real S3-compatible store. The test is opt-in because it
// needs a bucket the runner owns:
//
//	ASTRLINK_TEST_S3_ENDPOINT=http://127.0.0.1:9090
//	ASTRLINK_TEST_S3_BUCKET=astrlink-sync-test
//	ASTRLINK_TEST_S3_ACCESS_KEY=... ASTRLINK_TEST_S3_SECRET_KEY=...
//
// It exists because the unit tests use an in-process fake, and only a real
// store can say whether it honours the conditional writes the lock needs.
type liveS3 struct {
	endpoint  string
	bucket    string
	accessKey string
	secretKey string
}

func liveS3FromEnv(t *testing.T) liveS3 {
	t.Helper()
	endpoint := os.Getenv("ASTRLINK_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set ASTRLINK_TEST_S3_ENDPOINT to exercise a live S3 store")
	}
	return liveS3{
		endpoint:  endpoint,
		bucket:    os.Getenv("ASTRLINK_TEST_S3_BUCKET"),
		accessKey: os.Getenv("ASTRLINK_TEST_S3_ACCESS_KEY"),
		secretKey: os.Getenv("ASTRLINK_TEST_S3_SECRET_KEY"),
	}
}

func (live liveS3) remote(t *testing.T) Remote {
	t.Helper()
	if live.bucket == "" {
		t.Fatal("ASTRLINK_TEST_S3_ENDPOINT is set without ASTRLINK_TEST_S3_BUCKET")
	}
	target, err := New(Config{
		Kind:      "s3",
		Endpoint:  live.endpoint,
		Path:      live.bucket,
		AccessKey: live.accessKey,
		SecretKey: live.secretKey,
	}, nil, time.Now)
	if err != nil {
		t.Fatalf("new s3 remote: %v", err)
	}
	return target
}

// clearObject removes an object without a condition, so a rerun of the test
// starts from an empty store rather than a leftover from a failed run.
func clearObject(t *testing.T, target Remote) {
	t.Helper()
	store, ok := target.(*s3)
	if !ok {
		t.Fatalf("live remote is %T, want *s3", target)
	}
	for _, name := range []string{packageName, lockName} {
		response, err := store.do(context.Background(), http.MethodDelete, name, nil, nil)
		if err != nil {
			t.Fatalf("clear %s: %v", name, err)
		}
		response.Body.Close()
	}
}

func TestLiveS3HonoursTheConditionalWritesTheLockNeeds(t *testing.T) {
	live := liveS3FromEnv(t)
	target := live.remote(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	clearObject(t, target)
	t.Cleanup(func() { clearObject(t, target) })

	identity, err := target.Identity(ctx)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if !strings.HasPrefix(identity, "s3:") {
		t.Fatalf("identity %q is not an s3 store identity", identity)
	}

	if _, _, exists, err := target.Read(ctx); err != nil || exists {
		t.Fatalf("empty read = %v, %v", exists, err)
	}

	first, err := target.Write(ctx, []byte("first"), "")
	if err != nil {
		// A store that refuses If-None-Match reports ErrUnsupported here, and
		// that is the finding this test exists to produce.
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("drive does not support conditional writes: %v", err)
		}
		t.Fatalf("create: %v", err)
	}
	// The create must be exclusive: a second unconditional create has to lose.
	if _, err := target.Write(ctx, []byte("second"), ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("second create = %v, want ErrPrecondition", err)
	}
	if _, err := target.Write(ctx, []byte("third"), "stale-etag"); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale write = %v, want ErrPrecondition", err)
	}
	payload, tag, exists, err := target.Read(ctx)
	if err != nil || !exists || string(payload) != "first" || tag != first {
		t.Fatalf("read = %q %q %v %v", payload, tag, exists, err)
	}
	if _, err := target.Write(ctx, []byte("fourth"), tag); err != nil {
		t.Fatalf("matching write: %v", err)
	}

	// The lock rides the same conditions, and two devices are what it exists to
	// serialize.
	clock := time.Now
	held, err := Acquire(ctx, target, "device_a", time.Minute, clock)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := Acquire(ctx, target, "device_b", time.Minute, clock); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	if err := held.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	if lock, _, exists, err := target.ReadLock(ctx); err != nil || exists {
		t.Fatalf("lock after release = %+v %v %v", lock, exists, err)
	}
	if _, err := Acquire(ctx, target, "device_b", time.Minute, clock); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}
