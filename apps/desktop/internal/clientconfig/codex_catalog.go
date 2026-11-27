package clientconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// CodexCatalogCache shares a token-scoped discovery read between the settings,
// change preview and write. A different Core session or token starts fresh.
// The lock guards only the cached state; network reads run outside it, and
// callers for the same scope wait on one discovery read.
type CodexCatalogCache struct {
	mu       sync.Mutex
	key      catalogKey
	gen      uint64
	until    time.Time
	upstream map[string]json.RawMessage
	details  Catalog
	loading  *catalogLoad
}

type catalogKey struct{ session, tokenID, origin string }

type catalogLoad struct {
	key  catalogKey
	done chan struct{}
}

var catalogHTTP = &http.Client{
	Transport:     &http.Transport{DisableKeepAlives: true},
	Timeout:       8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func readUpstreamCodexCatalog(ctx context.Context, origin, token string) (map[string]json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/v1/models?client_version="+contract.MinCodexClientVersion, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(contract.CodexUpstreamCatalogHeader, "1")
	resp, err := catalogHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model discovery returned HTTP %d", resp.StatusCode)
	}
	const maxCatalog = 16 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalog+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCatalog {
		return nil, errors.New("model discovery response too large")
	}
	var entries map[string]json.RawMessage
	if err = json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *CodexCatalogCache) Load(ctx context.Context, g Gateway, tokenID, url string, models []string) (Catalog, error) {
	if len(models) > MaxListedModels {
		return nil, errors.New("too many models")
	}
	for _, model := range models {
		if model == "" || strings.TrimSpace(model) != model || !ValidModel(model) {
			return nil, errors.New("invalid model")
		}
	}
	origin, err := LocalOrigin(url)
	if err != nil {
		return nil, err
	}
	active, session, ready := g.Session()
	if !ready || active != url {
		return nil, errSessionChanged
	}
	key := catalogKey{session, tokenID, origin}
	upstream, known, gen, err := c.snapshot(ctx, g, key, url)
	if err != nil {
		return nil, err
	}
	result := make(Catalog, len(models))
	var missing []string
	for _, model := range models {
		if _, seen := result[model]; seen {
			continue
		}
		if info, ok := known[model]; ok {
			result[model] = info
			continue
		}
		var declaration struct {
			Slug      string            `json:"slug"`
			Supported []json.RawMessage `json:"supported_reasoning_levels"`
		}
		raw := upstream[model]
		if json.Unmarshal(raw, &declaration) != nil || declaration.Slug == "" {
			raw = nil
		}
		info := ModelDetails{CodexCatalog: raw}
		if raw == nil || declaration.Supported == nil {
			missing = append(missing, model)
		} else {
			// A native declaration, even an empty one, settles whether the
			// model's effort can be chosen.
			info.ReasoningKnown = true
			info.HasReasoning = len(contract.ModelReasoning(raw, nil).Supported) > 0
		}
		result[model] = info
	}
	metadataFailed := false
	if len(missing) > 0 {
		metadata, err := g.ModelMetadataForModels(ctx, tokenID, missing)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "Codex official model metadata unavailable: %v\n", err)
			metadataFailed = true
		}
		for _, model := range missing {
			info := detailsOf(metadata[model])
			info.CodexCatalog = result[model].CodexCatalog
			result[model] = info
		}
	}
	if nowURL, now, ok := g.Session(); !ok || nowURL != url || now != session {
		return nil, errSessionChanged
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A newer discovery read replaced this one meanwhile; keep its state.
	if c.key == key && c.gen == gen {
		if metadataFailed {
			if soon := time.Now().Add(5 * time.Second); soon.Before(c.until) {
				c.until = soon
			}
		}
		for model, info := range result {
			if len(c.details) < 4096 {
				c.details[model] = info
			}
		}
	}
	return result, nil
}

// snapshot returns the fresh discovery state for key, reading it at most
// once at a time. The returned maps are read-only for the caller.
func (c *CodexCatalogCache) snapshot(ctx context.Context, g Gateway, key catalogKey, url string) (map[string]json.RawMessage, Catalog, uint64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, err
		}
		c.mu.Lock()
		if c.key == key && c.details != nil && time.Now().Before(c.until) {
			known := make(Catalog, len(c.details))
			for model, info := range c.details {
				known[model] = info
			}
			upstream, gen := c.upstream, c.gen
			c.mu.Unlock()
			return upstream, known, gen, nil
		}
		if load := c.loading; load != nil {
			c.mu.Unlock()
			select {
			case <-load.done:
			case <-ctx.Done():
				return nil, nil, 0, ctx.Err()
			}
			continue
		}
		load := &catalogLoad{key: key, done: make(chan struct{})}
		c.loading = load
		c.mu.Unlock()

		entries, ttl, err := fetchUpstreamCodexCatalog(ctx, g, key, url)
		c.mu.Lock()
		c.loading = nil
		if err == nil {
			c.key, c.until = key, time.Now().Add(ttl)
			c.gen++
			c.upstream, c.details = entries, Catalog{}
		}
		close(load.done)
		c.mu.Unlock()
		if err != nil {
			return nil, nil, 0, err
		}
	}
}

func fetchUpstreamCodexCatalog(ctx context.Context, g Gateway, key catalogKey, url string) (map[string]json.RawMessage, time.Duration, error) {
	secret, err := RevealAccessToken(ctx, g, key.tokenID, url)
	if err != nil {
		return nil, 0, err
	}
	entries, err := readUpstreamCodexCatalog(ctx, key.origin, secret)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "Codex model discovery unavailable: %v\n", err)
		return nil, 5 * time.Second, nil
	}
	return entries, time.Minute, nil
}

// CodexModelReasoning contains only the chooser data the webview displays.
type CodexModelReasoning struct {
	Levels    []string `json:"levels"`
	Default   *string  `json:"default"`
	Reasoning *bool    `json:"reasoning"`
}

func (info ModelDetails) CodexReasoning() CodexModelReasoning {
	declared := contract.ModelReasoning(info.CodexCatalog, info.Reasoning)
	value := CodexModelReasoning{Levels: []string{}, Default: declared.Default}
	for _, level := range declared.Supported {
		value.Levels = append(value.Levels, level.Effort)
	}
	if info.ReasoningKnown {
		value.Reasoning = Some(info.HasReasoning)
	}
	return value
}
