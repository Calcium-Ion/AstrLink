package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// The privacy model reminder tells the operator when the privacy model the
// policy runs has a newer release, until the policy runs it. A configured
// gateway runs unattended, so the update hint on the privacy page alone
// reaches almost nobody. Installing and switching to the new version stay
// manual.
const (
	// Core caches release lookups for an hour, so each check reaches the
	// Hub.
	modelCheckInterval = 3 * time.Hour
	modelRetryInterval = 15 * time.Minute
	// The policy and installations are local reads. Reading them again
	// notices a finished download, or a switch made outside the privacy
	// page.
	modelEvaluateInterval = time.Minute
)

// ModelPhase is how far the newer model release is.
type ModelPhase string

const (
	// ModelAvailable still has to be downloaded.
	ModelAvailable ModelPhase = "available"
	// ModelReady is installed and waits for the policy to switch.
	ModelReady ModelPhase = "ready"
)

// PrivacyModelUpdate is a newer release of the model the policy runs.
type PrivacyModelUpdate struct {
	CatalogID string     `json:"catalog_id"`
	Name      string     `json:"name"`
	Version   string     `json:"version"`
	Phase     ModelPhase `json:"phase"`
}

// ModelSource reads what the reminder needs from the running Core.
type ModelSource interface {
	// Catalog is the models the app pins; Releases the newest release of
	// each, which Core answers from its own cache.
	Catalog(ctx context.Context) (json.RawMessage, error)
	Releases(ctx context.Context) (json.RawMessage, error)
	// Policy is the privacy policy record ({"policy": …}), Installations
	// the installed models ({"items": […]}).
	Policy(ctx context.Context) (json.RawMessage, error)
	Installations(ctx context.Context) (json.RawMessage, error)
}

// ModelUpdates keeps the reminder.
type ModelUpdates struct {
	Source ModelSource
	// Ready reports whether Core is ready to answer.
	Ready func() bool
	// Publish hands the main window the reminder, nil once it settled.
	Publish func(update *PrivacyModelUpdate)
	// Announce shows a reminder the main window may not show, once per
	// reminder.
	Announce func(update PrivacyModelUpdate)

	now  func() time.Time
	wake chan struct{}

	mu sync.Mutex
	// releases are the newest release of each catalog model, from the last
	// successful check; nil before one.
	releases  []catalogModel
	nextCheck time.Time
	update    *PrivacyModelUpdate
	notified  *PrivacyModelUpdate
}

func (m *ModelUpdates) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *ModelUpdates) wakeChannel() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wake == nil {
		m.wake = make(chan struct{}, 1)
	}
	return m.wake
}

// Status is the reminder, nil without one.
func (m *ModelUpdates) Status() *PrivacyModelUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.update == nil {
		return nil
	}
	update := *m.update
	return &update
}

// Wake reads the policy and installations again without waiting for the
// tick, as after the policy changed.
func (m *ModelUpdates) Wake() {
	select {
	case m.wakeChannel() <- struct{}{}:
	default:
	}
}

// Recheck also reads the releases again, which Core answers from its own
// cache, as after the privacy page read them.
func (m *ModelUpdates) Recheck() {
	m.mu.Lock()
	m.nextCheck = time.Time{}
	m.mu.Unlock()
	m.Wake()
}

// Run checks while Core is ready, every minute or when woken, until ctx
// ends.
func (m *ModelUpdates) Run(ctx context.Context) {
	wake := m.wakeChannel()
	ticker := time.NewTicker(modelEvaluateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
		if m.Ready() {
			m.tick(ctx)
		}
	}
}

func (m *ModelUpdates) tick(ctx context.Context) {
	now := m.clock()
	m.mu.Lock()
	due := m.nextCheck.IsZero() || !now.Before(m.nextCheck)
	m.mu.Unlock()
	if due {
		releases, err := m.readReleases(ctx)
		m.mu.Lock()
		if err != nil {
			log.Printf("astrlink: unable to check privacy model releases: %v", err)
			m.nextCheck = now.Add(modelRetryInterval)
		} else {
			m.releases = releases
			m.nextCheck = now.Add(modelCheckInterval)
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	releases := m.releases
	m.mu.Unlock()
	if releases == nil {
		return
	}
	policy, installations, err := m.readInUse(ctx)
	if err != nil {
		// Core may be restarting; the next tick reads again.
		log.Printf("astrlink: unable to read the privacy model in use: %v", err)
		return
	}
	m.publish(pendingUpdate(releases, installations, policy))
}

func (m *ModelUpdates) readReleases(ctx context.Context) ([]catalogModel, error) {
	var catalog, releases modelCatalog
	if err := readJSON(ctx, m.Source.Catalog, &catalog); err != nil {
		return nil, err
	}
	if err := readJSON(ctx, m.Source.Releases, &releases); err != nil {
		return nil, err
	}
	return latestReleases(catalog, releases), nil
}

func (m *ModelUpdates) readInUse(ctx context.Context) (privacyPolicy, []installation, error) {
	var record struct {
		Policy privacyPolicy `json:"policy"`
	}
	var list struct {
		Items []installation `json:"items"`
	}
	if err := readJSON(ctx, m.Source.Policy, &record); err != nil {
		return privacyPolicy{}, nil, err
	}
	if err := readJSON(ctx, m.Source.Installations, &list); err != nil {
		return privacyPolicy{}, nil, err
	}
	return record.Policy, list.Items, nil
}

func readJSON(ctx context.Context, read func(context.Context) (json.RawMessage, error), v any) error {
	body, err := read(ctx)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("invalid response: %v", err)
	}
	return nil
}

func samePending(a, b *PrivacyModelUpdate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (m *ModelUpdates) publish(update *PrivacyModelUpdate) {
	m.mu.Lock()
	if samePending(m.update, update) {
		m.mu.Unlock()
		return
	}
	m.update = update
	var announce *PrivacyModelUpdate
	if update != nil && !samePending(m.notified, update) {
		announce = update
		m.notified = update
	}
	m.mu.Unlock()
	if m.Publish != nil {
		m.Publish(update)
	}
	if announce != nil && m.Announce != nil {
		m.Announce(*announce)
	}
}

type modelCatalog struct {
	Items []catalogModel `json:"items"`
}

type catalogModel struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	RepoID   string           `json:"repo_id"`
	Revision string           `json:"revision"`
	Version  *string          `json:"version"`
	Variants []catalogVariant `json:"variants"`
}

type catalogVariant struct {
	ID        string `json:"id"`
	Supported bool   `json:"supported"`
}

type installation struct {
	ID        string  `json:"id"`
	Source    string  `json:"source"`
	CatalogID *string `json:"catalog_id"`
	RepoID    string  `json:"repo_id"`
	Revision  string  `json:"revision"`
	VariantID string  `json:"variant_id"`
	Status    string  `json:"status"`
}

type privacyPolicy struct {
	Enabled      bool    `json:"enabled"`
	Detector     string  `json:"detector"`
	LocalModelID *string `json:"local_model_id"`
}

// modelVersion is a catalog model's version, false when it has none or it
// is not a semantic version.
func modelVersion(model catalogModel) (semVersion, bool) {
	if model.Version == nil {
		return semVersion{}, false
	}
	return parseVersion(*model.Version)
}

// latestReleases picks the newest release of each catalog model, matching
// the privacy page's own update hint: a release that supersedes the pinned
// entry, otherwise the pinned entry itself, which installs of an earlier
// pin still move to.
func latestReleases(catalog, releases modelCatalog) []catalogModel {
	latest := []catalogModel{}
	for _, pinned := range catalog.Items {
		var release *catalogModel
		for i := range releases.Items {
			if releases.Items[i].ID == pinned.ID {
				release = &releases.Items[i]
				break
			}
		}
		if release == nil {
			continue
		}
		released, releasedOK := modelVersion(*release)
		current, currentOK := modelVersion(pinned)
		newer := release.RepoID == pinned.RepoID && release.Revision != pinned.Revision &&
			releasedOK && currentOK && released.compare(current) > 0
		if newer {
			latest = append(latest, *release)
		} else {
			latest = append(latest, pinned)
		}
	}
	return latest
}

// pendingUpdate is the reminder for the model the enabled policy runs, the
// only one that earns it. It moves to ready once the newer revision of
// that variant is installed and clears when the policy switches to it.
func pendingUpdate(releases []catalogModel, installations []installation, policy privacyPolicy) *PrivacyModelUpdate {
	if !policy.Enabled || policy.Detector != "local_model" || policy.LocalModelID == nil {
		return nil
	}
	var current *installation
	for i := range installations {
		if installations[i].ID == *policy.LocalModelID && installations[i].Source == "catalog" {
			current = &installations[i]
			break
		}
	}
	if current == nil || current.CatalogID == nil {
		return nil
	}
	var release *catalogModel
	for i := range releases {
		if releases[i].ID == *current.CatalogID {
			release = &releases[i]
			break
		}
	}
	if release == nil {
		return nil
	}
	supported := false
	for _, variant := range release.Variants {
		if variant.ID == current.VariantID && variant.Supported {
			supported = true
		}
	}
	if release.RepoID != current.RepoID || release.Revision == current.Revision || !supported || release.Version == nil {
		return nil
	}
	// A download in progress or failed still needs the operator to finish
	// it.
	phase := ModelAvailable
	for _, other := range installations {
		if other.RepoID == release.RepoID && other.Revision == release.Revision &&
			other.VariantID == current.VariantID && other.Status == "ready" {
			phase = ModelReady
		}
	}
	return &PrivacyModelUpdate{CatalogID: release.ID, Name: release.Name, Version: *release.Version, Phase: phase}
}
