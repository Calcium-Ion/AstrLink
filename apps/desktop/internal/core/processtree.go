package core

import (
	"context"
	"fmt"
	"time"
)

// processTreePoll is how often a wait for retired process trees looks
// again.
const processTreePoll = 25 * time.Millisecond

// processTree is what the desktop holds of a Core process and the helpers
// it starts: on Windows the kill-on-close Job Object Core runs in. Closing
// it ends whatever still runs in it.
type processTree interface {
	Close() error
	// Terminate asks every process in the tree to end. It returns at once;
	// Running reports when they are gone.
	Terminate() error
	// Running is how many processes in the tree have not exited.
	Running() (uint32, error)
}

// pruneRetired keeps only the trees that still run processes, closing the
// others, and returns how many processes the kept ones run. A tree whose
// count cannot be read is dropped too, so a broken handle never blocks an
// update.
func pruneRetired(trees []processTree) ([]processTree, uint32) {
	kept := trees[:0]
	var running uint32
	for _, tree := range trees {
		count, err := tree.Running()
		if err == nil && count > 0 {
			kept = append(kept, tree)
			running += count
			continue
		}
		tree.Close()
	}
	clear(trees[len(kept):])
	return kept, running
}

// waitForRetired polls prune until it reports no running processes, or
// fails once timeout has passed or ctx ends, naming how many are left.
// tick paces the polls.
func waitForRetired(ctx context.Context, prune func() uint32, timeout time.Duration, tick <-chan time.Time) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		running := prune()
		if running == 0 {
			return nil
		}
		select {
		case <-tick:
		case <-deadline.C:
			return fmt.Errorf("%d astrlink-core helper process(es) still running after %d ms", prune(), timeout.Milliseconds())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// WaitForProcessTreeExit waits until the helper processes of stopped Cores
// have exited too. Core returning does not mean its workers are gone: on
// Windows they end with Core's job and may still hold their executables
// and DLLs open while they shut down, which keeps an installer from
// replacing those files.
func (m *Manager) WaitForProcessTreeExit(ctx context.Context, timeout time.Duration) error {
	ticker := time.NewTicker(processTreePoll)
	defer ticker.Stop()
	return waitForRetired(ctx, m.pruneRetiredTrees, timeout, m.processTreeTick(ticker))
}

func (m *Manager) processTreeTick(ticker *time.Ticker) <-chan time.Time {
	if m.treeTick != nil {
		return m.treeTick
	}
	return ticker.C
}

func (m *Manager) pruneRetiredTrees() uint32 {
	m.lock()
	defer m.unlock()
	var running uint32
	m.s.retiredTrees, running = pruneRetired(m.s.retiredTrees)
	return running
}
