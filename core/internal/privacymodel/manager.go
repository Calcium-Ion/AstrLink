// Package privacymodel owns the lifecycle and integrity boundary for selectable
// local privacy-model installations. The Manager in this file is the internal
// single-install atomic download primitive retained for legacy compatibility
// and focused integrity tests; Registry is the multi-install coordinator.
package privacymodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRevision = "7ffa9a043d54d1be65afb281eddf0ffbe629385b"
	DefaultBaseURL  = "https://huggingface.co/openai/privacy-filter/resolve/" + DefaultRevision + "/"
	errorDownload   = "download_failed"
	readyMarkerName = ".astrlink-ready"
	downloadTimeout = 2 * time.Hour

	// Production downloads have a bounded HTTP deadline. Keeping recent
	// staging directories for one additional hour prevents another Core process
	// from deleting an active download while still giving crash leftovers a
	// deterministic cleanup bound.
	abandonedDownloadStaleAge = downloadTimeout + time.Hour
)

type Status string

const (
	StatusNotInstalled Status = "not_installed"
	StatusDownloading  Status = "downloading"
	StatusReady        Status = "ready"
	StatusError        Status = "error"
)

func (status Status) Valid() bool {
	return status == StatusNotInstalled || status == StatusDownloading ||
		status == StatusReady || status == StatusError
}

type Snapshot struct {
	Status          Status  `json:"status"`
	Revision        string  `json:"revision"`
	BytesDownloaded int64   `json:"bytes_downloaded"`
	BytesTotal      int64   `json:"bytes_total"`
	Error           *string `json:"error"`
}

type Asset struct {
	Path   string
	Size   int64
	SHA256 string
}

type Manifest struct {
	Revision string
	Assets   []Asset
}

func ProductionManifest() Manifest {
	return Manifest{
		Revision: DefaultRevision,
		Assets: []Asset{
			{
				Path: "onnx/model_q4.onnx", Size: 160_219,
				SHA256: "8f7dee8b46d096f052b359375dfba5d983cc4d18c44a783bf548615c472f8dea",
			},
			{
				Path: "onnx/model_q4.onnx_data", Size: 917_120_144,
				SHA256: "f30998e28c71c5374cc7e8b7de8f0f83e981592c0c2d652d2ad4928454dbb496",
			},
			{
				Path: "tokenizer.json", Size: 27_868_174,
				SHA256: "0614fe83cadab421296e664e1f48f4261fa8fef6e03e63bb75c20f38e37d07d3",
			},
			{
				Path: "config.json", Size: 3_039,
				SHA256: "b2b26a4a4a000639ad30b0c264adbefe365bdb567fbd7bb27303b8c438375bd1",
			},
			{
				Path: "viterbi_calibration.json", Size: 372,
				SHA256: "bbc8611ef08a55ed72d64856cbbbb9a91db8dfa881f0a92e2afbad6e4bbc775a",
			},
		},
	}
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	RootDirectory string
	BaseURL       string
	Manifest      Manifest
	HTTPClient    HTTPClient
}

var (
	ErrBusy             = errors.New("privacy model operation is already in progress")
	ErrAlreadyInstalled = errors.New("privacy model is already installed")
	ErrInvalidConfig    = errors.New("privacy model configuration is invalid")
	ErrFilesystem       = errors.New("privacy model filesystem operation failed")
)

var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,96}$`)

type Manager struct {
	lifetime       context.Context
	rootDirectory  string
	publishedPath  string
	baseURL        *url.URL
	manifest       Manifest
	manifestDigest string
	httpClient     HTTPClient
	bytesTotal     int64

	mu       sync.Mutex
	snapshot Snapshot
	cancel   context.CancelFunc
	done     chan struct{}
	deleting bool
}

func NewManager(lifetime context.Context, config Config) (*Manager, error) {
	if lifetime == nil {
		lifetime = context.Background()
	}
	if config.Manifest.Revision == "" && len(config.Manifest.Assets) == 0 {
		config.Manifest = ProductionManifest()
	}
	total, digest, err := validateManifest(config.Manifest)
	if err != nil || strings.TrimSpace(config.RootDirectory) == "" {
		return nil, ErrInvalidConfig
	}
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") ||
		parsedBaseURL.Host == "" || parsedBaseURL.User != nil ||
		parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
		return nil, ErrInvalidConfig
	}
	if !strings.HasSuffix(parsedBaseURL.Path, "/") {
		parsedBaseURL.Path += "/"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: downloadTimeout}
	}
	rootDirectory, err := filepath.Abs(config.RootDirectory)
	if err != nil {
		return nil, ErrFilesystem
	}
	if err := os.MkdirAll(rootDirectory, 0o700); err != nil {
		return nil, ErrFilesystem
	}
	if err := os.Chmod(rootDirectory, 0o700); err != nil {
		return nil, ErrFilesystem
	}
	manager := &Manager{
		lifetime: lifetime, rootDirectory: rootDirectory,
		publishedPath: filepath.Join(rootDirectory, config.Manifest.Revision),
		baseURL:       parsedBaseURL, manifest: copyManifest(config.Manifest),
		manifestDigest: digest, httpClient: config.HTTPClient, bytesTotal: total,
		snapshot: Snapshot{
			Status: StatusNotInstalled, Revision: config.Manifest.Revision, BytesTotal: total,
		},
	}
	if err := manager.removeAbandonedDownloads(); err != nil {
		return nil, err
	}
	if err := manager.inspectPublished(); err != nil {
		manager.snapshot.Status = StatusError
		message := errorDownload
		manager.snapshot.Error = &message
	}
	return manager, nil
}

func (manager *Manager) Status() Snapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return cloneSnapshot(manager.snapshot)
}

// ReadyDirectory exposes the verified, atomically published directory only to
// trusted in-process worker wiring. It is intentionally absent from the
// control-plane JSON contract.
func (manager *Manager) ReadyDirectory() (string, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.deleting || manager.snapshot.Status != StatusReady {
		return "", false
	}
	return manager.publishedPath, true
}

// Start begins a detached download tied to Core's lifetime, not the initiating
// HTTP request. Completion is observed through Status.
func (manager *Manager) Start() (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.deleting || manager.snapshot.Status == StatusDownloading {
		return cloneSnapshot(manager.snapshot), ErrBusy
	}
	if manager.snapshot.Status == StatusReady {
		return cloneSnapshot(manager.snapshot), ErrAlreadyInstalled
	}
	if err := manager.preparePublishedForDownload(); err != nil {
		return cloneSnapshot(manager.snapshot), err
	}
	downloadContext, cancel := context.WithCancel(manager.lifetime)
	manager.cancel = cancel
	manager.done = make(chan struct{})
	manager.snapshot = Snapshot{
		Status: StatusDownloading, Revision: manager.manifest.Revision,
		BytesTotal: manager.bytesTotal,
	}
	go manager.runDownload(downloadContext, manager.done)
	return cloneSnapshot(manager.snapshot), nil
}

func (manager *Manager) Delete(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	manager.mu.Lock()
	if manager.deleting {
		manager.mu.Unlock()
		return ErrBusy
	}
	manager.deleting = true
	cancel := manager.cancel
	done := manager.done
	if cancel != nil {
		cancel()
	}
	manager.mu.Unlock()

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			manager.mu.Lock()
			manager.deleting = false
			manager.mu.Unlock()
			return ctx.Err()
		}
	}
	if err := os.RemoveAll(manager.publishedPath); err != nil {
		manager.mu.Lock()
		manager.deleting = false
		manager.mu.Unlock()
		return ErrFilesystem
	}
	if err := manager.removeAbandonedDownloads(); err != nil {
		manager.mu.Lock()
		manager.deleting = false
		manager.mu.Unlock()
		return err
	}
	manager.mu.Lock()
	manager.cancel = nil
	manager.done = nil
	manager.deleting = false
	manager.snapshot = Snapshot{
		Status: StatusNotInstalled, Revision: manager.manifest.Revision,
		BytesTotal: manager.bytesTotal,
	}
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) runDownload(ctx context.Context, done chan struct{}) {
	err := manager.download(ctx)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.cancel = nil
	if err == nil {
		manager.snapshot.Status = StatusReady
		manager.snapshot.BytesDownloaded = manager.bytesTotal
		manager.snapshot.Error = nil
	} else if errors.Is(err, context.Canceled) {
		manager.snapshot = Snapshot{
			Status: StatusNotInstalled, Revision: manager.manifest.Revision,
			BytesTotal: manager.bytesTotal,
		}
	} else {
		manager.snapshot.Status = StatusError
		message := errorDownload
		manager.snapshot.Error = &message
	}
	close(done)
}

func (manager *Manager) download(ctx context.Context) error {
	temporaryDirectory, err := os.MkdirTemp(manager.rootDirectory, ".privacy-model-download-*")
	if err != nil {
		return ErrFilesystem
	}
	defer os.RemoveAll(temporaryDirectory)
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		return ErrFilesystem
	}
	for _, asset := range manager.manifest.Assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := manager.downloadAsset(ctx, temporaryDirectory, asset); err != nil {
			return err
		}
	}
	markerPath := filepath.Join(temporaryDirectory, readyMarkerName)
	if err := writeReadyMarker(markerPath, manager.manifestDigest); err != nil {
		return ErrFilesystem
	}
	if err := syncStagedDirectories(
		temporaryDirectory,
		manager.manifest.Assets,
		syncDirectory,
	); err != nil {
		return ErrFilesystem
	}
	if err := manager.inspectModelDirectoryContext(ctx, temporaryDirectory); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrFilesystem
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(manager.publishedPath); err == nil {
		return ErrFilesystem
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrFilesystem
	}
	if err := os.Rename(temporaryDirectory, manager.publishedPath); err != nil {
		return ErrFilesystem
	}
	if err := syncDirectory(manager.rootDirectory); err != nil {
		return ErrFilesystem
	}
	return nil
}

func (manager *Manager) downloadAsset(ctx context.Context, temporaryDirectory string, asset Asset) error {
	requestURL := manager.baseURL.ResolveReference(&url.URL{Path: asset.Path})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return ErrInvalidConfig
	}
	response, err := manager.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New(errorDownload)
	}
	if response == nil || response.Body == nil {
		return errors.New(errorDownload)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New(errorDownload)
	}
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		return errors.New(errorDownload)
	}
	targetPath := filepath.Join(temporaryDirectory, filepath.FromSlash(asset.Path))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return ErrFilesystem
	}
	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrFilesystem
	}
	hashValue := sha256.New()
	copyErr := manager.copyAsset(ctx, file, hashValue, response.Body, asset.Size)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return ErrFilesystem
	}
	if hex.EncodeToString(hashValue.Sum(nil)) != asset.SHA256 {
		return errors.New(errorDownload)
	}
	return nil
}

func (manager *Manager) copyAsset(
	ctx context.Context,
	target *os.File,
	hashValue hash.Hash,
	source io.Reader,
	expectedSize int64,
) error {
	buffer := make([]byte, 64*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if written+int64(read) > expectedSize {
				return errors.New(errorDownload)
			}
			chunk := buffer[:read]
			if _, err := target.Write(chunk); err != nil {
				return ErrFilesystem
			}
			if _, err := hashValue.Write(chunk); err != nil {
				return errors.New(errorDownload)
			}
			written += int64(read)
			manager.mu.Lock()
			manager.snapshot.BytesDownloaded += int64(read)
			manager.mu.Unlock()
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New(errorDownload)
		}
	}
	if written != expectedSize {
		return errors.New(errorDownload)
	}
	if err := target.Sync(); err != nil {
		return ErrFilesystem
	}
	return nil
}

func (manager *Manager) inspectPublished() error {
	info, err := os.Lstat(manager.publishedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrFilesystem
	}
	if err := manager.inspectModelDirectoryWithInfo(manager.publishedPath, info); err != nil {
		return err
	}
	manager.snapshot.Status = StatusReady
	manager.snapshot.BytesDownloaded = manager.bytesTotal
	manager.snapshot.Error = nil
	return nil
}

func (manager *Manager) preparePublishedForDownload() error {
	info, err := os.Lstat(manager.publishedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrFilesystem
	}
	if manager.inspectModelDirectoryWithInfo(manager.publishedPath, info) == nil {
		manager.snapshot.Status = StatusReady
		manager.snapshot.BytesDownloaded = manager.bytesTotal
		manager.snapshot.Error = nil
		return ErrAlreadyInstalled
	}
	if err := os.RemoveAll(manager.publishedPath); err != nil {
		return ErrFilesystem
	}
	if err := syncDirectory(manager.rootDirectory); err != nil {
		return ErrFilesystem
	}
	return nil
}

func (manager *Manager) inspectModelDirectory(directory string) error {
	return manager.inspectModelDirectoryContext(context.Background(), directory)
}

func (manager *Manager) inspectModelDirectoryContext(
	ctx context.Context,
	directory string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return ErrFilesystem
	}
	return manager.inspectModelDirectoryWithInfoContext(ctx, directory, info)
}

func (manager *Manager) inspectModelDirectoryWithInfo(
	directory string,
	info os.FileInfo,
) error {
	return manager.inspectModelDirectoryWithInfoContext(
		context.Background(),
		directory,
		info,
	)
}

func (manager *Manager) inspectModelDirectoryWithInfoContext(
	ctx context.Context,
	directory string,
	info os.FileInfo,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrFilesystem
	}
	markerPath := filepath.Join(directory, readyMarkerName)
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() ||
		markerInfo.Mode()&os.ModeSymlink != 0 ||
		markerInfo.Size() != int64(len(manager.manifestDigest)+1) {
		return ErrFilesystem
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(marker)) != manager.manifestDigest {
		return ErrFilesystem
	}
	for _, asset := range manager.manifest.Assets {
		if err := inspectPublishedAsset(ctx, directory, asset); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrFilesystem
		}
	}
	return nil
}

func inspectPublishedAsset(ctx context.Context, root string, asset Asset) error {
	current := root
	segments := strings.Split(asset.Path, "/")
	for index, segment := range segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrFilesystem
		}
		if index < len(segments)-1 {
			if !info.IsDir() {
				return ErrFilesystem
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() != asset.Size {
			return ErrFilesystem
		}
	}
	file, err := os.Open(current)
	if err != nil {
		return ErrFilesystem
	}
	hasher := sha256.New()
	copyErr := hashFile(ctx, hasher, io.LimitReader(file, asset.Size+1))
	closeErr := file.Close()
	if copyErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrFilesystem
	}
	if closeErr != nil || hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return ErrFilesystem
	}
	return nil
}

func hashFile(ctx context.Context, hasher hash.Hash, source io.Reader) error {
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := hasher.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (manager *Manager) removeAbandonedDownloads() error {
	entries, err := os.ReadDir(manager.rootDirectory)
	if err != nil {
		return ErrFilesystem
	}
	now := time.Now()
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".privacy-model-download-") {
			continue
		}
		stagingPath := filepath.Join(manager.rootDirectory, entry.Name())
		modified, err := newestTreeModification(stagingPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ErrFilesystem
		}
		age := now.Sub(modified)
		if age < abandonedDownloadStaleAge {
			continue
		}
		if err := os.RemoveAll(stagingPath); err != nil {
			return ErrFilesystem
		}
	}
	return nil
}

func newestTreeModification(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.Walk(root, func(_ string, info os.FileInfo, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	if newest.IsZero() {
		return time.Time{}, os.ErrNotExist
	}
	return newest, nil
}

func syncStagedDirectories(
	root string,
	assets []Asset,
	sync func(string) error,
) error {
	directories := make(map[string]struct{})
	for _, asset := range assets {
		for directory := path.Dir(asset.Path); directory != "."; directory = path.Dir(directory) {
			directories[directory] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Slice(ordered, func(left, right int) bool {
		leftDepth := strings.Count(ordered[left], "/")
		rightDepth := strings.Count(ordered[right], "/")
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return ordered[left] < ordered[right]
	})
	for _, directory := range ordered {
		if err := sync(filepath.Join(root, filepath.FromSlash(directory))); err != nil {
			return err
		}
	}
	return sync(root)
}

func validateManifest(manifest Manifest) (int64, string, error) {
	if !revisionPattern.MatchString(manifest.Revision) || manifest.Revision == "." ||
		manifest.Revision == ".." || len(manifest.Assets) == 0 {
		return 0, "", ErrInvalidConfig
	}
	seen := make(map[string]struct{}, len(manifest.Assets))
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "revision\x00%s\n", manifest.Revision)
	var total int64
	for _, asset := range manifest.Assets {
		cleaned := path.Clean(asset.Path)
		if cleaned != asset.Path || cleaned == "." || strings.HasPrefix(cleaned, "/") ||
			strings.HasPrefix(cleaned, "../") || strings.ContainsAny(asset.Path, `\:`) ||
			asset.Size <= 0 {
			return 0, "", ErrInvalidConfig
		}
		if _, exists := seen[asset.Path]; exists {
			return 0, "", ErrInvalidConfig
		}
		seen[asset.Path] = struct{}{}
		digest, err := hex.DecodeString(asset.SHA256)
		if err != nil || len(digest) != sha256.Size ||
			hex.EncodeToString(digest) != asset.SHA256 {
			return 0, "", ErrInvalidConfig
		}
		if total > int64(^uint64(0)>>1)-asset.Size {
			return 0, "", ErrInvalidConfig
		}
		total += asset.Size
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00%s\n", asset.Path, asset.Size, asset.SHA256)
	}
	return total, hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyManifest(manifest Manifest) Manifest {
	return Manifest{Revision: manifest.Revision, Assets: append([]Asset(nil), manifest.Assets...)}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	if snapshot.Error != nil {
		message := *snapshot.Error
		snapshot.Error = &message
	}
	return snapshot
}

func writeReadyMarker(markerPath, digest string) error {
	file, err := os.OpenFile(markerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, digest+"\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
