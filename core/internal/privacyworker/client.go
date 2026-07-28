// Package privacyworker owns the private process boundary between Core and the
// bundled Rust local privacy-model worker.
package privacyworker

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
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	protocolVersion   = 1
	maxFrameBytes     = 64 << 20
	defaultTimeout    = 2 * time.Minute
	processStopWait   = 5 * time.Second
	maxStartupRetries = 1

	installationManifestName     = "astrlink-model.json"
	maxInstallationManifestBytes = 256 << 10
	maxModelFiles                = 128
	maxModelLabels               = 256
	maxModelRequestTokens        = 128 * 1024
)

var tensorNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

type InstalledModel = contract.ReadyPrivacyModelInstallation

type ReadyInstallationProvider interface {
	ReadyInstallation(contract.PrivacyModelID) (InstalledModel, bool)
}

type ReadyInstallationProviderFunc func(contract.PrivacyModelID) (InstalledModel, bool)

func (function ReadyInstallationProviderFunc) ReadyInstallation(
	id contract.PrivacyModelID,
) (InstalledModel, bool) {
	return function(id)
}

type Config struct {
	ExecutablePath string
	Model          ReadyInstallationProvider
	Timeout        time.Duration
}

type Client struct {
	executablePath string
	model          ReadyInstallationProvider
	timeout        time.Duration
	command        func(string, ...string) *exec.Cmd
	slot           chan struct{}
	nextRequestID  atomic.Uint64

	mu         sync.Mutex
	active     bool
	modelID    contract.PrivacyModelID
	generation uint64
	change     chan struct{}
	process    *workerProcess
	starting   *workerProcess
	verified   *modelKey
	failed     *modelKey
}

type modelKey struct {
	modelID        contract.PrivacyModelID
	directory      string
	identity       string
	manifestSHA256 string
}

func installationKey(
	modelID contract.PrivacyModelID,
	installation InstalledModel,
) modelKey {
	return modelKey{
		modelID:        modelID,
		directory:      installation.Directory,
		identity:       installation.Identity,
		manifestSHA256: installation.ManifestSHA256,
	}
}

func New(config Config) (*Client, error) {
	if config.ExecutablePath == "" {
		return nil, fmt.Errorf("privacy worker executable path is required")
	}
	if config.Model == nil {
		return nil, fmt.Errorf("privacy model installation provider is required")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout < time.Second {
		return nil, fmt.Errorf("privacy worker timeout must be at least one second")
	}
	return &Client{
		executablePath: config.ExecutablePath,
		model:          config.Model,
		timeout:        config.Timeout,
		command:        exec.Command,
		slot:           make(chan struct{}, 1),
		change:         make(chan struct{}),
	}, nil
}

// ApplyPolicy synchronously makes the worker eligible for model requests. A
// disable, regex switch, or allow-through action immediately terminates any
// hot worker and cancels an in-flight local inspection.
func (client *Client) ApplyPolicy(policy contract.Policy) {
	var modelID contract.PrivacyModelID
	if policy.LocalModelID != nil {
		modelID = *policy.LocalModelID
	}
	active := policy.Enabled &&
		policy.Detector == contract.PolicyDetectorLocalModel &&
		modelID != "" &&
		policy.RequestAction != contract.PolicyActionAllow
	client.mu.Lock()
	selectionChanged := client.modelID != modelID
	client.active = active
	client.modelID = modelID
	if !active || selectionChanged {
		client.invalidateLocked()
		client.stopLocked()
	}
	client.mu.Unlock()
}

// Stop releases a hot worker without changing the configured policy. It is
// used before deleting verified model assets.
func (client *Client) Stop() {
	client.mu.Lock()
	client.invalidateLocked()
	client.stopLocked()
	client.mu.Unlock()
}

func (client *Client) Close() {
	client.mu.Lock()
	client.active = false
	client.modelID = ""
	client.invalidateLocked()
	client.stopLocked()
	client.mu.Unlock()
}

func (client *Client) Detect(ctx context.Context, input privacy.DetectInput) ([]privacy.Finding, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(input.Segments) == 0 {
		return nil, nil
	}
	expectedModelID := input.ExpectedLocalModelID
	if expectedModelID.Validate() != nil || !client.expectedModelActive(expectedModelID) {
		return nil, privacy.ErrDetectorUnavailable
	}
	select {
	case client.slot <- struct{}{}:
		defer func() { <-client.slot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	request := workerRequest{
		Version: protocolVersion,
		ID:      client.nextRequestID.Add(1),
		Texts:   make([]workerText, len(input.Segments)),
	}
	for index, segment := range input.Segments {
		request.Texts[index] = workerText{ID: uint32(index), Text: segment.Value}
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) == 0 || len(payload) > maxFrameBytes {
		return nil, privacy.ErrDetectorLimit
	}

	for attempt := 0; attempt <= maxStartupRetries; attempt++ {
		process, err := client.ensureProcess(ctx, expectedModelID)
		if err != nil {
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, privacy.ErrDetectorUnavailable
		}
		response, err := client.exchange(ctx, process, payload)
		if err == nil {
			findings, responseErr := responseFindings(
				response,
				request.ID,
				input.Segments,
			)
			if responseErr != nil && !errors.Is(responseErr, privacy.ErrDetectorLimit) {
				client.failProcess(process)
			}
			return findings, responseErr
		}
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, privacy.ErrDetectorTimeout) {
			client.stopProcess(process)
			return nil, err
		}
		if attempt == maxStartupRetries {
			client.failProcess(process)
			break
		}
		client.stopProcess(process)
	}
	return nil, privacy.ErrDetectorUnavailable
}

func (client *Client) expectedModelActive(expected contract.PrivacyModelID) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.active && client.modelID == expected
}

func (client *Client) ensureProcess(
	ctx context.Context,
	expectedModelID contract.PrivacyModelID,
) (*workerProcess, error) {
	client.mu.Lock()
	if !client.active || client.modelID != expectedModelID {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	modelID := expectedModelID
	generation := client.generation
	change := client.change
	client.mu.Unlock()

	installation, ready := client.model.ReadyInstallation(modelID)
	client.mu.Lock()
	if !client.active || client.modelID != modelID ||
		client.generation != generation {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	if !ready || installation.Directory == "" || installation.Identity == "" ||
		!validSHA256(installation.ManifestSHA256) {
		client.stopLocked()
		client.verified = nil
		client.failed = nil
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	key := installationKey(modelID, installation)
	client.resetCachedKeyLocked(key)
	if client.failed != nil && *client.failed == key {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	if client.process != nil {
		if client.process.key() == key &&
			client.process.running() {
			process := client.process
			client.mu.Unlock()
			return process, nil
		}
		client.stopLocked()
	}
	if client.starting != nil {
		client.stopLocked()
	}
	alreadyVerified := client.verified != nil && *client.verified == key
	client.mu.Unlock()

	if !alreadyVerified {
		if err := validateInstalledModel(ctx, change, modelID, installation); err != nil {
			if !errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) {
				client.latchKeyIfCurrent(key, generation)
			}
			return nil, err
		}
	}

	current, ready := client.model.ReadyInstallation(modelID)
	client.mu.Lock()
	if !client.active || client.modelID != modelID ||
		client.generation != generation {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	if !ready || current != installation {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	client.resetCachedKeyLocked(key)
	if client.failed != nil && *client.failed == key {
		client.mu.Unlock()
		return nil, privacy.ErrDetectorUnavailable
	}
	verified := key
	client.verified = &verified
	if client.process != nil {
		client.stopLocked()
	}

	command := client.command(
		client.executablePath,
		"--model-dir",
		installation.Directory,
	)
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		client.latchKeyLocked(key, generation)
		client.mu.Unlock()
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		client.latchKeyLocked(key, generation)
		client.mu.Unlock()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		client.latchKeyLocked(key, generation)
		client.mu.Unlock()
		return nil, err
	}
	process := &workerProcess{
		command:        command,
		modelID:        modelID,
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

	if err := client.awaitReady(ctx, process); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			client.stopProcess(process)
		} else {
			client.failProcess(process)
		}
		return nil, err
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	currentSelection := client.active && client.modelID == modelID &&
		client.generation == generation && process.key() == key
	if client.starting != process || !currentSelection {
		if client.starting == process {
			client.starting = nil
			process.stop()
		}
		return nil, privacy.ErrDetectorUnavailable
	}
	if !process.running() {
		client.starting = nil
		failed := key
		client.failed = &failed
		process.stop()
		return nil, privacy.ErrDetectorUnavailable
	}
	client.starting = nil
	client.process = process
	return process, nil
}

func (client *Client) invalidateLocked() {
	client.generation++
	if client.change != nil {
		close(client.change)
	}
	client.change = make(chan struct{})
	client.verified = nil
	client.failed = nil
}

func (client *Client) resetCachedKeyLocked(key modelKey) {
	if client.verified != nil && *client.verified != key {
		client.verified = nil
	}
	if client.failed != nil && *client.failed != key {
		client.failed = nil
	}
}

func (client *Client) latchKeyIfCurrent(key modelKey, generation uint64) {
	client.mu.Lock()
	client.latchKeyLocked(key, generation)
	client.mu.Unlock()
}

func (client *Client) latchKeyLocked(key modelKey, generation uint64) {
	if client.active && client.modelID == key.modelID &&
		client.generation == generation {
		failed := key
		client.failed = &failed
	}
}

func (client *Client) exchange(
	ctx context.Context,
	process *workerProcess,
	payload []byte,
) (workerResponse, error) {
	result := make(chan exchangeResult, 1)
	go func() {
		response, err := process.exchange(payload)
		result <- exchangeResult{response: response, err: err}
	}()

	timer := time.NewTimer(client.timeout)
	defer timer.Stop()
	select {
	case completed := <-result:
		return completed.response, completed.err
	case <-ctx.Done():
		client.stopProcess(process)
		return workerResponse{}, ctx.Err()
	case <-timer.C:
		client.stopProcess(process)
		return workerResponse{}, privacy.ErrDetectorTimeout
	}
}

func (client *Client) awaitReady(
	ctx context.Context,
	process *workerProcess,
) error {
	result := make(chan error, 1)
	go func() {
		result <- process.readReady()
	}()

	timer := time.NewTimer(client.timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return privacy.ErrDetectorTimeout
	}
}

func (client *Client) stopProcess(process *workerProcess) {
	client.mu.Lock()
	stopped := false
	if client.process == process {
		client.process = nil
		stopped = true
	}
	if client.starting == process {
		client.starting = nil
		stopped = true
	}
	if stopped {
		process.stop()
	}
	client.mu.Unlock()
}

func (client *Client) failProcess(process *workerProcess) {
	client.mu.Lock()
	if client.process == process || client.starting == process {
		client.process = nil
		client.starting = nil
		if client.active && client.modelID == process.modelID {
			failed := process.key()
			client.failed = &failed
		}
		process.stop()
	}
	client.mu.Unlock()
}

func (client *Client) stopLocked() {
	if client.process != nil {
		process := client.process
		client.process = nil
		process.stop()
	}
	if client.starting != nil {
		process := client.starting
		client.starting = nil
		process.stop()
	}
}

type exchangeResult struct {
	response workerResponse
	err      error
}

type workerProcess struct {
	command        *exec.Cmd
	modelID        contract.PrivacyModelID
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
		modelID:        process.modelID,
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

func (process *workerProcess) exchange(payload []byte) (workerResponse, error) {
	if err := writeFrame(process.stdin, payload); err != nil {
		return workerResponse{}, err
	}
	payload, err := readFrame(process.reader)
	if err != nil {
		return workerResponse{}, err
	}
	var response workerResponse
	decoder := json.NewDecoder(newByteReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return workerResponse{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return workerResponse{}, errors.New("worker response contains trailing data")
	}
	return response, nil
}

func (process *workerProcess) readReady() error {
	payload, err := readFrame(process.reader)
	if err != nil {
		return err
	}
	var ready workerReady
	decoder := json.NewDecoder(newByteReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ready); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("worker ready response contains trailing data")
	}
	if ready.Version != protocolVersion || !ready.Ready {
		return errors.New("invalid worker ready response")
	}
	return nil
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

type workerRequest struct {
	Version int          `json:"version"`
	ID      uint64       `json:"id"`
	Texts   []workerText `json:"texts"`
}

type workerReady struct {
	Version int  `json:"version"`
	Ready   bool `json:"ready"`
}

type workerText struct {
	ID   uint32 `json:"id"`
	Text string `json:"text"`
}

type workerResponse struct {
	Version int           `json:"version"`
	ID      uint64        `json:"id"`
	Spans   *[]workerSpan `json:"spans"`
	Error   *workerError  `json:"error,omitempty"`
}

type workerSpan struct {
	TextID uint32   `json:"text_id"`
	Label  string   `json:"label"`
	Start  int      `json:"start"`
	End    int      `json:"end"`
	Score  *float64 `json:"score"`
}

type workerError struct {
	Code string `json:"code"`
}

func responseFindings(
	response workerResponse,
	requestID uint64,
	segments []privacy.Segment,
) ([]privacy.Finding, error) {
	if response.Version != protocolVersion || response.ID != requestID {
		return nil, privacy.ErrDetectorUnavailable
	}
	if response.Spans == nil {
		return nil, privacy.ErrDetectorUnavailable
	}
	spans := *response.Spans
	if response.Error != nil {
		if len(spans) != 0 {
			return nil, privacy.ErrDetectorUnavailable
		}
		if response.Error.Code == "token_limit_exceeded" {
			return nil, privacy.ErrDetectorLimit
		}
		return nil, privacy.ErrDetectorUnavailable
	}
	findings := make([]privacy.Finding, 0, len(spans))
	for _, span := range spans {
		kind, valid := workerKind(span.Label)
		if !valid || uint64(span.TextID) >= uint64(len(segments)) ||
			span.Start < 0 || span.End <= span.Start ||
			span.Score == nil ||
			math.IsNaN(*span.Score) || math.IsInf(*span.Score, 0) ||
			*span.Score < 0 || *span.Score > 1 {
			return nil, privacy.ErrDetectorUnavailable
		}
		segment := int(span.TextID)
		value := segments[segment].Value
		if span.End > len(value) ||
			!utf8.ValidString(value[:span.Start]) ||
			!utf8.ValidString(value[:span.End]) {
			return nil, privacy.ErrDetectorUnavailable
		}
		findings = append(findings, privacy.Finding{
			Segment:    segment,
			Start:      span.Start,
			End:        span.End,
			Kind:       kind,
			Confidence: *span.Score,
		})
	}
	return findings, nil
}

func workerKind(label string) (privacy.Kind, bool) {
	switch label {
	case "email":
		return privacy.KindEmail, true
	case "phone":
		return privacy.KindPhone, true
	case "account":
		return privacy.KindAccount, true
	case "payment_card":
		return privacy.KindPaymentCard, true
	case "ip_address":
		return privacy.KindIPAddress, true
	case "url":
		return privacy.KindURL, true
	case "common_secret":
		return privacy.KindCommonSecret, true
	case "private_address":
		return privacy.KindAddress, true
	case "private_date":
		return privacy.KindDate, true
	case "private_person":
		return privacy.KindPerson, true
	default:
		return "", false
	}
}

type installationManifest struct {
	Version           int                                `json:"version"`
	InstallationID    contract.PrivacyModelID            `json:"installation_id"`
	Identity          string                             `json:"identity"`
	RepoID            string                             `json:"repo_id"`
	Revision          string                             `json:"revision"`
	VariantID         string                             `json:"variant_id"`
	Adapter           contract.PrivacyModelAdapter       `json:"adapter"`
	ModelPath         string                             `json:"model_path"`
	ExternalDataPaths []string                           `json:"external_data_paths"`
	TokenizerPath     string                             `json:"tokenizer_path"`
	ConfigPath        string                             `json:"config_path"`
	CalibrationPath   *string                            `json:"calibration_path"`
	TagScheme         string                             `json:"tag_scheme"`
	Window            int                                `json:"window"`
	Stride            int                                `json:"stride"`
	MaxRequestTokens  int                                `json:"max_request_tokens"`
	InputNames        installationInputNames             `json:"input_names"`
	OutputName        string                             `json:"output_name"`
	LabelMapping      map[string]*contract.CanonicalKind `json:"label_mapping"`
	Files             []installationFile                 `json:"files"`
}

type installationInputNames struct {
	InputIDs      string  `json:"input_ids"`
	AttentionMask string  `json:"attention_mask"`
	TokenTypeIDs  *string `json:"token_type_ids"`
}

type installationFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func validateInstalledModel(
	ctx context.Context,
	stale <-chan struct{},
	modelID contract.PrivacyModelID,
	installation InstalledModel,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validationInterrupted(ctx, stale); err != nil {
		return err
	}
	info, err := os.Lstat(installation.Directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return privacy.ErrDetectorUnavailable
	}
	directoryInfo := info
	manifestPath := filepath.Join(installation.Directory, installationManifestName)
	info, err = os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxInstallationManifestBytes {
		return privacy.ErrDetectorUnavailable
	}
	manifestInfo := info
	file, err := os.Open(manifestPath)
	if err != nil {
		return privacy.ErrDetectorUnavailable
	}
	openedManifest, statErr := file.Stat()
	if statErr != nil || !os.SameFile(manifestInfo, openedManifest) ||
		!openedManifest.Mode().IsRegular() {
		_ = file.Close()
		return privacy.ErrDetectorUnavailable
	}
	document, readErr := io.ReadAll(io.LimitReader(file, maxInstallationManifestBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(document) == 0 ||
		len(document) > maxInstallationManifestBytes ||
		int64(len(document)) != info.Size() ||
		!manifestMatchesBinding(document, installation.ManifestSHA256) {
		return privacy.ErrDetectorUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var manifest installationManifest
	decodeErr := decoder.Decode(&manifest)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || !errors.Is(trailingErr, io.EOF) || closeErr != nil ||
		!manifestFieldsPresent(document) ||
		validateInstallationManifest(manifest, modelID, installation.Identity) != nil {
		return privacy.ErrDetectorUnavailable
	}
	for _, declared := range manifest.Files {
		if err := verifyInstalledFile(ctx, stale, installation.Directory, declared); err != nil {
			if interrupted := validationInterrupted(ctx, stale); interrupted != nil {
				return interrupted
			}
			return privacy.ErrDetectorUnavailable
		}
	}
	if err := validationInterrupted(ctx, stale); err != nil {
		return err
	}
	finalDirectory, directoryErr := os.Lstat(installation.Directory)
	finalManifest, manifestErr := os.Lstat(manifestPath)
	finalDocument, finalReadErr := os.ReadFile(manifestPath)
	if directoryErr != nil || manifestErr != nil ||
		finalReadErr != nil || !bytes.Equal(document, finalDocument) ||
		finalDirectory.Mode()&os.ModeSymlink != 0 ||
		finalManifest.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(directoryInfo, finalDirectory) ||
		!os.SameFile(openedManifest, finalManifest) ||
		finalManifest.Size() != manifestInfo.Size() {
		return privacy.ErrDetectorUnavailable
	}
	return nil
}

func validationInterrupted(ctx context.Context, stale <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stale:
		return privacy.ErrDetectorUnavailable
	default:
		return nil
	}
}

func manifestFieldsPresent(document []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(document, &fields) != nil {
		return false
	}
	for _, field := range []string{
		"version", "installation_id", "identity", "repo_id", "revision",
		"variant_id", "adapter", "model_path", "external_data_paths",
		"tokenizer_path", "config_path", "calibration_path", "tag_scheme",
		"window", "stride", "max_request_tokens", "input_names",
		"output_name", "label_mapping", "files",
	} {
		if _, exists := fields[field]; !exists {
			return false
		}
	}
	var inputFields map[string]json.RawMessage
	if json.Unmarshal(fields["input_names"], &inputFields) != nil {
		return false
	}
	for _, field := range []string{"input_ids", "attention_mask", "token_type_ids"} {
		if _, exists := inputFields[field]; !exists {
			return false
		}
	}
	var files []map[string]json.RawMessage
	if json.Unmarshal(fields["files"], &files) != nil {
		return false
	}
	for _, file := range files {
		for _, field := range []string{"path", "size", "sha256"} {
			if _, exists := file[field]; !exists {
				return false
			}
		}
	}
	return true
}

func validateInstallationManifest(
	manifest installationManifest,
	modelID contract.PrivacyModelID,
	identity string,
) error {
	if manifest.Version != 1 || manifest.InstallationID != modelID ||
		manifest.InstallationID.Validate() != nil ||
		manifest.Identity != identity ||
		manifest.Identity != manifest.RepoID+"@"+manifest.Revision+"#"+manifest.VariantID ||
		contract.ValidatePrivacyModelRepoID(manifest.RepoID) != nil ||
		contract.ValidatePrivacyModelRevision(manifest.Revision) != nil ||
		contract.ValidatePrivacyModelVariantID(manifest.VariantID) != nil ||
		!manifest.Adapter.Valid() {
		return errors.New("invalid model identity")
	}
	if manifest.TagScheme != "bio" && manifest.TagScheme != "bioes" {
		return errors.New("invalid tag scheme")
	}
	switch manifest.Adapter {
	case contract.PrivacyModelAdapterOpenAIBIOES:
		if manifest.TagScheme != "bioes" || manifest.CalibrationPath == nil {
			return errors.New("invalid OpenAI adapter")
		}
	case contract.PrivacyModelAdapterHFToken:
		if manifest.CalibrationPath != nil {
			return errors.New("invalid Hugging Face adapter")
		}
	}
	if manifest.Window <= 0 || manifest.Stride < 0 ||
		manifest.Stride >= manifest.Window ||
		manifest.Window > manifest.MaxRequestTokens ||
		manifest.MaxRequestTokens <= 0 ||
		manifest.MaxRequestTokens > maxModelRequestTokens {
		return errors.New("invalid token limits")
	}
	if !tensorNamePattern.MatchString(manifest.InputNames.InputIDs) ||
		!tensorNamePattern.MatchString(manifest.InputNames.AttentionMask) ||
		manifest.InputNames.InputIDs == manifest.InputNames.AttentionMask ||
		!tensorNamePattern.MatchString(manifest.OutputName) {
		return errors.New("invalid model input or output")
	}
	if tokenTypeIDs := manifest.InputNames.TokenTypeIDs; tokenTypeIDs != nil &&
		(!tensorNamePattern.MatchString(*tokenTypeIDs) ||
			*tokenTypeIDs == manifest.InputNames.InputIDs ||
			*tokenTypeIDs == manifest.InputNames.AttentionMask) {
		return errors.New("invalid token type input")
	}
	if err := validateManifestMapping(manifest.Adapter, manifest.LabelMapping); err != nil {
		return err
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maxModelFiles {
		return errors.New("invalid model files")
	}
	declared := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		if !safeModelPath(file.Path) || file.Size <= 0 ||
			!validSHA256(file.SHA256) {
			return errors.New("invalid model file")
		}
		if _, duplicate := declared[file.Path]; duplicate {
			return errors.New("duplicate model file")
		}
		declared[file.Path] = struct{}{}
	}
	required := []string{
		manifest.ModelPath,
		manifest.TokenizerPath,
		manifest.ConfigPath,
	}
	required = append(required, manifest.ExternalDataPaths...)
	if manifest.CalibrationPath != nil {
		required = append(required, *manifest.CalibrationPath)
	}
	for _, requiredPath := range required {
		if !safeModelPath(requiredPath) {
			return errors.New("invalid required model path")
		}
		if _, exists := declared[requiredPath]; !exists {
			return errors.New("required model file is not declared")
		}
	}
	return nil
}

func validateManifestMapping(
	adapter contract.PrivacyModelAdapter,
	mapping map[string]*contract.CanonicalKind,
) error {
	if len(mapping) > maxModelLabels {
		return errors.New("too many model labels")
	}
	for label, kind := range mapping {
		if strings.TrimSpace(label) != label || label == "" ||
			utf8.RuneCountInString(label) > 128 ||
			strings.ContainsFunc(label, unicodeControl) ||
			(kind != nil && !kind.Valid()) {
			return errors.New("invalid model label mapping")
		}
	}
	if adapter == contract.PrivacyModelAdapterHFToken {
		if len(mapping) == 0 {
			return errors.New("generic model mapping is required")
		}
		return nil
	}
	expected := []string{
		"account_number",
		"private_address",
		"private_date",
		"private_email",
		"private_person",
		"private_phone",
		"private_url",
		"secret",
	}
	if len(mapping) != len(expected) {
		return errors.New("invalid OpenAI model mapping")
	}
	for _, label := range expected {
		if _, exists := mapping[label]; !exists {
			return errors.New("invalid OpenAI model mapping")
		}
	}
	return nil
}

func unicodeControl(character rune) bool {
	return unicode.IsControl(character)
}

func safeModelPath(value string) bool {
	return value != "" &&
		len(value) <= 512 &&
		!strings.ContainsAny(value, `\:`) &&
		!path.IsAbs(value) &&
		path.Clean(value) == value &&
		value != "."
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func manifestMatchesBinding(document []byte, expected string) bool {
	if !validSHA256(expected) {
		return false
	}
	expectedDigest, err := hex.DecodeString(expected)
	if err != nil {
		return false
	}
	actualDigest := sha256.Sum256(document)
	return subtle.ConstantTimeCompare(actualDigest[:], expectedDigest) == 1
}

func verifyInstalledFile(
	ctx context.Context,
	stale <-chan struct{},
	directory string,
	declared installationFile,
) error {
	if err := validationInterrupted(ctx, stale); err != nil {
		return err
	}
	filePath := filepath.Join(directory, filepath.FromSlash(declared.Path))
	before, err := os.Lstat(filePath)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 ||
		before.Size() != declared.Size {
		return errors.New("invalid installed file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(before, opened) ||
		!opened.Mode().IsRegular() || opened.Size() != declared.Size {
		_ = file.Close()
		return errors.New("installed file changed")
	}
	digest := sha256.New()
	buffer := make([]byte, 256<<10)
	for {
		if err := validationInterrupted(ctx, stale); err != nil {
			_ = file.Close()
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = digest.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = file.Close()
			return readErr
		}
	}
	if err := validationInterrupted(ctx, stale); err != nil {
		_ = file.Close()
		return err
	}
	closeErr := file.Close()
	after, statErr := os.Lstat(filePath)
	if closeErr != nil || statErr != nil ||
		after.Mode()&os.ModeSymlink != 0 ||
		!after.Mode().IsRegular() ||
		!os.SameFile(opened, after) ||
		after.Size() != declared.Size ||
		hex.EncodeToString(digest.Sum(nil)) != declared.SHA256 {
		return errors.New("installed file verification failed")
	}
	return nil
}

type byteReader struct {
	value []byte
	index int
}

func newByteReader(value []byte) *byteReader {
	return &byteReader{value: value}
}

func (reader *byteReader) Read(buffer []byte) (int, error) {
	if reader.index == len(reader.value) {
		return 0, io.EOF
	}
	read := copy(buffer, reader.value[reader.index:])
	reader.index += read
	return read, nil
}

var _ privacy.Detector = (*Client)(nil)
