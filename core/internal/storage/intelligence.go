package storage

import (
	"context"
	"encoding/json"
)

// IntelligenceStore owns evaluation configuration and immutable run snapshots.
type IntelligenceStore interface {
	LoadIntelligence(context.Context, string) (json.RawMessage, error)
	SaveIntelligence(context.Context, string, string, string, json.RawMessage) error
	ListIntelligenceRuns(context.Context, string) ([]json.RawMessage, error)
}
