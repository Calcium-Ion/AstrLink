// Package autoclassifier owns the private process boundary between Core and
// the bundled Rust sequence-classification worker.
//
// Fail-open is the product contract. Privacy's worker is fail-closed because
// it sits on a safety boundary. This classifier is a routing hint: timeout,
// crash, or a missing binary must not block the request. Callers receive
// (outcome, fallback) and apply deterministic priority fallback. Do not
// invert that polarity.
package autoclassifier

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

const (
	protocolVersion   = 1
	maxFrameBytes     = 64 << 20
	defaultTimeout    = 2 * time.Second
	processStopWait   = 5 * time.Second
	maxStartupRetries = 1

	installationManifestName     = "astrlink-classifier-model.json"
	maxInstallationManifestBytes = 256 << 10

	FallbackEmptyText   = "empty_text"
	FallbackUnavailable = "classifier_unavailable"
	FallbackTimeout     = "timeout"
	FallbackInvalidText = "invalid_text"
)

type ReadyInstallationProvider interface {
	FirstReady() (contract.ReadyAutoClassifierInstallation, bool)
	ReadyInstallation(contract.AutoClassifierID) (contract.ReadyAutoClassifierInstallation, bool)
}

type ReadyInstallationProviderFunc func() (contract.ReadyAutoClassifierInstallation, bool)

func (function ReadyInstallationProviderFunc) FirstReady() (contract.ReadyAutoClassifierInstallation, bool) {
	return function()
}

func (function ReadyInstallationProviderFunc) ReadyInstallation(
	id contract.AutoClassifierID,
) (contract.ReadyAutoClassifierInstallation, bool) {
	ready, ok := function()
	if !ok || ready.ID != id {
		return contract.ReadyAutoClassifierInstallation{}, false
	}
	return ready, true
}

type Config struct {
	ExecutablePath string
	Model          ReadyInstallationProvider
	Timeout        time.Duration
}

type Outcome struct {
	Category       string
	Logits         []float32
	FallbackReason string
}

func (outcome Outcome) OK() bool {
	return outcome.FallbackReason == "" && outcome.Category != ""
}

type Client struct {
	executablePath string
	model          ReadyInstallationProvider
	timeout        time.Duration
	command        func(string, ...string) *exec.Cmd
	slot           chan struct{}
	nextRequestID  atomic.Uint64

	mu       sync.Mutex
	process  *workerProcess
	starting *workerProcess
	verified *modelKey
	failed   *modelKey
}

type modelKey struct {
	id             contract.AutoClassifierID
	directory      string
	identity       string
	manifestSHA256 string
}

func New(config Config) (*Client, error) {
	if config.ExecutablePath == "" {
		return nil, fmt.Errorf("classifier worker executable path is required")
	}
	if config.Model == nil {
		return nil, fmt.Errorf("classifier installation provider is required")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout < 100*time.Millisecond {
		return nil, fmt.Errorf("classifier worker timeout is too short")
	}
	return &Client{
		executablePath: config.ExecutablePath,
		model:          config.Model,
		timeout:        config.Timeout,
		command:        exec.Command,
		slot:           make(chan struct{}, 1),
	}, nil
}

func (client *Client) ArtifactTier() contract.AutoClassifierArtifactTier {
	ready, ok := client.model.FirstReady()
	if !ok {
		return ""
	}
	return ready.ArtifactTier
}

func (client *Client) EligibleForRouting() bool {
	// An installed artifact is eligible. artifact_tier is provenance, not a gate.
	_, ok := client.model.FirstReady()
	return ok
}

func (client *Client) Classify(ctx context.Context, text string) Outcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) {
		return Outcome{FallbackReason: FallbackEmptyText}
	}
	ctx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()

	select {
	case client.slot <- struct{}{}:
		defer func() { <-client.slot }()
	case <-ctx.Done():
		return fallbackFromContext(ctx)
	}

	installation, ok := client.model.FirstReady()
	if !ok {
		return Outcome{FallbackReason: FallbackUnavailable}
	}

	request := classifyRequest{
		Version: protocolVersion,
		ID:      client.nextRequestID.Add(1),
		Text:    text,
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) == 0 || len(payload) > maxFrameBytes {
		return Outcome{FallbackReason: FallbackUnavailable}
	}

	for attempt := 0; attempt <= maxStartupRetries; attempt++ {
		process, err := client.ensureProcess(ctx, installation)
		if err != nil {
			return fallbackFromError(err)
		}
		response, err := client.exchange(ctx, process, payload)
		if err == nil {
			return outcomeFromResponse(request.ID, response)
		}
		client.stopProcess(process)
		if attempt == maxStartupRetries {
			client.latch(installation)
			return fallbackFromError(err)
		}
	}
	return Outcome{FallbackReason: FallbackUnavailable}
}

func (client *Client) Close() {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.stopLocked()
}

func (client *Client) ensureProcess(
	ctx context.Context,
	installation contract.ReadyAutoClassifierInstallation,
) (*workerProcess, error) {
	key := modelKey{
		id:             installation.ID,
		directory:      installation.Directory,
		identity:       installation.Identity,
		manifestSHA256: installation.ManifestSHA256,
	}
	client.mu.Lock()
	if client.failed != nil && *client.failed == key {
		client.mu.Unlock()
		return nil, errUnavailable
	}
	if client.process != nil && client.process.key() == key && client.process.running() {
		process := client.process
		client.mu.Unlock()
		return process, nil
	}
	alreadyVerified := client.verified != nil && *client.verified == key
	client.stopLocked()
	client.mu.Unlock()

	if !alreadyVerified {
		if err := validateInstalledModel(ctx, installation); err != nil {
			if !errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) {
				client.latch(installation)
			}
			return nil, err
		}
	}

	current, ready := client.model.ReadyInstallation(installation.ID)
	if !ready || current != installation {
		return nil, errUnavailable
	}

	client.mu.Lock()
	if client.failed != nil && *client.failed == key {
		client.mu.Unlock()
		return nil, errUnavailable
	}
	verified := key
	client.verified = &verified
	command := client.command(client.executablePath, "--model-dir", installation.Directory)
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		client.latchLocked(key)
		client.mu.Unlock()
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		client.latchLocked(key)
		client.mu.Unlock()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		client.latchLocked(key)
		client.mu.Unlock()
		return nil, err
	}
	process := &workerProcess{
		command:        command,
		id:             installation.ID,
		directory:      installation.Directory,
		identity:       installation.Identity,
		manifestSHA256: installation.ManifestSHA256,
		stdin:          stdin,
		stdout:         stdout,
		reader:         bufio.NewReader(stdout),
		done:           make(chan error, 1),
	}
	go func() {
		process.done <- command.Wait()
		close(process.done)
	}()
	client.starting = process
	client.mu.Unlock()

	if err := process.readReady(ctx); err != nil {
		client.stopProcess(process)
		return nil, err
	}

	client.mu.Lock()
	if client.starting != process {
		client.mu.Unlock()
		process.stop()
		return nil, errUnavailable
	}
	client.starting = nil
	client.process = process
	client.mu.Unlock()
	return process, nil
}

func (client *Client) exchange(
	ctx context.Context,
	process *workerProcess,
	payload []byte,
) (classifyResponse, error) {
	type result struct {
		response classifyResponse
		err      error
	}
	done := make(chan result, 1)
	go func() {
		if err := writeFrame(process.stdin, payload); err != nil {
			done <- result{err: err}
			return
		}
		frame, err := readFrame(process.reader)
		if err != nil {
			done <- result{err: err}
			return
		}
		var response classifyResponse
		if json.Unmarshal(frame, &response) != nil {
			done <- result{err: errUnavailable}
			return
		}
		done <- result{response: response}
	}()
	select {
	case <-ctx.Done():
		process.stop()
		return classifyResponse{}, ctx.Err()
	case <-process.done:
		return classifyResponse{}, errUnavailable
	case item := <-done:
		return item.response, item.err
	}
}

func (client *Client) latch(installation contract.ReadyAutoClassifierInstallation) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.latchLocked(modelKey{
		id:             installation.ID,
		directory:      installation.Directory,
		identity:       installation.Identity,
		manifestSHA256: installation.ManifestSHA256,
	})
}

func (client *Client) latchLocked(key modelKey) {
	failed := key
	client.failed = &failed
	if client.verified != nil && *client.verified == key {
		client.verified = nil
	}
}

func (client *Client) stopProcess(process *workerProcess) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process == process {
		client.process = nil
	}
	if client.starting == process {
		client.starting = nil
	}
	process.stop()
}

func (client *Client) stopLocked() {
	if client.process != nil {
		client.process.stop()
		client.process = nil
	}
	if client.starting != nil {
		client.starting.stop()
		client.starting = nil
	}
}

var errUnavailable = errors.New(FallbackUnavailable)

type workerProcess struct {
	command        *exec.Cmd
	id             contract.AutoClassifierID
	directory      string
	identity       string
	manifestSHA256 string
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	reader         *bufio.Reader
	done           chan error
	stopOnce       sync.Once
}

func (process *workerProcess) key() modelKey {
	return modelKey{
		id:             process.id,
		directory:      process.directory,
		identity:       process.identity,
		manifestSHA256: process.manifestSHA256,
	}
}

func (process *workerProcess) running() bool {
	select {
	case <-process.done:
		return false
	default:
		return true
	}
}

func (process *workerProcess) readReady(ctx context.Context) error {
	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		frame, err := readFrame(process.reader)
		if err != nil {
			done <- result{err: err}
			return
		}
		var ready workerReady
		if json.Unmarshal(frame, &ready) != nil ||
			ready.Version != protocolVersion ||
			!ready.Ready {
			done <- result{err: errUnavailable}
			return
		}
		done <- result{}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-process.done:
		return errUnavailable
	case item := <-done:
		return item.err
	}
}

func (process *workerProcess) stop() {
	process.stopOnce.Do(func() {
		_ = process.stdin.Close()
		_ = process.stdout.Close()
		if process.command.Process != nil {
			_ = process.command.Process.Kill()
		}
		select {
		case <-process.done:
		case <-time.After(processStopWait):
		}
	})
}

type workerReady struct {
	Version int  `json:"version"`
	Ready   bool `json:"ready"`
}

type classifyRequest struct {
	Version int    `json:"version"`
	ID      uint64 `json:"id"`
	Text    string `json:"text"`
}

type classifyResponse struct {
	Version  int        `json:"version"`
	ID       uint64     `json:"id"`
	Category *string    `json:"category"`
	Logits   []float32  `json:"logits"`
	Error    *workerErr `json:"error"`
}

type workerErr struct {
	Code string `json:"code"`
}

func writeFrame(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxFrameBytes {
		return errors.New("invalid worker request length")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(header[:]))
	if length <= 0 || length > maxFrameBytes {
		return nil, errors.New("invalid worker response length")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func outcomeFromResponse(id uint64, response classifyResponse) Outcome {
	if response.Version != protocolVersion || response.ID != id {
		return Outcome{FallbackReason: FallbackUnavailable}
	}
	if response.Error != nil {
		switch response.Error.Code {
		case "empty_text":
			return Outcome{FallbackReason: FallbackEmptyText}
		case "invalid_text":
			return Outcome{FallbackReason: FallbackInvalidText}
		default:
			return Outcome{FallbackReason: FallbackUnavailable}
		}
	}
	if response.Category == nil || *response.Category == "" || len(response.Logits) != 4 {
		return Outcome{FallbackReason: FallbackUnavailable}
	}
	if _, err := autotaxonomy.IndexOf(*response.Category); err != nil {
		return Outcome{FallbackReason: FallbackUnavailable}
	}
	return Outcome{Category: *response.Category, Logits: response.Logits}
}

func fallbackFromContext(ctx context.Context) Outcome {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return Outcome{FallbackReason: FallbackTimeout}
	}
	return Outcome{FallbackReason: FallbackUnavailable}
}

func fallbackFromError(err error) Outcome {
	if errors.Is(err, context.DeadlineExceeded) {
		return Outcome{FallbackReason: FallbackTimeout}
	}
	return Outcome{FallbackReason: FallbackUnavailable}
}

func validateInstalledModel(
	ctx context.Context,
	installation contract.ReadyAutoClassifierInstallation,
) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if installation.Directory == "" ||
		installation.Identity == "" ||
		len(installation.ManifestSHA256) != 64 {
		return errUnavailable
	}
	info, err := os.Lstat(installation.Directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errUnavailable
	}
	manifestPath := filepath.Join(installation.Directory, installationManifestName)
	info, err = os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxInstallationManifestBytes {
		return errUnavailable
	}
	document, err := os.ReadFile(manifestPath)
	if err != nil || len(document) == 0 ||
		len(document) > maxInstallationManifestBytes {
		return errUnavailable
	}
	digest := sha256.Sum256(document)
	if subtle.ConstantTimeCompare(
		[]byte(hex.EncodeToString(digest[:])),
		[]byte(installation.ManifestSHA256),
	) != 1 {
		return errUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	var manifest struct {
		Version        uint32 `json:"version"`
		InstallationID string `json:"installation_id"`
		Identity       string `json:"identity"`
		TaxonomyID     string `json:"taxonomy_id"`
		TaxonomySHA256 string `json:"taxonomy_sha256"`
		ArtifactTier   string `json:"artifact_tier"`
		Files          []struct {
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if decoder.Decode(&manifest) != nil ||
		manifest.Version != 1 ||
		manifest.InstallationID != string(installation.ID) ||
		manifest.Identity != installation.Identity ||
		manifest.TaxonomyID != autotaxonomy.ID ||
		manifest.TaxonomySHA256 != autotaxonomy.SHA256 ||
		!contract.AutoClassifierArtifactTier(manifest.ArtifactTier).Valid() ||
		len(manifest.Files) == 0 {
		return errUnavailable
	}
	for _, file := range manifest.Files {
		if err := verifyInstalledFile(installation.Directory, file.Path, file.Size, file.SHA256); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func verifyInstalledFile(root, relative string, size int64, expectedSHA string) error {
	if relative == "" || strings.Contains(relative, "\\") ||
		strings.Contains(relative, "..") || filepath.IsAbs(relative) {
		return errUnavailable
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() != size {
		return errUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return errUnavailable
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, io.LimitReader(file, size+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != size {
		return errUnavailable
	}
	if subtle.ConstantTimeCompare(
		[]byte(hex.EncodeToString(hasher.Sum(nil))),
		[]byte(expectedSHA),
	) != 1 {
		return errUnavailable
	}
	return nil
}
