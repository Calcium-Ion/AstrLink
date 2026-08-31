package automodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

const (
	installationsDirName         = "installations"
	stagingDirName               = "staging"
	manifestName                 = "astrlink-classifier-model.json"
	maxInstallations             = 8
	localProbeCacheLimit         = 16
	localProbeCacheTTL           = 10 * time.Minute
	localRepoPrefix              = "local/model-"
	maxInstallationManifestBytes = 256 << 10
)

type Asset struct {
	Path   string
	Size   int64
	SHA256 string
}

type cachedProbe struct {
	directory    string
	response     contract.AutoClassifierProbeResponse
	assets       []Asset
	modelPath    string
	tokenizer    string
	config       string
	artifactTier contract.AutoClassifierArtifactTier
	releaseMode  string
	expiresAt    time.Time
}

type Registry struct {
	root  string
	mu    sync.Mutex
	busy  bool
	cache map[string]cachedProbe
}

func NewRegistry(root string) (*Registry, error) {
	if root == "" {
		return nil, ErrInvalidConfig
	}
	if err := os.MkdirAll(filepath.Join(root, installationsDirName), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, stagingDirName), 0o700); err != nil {
		return nil, err
	}
	return &Registry{
		root:  root,
		cache: map[string]cachedProbe{},
	}, nil
}

func InstallationID(repoID, revision string) contract.AutoClassifierID {
	digest := sha256.Sum256([]byte("classifier\n" + repoID + "\n" + revision))
	return contract.AutoClassifierID("classifier_" + hex.EncodeToString(digest[:16]))
}

func (registry *Registry) ListInstallations() []contract.AutoClassifierInstallation {
	items, _ := registry.scanInstallations()
	return items
}

func (registry *Registry) GetInstallation(
	id contract.AutoClassifierID,
) (contract.AutoClassifierInstallation, error) {
	items, ready := registry.scanInstallations()
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	for _, item := range ready {
		if item.ID == id {
			return contract.AutoClassifierInstallation{
				ID:                 item.ID,
				Identity:           item.Identity,
				Name:               item.Identity,
				ArtifactTier:       item.ArtifactTier,
				EligibleForRouting: contract.EligibleForRouting(item.ArtifactTier),
				TaxonomyID:         autotaxonomy.ID,
				Status:             contract.AutoClassifierStatusReady,
				BytesTotal:         1,
			}, nil
		}
	}
	return contract.AutoClassifierInstallation{}, ErrNotFound
}

func (registry *Registry) ReadyInstallation(
	id contract.AutoClassifierID,
) (contract.ReadyAutoClassifierInstallation, bool) {
	_, ready := registry.scanInstallations()
	for _, item := range ready {
		if item.ID == id {
			return item, true
		}
	}
	return contract.ReadyAutoClassifierInstallation{}, false
}

func (registry *Registry) FirstReady() (contract.ReadyAutoClassifierInstallation, bool) {
	_, ready := registry.scanInstallations()
	if len(ready) == 0 {
		return contract.ReadyAutoClassifierInstallation{}, false
	}
	return ready[0], true
}

func (registry *Registry) scanInstallations() (
	[]contract.AutoClassifierInstallation,
	[]contract.ReadyAutoClassifierInstallation,
) {
	root := filepath.Join(registry.root, installationsDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	items := make([]contract.AutoClassifierInstallation, 0, len(entries))
	ready := make([]contract.ReadyAutoClassifierInstallation, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := contract.AutoClassifierID(entry.Name())
		if id.Validate() != nil {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		installation, readyItem, ok := readInstalled(directory, id)
		if !ok {
			continue
		}
		items = append(items, installation)
		ready = append(ready, readyItem)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].ID < items[right].ID
	})
	sort.Slice(ready, func(left, right int) bool {
		return ready[left].ID < ready[right].ID
	})
	return items, ready
}

func readInstalled(
	directory string,
	id contract.AutoClassifierID,
) (contract.AutoClassifierInstallation, contract.ReadyAutoClassifierInstallation, bool) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return contract.AutoClassifierInstallation{}, contract.ReadyAutoClassifierInstallation{}, false
	}
	document, err := os.ReadFile(filepath.Join(directory, manifestName))
	if err != nil || len(document) == 0 || len(document) > maxInstallationManifestBytes {
		return contract.AutoClassifierInstallation{}, contract.ReadyAutoClassifierInstallation{}, false
	}
	var manifest installationManifest
	if json.Unmarshal(document, &manifest) != nil ||
		manifest.InstallationID != string(id) ||
		manifest.validate() != nil {
		return contract.AutoClassifierInstallation{}, contract.ReadyAutoClassifierInstallation{}, false
	}
	bytesTotal := int64(0)
	for _, file := range manifest.Files {
		bytesTotal += file.Size
	}
	digest := sha256.Sum256(document)
	installation := contract.AutoClassifierInstallation{
		ID:                 id,
		Identity:           manifest.Identity,
		Name:               displayName(manifest.Identity),
		ArtifactTier:       contract.AutoClassifierArtifactTier(manifest.ArtifactTier),
		EligibleForRouting: contract.EligibleForRouting(contract.AutoClassifierArtifactTier(manifest.ArtifactTier)),
		TaxonomyID:         manifest.TaxonomyID,
		Status:             contract.AutoClassifierStatusReady,
		BytesTotal:         bytesTotal,
	}
	return installation, contract.ReadyAutoClassifierInstallation{
		ID:             id,
		Directory:      directory,
		Identity:       manifest.Identity,
		ManifestSHA256: hex.EncodeToString(digest[:]),
		ArtifactTier:   contract.AutoClassifierArtifactTier(manifest.ArtifactTier),
	}, true
}

func displayName(identity string) string {
	if identity == "" {
		return "Local classifier"
	}
	return "Local classifier · " + identity
}

func (registry *Registry) cacheProbe(entry cachedProbe) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := time.Now()
	for key, cached := range registry.cache {
		if !cached.expiresAt.After(now) {
			delete(registry.cache, key)
		}
	}
	if _, exists := registry.cache[entry.directory]; !exists &&
		len(registry.cache) >= localProbeCacheLimit {
		var oldestKey string
		var oldest time.Time
		for key, cached := range registry.cache {
			if oldestKey == "" || cached.expiresAt.Before(oldest) {
				oldestKey = key
				oldest = cached.expiresAt
			}
		}
		delete(registry.cache, oldestKey)
	}
	entry.expiresAt = now.Add(localProbeCacheTTL)
	registry.cache[entry.directory] = entry
}

func (registry *Registry) takeCachedProbe(directory string) (cachedProbe, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry, exists := registry.cache[directory]
	if !exists || !entry.expiresAt.After(time.Now()) {
		delete(registry.cache, directory)
		return cachedProbe{}, false
	}
	return entry, true
}

func localRepoID(revision string) string {
	if len(revision) < 12 {
		revision = revision + strings.Repeat("0", 12)
	}
	return localRepoPrefix + revision[:12]
}

func (registry *Registry) withBusy() (func(), error) {
	registry.mu.Lock()
	if registry.busy {
		registry.mu.Unlock()
		return nil, ErrBusy
	}
	registry.busy = true
	registry.mu.Unlock()
	return func() {
		registry.mu.Lock()
		registry.busy = false
		registry.mu.Unlock()
	}, nil
}

func IsLocalClassifierRepoID(repoID string) bool {
	if len(repoID) != len(localRepoPrefix)+12 ||
		!strings.HasPrefix(repoID, localRepoPrefix) {
		return false
	}
	for _, character := range repoID[len(localRepoPrefix):] {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
