package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// PrivacyModelManifestBinding is the private, persistent integrity binding for
// a ready installation. ManifestJSON is the exact normalized
// astrlink-model.json document that was verified before atomic publication;
// it includes the runtime configuration and complete file hash list. These
// fields are deliberately absent from the public control API.
type PrivacyModelManifestBinding struct {
	Identity string
	SHA256   string
	JSON     []byte
}

type PrivacyModelInstallationRecord struct {
	Installation contract.PrivacyModelInstallation
	Manifest     *PrivacyModelManifestBinding
}

type PrivacyModelInstallationStore interface {
	ListPrivacyModelInstallations(context.Context) ([]PrivacyModelInstallationRecord, error)
	GetPrivacyModelInstallation(context.Context, contract.PrivacyModelID) (PrivacyModelInstallationRecord, error)
	PutPrivacyModelInstallation(context.Context, PrivacyModelInstallationRecord) error
	DeletePrivacyModelInstallation(context.Context, contract.PrivacyModelID) error
}
