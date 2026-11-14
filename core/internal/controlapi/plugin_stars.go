package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// githubAPIBaseURL is where Core asks how many stars a repository has.
const githubAPIBaseURL = "https://api.github.com"

const (
	// starsFresh is how long an answer stands before Core asks again; one
	// person's plugin page stays far below GitHub's 60 anonymous calls an
	// hour.
	starsFresh = 6 * time.Hour
	// starsRetry is how long Core waits after GitHub did not answer.
	starsRetry = 10 * time.Minute
	// starsTimeout bounds one question, which the page waits for apart from
	// everything else it shows.
	starsTimeout = 5 * time.Second
	// maxStarsBody bounds GitHub's answer; a repository's runs to a few KiB.
	maxStarsBody = 1 << 20
)

// githubName is an owner or a repository name as GitHub allows it.
var githubName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// githubRepository returns owner/name of a github.com repository address,
// "" for any other address.
func githubRepository(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || (parsed.Host != "github.com" && parsed.Host != "www.github.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return ""
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !githubName.MatchString(owner) || !githubName.MatchString(name) || name == "." || name == ".." {
		return ""
	}
	return owner + "/" + name
}

// repoStars remembers how many stars the repositories plugins credit have.
// Pages asking at once share one question to GitHub.
type repoStars struct {
	baseURL string
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	known   map[string]starsAnswer
	pending map[string]chan struct{}
}

// starsAnswer is the last count GitHub gave and when to ask again; known is
// false until it answered once.
type starsAnswer struct {
	stars int
	known bool
	next  time.Time
}

func newRepoStars(baseURL string) *repoStars {
	return &repoStars{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		// The default transport carries Core's outbound proxy.
		client:  &http.Client{Timeout: starsTimeout},
		now:     time.Now,
		known:   make(map[string]starsAnswer),
		pending: make(map[string]chan struct{}),
	}
}

// stars returns the repository's stars, asking GitHub when the last answer
// is stale. A count GitHub gave before stays while it cannot be reached.
func (cache *repoStars) stars(ctx context.Context, repository string) (int, bool) {
	cache.mu.Lock()
	answer := cache.known[repository]
	if cache.now().Before(answer.next) {
		cache.mu.Unlock()
		return answer.stars, answer.known
	}
	wait, asking := cache.pending[repository]
	if !asking {
		wait = make(chan struct{})
		cache.pending[repository] = wait
		// The question outlives a page that stops waiting, so the next one
		// finds its answer.
		go cache.ask(repository, wait)
	}
	cache.mu.Unlock()
	select {
	case <-wait:
	case <-ctx.Done():
		return answer.stars, answer.known
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	answer = cache.known[repository]
	return answer.stars, answer.known
}

func (cache *repoStars) ask(repository string, done chan struct{}) {
	stars, err := cache.fetch(repository)
	cache.mu.Lock()
	answer := cache.known[repository]
	if err != nil {
		answer.next = cache.now().Add(starsRetry)
	} else {
		answer = starsAnswer{stars: stars, known: true, next: cache.now().Add(starsFresh)}
	}
	cache.known[repository] = answer
	delete(cache.pending, repository)
	cache.mu.Unlock()
	close(done)
}

func (cache *repoStars) fetch(repository string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), starsTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cache.baseURL+"/repos/"+repository, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub refuses a request without one.
	request.Header.Set("User-Agent", "AstrLink")
	response, err := cache.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub answered %s", response.Status)
	}
	var body struct {
		Stars *int `json:"stargazers_count"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxStarsBody)).Decode(&body); err != nil {
		return 0, err
	}
	if body.Stars == nil || *body.Stars < 0 {
		return 0, errors.New("GitHub gave no star count")
	}
	return *body.Stars, nil
}

// pluginCredits answers how many stars the GitHub repositories the plugin
// credits have, asking for them all at once.
func (handler *Handler) pluginCredits(writer http.ResponseWriter, ctx context.Context, target *plugin.Plugin) {
	response := contract.PluginCreditStars{Stars: map[string]int{}}
	var mu sync.Mutex
	var group sync.WaitGroup
	for _, credit := range target.Meta.Credits {
		repository := githubRepository(credit.URL)
		if repository == "" {
			continue
		}
		group.Go(func() {
			if stars, ok := handler.repoStars.stars(ctx, repository); ok {
				mu.Lock()
				response.Stars[credit.URL] = stars
				mu.Unlock()
			}
		})
	}
	group.Wait()
	writeJSON(writer, http.StatusOK, response)
}
