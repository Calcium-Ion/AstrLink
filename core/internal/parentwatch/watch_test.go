package parentwatch

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotifyContextWithoutParentPIDStaysIndependent(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel, err := notifyContext(context.Background(), 0, 0, func() (int, bool) {
		calls.Add(1)
		return 99, true
	})
	if err != nil {
		t.Fatalf("notifyContext: %v", err)
	}
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatal("independent Core context was cancelled")
	case <-time.After(20 * time.Millisecond):
	}
	if calls.Load() != 0 {
		t.Fatalf("parent lookup calls = %d, want 0", calls.Load())
	}
}

func TestNotifyContextCancelsWhenParentChanges(t *testing.T) {
	var pid atomic.Int64
	pid.Store(42)
	ctx, cancel, err := notifyContext(context.Background(), 42, time.Millisecond, func() (int, bool) {
		return int(pid.Load()), true
	})
	if err != nil {
		t.Fatalf("notifyContext: %v", err)
	}
	defer cancel()

	pid.Store(7)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not cancelled after parent PID changed")
	}
}

func TestNotifyContextRejectsInitialParentMismatch(t *testing.T) {
	ctx, cancel, err := notifyContext(context.Background(), 42, time.Second, func() (int, bool) {
		return 7, true
	})
	if ctx != nil || cancel != nil {
		t.Fatalf("mismatch returned context=%v cancel=%v", ctx, cancel)
	}
	if err == nil || !strings.Contains(err.Error(), "expected 42, got 7") {
		t.Fatalf("error = %v, want parent mismatch", err)
	}
}

func TestNotifyContextUnsupportedPlatformUsesParentContext(t *testing.T) {
	parent, stopParent := context.WithCancel(context.Background())
	ctx, cancel, err := notifyContext(parent, 42, time.Millisecond, func() (int, bool) {
		return 0, false
	})
	if err != nil {
		t.Fatalf("notifyContext: %v", err)
	}
	defer cancel()

	stopParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context did not follow its parent")
	}
}

func TestNotifyContextRejectsInvalidConfiguration(t *testing.T) {
	lookup := func() (int, bool) { return 1, true }
	for name, testCase := range map[string]struct {
		pid      int
		interval time.Duration
	}{
		"negative pid":  {-1, time.Second},
		"zero interval": {1, 0},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel, err := notifyContext(context.Background(), testCase.pid, testCase.interval, lookup)
			if ctx != nil || cancel != nil || err == nil {
				t.Fatalf("notifyContext = (%v, %v, %v), want configuration error", ctx, cancel, err)
			}
		})
	}
}
