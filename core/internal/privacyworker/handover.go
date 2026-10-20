package privacyworker

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// Handover runs the local model the privacy policy selects, one Client per
// selection, and decides what inspects requests while that model is not
// ready yet.
//
// A Core ML installation is compiled for this device the first time its
// worker loads, which takes seconds. Handover starts that worker as soon as
// the policy selects the installation, instead of on the first request, and
// lets the detector of the previous policy keep inspecting requests until
// the worker is ready. Then it switches to the new model at once. When the
// worker fails, the previous detector stays. Without a previous detector,
// requests wait for the worker within the usual timeouts and fail closed.
//
// CPU installations start on their first request, as they always have, and
// a selection change stops the previous worker at once.
type Handover struct {
	model      ReadyInstallationProvider
	newClient  func() (*Client, error)
	usesCoreML func(InstalledModel) bool

	mu       sync.Mutex
	closed   bool
	policy   contract.Policy
	modelID  contract.PrivacyModelID
	target   *Client
	prep     *preparation
	fallback *fallbackDetector
}

type preparation struct {
	state contract.PrivacyModelPreparationState
	done  chan struct{}
}

// fallbackDetector is the detector of the previous policy: the Client of
// the previous local model, or the regex detector it used.
type fallbackDetector struct {
	kind     contract.PrivacyModelPreparationFallback
	client   *Client
	modelID  contract.PrivacyModelID
	regex    privacy.Detector
	inflight sync.WaitGroup
}

func NewHandover(config Config) (*Handover, error) {
	if _, err := New(config); err != nil {
		return nil, err
	}
	return &Handover{
		model:      config.Model,
		newClient:  func() (*Client, error) { return New(config) },
		usesCoreML: installationRunsCoreML,
	}, nil
}

// ApplyPolicy synchronously applies the policy's detector selection. Leaving
// local-model mode stops every worker and cancels in-flight inspections. A
// new Core ML selection starts preparing at once; so does applying a policy
// again after its Core ML model failed to prepare.
func (handover *Handover) ApplyPolicy(policy contract.Policy) {
	handover.mu.Lock()
	if handover.closed {
		handover.mu.Unlock()
		return
	}
	previous := handover.policy
	handover.policy = policy
	modelID, active := activeLocalModel(policy)
	if !active {
		released := handover.releaseLocked()
		handover.mu.Unlock()
		closeClients(released)
		return
	}
	if handover.target != nil && handover.modelID == modelID &&
		(handover.prep == nil ||
			handover.prep.state != contract.PrivacyModelPreparationFailed) {
		target := handover.target
		handover.mu.Unlock()
		target.ApplyPolicy(policy)
		return
	}
	next, err := handover.newClient()
	if err != nil {
		// New accepted the same configuration when the handover was built.
		released := handover.releaseLocked()
		handover.mu.Unlock()
		closeClients(released)
		return
	}
	next.ApplyPolicy(policy)
	installation, ready := handover.model.ReadyInstallation(modelID)
	coreML := ready && handover.usesCoreML(installation)
	var fallback *fallbackDetector
	if coreML {
		fallback = handover.servingLocked(previous)
	}
	var released []*Client
	if handover.target != nil &&
		(fallback == nil || fallback.client != handover.target) {
		released = append(released, handover.target)
	}
	if handover.fallback != nil && handover.fallback != fallback &&
		handover.fallback.client != nil {
		released = append(released, handover.fallback.client)
	}
	handover.target = next
	handover.modelID = modelID
	handover.fallback = fallback
	handover.prep = nil
	if coreML {
		prep := &preparation{
			state: contract.PrivacyModelPreparationPreparing,
			done:  make(chan struct{}),
		}
		handover.prep = prep
		go handover.prepare(next, prep)
	}
	handover.mu.Unlock()
	closeClients(released)
}

// servingLocked returns the detector that inspects requests for the current
// selection right now, which becomes the fallback of the next one.
func (handover *Handover) servingLocked(previous contract.Policy) *fallbackDetector {
	if handover.target != nil {
		if handover.prep != nil &&
			handover.prep.state != contract.PrivacyModelPreparationReady {
			// The previous model never became ready; what served it serves on.
			return handover.fallback
		}
		if !handover.target.servable() {
			return nil
		}
		return &fallbackDetector{
			kind:    contract.PrivacyModelPreparationFallbackLocalModel,
			client:  handover.target,
			modelID: handover.modelID,
		}
	}
	detector := regexDetectorOf(previous)
	if detector == nil {
		return nil
	}
	return &fallbackDetector{
		kind:  contract.PrivacyModelPreparationFallbackRegex,
		regex: detector,
	}
}

func (handover *Handover) prepare(client *Client, prep *preparation) {
	err := client.prepare(context.Background())
	handover.mu.Lock()
	defer close(prep.done)
	if handover.prep != prep {
		handover.mu.Unlock()
		return
	}
	var retired *fallbackDetector
	if err == nil {
		prep.state = contract.PrivacyModelPreparationReady
		retired = handover.fallback
		handover.fallback = nil
	} else {
		prep.state = contract.PrivacyModelPreparationFailed
	}
	handover.mu.Unlock()
	if retired != nil && retired.client != nil {
		// Requests already inspecting with the previous model finish there.
		retired.inflight.Wait()
		retired.client.Close()
	}
}

func (handover *Handover) Detect(ctx context.Context, input privacy.DetectInput) ([]privacy.Finding, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	handover.mu.Lock()
	target := handover.target
	if target == nil {
		handover.mu.Unlock()
		if len(input.Segments) == 0 {
			return nil, nil
		}
		return nil, privacy.ErrDetectorUnavailable
	}
	var fallback *fallbackDetector
	if handover.prep != nil &&
		handover.prep.state != contract.PrivacyModelPreparationReady &&
		handover.fallback != nil &&
		input.ExpectedLocalModelID == handover.modelID &&
		len(input.Segments) > 0 {
		fallback = handover.fallback
		fallback.inflight.Add(1)
	}
	handover.mu.Unlock()
	if fallback != nil {
		findings, err := fallback.detect(ctx, input)
		fallback.inflight.Done()
		if !errors.Is(err, privacy.ErrDetectorUnavailable) || ctx.Err() != nil {
			return findings, err
		}
		// The previous model cannot inspect any more, for instance because
		// its installation was deleted: wait for the selected one instead.
	}
	return target.Detect(ctx, input)
}

func (fallback *fallbackDetector) detect(
	ctx context.Context,
	input privacy.DetectInput,
) ([]privacy.Finding, error) {
	if fallback.regex != nil {
		return fallback.regex.Detect(ctx, input)
	}
	input.ExpectedLocalModelID = fallback.modelID
	return fallback.client.Detect(ctx, input)
}

// Preparation reports the preparation of id when it is the policy's model
// and is prepared ahead of requests; it is nil otherwise.
func (handover *Handover) Preparation(id contract.PrivacyModelID) *contract.PrivacyModelPreparation {
	handover.mu.Lock()
	defer handover.mu.Unlock()
	if handover.target == nil || handover.prep == nil || handover.modelID != id {
		return nil
	}
	result := &contract.PrivacyModelPreparation{State: handover.prep.state}
	if handover.prep.state != contract.PrivacyModelPreparationReady &&
		handover.fallback != nil {
		kind := handover.fallback.kind
		result.Fallback = &kind
	}
	return result
}

func (handover *Handover) Close() {
	handover.mu.Lock()
	handover.closed = true
	released := handover.releaseLocked()
	handover.mu.Unlock()
	closeClients(released)
}

func (handover *Handover) releaseLocked() []*Client {
	var released []*Client
	if handover.target != nil {
		released = append(released, handover.target)
	}
	if handover.fallback != nil && handover.fallback.client != nil &&
		handover.fallback.client != handover.target {
		released = append(released, handover.fallback.client)
	}
	handover.target = nil
	handover.modelID = ""
	handover.prep = nil
	handover.fallback = nil
	return released
}

func closeClients(clients []*Client) {
	for _, client := range clients {
		client.Close()
	}
}

// activeLocalModel mirrors Client.ApplyPolicy: the worker serves only an
// enabled local-model policy that inspects requests.
func activeLocalModel(policy contract.Policy) (contract.PrivacyModelID, bool) {
	if policy.LocalModelID == nil {
		return "", false
	}
	modelID := *policy.LocalModelID
	return modelID, policy.Enabled &&
		policy.Detector == contract.PolicyDetectorLocalModel &&
		modelID != "" &&
		policy.RequestAction != contract.PolicyActionAllow
}

// regexDetectorOf returns the regex detector an enabled regex policy
// inspects with, or nil when the policy inspects nothing with regex.
func regexDetectorOf(policy contract.Policy) privacy.Detector {
	if !policy.Enabled || policy.Detector != contract.PolicyDetectorRegex ||
		policy.RequestAction == contract.PolicyActionAllow {
		return nil
	}
	runtime, err := privacy.FromContractPolicy(policy)
	if err != nil || runtime.Mode != privacy.ModeRegex {
		return nil
	}
	if runtime.RegexSource != contract.PolicyRegexSourceCustom {
		return privacy.NewRegexDetector()
	}
	detector, err := privacy.NewCustomRegexDetector(runtime.CustomRegexRules)
	if err != nil {
		return nil
	}
	return detector
}

// installationRunsCoreML reports whether the installation's bound manifest
// selects the Core ML execution provider. Anything unreadable counts as a
// CPU installation, which keeps the first request responsible for it, and
// that request then fails validation as before.
func installationRunsCoreML(installation InstalledModel) bool {
	if installation.Directory == "" {
		return false
	}
	document, err := readBoundedModelFile(
		filepath.Join(installation.Directory, installationManifestName),
		maxInstallationManifestBytes,
	)
	if err != nil || !manifestMatchesBinding(document, installation.ManifestSHA256) {
		return false
	}
	var manifest struct {
		Execution *installationExecution `json:"execution"`
	}
	if json.Unmarshal(document, &manifest) != nil || manifest.Execution == nil {
		return false
	}
	return manifest.Execution.Provider == "coreml"
}
