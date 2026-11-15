package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTree is a process tree whose running count the test sets.
type fakeTree struct {
	mu         sync.Mutex
	running    uint32
	err        error
	terminated bool
	closed     bool
}

func (f *fakeTree) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeTree) Terminate() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = true
	return f.err
}

func (f *fakeTree) Running() (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, f.err
}

func (f *fakeTree) set(running uint32) {
	f.mu.Lock()
	f.running = running
	f.mu.Unlock()
}

func TestPruningForgetsAndClosesTreesWithoutRunningProcesses(t *testing.T) {
	two, done, broken, one := &fakeTree{running: 2}, &fakeTree{}, &fakeTree{running: 5, err: errors.New("handle closed")}, &fakeTree{running: 1}
	kept, running := pruneRetired([]processTree{two, done, broken, one})
	if running != 3 || len(kept) != 2 || kept[0] != two || kept[1] != one {
		t.Fatal(running, kept)
	}
	if !done.closed || !broken.closed || two.closed || one.closed {
		t.Fatal("only finished and unreadable trees close")
	}
}

func TestStoppingCoreTerminatesItsTreeAndKeepsItUntilItIsEmpty(t *testing.T) {
	tree := &fakeTree{running: 2}
	s := defaultState()
	s.job = tree
	s.clearProcessGuard()
	if !tree.terminated || tree.closed || s.job != nil || len(s.retiredTrees) != 1 {
		t.Fatalf("%+v %+v", tree, s.retiredTrees)
	}
	// A tree that cannot be terminated is closed, which ends it too.
	stuck := &fakeTree{running: 1, err: errors.New("denied")}
	s.job = stuck
	s.clearProcessGuard()
	if !stuck.closed || len(s.retiredTrees) != 1 {
		t.Fatal("a tree that could not be terminated was kept")
	}
	tree.set(0)
	s.clearProcessGuard()
	if !tree.closed || len(s.retiredTrees) != 0 {
		t.Fatal("an empty tree outlived the next prune")
	}
}

func TestTheProcessTreeWaitReturnsOnceEveryProcessExited(t *testing.T) {
	m, _ := newTestManager(t)
	tick := make(chan time.Time)
	m.treeTick = tick
	tree := &fakeTree{running: 2}
	m.lock()
	m.s.retiredTrees = []processTree{tree}
	m.unlock()

	done := make(chan error, 1)
	go func() { done <- m.WaitForProcessTreeExit(context.Background(), time.Hour) }()
	tick <- time.Time{}
	select {
	case err := <-done:
		t.Fatalf("the wait ended with processes left: %v", err)
	default:
	}
	tree.set(0)
	tick <- time.Time{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end once the processes exited")
	}
	if !tree.closed {
		t.Fatal("the empty tree was not closed")
	}
}

func TestTheProcessTreeWaitReportsProcessesLeftAfterTheTimeout(t *testing.T) {
	m, _ := newTestManager(t)
	m.treeTick = make(chan time.Time)
	m.lock()
	m.s.retiredTrees = []processTree{&fakeTree{running: 2}}
	m.unlock()
	err := m.WaitForProcessTreeExit(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "2 astrlink-core helper process") {
		t.Fatal(err)
	}
}

func TestTheProcessTreeWaitIsImmediateWithoutRetiredCores(t *testing.T) {
	m, _ := newTestManager(t)
	m.treeTick = make(chan time.Time)
	if err := m.WaitForProcessTreeExit(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}
