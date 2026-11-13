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
// (src/native-host/commands-release.ts).
type Release struct {
	Models *ModelUpdates
}

// PrivacyModelUpdateStatus is the privacy model reminder, null without one.
func (r Release) PrivacyModelUpdateStatus() *PrivacyModelUpdate { return r.Models.Status() }
