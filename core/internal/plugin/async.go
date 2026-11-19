package plugin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
	"github.com/MoeForge/moejs/eventloop"
)

// errNeverSettles ends a call whose hook waits on a promise that nothing is
// left to settle: no host work, request or timer is still to come back.
var errNeverSettles = errors.New("the hook waits on a promise that never settles")

// maxTimers bounds the timers one call may have set at once.
const maxTimers = 64

// restTime bounds how long what a call left behind has to settle once the
// call ended: the end of the call cancels the requests and host work it
// started and clears its timers, and their promises then settle on the
// loop. A runtime whose loop does not come to rest by then is dropped, so
// that nothing of one call runs in the next.
var restTime = 250 * time.Millisecond

var errRestTime = errors.New("what the call left behind did not settle")

// The runtime of a plugin that is not a task plugin runs on an event loop
// of moejs (package eventloop), which the web APIs and node:fs of moejs
// need: the hook of an apiVersion 2 call runs in it until the promise it
// returned settles, and with it the continuations of the host functions,
// the requests of fetch, the operations of node:fs and the timers.
//
// What the call runs on the loop counts as its script time and needs one of
// the pool's execution slots; while it waits on the host, the network or a
// timer, it holds neither. The loop has no hook for the moments it falls
// idle, so the host keeps the waits out instead: everything that comes back
// to the loop as a task of the host's, the settling of the host functions,
// the timers' callbacks and the operations of node:fs, runs through script,
// and the promises of the web APIs reach the plugin through such a task too
// (prelude.js relays them). What the web APIs run of the plugin's own
// directly, the callbacks of a stream it built, is not counted; the call's
// deadline bounds it.

// newLoop gives the runtime its loop and the timers of Node on it, which
// fire while a call runs.
func (bound *instance) newLoop() error {
	loop, err := eventloop.New(bound.runtime, eventloop.Options{})
	if err != nil {
		return err
	}
	bound.loop = loop
	bound.runtime.AddLiveRoots(bound.roots)
	promise, _, _ := bound.runtime.NewPromise()
	if bound.then, err = bound.runtime.Get(promise, "then"); err != nil {
		return err
	}
	bound.stop = bound.runtime.Function("stop", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
		loop.StopNoWait()
		return moejs.Undefined(), nil
	})
	return bound.wrapTimers()
}

// interrupt stops what the runtime runs, and wakes its loop when it sleeps.
func (bound *instance) interrupt(cause error) {
	if bound.loop != nil {
		bound.loop.Interrupt(cause)
		return
	}
	bound.runtime.Interrupt(cause)
}

// enter starts the call's script time: it takes an execution slot, waiting
// for one when it gave its own up, and its watchdog runs.
func (c *call) enter() error {
	if !c.slot {
		if err := c.pool.acquire(c.ctx); err != nil {
			return err
		}
		c.slot = true
	}
	c.watchdog.start()
	return nil
}

// leave ends the call's script time: it waits on the host, the network or
// a timer, which does not count, and its execution slot goes to another
// call meanwhile.
func (c *call) leave() {
	c.watchdog.pause()
	if c.slot {
		c.pool.release()
		c.slot = false
	}
}

// script returns task as the loop runs it for the call that runs: as its
// script time. Once no call runs, task runs as it is.
func (bound *instance) script(task func(*moejs.Runtime) error) func(*moejs.Runtime) error {
	return func(runtime *moejs.Runtime) error {
		c := bound.current
		if c == nil {
			return task(runtime)
		}
		if err := c.enter(); err != nil {
			return err
		}
		defer c.leave()
		return task(runtime)
	}
}

// post queues task on the loop, to run as the call's script time. It may
// be called from any goroutine, and reports false once the loop is over.
func (bound *instance) post(task func(*moejs.Runtime) error) bool {
	return eventloop.Post(bound.loop, bound.script(task))
}

// queue notes that the call that runs gave the loop work, which may
// outlive it (rest). It runs on the loop.
func (bound *instance) queue() {
	if c := bound.current; c != nil {
		c.queued = true
	}
}

// settle is how a host function's work settles its promise, on the loop,
// where JavaScript values can be made: a value fulfills it, an error rejects
// it with what a host function returning that error would throw.
type settle func(runtime *moejs.Runtime) (moejs.Value, error)

// later returns a promise that work settles. work runs on a goroutine of its
// own with the call's context; how it settles the promise runs on the loop,
// as the call's script time. Until then the promise keeps the loop alive.
func (c *call) later(work func(ctx context.Context) settle) moejs.Value {
	bound := c.instance
	c.queued = true
	promise, resolve, reject := c.runtime.NewPromise()
	kept := bound.keep(promise)
	done := bound.loop.Hold()
	go func() {
		result := work(c.ctx)
		bound.post(func(runtime *moejs.Runtime) error {
			bound.release(kept)
			value, err := result(runtime)
			if err != nil {
				reason, ok := runtime.ThrownValue(err)
				if !ok {
					return err
				}
				return reject(reason)
			}
			return resolve(value)
		})
		done(nil)
	}()
	return promise
}

// callHook calls hook with values. The hook of an apiVersion 2 call runs on
// the loop, which runs until the promise it returned settles; the call
// returns what it fulfilled with, and a rejection is the call's exception.
// The hook's own run is script time; the watchdog runs as it starts.
func (bound *instance) callHook(c *call, hook moejs.Hook, values []moejs.Value) (moejs.Value, error) {
	if bound.loop == nil {
		return bound.runtime.Call(hook, values...)
	}
	var value moejs.Value
	pending := false
	err := bound.loop.Run(func(runtime *moejs.Runtime) error {
		var err error
		value, err = runtime.Call(hook, values...)
		if err != nil {
			return err
		}
		if state, _, ok := moejs.PromiseResult(value); ok && c.async && state == moejs.PromisePending {
			pending = true
			_, err := runtime.CallFunction(bound.then, value, bound.stop, bound.stop)
			// The call waits: what runs next is the loop's.
			c.leave()
			return err
		}
		bound.loop.StopNoWait()
		return nil
	})
	if err != nil {
		return moejs.Undefined(), err
	}
	if !c.async || (!pending && !promiseLike(value)) {
		return value, nil
	}
	state, result, _ := moejs.PromiseResult(value)
	switch state {
	case moejs.PromiseFulfilled:
		return result, nil
	case moejs.PromiseRejected:
		return moejs.Undefined(), &moejs.Exception{Value: result}
	}
	// The loop ran out of work with the promise pending.
	return moejs.Undefined(), errNeverSettles
}

// rest ends what the call left behind once it ended: it clears the call's
// timers and runs the loop until the requests and host work the end of the
// call canceled settled. It reports whether the loop came to rest within
// restTime without an error; a runtime whose loop did not is dropped. A
// call that never used the loop, its timers, fetch, the host functions
// that return promises, node:fs or the promises the prelude relays, left
// nothing behind.
func (bound *instance) rest(c *call) bool {
	if bound.loop == nil || !c.queued {
		return true
	}
	for handle := range c.timers {
		if _, err := bound.runtime.CallFunction(bound.timers.clearTimeout, moejs.Undefined(), engine.ObjectValue(handle)); err != nil {
			return false
		}
		if _, err := bound.runtime.CallFunction(bound.timers.clearImmediate, moejs.Undefined(), engine.ObjectValue(handle)); err != nil {
			return false
		}
	}
	clear(c.timers)
	var mutex sync.Mutex
	stopped, fired := false, false
	timer := time.AfterFunc(restTime, func() {
		mutex.Lock()
		defer mutex.Unlock()
		if !stopped {
			fired = true
			bound.loop.Interrupt(errRestTime)
		}
	})
	err := bound.loop.Run(nil)
	mutex.Lock()
	stopped = true
	mutex.Unlock()
	timer.Stop()
	return err == nil && !fired
}

// close ends the runtime for good: its loop, the requests in flight, and
// the files and databases it holds open.
func (bound *instance) close() {
	if bound.loop != nil {
		bound.loop.Terminate()
	}
	if bound.web != nil {
		bound.web.Close()
	}
	if bound.files != nil {
		_ = bound.files.Close()
	}
	bound.resources.close()
}

// promiseLike reports whether value is a promise the call has to wait for.
func promiseLike(value moejs.Value) bool {
	_, _, ok := moejs.PromiseResult(value)
	return ok
}

// keep holds values that only Go keeps for a while, a promise host work
// settles or what a task of the loop passes to JavaScript, for the memory
// limit's measurements, until release. It runs on the loop.
func (bound *instance) keep(values ...moejs.Value) uint64 {
	if bound.kept == nil {
		bound.kept = make(map[uint64][]moejs.Value)
	}
	bound.lastKept++
	bound.kept[bound.lastKept] = values
	return bound.lastKept
}

// release stops holding what keep held.
func (bound *instance) release(key uint64) { delete(bound.kept, key) }

// roots adds what only Go holds of the runtime's JavaScript to a
// measurement of its heap: the values kept, and the callbacks of the call's
// timers. It runs on the runtime's goroutine.
func (bound *instance) roots(add func(moejs.Value)) {
	for _, values := range bound.kept {
		for _, value := range values {
			add(value)
		}
	}
	if c := bound.current; c != nil {
		for _, timer := range c.timers {
			add(timer.callback)
		}
	}
}

// loopTimers are the timer functions of the loop, which the globals of the
// same names wrap.
type loopTimers struct {
	setTimeout, setInterval, setImmediate moejs.Value
	clearTimeout, clearImmediate          moejs.Value
	// fire is the callback the loop gets for every timer of a call; it
	// runs the plugin's callback as a task of the call.
	fire moejs.Value
}

// callTimer is a timer a call set that has not fired or been cleared.
type callTimer struct {
	callback moejs.Value
	repeat   bool
}

// wrapTimers wraps the timers of the loop. A call may have at most
// maxTimers of them at once, and those it leaves end with it; a callback
// runs as a task of the call, its script time. A timer set outside a call,
// as by a module's top level, never fires. apiVersion 1 calls have none.
func (bound *instance) wrapTimers() error {
	runtime := bound.runtime
	global := engine.ObjectValue(runtime.Realm().Global)
	read := func(name string) (moejs.Value, error) {
		value, err := runtime.Get(global, name)
		if err == nil && !engine.IsCallable(value) {
			err = errors.New("the loop did not set " + name)
		}
		return value, err
	}
	timers := &bound.timers
	for name, target := range map[string]*moejs.Value{
		"setTimeout": &timers.setTimeout, "setInterval": &timers.setInterval, "setImmediate": &timers.setImmediate,
		"clearTimeout": &timers.clearTimeout, "clearImmediate": &timers.clearImmediate,
	} {
		value, err := read(name)
		if err != nil {
			return err
		}
		*target = value
	}
	timers.fire = runtime.Function("fire", 0, func(realm *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		c := bound.current
		var timer callTimer
		found := false
		if c != nil && this.IsObject() {
			timer, found = c.timers[this.AsObject()]
		}
		if !found {
			// A timer of a call that ended, which a refresh brought back.
			_, err := realm.Call(timers.clearTimeout, moejs.Undefined(), []moejs.Value{this})
			return moejs.Undefined(), err
		}
		if !timer.repeat {
			delete(c.timers, this.AsObject())
		}
		callArgs := append([]moejs.Value(nil), args...)
		kept := bound.keep(append([]moejs.Value{timer.callback, this}, callArgs...)...)
		bound.post(func(runtime *moejs.Runtime) error {
			bound.release(kept)
			_, err := runtime.CallFunction(timer.callback, this, callArgs...)
			return err
		})
		return moejs.Undefined(), nil
	})
	set := func(name string, length int, official moejs.Value, repeat, immediate bool) moejs.Value {
		return runtime.Function(name, length, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				// No call runs: the timer never fires.
				handle, err := realm.Call(official, moejs.Undefined(), args)
				if err != nil {
					return moejs.Undefined(), err
				}
				clearer := timers.clearTimeout
				if immediate {
					clearer = timers.clearImmediate
				}
				_, err = realm.Call(clearer, moejs.Undefined(), []moejs.Value{handle})
				return handle, err
			}
			if !c.async {
				return moejs.Undefined(), realm.TypeError("%s works only while the host runs a hook of an apiVersion 2 plugin", name)
			}
			callback := moejs.Arg(args, 0)
			if !engine.IsCallable(callback) {
				// The loop's own TypeError.
				return realm.Call(official, moejs.Undefined(), args)
			}
			if len(c.timers) >= maxTimers {
				return moejs.Undefined(), realm.RangeError("a call may have at most %d timers at once", maxTimers)
			}
			wrapped := make([]moejs.Value, len(args))
			copy(wrapped, args)
			wrapped[0] = timers.fire
			handle, err := realm.Call(official, moejs.Undefined(), wrapped)
			if err != nil {
				return moejs.Undefined(), err
			}
			c.queued = true
			if handle.IsObject() {
				if c.timers == nil {
					c.timers = make(map[*moejs.Object]callTimer)
				}
				c.timers[handle.AsObject()] = callTimer{callback: callback, repeat: repeat}
			}
			return handle, nil
		})
	}
	clearer := func(name string, official moejs.Value) moejs.Value {
		return runtime.Function(name, 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if c := bound.current; c != nil {
				if handle := moejs.Arg(args, 0); handle.IsObject() {
					delete(c.timers, handle.AsObject())
				}
			}
			return realm.Call(official, moejs.Undefined(), args)
		})
	}
	for name, value := range map[string]moejs.Value{
		"setTimeout":     set("setTimeout", 5, timers.setTimeout, false, false),
		"setInterval":    set("setInterval", 5, timers.setInterval, true, false),
		"setImmediate":   set("setImmediate", 4, timers.setImmediate, false, true),
		"clearTimeout":   clearer("clearTimeout", timers.clearTimeout),
		"clearInterval":  clearer("clearInterval", timers.clearTimeout),
		"clearImmediate": clearer("clearImmediate", timers.clearImmediate),
	} {
		if err := runtime.SetGlobal(name, value); err != nil {
			return err
		}
	}
	return nil
}
