// Package devreload reloads every page of a development build when the
// frontend rebuilds. Fast Refresh often applies without repainting in
// WKWebView, so the dev server only publishes a build generation at
// /__astrlink_build and the host polls it; the frontend bundle drives no
// reload of its own, so a module error in it cannot silence reloading.
package devreload

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BuildIDPath is where the dev server publishes the build generation.
const BuildIDPath = "/__astrlink_build"

const (
	pollInterval   = 500 * time.Millisecond
	requestTimeout = 2 * time.Second
	// unreachableWarnAfter failed polls in a row are reported once.
	unreachableWarnAfter = 20
)

type pollKind int

const (
	pollOK pollKind = iota
	// pollHTTP is an answer other than 200: the dev server no longer
	// publishes generations, so polling stops.
	pollHTTP
	pollUnreachable
)

type pollResult struct {
	kind       pollKind
	generation string
	status     int
}

type actionKind int

const (
	actionIdle actionKind = iota
	actionRemember
	actionReload
	actionStopHTTP
	actionWarnUnreachable
)

type tick struct {
	action     actionKind
	generation string
	status     int
	recovered  bool
}

// supervisor turns polls into reloads: the first generation is
// remembered, a new one reloads until a page took it.
type supervisor struct {
	seen                *string
	consecutiveFailures int
	warnedUnreachable   bool
	stopped             bool
}

func (s *supervisor) apply(result pollResult) tick {
	if s.stopped {
		return tick{}
	}
	switch result.kind {
	case pollOK:
		recovered := s.warnedUnreachable
		s.consecutiveFailures, s.warnedUnreachable = 0, false
		switch {
		case s.seen == nil:
			generation := result.generation
			s.seen = &generation
			return tick{action: actionRemember, generation: generation, recovered: recovered}
		case *s.seen == result.generation:
			return tick{recovered: recovered}
		default:
			return tick{action: actionReload, generation: result.generation, recovered: recovered}
		}
	case pollHTTP:
		s.stopped = true
		return tick{action: actionStopHTTP, status: result.status}
	default:
		s.consecutiveFailures++
		if !s.warnedUnreachable && s.consecutiveFailures >= unreachableWarnAfter {
			s.warnedUnreachable = true
			return tick{action: actionWarnUnreachable}
		}
		return tick{}
	}
}

func (s *supervisor) commit(generation string) { s.seen = &generation }

// generationURL is BuildIDPath on the dev server at devURL.
func generationURL(devURL string) (string, error) {
	base, err := url.Parse(devURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errors.New("invalid dev URL for the reload supervisor: " + devURL)
	}
	return base.JoinPath(BuildIDPath).String(), nil
}

func poll(ctx context.Context, client *http.Client, endpoint string) pollResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return pollResult{kind: pollUnreachable}
	}
	resp, err := client.Do(req)
	if err != nil {
		return pollResult{kind: pollUnreachable}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pollResult{kind: pollHTTP, status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	generation := strings.TrimSpace(string(body))
	if err != nil || generation == "" {
		return pollResult{kind: pollUnreachable}
	}
	return pollResult{kind: pollOK, generation: generation}
}

// Run polls the dev server at devURL until ctx ends and calls reload when
// the frontend rebuilt. reload reports whether a page reloaded, so a
// generation counts as seen only once it landed somewhere. Failures are
// logged to stderr.
func Run(ctx context.Context, devURL string, reload func() bool) {
	endpoint, err := generationURL(devURL)
	if err != nil {
		log.Printf("[astrlink dev] %v", err)
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var s supervisor
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if !apply(&s, s.apply(poll(ctx, client, endpoint)), endpoint, reload) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// apply acts on a tick; it reports false once polling should stop.
func apply(s *supervisor, t tick, endpoint string, reload func() bool) bool {
	if t.recovered {
		log.Printf("[astrlink dev] %s is reachable again", BuildIDPath)
	}
	switch t.action {
	case actionReload:
		log.Printf("[astrlink dev] frontend rebuild #%s -> reloading pages", t.generation)
		if reload() {
			s.commit(t.generation)
		} else {
			log.Printf("[astrlink dev] no page is available to reload")
		}
	case actionStopHTTP:
		log.Printf("[astrlink dev] %s answered HTTP %d; reloading stopped (the rsbuild config may have changed)", BuildIDPath, t.status)
		return false
	case actionWarnUnreachable:
		log.Printf("[astrlink dev] unable to reach %s; reloading paused", endpoint)
	}
	return true
}
