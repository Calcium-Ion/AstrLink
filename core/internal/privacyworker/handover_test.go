package privacyworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	handoverCPUModel    contract.PrivacyModelID = "model_00000000000000000000000000000011"
	handoverCoreMLModel contract.PrivacyModelID = "model_00000000000000000000000000000012"
	handoverOtherModel  contract.PrivacyModelID = "model_00000000000000000000000000000013"
)

type handoverModel struct {
	id     contract.PrivacyModelID
	mode   string
	coreML bool
}

type handoverFixture struct {
	handover    *Handover
	directories map[contract.PrivacyModelID]string
}

// newTestHandover installs each model in its own directory and runs the
// helper worker in the mode the model names.
func newTestHandover(t *testing.T, models ...handoverModel) *handoverFixture {
	t.Helper()
	provider := &testModelProvider{installations: map[contract.PrivacyModelID]InstalledModel{}}
	modes := map[string]string{}
	coreML := map[string]bool{}
	directories := map[contract.PrivacyModelID]string{}
	for index, model := range models {
		directory := t.TempDir()
		identity := "openai/privacy-filter@" +
			hex.EncodeToString([]byte{byte(index + 1)}) +
			"fa9a043d54d1be65afb281eddf0ffbe629385b#q4"
		manifestSHA256 := writeTestManifest(t, directory, model.id, identity)
		provider.set(model.id, InstalledModel{
			Directory: directory, Identity: identity, ManifestSHA256: manifestSHA256,
		})
		modes[directory] = model.mode
		coreML[directory] = model.coreML
		directories[model.id] = directory
	}
	config := Config{ExecutablePath: os.Args[0], Model: provider, Timeout: 30 * time.Second}
	handover, err := NewHandover(config)
	if err != nil {
		t.Fatalf("NewHandover: %v", err)
	}
	handover.usesCoreML = func(installation InstalledModel) bool {
		return coreML[installation.Directory]
	}
	handover.newClient = func() (*Client, error) {
		client, err := New(config)
		if err != nil {
			return nil, err
		}
		client.command = func(_ string, arguments ...string) *exec.Cmd {
			helperArguments := []string{"-test.run=^TestPrivacyWorkerHelper$", "--"}
			helperArguments = append(helperArguments, arguments...)
			command := exec.Command(os.Args[0], helperArguments...)
			command.Env = append(os.Environ(),
				helperEnvironment+"=1",
				"ASTRLINK_PRIVACY_WORKER_TEST_MODE="+modes[helperModelDirectory(arguments)],
			)
			return command
		}
		return client, nil
	}
	t.Cleanup(handover.Close)
	return &handoverFixture{handover: handover, directories: directories}
}

func (fixture *handoverFixture) releaseReady(t *testing.T, id contract.PrivacyModelID) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.directories[id], testReadyRelease), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// inspectedBy reports whether the worker of model id received value.
func (fixture *handoverFixture) inspectedBy(t *testing.T, id contract.PrivacyModelID, value string) bool {
	t.Helper()
	for _, frame := range framesInDirectory(t, fixture.directories[id]) {
		if slices.Contains(frame, value) {
			return true
		}
	}
	return false
}

func (fixture *handoverFixture) current() (*Client, *preparation) {
	fixture.handover.mu.Lock()
	defer fixture.handover.mu.Unlock()
	return fixture.handover.target, fixture.handover.prep
}

func waitForPreparation(t *testing.T, prep *preparation) {
	t.Helper()
	if prep == nil {
		t.Fatal("no preparation started")
	}
	select {
	case <-prep.done:
	case <-time.After(10 * time.Second):
		t.Fatal("preparation did not finish")
	}
}

func requirePreparation(
	t *testing.T,
	handover *Handover,
	id contract.PrivacyModelID,
	state contract.PrivacyModelPreparationState,
	fallback contract.PrivacyModelPreparationFallback,
) {
	t.Helper()
	got := handover.Preparation(id)
	if got == nil || got.State != state {
		t.Fatalf("preparation = %+v, want state %q", got, state)
	}
	if fallback == "" {
		if got.Fallback != nil {
			t.Fatalf("fallback = %q, want none", *got.Fallback)
		}
	} else if got.Fallback == nil || *got.Fallback != fallback {
		t.Fatalf("fallback = %v, want %q", got.Fallback, fallback)
	}
	if err := contract.ValidatePrivacyModelPreparation(got); err != nil {
		t.Fatalf("invalid preparation %+v: %v", got, err)
	}
}

func detectEmail(
	t *testing.T,
	handover *Handover,
	id contract.PrivacyModelID,
	value string,
) {
	t.Helper()
	findings, err := handover.Detect(context.Background(), testDetectValue(id, value))
	if err != nil {
		t.Fatalf("Detect %q: %v", value, err)
	}
	if len(findings) != 1 || findings[0].Kind != privacy.KindEmail {
		t.Fatalf("Detect %q findings = %+v", value, findings)
	}
}

func clientActive(client *Client) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.active
}

func TestHandoverServesWithPreviousModelUntilCoreMLWorkerIsReady(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCPUModel, mode: "per_text"},
		handoverModel{id: handoverCoreMLModel, mode: "ready_gated", coreML: true},
	)
	handover := fixture.handover
	handover.ApplyPolicy(localModelPolicy(handoverCPUModel))
	if got := handover.Preparation(handoverCPUModel); got != nil {
		t.Fatalf("CPU model preparation = %+v, want none", got)
	}
	detectEmail(t, handover, handoverCPUModel, "before@example.com")
	previous, _ := fixture.current()

	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	requirePreparation(t, handover, handoverCoreMLModel,
		contract.PrivacyModelPreparationPreparing,
		contract.PrivacyModelPreparationFallbackLocalModel)
	detectEmail(t, handover, handoverCoreMLModel, "during@example.com")
	if !fixture.inspectedBy(t, handoverCPUModel, "during@example.com") ||
		fixture.inspectedBy(t, handoverCoreMLModel, "during@example.com") {
		t.Fatal("a request during preparation was not inspected by the previous model")
	}

	_, prep := fixture.current()
	fixture.releaseReady(t, handoverCoreMLModel)
	waitForPreparation(t, prep)
	requirePreparation(t, handover, handoverCoreMLModel, contract.PrivacyModelPreparationReady, "")
	detectEmail(t, handover, handoverCoreMLModel, "after@example.com")
	if !fixture.inspectedBy(t, handoverCoreMLModel, "after@example.com") ||
		fixture.inspectedBy(t, handoverCPUModel, "after@example.com") {
		t.Fatal("a request after preparation was not inspected by the new model")
	}
	if clientActive(previous) {
		t.Fatal("the previous model's worker was kept after the switch")
	}
}

func TestHandoverServesWithPreviousRegexDetectorUntilCoreMLWorkerIsReady(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCoreMLModel, mode: "ready_gated", coreML: true},
	)
	handover := fixture.handover
	regex := contract.DefaultPrivacyPolicy()
	regex.Enabled = true
	handover.ApplyPolicy(regex)
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	requirePreparation(t, handover, handoverCoreMLModel,
		contract.PrivacyModelPreparationPreparing,
		contract.PrivacyModelPreparationFallbackRegex)
	detectEmail(t, handover, handoverCoreMLModel, "regex@example.com")
	if fixture.inspectedBy(t, handoverCoreMLModel, "regex@example.com") {
		t.Fatal("the preparing worker received a request")
	}

	_, prep := fixture.current()
	fixture.releaseReady(t, handoverCoreMLModel)
	waitForPreparation(t, prep)
	requirePreparation(t, handover, handoverCoreMLModel, contract.PrivacyModelPreparationReady, "")
	detectEmail(t, handover, handoverCoreMLModel, "model@example.com")
	if !fixture.inspectedBy(t, handoverCoreMLModel, "model@example.com") {
		t.Fatal("the ready model did not inspect the request")
	}
}

func TestHandoverKeepsPreviousModelWhenCoreMLPreparationFails(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCPUModel, mode: "per_text"},
		handoverModel{id: handoverCoreMLModel, mode: "startup_fail", coreML: true},
	)
	handover := fixture.handover
	handover.ApplyPolicy(localModelPolicy(handoverCPUModel))
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	_, prep := fixture.current()
	waitForPreparation(t, prep)
	requirePreparation(t, handover, handoverCoreMLModel,
		contract.PrivacyModelPreparationFailed,
		contract.PrivacyModelPreparationFallbackLocalModel)
	detectEmail(t, handover, handoverCoreMLModel, "failed@example.com")
	if !fixture.inspectedBy(t, handoverCPUModel, "failed@example.com") {
		t.Fatal("the previous model stopped inspecting after the failure")
	}

	// Applying the policy again retries, and the previous model stays.
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	_, retry := fixture.current()
	if retry == prep {
		t.Fatal("applying the policy again did not retry the failed preparation")
	}
	waitForPreparation(t, retry)
	requirePreparation(t, handover, handoverCoreMLModel,
		contract.PrivacyModelPreparationFailed,
		contract.PrivacyModelPreparationFallbackLocalModel)
	detectEmail(t, handover, handoverCoreMLModel, "retried@example.com")
	if !fixture.inspectedBy(t, handoverCPUModel, "retried@example.com") {
		t.Fatal("the previous model stopped inspecting after the retry")
	}
}

func TestHandoverWaitsForCoreMLWorkerWithoutPreviousDetector(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCoreMLModel, mode: "ready_gated", coreML: true},
	)
	handover := fixture.handover
	// As at Core startup: no detector inspected requests before.
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	requirePreparation(t, handover, handoverCoreMLModel, contract.PrivacyModelPreparationPreparing, "")

	type result struct {
		findings []privacy.Finding
		err      error
	}
	done := make(chan result, 1)
	go func() {
		findings, err := handover.Detect(context.Background(), testDetectValue(handoverCoreMLModel, "wait@example.com"))
		done <- result{findings, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("Detect returned before the worker was ready: %+v", got)
	default:
	}
	fixture.releaseReady(t, handoverCoreMLModel)
	select {
	case got := <-done:
		if got.err != nil || len(got.findings) != 1 {
			t.Fatalf("Detect = %+v, %v", got.findings, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Detect did not finish after the worker became ready")
	}
	if !fixture.inspectedBy(t, handoverCoreMLModel, "wait@example.com") {
		t.Fatal("the waiting request was not inspected by the new model")
	}
}

func TestHandoverFailsClosedWhenCoreMLPreparationFailsWithoutPreviousDetector(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCoreMLModel, mode: "startup_fail", coreML: true},
	)
	handover := fixture.handover
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	_, prep := fixture.current()
	waitForPreparation(t, prep)
	requirePreparation(t, handover, handoverCoreMLModel, contract.PrivacyModelPreparationFailed, "")
	if _, err := handover.Detect(context.Background(), testDetectValue(handoverCoreMLModel, "closed@example.com")); !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v, want ErrDetectorUnavailable", err)
	}
}

func TestHandoverStopsPreparationWhenPolicyLeavesModelMode(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCoreMLModel, mode: "ready_gated", coreML: true},
	)
	handover := fixture.handover
	handover.ApplyPolicy(localModelPolicy(handoverCoreMLModel))
	_, prep := fixture.current()
	disabled := localModelPolicy(handoverCoreMLModel)
	disabled.Enabled = false
	handover.ApplyPolicy(disabled)
	waitForPreparation(t, prep)
	if got := handover.Preparation(handoverCoreMLModel); got != nil {
		t.Fatalf("preparation after disabling = %+v, want none", got)
	}
	if _, err := handover.Detect(context.Background(), testDetectValue(handoverCoreMLModel, "off@example.com")); !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v, want ErrDetectorUnavailable", err)
	}
}

func TestHandoverLeavesCPUModelsToTheirFirstRequest(t *testing.T) {
	fixture := newTestHandover(t,
		handoverModel{id: handoverCPUModel, mode: "per_text"},
		handoverModel{id: handoverOtherModel, mode: "per_text"},
	)
	handover := fixture.handover
	handover.ApplyPolicy(localModelPolicy(handoverCPUModel))
	first, prep := fixture.current()
	first.mu.Lock()
	started := first.process != nil || first.starting != nil
	first.mu.Unlock()
	if started || prep != nil || handover.Preparation(handoverCPUModel) != nil {
		t.Fatal("a CPU model was started before its first request")
	}
	detectEmail(t, handover, handoverCPUModel, "cpu@example.com")

	// A switch between CPU models stops the previous worker at once.
	handover.ApplyPolicy(localModelPolicy(handoverOtherModel))
	if clientActive(first) {
		t.Fatal("the previous CPU model's worker survived the switch")
	}
	if handover.Preparation(handoverOtherModel) != nil {
		t.Fatal("a CPU model reported a preparation")
	}
	detectEmail(t, handover, handoverOtherModel, "other@example.com")
	if fixture.inspectedBy(t, handoverCPUModel, "other@example.com") {
		t.Fatal("the previous CPU model inspected a request after the switch")
	}
}

func TestInstallationRunsCoreMLReadsTheBoundManifest(t *testing.T) {
	directory := t.TempDir()
	write := func(document string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, installationManifestName), []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(document))
		return hex.EncodeToString(digest[:])
	}
	coreML := write(`{"execution":{"provider":"coreml","model_format":"mlprogram","compute_units":"cpu_and_gpu","static_sequence_length":512,"pad_token_id":1}}`)
	if !installationRunsCoreML(InstalledModel{Directory: directory, ManifestSHA256: coreML}) {
		t.Fatal("a Core ML manifest was not recognized")
	}
	if installationRunsCoreML(InstalledModel{Directory: directory, ManifestSHA256: hex.EncodeToString(make([]byte, 32))}) {
		t.Fatal("a manifest that does not match its binding was trusted")
	}
	cpu := write(`{"execution":null}`)
	if installationRunsCoreML(InstalledModel{Directory: directory, ManifestSHA256: cpu}) {
		t.Fatal("a CPU manifest was taken for Core ML")
	}
	if installationRunsCoreML(InstalledModel{Directory: filepath.Join(directory, "missing"), ManifestSHA256: cpu}) {
		t.Fatal("a missing installation was taken for Core ML")
	}
}
