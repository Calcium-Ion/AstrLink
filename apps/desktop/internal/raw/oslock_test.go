package raw

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/keystore"
)

// lockCore answers the raw lock route and reports each call.
type lockCore struct{ locked chan string }

func (c lockCore) Call(_ context.Context, req control.Request) (control.Response, error) {
	c.locked <- req.Method + " " + req.Path
	return control.Response{Status: http.StatusOK, Body: []byte(sealedStatus)}, nil
}
func (lockCore) DataDirectory() string               { return "" }
func (lockCore) NoteRawKeyReplaced(bool) bool        { return false }
func (lockCore) PollObservers(context.Context) error { return nil }

type noPins struct{}

func (noPins) Lock()                                                  {}
func (noPins) Unlock()                                                {}
func (noPins) Apply(string, json.RawMessage, keystore.PinAction) bool { return false }

func TestTheSessionLockLocksRawContentOnlyWhileCoreIsReady(t *testing.T) {
	locked := make(chan string, 2)
	changed := make(chan struct{}, 2)
	ready := false
	lock := SessionLock{
		Raw:   Raw{Core: lockCore{locked}, Pins: noPins{}, Changed: func() { changed <- struct{}{} }},
		Ready: func() bool { return ready },
	}
	lock.Lock("sleep")
	ready = true
	lock.Lock("screen lock")
	select {
	case call := <-locked:
		if call != http.MethodPost+" "+control.RawLockPath {
			t.Fatalf("locked with %s", call)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a ready Core was not locked")
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("the windows were not told of the lock")
	}
	if len(locked) != 0 {
		t.Fatal("a Core that was not ready was locked")
	}
}
