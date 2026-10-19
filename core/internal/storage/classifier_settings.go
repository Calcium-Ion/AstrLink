package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ClassifierSettingsStore persists the singleton external-classifier
// configuration document. The API key never crosses this interface: it lives
// only behind the sealed credential ref inside the document.
type ClassifierSettingsStore interface {
	GetClassifierSettings(context.Context) (contract.ClassifierSettings, error)
	UpdateClassifierSettings(context.Context, contract.ClassifierSettings) error
}
