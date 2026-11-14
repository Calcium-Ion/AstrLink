package plugininstall

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Bounds of the packages one build fetches.
const (
	// maxPackages bounds the packages placed.
	maxPackages = 400
	// maxPackageArchiveBytes bounds one package's archive.
	maxPackageArchiveBytes = 32 << 20
	// maxPackageBytes and maxPackageFiles bound one package unpacked.
	maxPackageBytes = 128 << 20
	maxPackageFiles = 20000
	// maxPackagesBytes bounds every package unpacked together.
	maxPackagesBytes = 256 << 20
)

// registryFetcher fetches package archives from the registries in turn,
// keeping only what matches the lock file's hash.
type registryFetcher struct {
	client     *http.Client
	registries []string
}

// fetch returns the archive at path whose content integrity names.
func (fetcher *registryFetcher) fetch(ctx context.Context, path, integrity, label string) ([]byte, error) {
	check, err := parseIntegrity(integrity)
	if err != nil {
		return nil, fail(contract.PluginInstallIntegrity, label)
	}
	mismatched := false
	for _, registry := range fetcher.registries {
		data, err := fetcher.get(ctx, registry+path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var failure *Failure
			if errors.As(err, &failure) {
				return nil, failure
			}
			continue
		}
		if !check(data) {
			mismatched = true
			continue
		}
		return data, nil
	}
	if mismatched {
		return nil, fail(contract.PluginInstallIntegrity, label)
	}
	return nil, fail(contract.PluginInstallRegistryUnavailable, label)
}

func (fetcher *registryFetcher) get(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New(response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxPackageArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPackageArchiveBytes {
		return nil, fail(contract.PluginInstallTooLarge, address[strings.LastIndex(address, "/")+1:])
	}
	return data, nil
}

// parseIntegrity reads a subresource integrity value, as lock files keep
// it, into a check of content: the strongest hash it lists. sha1 counts
// only for an old lock file that keeps nothing stronger.
func parseIntegrity(text string) (func([]byte) bool, error) {
	strength := map[string]int{"sha1": 1, "sha256": 2, "sha512": 3}
	best, bestDigest := "", []byte(nil)
	for field := range strings.FieldsSeq(text) {
		algorithm, encoded, ok := strings.Cut(field, "-")
		if !ok || strength[algorithm] == 0 || strength[algorithm] <= strength[best] {
			continue
		}
		encoded, _, _ = strings.Cut(encoded, "?")
		digest, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		best, bestDigest = algorithm, digest
	}
	var newHash func() hash.Hash
	switch best {
	case "sha512":
		newHash = sha512.New
	case "sha256":
		newHash = sha256.New
	case "sha1":
		newHash = sha1.New
	default:
		return nil, errors.New("no usable hash")
	}
	if len(bestDigest) != newHash().Size() {
		return nil, errors.New("malformed hash")
	}
	return func(data []byte) bool {
		sum := newHash()
		sum.Write(data)
		return bytes.Equal(sum.Sum(nil), bestDigest)
	}, nil
}

// packageStore places the packages a build imports, each the first time
// it is imported: fetched, checked against the lock file's hash, and
// unpacked into a directory of its own. It is the build's Packages.
type packageStore struct {
	ctx     context.Context
	root    string
	lock    lockfile
	fetcher *registryFetcher

	mutex   sync.Mutex
	placed  map[string]*placement
	total   int64
	failure *Failure
	// packages are what the build took, by name@version.
	packages map[string]*contract.PluginDependency
}

// placement is a package placed, or being placed, for the name it is
// imported as.
type placement struct {
	key  string
	name string
	// dir holds node_modules/<name>.
	dir  string
	done chan struct{}
	err  error
}

func newPackageStore(ctx context.Context, root string, lock lockfile, fetcher *registryFetcher) *packageStore {
	return &packageStore{ctx: ctx, root: root, lock: lock, fetcher: fetcher, placed: map[string]*placement{}, packages: map[string]*contract.PluginDependency{}}
}

// Locate places the package that importer's code gets for name.
func (store *packageStore) Locate(importer, name string) (string, bool, error) {
	from := store.owner(importer)
	if store.lock == nil {
		return "", false, store.note(fail(contract.PluginInstallNoLockfile, name))
	}
	fromKey := ""
	if from != nil {
		fromKey = from.key
	}
	key, ok := store.lock.dependency(fromKey, name)
	if !ok {
		// A package may import itself by its own name.
		if from != nil && from.name == name {
			return from.dir, true, nil
		}
		return "", false, nil
	}
	placed, err := store.place(key, name)
	if err != nil {
		return "", false, store.note(err)
	}
	return placed.dir, true, nil
}

// note keeps the first failure, which the install reports instead of the
// build's.
func (store *packageStore) note(err error) error {
	var failure *Failure
	if errors.As(err, &failure) {
		store.mutex.Lock()
		if store.failure == nil {
			store.failure = failure
		}
		store.mutex.Unlock()
	}
	return err
}

// owner is the placement a file belongs to, nil for the project's own.
func (store *packageStore) owner(importer string) *placement {
	rel, err := filepath.Rel(store.root, importer)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil
	}
	id, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return store.placed[id]
}

// place places the package key names under name, once.
func (store *packageStore) place(key, name string) (*placement, error) {
	sum := sha256.Sum256([]byte(key + "\x00" + name))
	id := hex.EncodeToString(sum[:8])
	store.mutex.Lock()
	placed, ok := store.placed[id]
	if !ok {
		if len(store.placed) >= maxPackages {
			store.mutex.Unlock()
			return nil, fail(contract.PluginInstallTooLarge, "packages")
		}
		placed = &placement{key: key, name: name, dir: filepath.Join(store.root, id), done: make(chan struct{})}
		store.placed[id] = placed
	}
	store.mutex.Unlock()
	if ok {
		select {
		case <-placed.done:
			return placed, placed.err
		case <-store.ctx.Done():
			return nil, store.ctx.Err()
		}
	}
	placed.err = store.unpack(placed)
	close(placed.done)
	return placed, placed.err
}

// unpack fetches and unpacks a placement's package.
func (store *packageStore) unpack(placed *placement) error {
	locked, err := store.lock.resolve(placed.key)
	if err != nil {
		return err
	}
	label := locked.Name + "@" + locked.Version
	if locked.Integrity == "" {
		return fail(contract.PluginInstallIntegrity, label)
	}
	archive, err := store.fetcher.fetch(store.ctx, locked.Path, locked.Integrity, label)
	if err != nil {
		return err
	}
	dest := filepath.Join(placed.dir, "node_modules", filepath.FromSlash(placed.name))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	files, err := extractPackage(bytes.NewReader(archive), dest, archiveLimits{files: maxPackageFiles, bytes: maxPackageBytes})
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			failure.Detail = label + " " + failure.Detail
			return failure
		}
		return fail(contract.PluginInstallUnavailable, label+": "+err.Error())
	}
	manifest := readPackageManifest(dest)
	if files.native != "" || manifest.Gypfile {
		return fail(contract.PluginInstallNativeModule, label)
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.total += files.bytes
	if store.total > maxPackagesBytes {
		return fail(contract.PluginInstallTooLarge, "packages")
	}
	if store.packages[label] == nil {
		store.packages[label] = &contract.PluginDependency{
			Name: locked.Name, Version: locked.Version, Bytes: files.bytes, Scripts: manifest.hasInstallScripts(),
		}
	}
	return nil
}

// dependencies are the packages the build took, by name.
func (store *packageStore) dependencies() []contract.PluginDependency {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	list := make([]contract.PluginDependency, 0, len(store.packages))
	for _, dependency := range store.packages {
		list = append(list, *dependency)
	}
	slices.SortFunc(list, func(a, b contract.PluginDependency) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.Version, b.Version)
	})
	return list
}

// packageManifest is what the installer reads of a package's package.json.
type packageManifest struct {
	Gypfile bool              `json:"gypfile"`
	Scripts map[string]string `json:"scripts"`
}

func readPackageManifest(dir string) packageManifest {
	var manifest packageManifest
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err == nil && len(data) <= 1<<20 {
		_ = json.Unmarshal(data, &manifest)
	}
	return manifest
}

// hasInstallScripts reports whether npm would run a script when installing
// the package, which the installer never does.
func (manifest packageManifest) hasInstallScripts() bool {
	for _, name := range []string{"preinstall", "install", "postinstall"} {
		if strings.TrimSpace(manifest.Scripts[name]) != "" {
			return true
		}
	}
	return false
}
