//go:build darwin

package darwin

import (
	"runtime"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// biometryPolicy is LAPolicyDeviceOwnerAuthenticationWithBiometrics: a
// fingerprint only, so the raw password is the fallback, not the login
// password.
const biometryPolicy = 1

var la struct {
	once sync.Once
	ok   bool

	poolPush, poolPop uintptr
	canEvaluate       func(ctx objc.ID, sel objc.SEL, policy int, err *objc.ID) bool
	evaluate          func(ctx objc.ID, sel objc.SEL, policy int, reason objc.ID, reply objc.Block)
	code              func(err objc.ID, sel objc.SEL) int
	stringUTF8        func(obj objc.ID, sel objc.SEL, text *byte) objc.ID
}

func loadLocalAuthentication() bool {
	la.once.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/LocalAuthentication.framework/LocalAuthentication", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
			return
		}
		send, err := purego.Dlsym(lib, "objc_msgSend")
		if err != nil {
			return
		}
		if la.poolPush, err = purego.Dlsym(lib, "objc_autoreleasePoolPush"); err != nil {
			return
		}
		if la.poolPop, err = purego.Dlsym(lib, "objc_autoreleasePoolPop"); err != nil {
			return
		}
		purego.RegisterFunc(&la.canEvaluate, send)
		purego.RegisterFunc(&la.evaluate, send)
		purego.RegisterFunc(&la.code, send)
		purego.RegisterFunc(&la.stringUTF8, send)
		la.ok = objc.GetClass("LAContext") != 0
	})
	return la.ok
}

// withPool runs fn in an autorelease pool on one thread: these calls run
// on goroutines, which have none.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(la.poolPush)
	defer purego.SyscallN(la.poolPop, pool)
	fn()
}

func newContext() objc.ID {
	return objc.ID(objc.GetClass("LAContext")).Send(sel("alloc")).Send(sel("init"))
}

// canEvaluate asks context whether the policy can run, and the LAError
// code when it cannot.
func canEvaluate(context objc.ID) (bool, int) {
	var failure objc.ID
	if la.canEvaluate(context, sel("canEvaluatePolicy:error:"), biometryPolicy, &failure) {
		return true, 0
	}
	if failure == 0 {
		return false, 0
	}
	return false, la.code(failure, sel("code"))
}

// BiometryAvailable reports whether this Mac has Touch ID with an enrolled
// fingerprint that this process may use.
func BiometryAvailable() bool {
	if !loadLocalAuthentication() {
		return false
	}
	available := false
	withPool(func() {
		context := newContext()
		if context == 0 {
			return
		}
		available, _ = canEvaluate(context)
		context.Send(sel("release"))
	})
	return available
}

// BiometryOutcome is how the Touch ID prompt ended.
type BiometryOutcome struct {
	// Passed is a matched fingerprint.
	Passed bool
	// Code is the LAError code of a refusal, 0 when there is none.
	Code int
	// TimedOut is a prompt left unanswered for the wait, then withdrawn.
	TimedOut bool
}

// EvaluateBiometry shows the system's Touch ID prompt, which names reason,
// and waits for the operator for at most wait; an unanswered prompt is
// withdrawn so the dialog that asked recovers. It blocks; call it off the
// main thread.
func EvaluateBiometry(reason string, wait time.Duration) BiometryOutcome {
	if !loadLocalAuthentication() {
		return BiometryOutcome{Code: LAErrorBiometryNotAvailable}
	}
	replies := make(chan BiometryOutcome, 1)
	reply := objc.NewBlock(func(_ objc.Block, success bool, failure objc.ID) {
		outcome := BiometryOutcome{Passed: success}
		if !success && failure != 0 {
			// LocalAuthentication passes nil or a valid error.
			outcome.Code = la.code(failure, sel("code"))
		}
		select {
		case replies <- outcome:
		default:
		}
	})
	defer reply.Release()
	var context objc.ID
	var refused *BiometryOutcome
	withPool(func() {
		context = newContext()
		if context == 0 {
			refused = &BiometryOutcome{Code: LAErrorBiometryNotAvailable}
			return
		}
		if ok, code := canEvaluate(context); !ok {
			refused = &BiometryOutcome{Code: code}
			return
		}
		text := append([]byte(reason), 0)
		message := la.stringUTF8(objc.ID(objc.GetClass("NSString")).Send(sel("alloc")), sel("initWithUTF8String:"), &text[0])
		la.evaluate(context, sel("evaluatePolicy:localizedReason:reply:"), biometryPolicy, message, reply)
		message.Send(sel("release"))
	})
	// The context stays alive until the reply arrives or it is invalidated.
	defer func() {
		if context != 0 {
			withPool(func() { context.Send(sel("release")) })
		}
	}()
	if refused != nil {
		if refused.Code == 0 {
			refused.Code = LAErrorBiometryNotAvailable
		}
		return *refused
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case outcome := <-replies:
		return outcome
	case <-timer.C:
		// Invalidating ends the pending prompt.
		withPool(func() { context.Send(sel("invalidate")) })
		return BiometryOutcome{TimedOut: true}
	}
}
