package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type CatalogStore interface {
	PricingCatalog(context.Context) (Catalog, error)
	SavePricingCatalog(context.Context, Catalog) error
}
type Manager struct {
	store     CatalogStore
	client    *http.Client
	base      string
	mu        sync.Mutex
	syncMu    sync.Mutex
	checked   *time.Time
	lastError string
}

func NewManager(store CatalogStore, client *http.Client) *Manager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{store: store, client: client, base: SourceURL}
}
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status(ctx)
}
func (m *Manager) status(ctx context.Context) (Status, error) {
	c, err := m.store.PricingCatalog(ctx)
	if err != nil {
		return Status{}, err
	}
	s := Status{Source: SourceURL, Version: c.Version, CheckedAt: m.checked, Error: m.lastError, Models: len(c.Prices), Warnings: c.Warnings}
	if s.Warnings == nil {
		s.Warnings = []string{}
	}
	if !c.ActivatedAt.IsZero() {
		s.UpdatedAt = &c.ActivatedAt
	}
	return s, nil
}
func (m *Manager) get(ctx context.Context, path, etag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.base+"/"+path, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", transport.SupportedResponseEncodings)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("price source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		return nil, etag, true, nil
	}
	if resp.StatusCode != 200 {
		return nil, "", false, fmt.Errorf("price source HTTP %d", resp.StatusCode)
	}
	body, err := transport.ReadResponseBody(resp, 8<<20)
	if err != nil {
		return nil, "", false, fmt.Errorf("invalid price source response")
	}
	return body, resp.Header.Get("ETag"), false, nil
}
func (m *Manager) Sync(ctx context.Context) (status Status, err error) {
	if !m.syncMu.TryLock() {
		return Status{}, fmt.Errorf("price sync already in progress")
	}
	defer m.syncMu.Unlock()
	m.mu.Lock()
	now := time.Now().UTC()
	m.checked = &now
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if err != nil {
			m.lastError = err.Error()
		} else {
			m.lastError = ""
		}
		m.mu.Unlock()
		status, _ = m.Status(ctx)
	}()
	old, err := m.store.PricingCatalog(ctx)
	if err != nil {
		return status, err
	}
	manifest, etag, unchanged, err := m.get(ctx, "manifest.json", old.ETag)
	if err != nil || unchanged {
		return status, err
	}
	var meta struct {
		Version     int       `json:"version"`
		GeneratedAt time.Time `json:"generatedAt"`
		Warnings    []string  `json:"warnings"`
	}
	if json.Unmarshal(manifest, &meta) != nil || meta.Version != 1 || meta.GeneratedAt.IsZero() {
		return status, fmt.Errorf("unsupported pricing manifest")
	}
	all, _, _, err := m.get(ctx, "all.json", "")
	if err != nil {
		return status, err
	}
	var providers map[string]struct {
		Models map[string]struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(all, &providers) != nil {
		return status, fmt.Errorf("invalid pricing catalog")
	}
	prices := []Price{}
	warnings := append([]string{}, meta.Warnings...)
	parts := []string{string(manifest)}
	ids := []string{}
	for id := range providers {
		if Providers[id] != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		body, _, _, e := m.get(ctx, "newapi/providers/"+id+"/ratio_config-v1-base.json", "")
		if e != nil {
			return status, e
		}
		var rules struct {
			Success bool `json:"success"`
			Data    struct {
				Expressions map[string]string `json:"billing_expr"`
				Modes       map[string]string `json:"billing_mode"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &rules) != nil || !rules.Success {
			return status, fmt.Errorf("invalid provider prices: %s", id)
		}
		parts = append(parts, string(body))
		models := []string{}
		for model := range rules.Data.Expressions {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			expr := rules.Data.Expressions[model]
			if len(model) > 256 || rules.Data.Modes[model] != "tiered_expr" {
				return status, fmt.Errorf("unsupported provider price: %s", id)
			}
			if e := ValidateExpression(expr); e != nil {
				warnings = append(warnings, id+"/"+model+": unsupported billing expression")
				continue
			}
			prices = append(prices, Price{Provider: id, Model: model, Name: providers[id].Models[model].Name, Expression: expr})
		}
	}
	if len(prices) == 0 {
		return status, fmt.Errorf("no official API prices found")
	}
	// Reject a deployment that changed during the multi-file download.
	after, _, _, err := m.get(ctx, "manifest.json", "")
	if err != nil {
		return status, err
	}
	if string(after) != string(manifest) {
		return status, fmt.Errorf("price publication changed; retry sync")
	}
	c := Catalog{Version: Digest(parts...), GeneratedAt: meta.GeneratedAt, ActivatedAt: time.Now().UTC(), ETag: etag, Prices: prices, Warnings: warnings}
	err = m.store.SavePricingCatalog(ctx, c)
	return status, err
}

// Run synchronizes in Core, independently of any frontend page lifecycle.
func (m *Manager) Run(ctx context.Context, logf func(string, ...any)) {
	for {
		c, err := m.store.PricingCatalog(ctx)
		m.mu.Lock()
		checked := c.ActivatedAt
		if m.checked != nil && m.checked.After(checked) {
			checked = *m.checked
		}
		due := err != nil || m.lastError != "" || checked.IsZero() || time.Since(checked) >= 24*time.Hour
		m.mu.Unlock()
		if due {
			syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			_, err = m.Sync(syncCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				logf("price sync: %v", err)
			}
		}
		// Calls recorded before their first price sync are filled automatically.
		// Already valued entries retain their original price version.
		if pending, ok := m.store.(interface {
			PriceUnpriced(context.Context) (int, error)
		}); ok && ctx.Err() == nil {
			if _, err := pending.PriceUnpriced(ctx); err != nil && ctx.Err() == nil {
				logf("pending price valuation: %v", err)
			}
		}
		timer := time.NewTimer(5 * time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
