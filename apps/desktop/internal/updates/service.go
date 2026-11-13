package updates

import (
	"context"
	"encoding/json"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
)

// CoreModels reads the privacy model reminder's inputs through the control
// API. It is a control.Privacy without its hooks, so the reminder's own
// release reads do not wake it again.
type CoreModels struct{ Privacy control.Privacy }

func (c CoreModels) Catalog(ctx context.Context) (json.RawMessage, error) {
	return c.Privacy.GetPrivacyModelCatalog(ctx)
}

func (c CoreModels) Releases(ctx context.Context) (json.RawMessage, error) {
	return c.Privacy.GetPrivacyModelReleases(ctx, nil)
}

func (c CoreModels) Policy(ctx context.Context) (json.RawMessage, error) {
	record, err := c.Privacy.GetPrivacyPolicy(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func (c CoreModels) Installations(ctx context.Context) (json.RawMessage, error) {
	return c.Privacy.ListPrivacyModelInstallations(ctx)
}

// Release is the service of app and privacy model updates
// (src/native-host/commands-release.ts). Checks, downloads and installs
// outlive the page that asked for them.
type Release struct {
	Models *ModelUpdates

	app   *Manager
	ready chan struct{}
}

// NewRelease returns the service; its app update commands wait for Start
// (Attach).
func NewRelease(models *ModelUpdates) *Release {
	return &Release{Models: models, ready: make(chan struct{})}
}

// Attach hands the service the app updater, once.
func Attach(r *Release, m *Manager) {
	r.app = m
	close(r.ready)
}

func (r *Release) manager(ctx context.Context) (*Manager, error) {
	select {
	case <-r.ready:
		return r.app, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AppUpdateStatus is the app update state.
func (r *Release) AppUpdateStatus(ctx context.Context) (UpdateSnapshot, error) {
	m, err := r.manager(ctx)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	return m.Snapshot(), nil
}

// CheckAppUpdate looks for an update now.
func (r *Release) CheckAppUpdate(ctx context.Context) (UpdateSnapshot, error) {
	m, err := r.manager(ctx)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	return m.Check(context.WithoutCancel(ctx))
}

// DownloadAppUpdate downloads and verifies the update a check found.
func (r *Release) DownloadAppUpdate(ctx context.Context) (UpdateSnapshot, error) {
	m, err := r.manager(ctx)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	return m.Download(context.WithoutCancel(ctx))
}

// InstallAppUpdate stops Core, installs the update and starts the new
// version.
func (r *Release) InstallAppUpdate(ctx context.Context) (UpdateSnapshot, error) {
	m, err := r.manager(ctx)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	return m.Install(ctx)
}

// UpdateUpdatePreferences saves the update settings of About.
func (r *Release) UpdateUpdatePreferences(ctx context.Context, input json.RawMessage) (UpdateSnapshot, error) {
	m, err := r.manager(ctx)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	values, err := ParsePreferences(input)
	if err != nil {
		return UpdateSnapshot{}, err
	}
	return m.SetPreferences(values)
}

// PrivacyModelUpdateStatus is the privacy model reminder, null without one.
func (r *Release) PrivacyModelUpdateStatus() *PrivacyModelUpdate { return r.Models.Status() }
