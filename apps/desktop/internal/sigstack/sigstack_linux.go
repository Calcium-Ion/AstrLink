//go:build linux && (amd64 || arm64)

package sigstack

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const supported = true

// saOnStack is SA_ONSTACK; the handler runs on the thread's signal stack.
const saOnStack = 0x08000000

// action is the kernel's struct sigaction on amd64 and arm64.
type action struct {
	handler  uintptr
	flags    uint64
	restorer uintptr
	mask     uint64
}

func sigaction(sig int, set, old *action) syscall.Errno {
	_, _, errno := syscall.RawSyscall6(
		syscall.SYS_RT_SIGACTION,
		uintptr(sig),
		uintptr(unsafe.Pointer(set)),
		uintptr(unsafe.Pointer(old)),
		unsafe.Sizeof(action{}.mask),
		0, 0,
	)
	return errno
}

// suspendSignal is the signal JavaScriptCore's garbage collector sends a
// thread to pause it and scan its stack: SIGUSR1, or the number at the start
// of JSC_SIGNAL_FOR_GC, read as sscanf's %d reads it.
func suspendSignal() int {
	var sig int
	if n, _ := fmt.Sscanf(os.Getenv("JSC_SIGNAL_FOR_GC"), "%d", &sig); n == 1 {
		return sig
	}
	return int(syscall.SIGUSR1)
}

// fix marks every installed handler SA_ONSTACK except the one for suspend,
// JavaScriptCore's suspend signal. The default and ignored dispositions run
// no code, and SIGKILL and SIGSTOP cannot be caught.
//
// The suspend handler pauses its thread only when it runs on the thread's
// own stack; anywhere else it gives up and the collector sends the signal
// again at once. Go gives the UI thread, which runs JavaScript through the
// web view, a signal stack of its own, so on it the collector and the UI
// thread would trade the signal forever and the app would hang. The
// collector signals only threads that ran JavaScript, never Go's other
// threads, so the handler stays as installed.
func fix(suspend int) {
	for sig := 1; sig <= 64; sig++ {
		if sig == int(syscall.SIGKILL) || sig == int(syscall.SIGSTOP) || sig == suspend {
			continue
		}
		var current action
		if sigaction(sig, nil, &current) != 0 {
			continue
		}
		if current.handler <= 1 || current.flags&saOnStack != 0 {
			continue
		}
		current.flags |= saOnStack
		sigaction(sig, &current, nil)
	}
}
