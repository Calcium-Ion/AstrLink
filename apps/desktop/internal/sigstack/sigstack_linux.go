//go:build linux && (amd64 || arm64)

package sigstack

import (
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

// fix marks every installed handler SA_ONSTACK. The default and ignored
// dispositions run no code, and SIGKILL and SIGSTOP cannot be caught.
func fix() {
	for sig := 1; sig <= 64; sig++ {
		if sig == int(syscall.SIGKILL) || sig == int(syscall.SIGSTOP) {
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
