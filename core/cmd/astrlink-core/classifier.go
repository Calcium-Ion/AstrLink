package main

import (
	"path/filepath"

	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/automodel"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

// classifierRegistryRootName mirrors the privacy-model root convention. The
// future controlapi installer side must install under this same root name
// (share this constant or its construction point) so an installed model never
// silently diverges from the directory Core reads.
const classifierRegistryRootName = "classifier-model"

type classifierOptions struct {
	workerPath    string
	dataDirectory string
	siblingPath   func() (string, error)
	newRegistry   func(root string) (autoclassifier.ReadyInstallationProvider, error)
	logf          func(string, ...any)
}

// buildClassifierDependency wires the bundled classifier worker for model
// routing. Fail-open is the product contract: any construction failure only
// logs and returns (nil, nil) so routing keeps its deterministic fallback.
// This polarity is deliberately opposite to the privacy worker's fail-closed
// startup exit — a routing hint must never block the request.
func buildClassifierDependency(options classifierOptions) (ingress.Classifier, func()) {
	logf := options.logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	workerPath := options.workerPath
	if workerPath == "" {
		siblingPath := options.siblingPath
		if siblingPath == nil {
			siblingPath = autoclassifier.SiblingExecutablePath
		}
		path, err := siblingPath()
		if err != nil {
			logf("locate classifier worker: %v", err)
			return nil, nil
		}
		workerPath = path
	}
	newRegistry := options.newRegistry
	if newRegistry == nil {
		newRegistry = func(root string) (autoclassifier.ReadyInstallationProvider, error) {
			return automodel.NewRegistry(root)
		}
	}
	registry, err := newRegistry(filepath.Join(options.dataDirectory, classifierRegistryRootName))
	if err != nil {
		logf("configure classifier model registry: %v", err)
		return nil, nil
	}
	client, err := autoclassifier.New(autoclassifier.Config{
		ExecutablePath: workerPath,
		Model:          registry,
	})
	if err != nil {
		logf("configure classifier worker: %v", err)
		return nil, nil
	}
	return client, client.Close
}
