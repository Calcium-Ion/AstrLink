//go:build linux && (amd64 || arm64)

package sigstack

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

func TestKeepPutsAHandlerWithoutTheSignalStackBackOnIt(t *testing.T) {
	// SIGUSR2 gets Go's handler, which a library then reinstalls without
	// SA_ONSTACK, as WebKit does with SIGUSR1.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGUSR2)
	defer signal.Reset(syscall.SIGUSR2)
	var installed action
	if errno := sigaction(int(syscall.SIGUSR2), nil, &installed); errno != 0 {
		t.Fatal(errno)
	}
	if installed.handler <= 1 || installed.flags&saOnStack == 0 {
		t.Fatalf("Go's handler = %+v", installed)
	}
	stripped := installed
	stripped.flags &^= saOnStack
	if errno := sigaction(int(syscall.SIGUSR2), &stripped, nil); errno != 0 {
		t.Fatal(errno)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Keep(ctx)

	var fixed action
	if errno := sigaction(int(syscall.SIGUSR2), nil, &fixed); errno != 0 {
		t.Fatal(errno)
	}
	if fixed != installed {
		t.Fatalf("handler = %+v, want %+v", fixed, installed)
	}
}
