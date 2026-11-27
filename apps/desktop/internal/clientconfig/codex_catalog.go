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
type CodexCatalogCache struct {
	mu                       sync.Mutex
	session, tokenID, origin string
	until                    time.Time
	upstream                 map[string]json.RawMessage
	details                  Catalog
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
	c.mu.Lock()
	defer c.mu.Unlock()
	active, session, ready := g.Session()
	if !ready || active != url {
		return nil, errSessionChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.session != session || c.tokenID != tokenID || c.origin != origin || time.Now().After(c.until) {
		secret, err := RevealAccessToken(ctx, g, tokenID, url)
		if err != nil {
			return nil, err
		}
		entries, err := readUpstreamCodexCatalog(ctx, origin, secret)
		ttl := time.Minute
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "Codex model discovery unavailable: %v\n", err)
			ttl = 5 * time.Second
		}
		c.session, c.tokenID, c.origin, c.until = session, tokenID, origin, time.Now().Add(ttl)
		c.upstream, c.details = entries, Catalog{}
	}
	result := make(Catalog, len(models))
	var missing []string
	for _, model := range models {
		if info, ok := c.details[model]; ok {
			result[model] = info
			continue
		}
		if _, seen := result[model]; seen {
			continue
		}
		var declaration struct {
			Slug      string            `json:"slug"`
			Supported []json.RawMessage `json:"supported_reasoning_levels"`
		}
		raw := c.upstream[model]
		if json.Unmarshal(raw, &declaration) != nil || declaration.Slug == "" {
			raw = nil
		}
		result[model] = ModelDetails{CodexCatalog: raw}
		if raw == nil || declaration.Supported == nil {
			missing = append(missing, model)
		}
	}
	if len(missing) > 0 {
		metadata, err := g.ModelMetadataForModels(ctx, tokenID, missing)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "Codex official model metadata unavailable: %v\n", err)
			c.until = time.Now().Add(5 * time.Second)
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
	for model, info := range result {
		if len(c.details) < 4096 {
			c.details[model] = info
		}
	}
	return result, nil
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
