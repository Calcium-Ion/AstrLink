package devreload

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func ok(generation string) pollResult { return pollResult{kind: pollOK, generation: generation} }

func TestJoinsTheBuildIDOntoTheDevURL(t *testing.T) {
	for _, devURL := range []string{"http://127.0.0.1:1420", "http://127.0.0.1:1420/"} {
		if got, err := generationURL(devURL); err != nil || got != "http://127.0.0.1:1420/__astrlink_build" {
			t.Fatalf("%s: %s, %v", devURL, got, err)
		}
	}
	if _, err := generationURL("not a url"); err == nil {
		t.Fatal("a bad dev URL was accepted")
	}
}

func TestTheFirstGenerationIsRememberedWithoutAReload(t *testing.T) {
	var s supervisor
	if got := s.apply(ok("0")); got.action != actionRemember || got.generation != "0" || got.recovered {
		t.Fatalf("first = %+v", got)
	}
	if got := s.apply(ok("0")); got.action != actionIdle {
		t.Fatalf("again = %+v", got)
	}
}

func TestAGenerationChangeReloadsUntilCommitted(t *testing.T) {
	var s supervisor
	s.apply(ok("0"))
	for range 2 {
		if got := s.apply(ok("3")); got.action != actionReload || got.generation != "3" {
			t.Fatalf("change = %+v", got)
		}
	}
	s.commit("3")
	if got := s.apply(ok("3")); got.action != actionIdle {
		t.Fatalf("committed = %+v", got)
	}
}

func TestAnAnswerOtherThan200StopsTheSupervisor(t *testing.T) {
	var s supervisor
	if got := s.apply(pollResult{kind: pollHTTP, status: 404}); got.action != actionStopHTTP || got.status != 404 {
		t.Fatalf("404 = %+v", got)
	}
	if got := s.apply(ok("1")); got.action != actionIdle {
		t.Fatalf("after stop = %+v", got)
	}
}

func TestUnreachableWarnsOnceThenRecovers(t *testing.T) {
	var s supervisor
	for range unreachableWarnAfter - 1 {
		if got := s.apply(pollResult{kind: pollUnreachable}); got.action != actionIdle {
			t.Fatalf("early = %+v", got)
		}
	}
	if got := s.apply(pollResult{kind: pollUnreachable}); got.action != actionWarnUnreachable {
		t.Fatalf("warn = %+v", got)
	}
	if got := s.apply(pollResult{kind: pollUnreachable}); got.action != actionIdle {
		t.Fatalf("after warn = %+v", got)
	}
	if got := s.apply(ok("4")); !got.recovered || got.action != actionRemember || got.generation != "4" {
		t.Fatalf("recovered = %+v", got)
	}
}

// TestRunReloadsOnARebuildAndStopsWhenTheServerGoesAway serves generations
// from a test server: the second one reloads, then a 404 ends Run.
func TestRunReloadsOnARebuildAndStopsWhenTheServerGoesAway(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != BuildIDPath {
			http.NotFound(w, r)
			return
		}
		switch polls.Add(1) {
		case 1:
			_, _ = w.Write([]byte("1\n"))
		case 2, 3:
			_, _ = w.Write([]byte("2"))
		default:
			http.Error(w, "gone", http.StatusNotFound)
		}
	}))
	defer server.Close()
	reloads := make(chan struct{}, 4)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		Run(ctx, server.URL, func() bool {
			reloads <- struct{}{}
			return true
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run kept polling after a 404")
	}
	// Generation 2 reloaded once; the poll after it saw it committed.
	if len(reloads) != 1 {
		t.Fatalf("reloaded %d times", len(reloads))
	}
}

func TestRunEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// Nothing listens on port 1; every poll fails.
		Run(ctx, "http://127.0.0.1:1", func() bool { return true })
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run kept polling after its context ended")
	}
}
