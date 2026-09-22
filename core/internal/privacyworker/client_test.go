package privacyworker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const helperEnvironment = "ASTRLINK_PRIVACY_WORKER_TEST_HELPER"

const (
	testInstallationID contract.PrivacyModelID = "model_00000000000000000000000000000001"
	testIdentity                               = "openai/privacy-filter@7ffa9a043d54d1be65afb281eddf0ffbe629385b#q4"
)

type testModelProvider struct {
	mu            sync.Mutex
	installations map[contract.PrivacyModelID]InstalledModel
}

type blockingModelProvider struct {
	installations map[contract.PrivacyModelID]InstalledModel
	entered       chan contract.PrivacyModelID
	release       chan struct{}
}

func (model *blockingModelProvider) ReadyInstallation(
	id contract.PrivacyModelID,
) (InstalledModel, bool) {
	model.entered <- id
	<-model.release
	installation, ready := model.installations[id]
	return installation, ready
}

func (model *testModelProvider) ReadyInstallation(
	id contract.PrivacyModelID,
) (InstalledModel, bool) {
	model.mu.Lock()
	defer model.mu.Unlock()
	installation, ready := model.installations[id]
	return installation, ready
}

func (model *testModelProvider) set(
	id contract.PrivacyModelID,
	installation InstalledModel,
) {
	model.mu.Lock()
	model.installations[id] = installation
	model.mu.Unlock()
}

func TestClientUsesFramedWorkerAndMapsCanonicalLabels(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	findings, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(findings) != 1 || findings[0].Segment != 0 ||
		findings[0].Kind != privacy.KindEmail ||
		findings[0].Start != 0 || findings[0].End != len("person@example.test") ||
		findings[0].Confidence != 0.99 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestClientRequiresExpectedModelSnapshot(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	_, err := client.Detect(context.Background(), privacy.DetectInput{
		Segments: []privacy.Segment{{Value: "person@example.test"}},
	})
	if !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil {
		t.Fatal("worker started without an expected model snapshot")
	}
}

func TestClientSerializesConcurrentRequests(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	const requestCount = 8
	var wait sync.WaitGroup
	errorsSeen := make(chan error, requestCount)
	for index := 0; index < requestCount; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			findings, err := client.Detect(
				context.Background(),
				testDetectInput(testInstallationID),
			)
			if err == nil && len(findings) != 1 {
				err = errors.New("unexpected finding count")
			}
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Detect: %v", err)
		}
	}
}

func TestClientFailsClosedWhenPolicySwitchesWhileInstallationLookupIsBlocked(t *testing.T) {
	secondID := contract.PrivacyModelID("model_00000000000000000000000000000002")
	firstDirectory := t.TempDir()
	secondDirectory := t.TempDir()
	secondIdentity := "example/privacy@1111111111111111111111111111111111111111#q4"
	firstManifestSHA256 := writeTestManifest(
		t,
		firstDirectory,
		testInstallationID,
		testIdentity,
	)
	secondManifestSHA256 := writeTestManifest(
		t,
		secondDirectory,
		secondID,
		secondIdentity,
	)
	provider := &blockingModelProvider{
		installations: map[contract.PrivacyModelID]InstalledModel{
			testInstallationID: {
				Directory:      firstDirectory,
				Identity:       testIdentity,
				ManifestSHA256: firstManifestSHA256,
			},
			secondID: {
				Directory:      secondDirectory,
				Identity:       secondIdentity,
				ManifestSHA256: secondManifestSHA256,
			},
		},
		entered: make(chan contract.PrivacyModelID, 1),
		release: make(chan struct{}),
	}
	client, err := New(Config{
		ExecutablePath: os.Args[0],
		Model:          provider,
		Timeout:        time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	commandCalled := false
	client.command = func(string, ...string) *exec.Cmd {
		commandCalled = true
		return exec.Command(os.Args[0])
	}
	client.ApplyPolicy(localModelPolicy(testInstallationID))
	t.Cleanup(client.Close)

	result := make(chan error, 1)
	go func() {
		_, detectErr := client.Detect(
			context.Background(),
			testDetectInput(testInstallationID),
		)
		result <- detectErr
	}()
	if requested := <-provider.entered; requested != testInstallationID {
		t.Fatalf("provider requested %q", requested)
	}
	client.ApplyPolicy(localModelPolicy(secondID))
	close(provider.release)

	if detectErr := <-result; !errors.Is(detectErr, privacy.ErrDetectorUnavailable) {
		t.Fatalf("stale detection error = %v", detectErr)
	}
	client.mu.Lock()
	process := client.process
	client.mu.Unlock()
	if commandCalled || process != nil {
		t.Fatalf("stale lookup started a worker: command=%t process=%#v", commandCalled, process)
	}
}

func TestClientRestartsOnceAfterWorkerCrash(t *testing.T) {
	client := newTestClient(t, "crash_once", 5*time.Second)
	findings, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if err != nil {
		t.Fatalf("Detect after restart: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestClientRecoversWhenHotWorkerCrashesAfterSuccessfulRequest(t *testing.T) {
	client := newTestClient(t, "crash_after_success_once", 5*time.Second)
	originalCommand := client.command
	commandCalls := 0
	client.command = func(name string, arguments ...string) *exec.Cmd {
		commandCalls++
		return originalCommand(name, arguments...)
	}
	input := testDetectInput(testInstallationID)
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()

	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("Detect after hot worker crash: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if first == nil || second == nil || first == second || first.running() {
		t.Fatalf("hot worker was not replaced: first=%#v second=%#v", first, second)
	}
	if commandCalls != 2 {
		t.Fatalf("worker starts = %d, want 2", commandCalls)
	}
}

func TestClientLatchesStartupFailureForExactInstallationKey(t *testing.T) {
	for _, mode := range []string{"missing_ready", "startup_fail"} {
		t.Run(mode, func(t *testing.T) {
			client := newTestClient(t, mode, 5*time.Second)
			originalCommand := client.command
			commandCalls := 0
			client.command = func(name string, arguments ...string) *exec.Cmd {
				commandCalls++
				return originalCommand(name, arguments...)
			}
			input := testDetectInput(testInstallationID)

			for request := 0; request < 2; request++ {
				if _, err := client.Detect(context.Background(), input); !errors.Is(
					err,
					privacy.ErrDetectorUnavailable,
				) {
					t.Fatalf("Detect %d error = %v", request+1, err)
				}
			}
			if commandCalls != 1 {
				t.Fatalf("startup attempts = %d, want 1", commandCalls)
			}

			regex := contract.DefaultPrivacyPolicy()
			regex.Enabled = true
			client.ApplyPolicy(regex)
			client.ApplyPolicy(localModelPolicy(testInstallationID))
			if _, err := client.Detect(context.Background(), input); !errors.Is(
				err,
				privacy.ErrDetectorUnavailable,
			) {
				t.Fatalf("Detect after policy generation change error = %v", err)
			}
			if commandCalls != 2 {
				t.Fatalf(
					"startup attempts after generation change = %d, want 2",
					commandCalls,
				)
			}
		})
	}
}

func TestClientLatchesCommandStartFailure(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	commandCalls := 0
	missingExecutable := filepath.Join(t.TempDir(), "missing-worker")
	client.command = func(string, ...string) *exec.Cmd {
		commandCalls++
		return exec.Command(missingExecutable)
	}
	input := testDetectInput(testInstallationID)

	for request := 0; request < 2; request++ {
		if _, err := client.Detect(context.Background(), input); !errors.Is(
			err,
			privacy.ErrDetectorUnavailable,
		) {
			t.Fatalf("Detect %d error = %v", request+1, err)
		}
	}
	if commandCalls != 1 {
		t.Fatalf("command starts = %d, want 1", commandCalls)
	}
}

func TestClientRejectsAndLatchesMalformedReadyHandshake(t *testing.T) {
	client := newTestClient(t, "malformed_ready", 5*time.Second)
	originalCommand := client.command
	commandCalls := 0
	client.command = func(name string, arguments ...string) *exec.Cmd {
		commandCalls++
		return originalCommand(name, arguments...)
	}
	input := testDetectInput(testInstallationID)

	for request := 0; request < 2; request++ {
		if _, err := client.Detect(context.Background(), input); !errors.Is(
			err,
			privacy.ErrDetectorUnavailable,
		) {
			t.Fatalf("Detect %d error = %v", request+1, err)
		}
	}
	if commandCalls != 1 {
		t.Fatalf("startup attempts = %d, want 1", commandCalls)
	}
}

func TestWorkerReadyHandshakeRequiresExactVersionedMessage(t *testing.T) {
	read := func(t *testing.T, payload string) error {
		t.Helper()
		var frame bytes.Buffer
		if err := writeFrame(&frame, []byte(payload)); err != nil {
			t.Fatalf("writeFrame: %v", err)
		}
		process := &workerProcess{reader: bufio.NewReader(&frame)}
		return process.readReady()
	}
	if err := read(t, `{"version":1,"ready":true}`); err != nil {
		t.Fatalf("valid ready handshake: %v", err)
	}
	for _, payload := range []string{
		`{"version":2,"ready":true}`,
		`{"version":1,"ready":false}`,
		`{"version":1,"ready":true,"unexpected":true}`,
		`{"version":1,"ready":true} trailing`,
	} {
		if err := read(t, payload); err == nil {
			t.Errorf("accepted invalid ready handshake %q", payload)
		}
	}
}

func TestClientCancellationKillsWorkerAndFailsClosed(t *testing.T) {
	client := newTestClient(t, "hang", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Detect(ctx, testDetectInput(testInstallationID))
		result <- err
	}()
	waitForTestProcess(t, client)
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Detect error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil {
		t.Fatal("worker remained active after cancellation")
	}
}

func TestClientTimeoutKillsWorkerAndFailsClosed(t *testing.T) {
	client := newTestClient(t, "hang", time.Second)
	_, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if !errors.Is(err, privacy.ErrDetectorTimeout) {
		t.Fatalf("Detect error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil {
		t.Fatal("worker remained active after timeout")
	}
}

func TestExchangeCancellationHasHardBoundWhenStoppedReaderDoesNotUnblock(t *testing.T) {
	process, stuck := newStuckWorkerProcess()
	client := &Client{timeout: time.Hour, process: process}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.exchange(ctx, process, []byte(`{"request":"test"}`))
		result <- err
	}()

	<-stuck.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("exchange error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		stuck.unblock()
		<-result
		t.Fatal("canceled exchange waited for a stuck worker reader")
	}
	stuck.unblock()
}

func TestExchangeTimeoutHasHardBoundWhenStoppedReaderDoesNotUnblock(t *testing.T) {
	process, stuck := newStuckWorkerProcess()
	client := &Client{timeout: 25 * time.Millisecond, process: process}
	result := make(chan error, 1)
	go func() {
		_, err := client.exchange(context.Background(), process, []byte(`{"request":"test"}`))
		result <- err
	}()

	<-stuck.started
	select {
	case err := <-result:
		if !errors.Is(err, privacy.ErrDetectorTimeout) {
			t.Fatalf("exchange error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		stuck.unblock()
		<-result
		t.Fatal("timed out exchange waited for a stuck worker reader")
	}
	stuck.unblock()
}

func TestClientRejectsMalformedWorkerResponse(t *testing.T) {
	client := newTestClient(t, "invalid_label", 5*time.Second)
	_, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v", err)
	}
}

func TestClientRejectsMissingOrNullWorkerSpans(t *testing.T) {
	for _, mode := range []string{"missing_spans", "null_spans"} {
		t.Run(mode, func(t *testing.T) {
			client := newTestClient(t, mode, 5*time.Second)
			_, err := client.Detect(
				context.Background(),
				testDetectInput(testInstallationID),
			)
			if !errors.Is(err, privacy.ErrDetectorUnavailable) {
				t.Fatalf("Detect error = %v", err)
			}
		})
	}
}

func TestClientRejectsMissingOrInvalidWorkerScore(t *testing.T) {
	for _, mode := range []string{"missing_score", "invalid_score"} {
		t.Run(mode, func(t *testing.T) {
			client := newTestClient(t, mode, 5*time.Second)
			_, err := client.Detect(
				context.Background(),
				testDetectInput(testInstallationID),
			)
			if !errors.Is(err, privacy.ErrDetectorUnavailable) {
				t.Fatalf("Detect error = %v", err)
			}
		})
	}
}

func TestClientImmediatelyStopsWhenPolicyLeavesModelMode(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	client.mu.Lock()
	process := client.process
	client.mu.Unlock()
	if process == nil {
		t.Fatal("worker was not kept hot")
	}

	policy := contract.DefaultPrivacyPolicy()
	policy.Enabled = true
	policy.Detector = contract.PolicyDetectorRegex
	policy.LocalModelID = nil
	client.ApplyPolicy(policy)

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil || process.running() {
		t.Fatal("worker remained active after regex switch")
	}
}

func TestClientRequiresVerifiedReadyInstallation(t *testing.T) {
	model := &testModelProvider{
		installations: make(map[contract.PrivacyModelID]InstalledModel),
	}
	client, err := New(Config{
		ExecutablePath: os.Args[0],
		Model:          model,
		Timeout:        time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.ApplyPolicy(localModelPolicy(testInstallationID))
	_, err = client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v", err)
	}
}

func TestClientStopsAndRestartsWhenSelectedInstallationChanges(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()

	secondID := contract.PrivacyModelID("model_00000000000000000000000000000002")
	secondIdentity := "example/privacy@1111111111111111111111111111111111111111#q4"
	secondDirectory := t.TempDir()
	secondManifestSHA256 := writeTestManifest(
		t,
		secondDirectory,
		secondID,
		secondIdentity,
	)
	provider := client.model.(*testModelProvider)
	provider.set(secondID, InstalledModel{
		Directory:      secondDirectory,
		Identity:       secondIdentity,
		ManifestSHA256: secondManifestSHA256,
	})
	client.ApplyPolicy(localModelPolicy(secondID))
	if first.running() {
		t.Fatal("old worker remained active after policy selection changed")
	}
	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("stale model snapshot error = %v", err)
	}

	if _, err := client.Detect(
		context.Background(),
		testDetectInput(secondID),
	); err != nil {
		t.Fatalf("second Detect: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if second == nil || second == first || second.modelID != secondID ||
		second.directory != secondDirectory {
		t.Fatalf("unexpected replacement worker: %#v", second)
	}
}

func TestClientRestartsWhenInstallationIdentityChangesInPlace(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()

	replacementIdentity := "openai/privacy-filter@1111111111111111111111111111111111111111#q4"
	provider := client.model.(*testModelProvider)
	installation, ready := provider.ReadyInstallation(testInstallationID)
	if !ready {
		t.Fatal("test installation not ready")
	}
	installation.ManifestSHA256 = writeTestManifest(
		t,
		installation.Directory,
		testInstallationID,
		replacementIdentity,
	)
	installation.Identity = replacementIdentity
	provider.set(testInstallationID, installation)

	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("replacement Detect: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if second == nil || second == first || first.running() ||
		second.identity != replacementIdentity {
		t.Fatalf("worker was not restarted for identity change: %#v", second)
	}
}

func TestClientRestartsWhenBoundManifestDigestChangesInPlace(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()

	provider := client.model.(*testModelProvider)
	installation, _ := provider.ReadyInstallation(testInstallationID)
	manifestPath := filepath.Join(installation.Directory, installationManifestName)
	document, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var manifest installationManifest
	if err := json.Unmarshal(document, &manifest); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	remapped := contract.CanonicalKindPaymentCard
	manifest.LabelMapping["private_email"] = &remapped
	document, err = json.Marshal(manifest)
	if err != nil || os.WriteFile(manifestPath, document, 0o600) != nil {
		t.Fatal("replace bound manifest")
	}
	digest := sha256.Sum256(document)
	installation.ManifestSHA256 = hex.EncodeToString(digest[:])
	provider.set(testInstallationID, installation)

	if _, err := client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	); err != nil {
		t.Fatalf("replacement Detect: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if second == nil || second == first || first.running() ||
		second.manifestSHA256 != installation.ManifestSHA256 {
		t.Fatalf("worker was not restarted for manifest binding change: %#v", second)
	}
}

func TestClientRejectsManifestIdentityMismatchAndUnknownFields(t *testing.T) {
	t.Run("identity mismatch", func(t *testing.T) {
		client := newTestClient(t, "success", 5*time.Second)
		provider := client.model.(*testModelProvider)
		installation, _ := provider.ReadyInstallation(testInstallationID)
		installation.Identity = "openai/privacy-filter@1111111111111111111111111111111111111111#q4"
		provider.set(testInstallationID, installation)
		_, err := client.Detect(
			context.Background(),
			testDetectInput(testInstallationID),
		)
		if !errors.Is(err, privacy.ErrDetectorUnavailable) {
			t.Fatalf("Detect error = %v", err)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		client := newTestClient(t, "success", 5*time.Second)
		provider := client.model.(*testModelProvider)
		installation, _ := provider.ReadyInstallation(testInstallationID)
		path := filepath.Join(installation.Directory, installationManifestName)
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		var manifest map[string]any
		if err := json.Unmarshal(document, &manifest); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		manifest["unexpected"] = true
		document, err = json.Marshal(manifest)
		if err != nil || os.WriteFile(path, document, 0o600) != nil {
			t.Fatal("write malformed manifest")
		}
		digest := sha256.Sum256(document)
		installation.ManifestSHA256 = hex.EncodeToString(digest[:])
		provider.set(testInstallationID, installation)
		_, err = client.Detect(
			context.Background(),
			testDetectInput(testInstallationID),
		)
		if !errors.Is(err, privacy.ErrDetectorUnavailable) {
			t.Fatalf("Detect error = %v", err)
		}
	})
}

func TestClientRejectsManifestReplacementWithUnchangedIdentity(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	provider := client.model.(*testModelProvider)
	installation, _ := provider.ReadyInstallation(testInstallationID)
	manifestPath := filepath.Join(installation.Directory, installationManifestName)
	document, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var manifest installationManifest
	if err := json.Unmarshal(document, &manifest); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	remapped := contract.CanonicalKindPaymentCard
	manifest.LabelMapping["private_email"] = &remapped
	replacement, err := json.Marshal(manifest)
	if err != nil || os.WriteFile(manifestPath, replacement, 0o600) != nil {
		t.Fatal("replace manifest")
	}

	_, err = client.Detect(
		context.Background(),
		testDetectInput(testInstallationID),
	)
	if !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("Detect error = %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil {
		t.Fatal("worker started from a manifest not bound by the registry")
	}
}

func TestClientRejectsCorruptOrSymlinkedInstalledFiles(t *testing.T) {
	t.Run("hash mismatch", func(t *testing.T) {
		client := newTestClient(t, "success", 5*time.Second)
		provider := client.model.(*testModelProvider)
		installation, _ := provider.ReadyInstallation(testInstallationID)
		filePath := filepath.Join(installation.Directory, "tokenizer.json")
		contents, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		for index := range contents {
			contents[index] = 'x'
		}
		if err := os.WriteFile(filePath, contents, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err = client.Detect(
			context.Background(),
			testDetectInput(testInstallationID),
		)
		if !errors.Is(err, privacy.ErrDetectorUnavailable) {
			t.Fatalf("Detect error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		client := newTestClient(t, "success", 5*time.Second)
		provider := client.model.(*testModelProvider)
		installation, _ := provider.ReadyInstallation(testInstallationID)
		filePath := filepath.Join(installation.Directory, "tokenizer.json")
		targetPath := filepath.Join(installation.Directory, "same-size-target")
		contents, err := os.ReadFile(filePath)
		if err != nil || os.WriteFile(targetPath, contents, 0o600) != nil {
			t.Fatal("prepare symlink target")
		}
		if err := os.Remove(filePath); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if err := os.Symlink(targetPath, filePath); err != nil {
			t.Skipf("symlink is unavailable: %v", err)
		}
		_, err = client.Detect(
			context.Background(),
			testDetectInput(testInstallationID),
		)
		if !errors.Is(err, privacy.ErrDetectorUnavailable) {
			t.Fatalf("Detect error = %v", err)
		}
	})
}

func TestClientReusesHotWorkerWithoutRehashingInstallation(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	input := testDetectInput(testInstallationID)
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()
	provider := client.model.(*testModelProvider)
	installation, _ := provider.ReadyInstallation(testInstallationID)
	if err := os.Remove(
		filepath.Join(installation.Directory, installationManifestName),
	); err != nil {
		t.Fatalf("Remove manifest: %v", err)
	}
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("hot Detect: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if second != first {
		t.Fatal("hot worker was unnecessarily restarted")
	}

	client.Stop()
	if _, err := client.Detect(context.Background(), input); !errors.Is(
		err,
		privacy.ErrDetectorUnavailable,
	) {
		t.Fatalf("Detect after restart error = %v", err)
	}
}

func TestClientReusesSuccessfulValidationAfterWorkerRestart(t *testing.T) {
	client := newTestClient(t, "success", 5*time.Second)
	input := testDetectInput(testInstallationID)
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("first Detect: %v", err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()
	client.stopProcess(first)

	provider := client.model.(*testModelProvider)
	installation, _ := provider.ReadyInstallation(testInstallationID)
	if err := os.Remove(
		filepath.Join(installation.Directory, installationManifestName),
	); err != nil {
		t.Fatalf("Remove manifest: %v", err)
	}
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatalf("Detect after worker restart: %v", err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if second == nil || second == first {
		t.Fatal("validated installation was not reused for a fresh worker")
	}
}

func TestWorkerKindAcceptsOnlyCanonicalExecutionKinds(t *testing.T) {
	expected := map[string]privacy.Kind{
		"email":           privacy.KindEmail,
		"phone":           privacy.KindPhone,
		"account":         privacy.KindAccount,
		"payment_card":    privacy.KindPaymentCard,
		"ip_address":      privacy.KindIPAddress,
		"url":             privacy.KindURL,
		"common_secret":   privacy.KindCommonSecret,
		"private_address": privacy.KindAddress,
		"private_date":    privacy.KindDate,
		"private_person":  privacy.KindPerson,
	}
	for label, want := range expected {
		if got, valid := workerKind(label); !valid || got != want {
			t.Errorf("workerKind(%q) = (%q,%t), want (%q,true)", label, got, valid, want)
		}
	}
	for _, legacy := range []string{
		"private_email", "private_phone", "private_url", "account_number", "secret",
	} {
		if _, valid := workerKind(legacy); valid {
			t.Errorf("workerKind accepted legacy label %q", legacy)
		}
	}
}

func TestOpenAIManifestMappingAllowsCompleteRemapAndIgnore(t *testing.T) {
	mapping := testOpenAILabelMapping()
	remapped := contract.CanonicalKindPaymentCard
	mapping["private_email"] = &remapped
	mapping["secret"] = nil
	if err := validateManifestMapping(
		contract.PrivacyModelAdapterOpenAIBIOES,
		mapping,
	); err != nil {
		t.Fatalf("complete remapped mapping: %v", err)
	}

	delete(mapping, "private_phone")
	if err := validateManifestMapping(
		contract.PrivacyModelAdapterOpenAIBIOES,
		mapping,
	); err == nil {
		t.Fatal("incomplete OpenAI mapping was accepted")
	}
}

func newTestClient(t *testing.T, mode string, timeout time.Duration) *Client {
	t.Helper()
	directory := t.TempDir()
	manifestSHA256 := writeTestManifest(t, directory, testInstallationID, testIdentity)
	model := &testModelProvider{
		installations: map[contract.PrivacyModelID]InstalledModel{
			testInstallationID: {
				Directory:      directory,
				Identity:       testIdentity,
				ManifestSHA256: manifestSHA256,
			},
		},
	}
	client, err := New(Config{
		ExecutablePath: os.Args[0],
		Model:          model,
		Timeout:        timeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client.command = func(_ string, arguments ...string) *exec.Cmd {
		helperArguments := []string{"-test.run=^TestPrivacyWorkerHelper$", "--"}
		helperArguments = append(helperArguments, arguments...)
		command := exec.Command(os.Args[0], helperArguments...)
		command.Env = append(os.Environ(),
			helperEnvironment+"=1",
			"ASTRLINK_PRIVACY_WORKER_TEST_MODE="+mode,
		)
		return command
	}
	client.ApplyPolicy(localModelPolicy(testInstallationID))
	t.Cleanup(client.Close)
	return client
}

func localModelPolicy(id contract.PrivacyModelID) contract.Policy {
	policy := contract.DefaultPrivacyPolicy()
	policy.Enabled = true
	policy.Detector = contract.PolicyDetectorLocalModel
	policy.LocalModelID = &id
	return policy
}

func testDetectInput(id contract.PrivacyModelID) privacy.DetectInput {
	return privacy.DetectInput{
		ExpectedLocalModelID: id,
		Segments:             []privacy.Segment{{Value: "person@example.test"}},
	}
}

func writeTestManifest(
	t *testing.T,
	directory string,
	id contract.PrivacyModelID,
	identity string,
) string {
	t.Helper()
	identityWithoutVariant, variantID, validIdentity := strings.Cut(identity, "#")
	separator := strings.LastIndex(identityWithoutVariant, "@")
	if !validIdentity || separator <= 0 {
		t.Fatalf("invalid test identity %q", identity)
	}
	repoID := identityWithoutVariant[:separator]
	revision := identityWithoutVariant[separator+1:]
	if err := os.MkdirAll(filepath.Join(directory, "onnx"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	paths := []string{
		"onnx/model_q4.onnx",
		"onnx/model_q4.onnx_data",
		"tokenizer.json",
		"config.json",
		"viterbi_calibration.json",
	}
	files := make([]installationFile, len(paths))
	for index, relativePath := range paths {
		content := []byte(relativePath)
		if relativePath == "config.json" {
			content = []byte(`{}`)
		}
		if err := os.WriteFile(
			filepath.Join(directory, filepath.FromSlash(relativePath)),
			content,
			0o600,
		); err != nil {
			t.Fatalf("WriteFile fixture: %v", err)
		}
		digest := sha256.Sum256(content)
		files[index] = installationFile{
			Path:   relativePath,
			Size:   int64(len(content)),
			SHA256: hex.EncodeToString(digest[:]),
		}
	}
	document, err := json.Marshal(installationManifest{
		Version:           1,
		InstallationID:    id,
		Identity:          identity,
		RepoID:            repoID,
		Revision:          revision,
		VariantID:         variantID,
		Adapter:           contract.PrivacyModelAdapterOpenAIBIOES,
		ModelPath:         "onnx/model_q4.onnx",
		ExternalDataPaths: []string{"onnx/model_q4.onnx_data"},
		TokenizerPath:     "tokenizer.json",
		ConfigPath:        "config.json",
		CalibrationPath:   stringPointer("viterbi_calibration.json"),
		TagScheme:         "bioes",
		Window:            4096,
		Stride:            128,
		MaxRequestTokens:  131_072,
		InputNames: installationInputNames{
			InputIDs:      "input_ids",
			AttentionMask: "attention_mask",
		},
		OutputName:   "logits",
		LabelMapping: testOpenAILabelMapping(),
		Files:        files,
	})
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, installationManifestName),
		document,
		0o600,
	); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}

func stringPointer(value string) *string {
	return &value
}

func testOpenAILabelMapping() map[string]*contract.CanonicalKind {
	values := map[string]contract.CanonicalKind{
		"account_number":  contract.CanonicalKindAccount,
		"private_address": contract.CanonicalKindAddress,
		"private_date":    contract.CanonicalKindDate,
		"private_email":   contract.CanonicalKindEmail,
		"private_person":  contract.CanonicalKindPerson,
		"private_phone":   contract.CanonicalKindPhone,
		"private_url":     contract.CanonicalKindURL,
		"secret":          contract.CanonicalKindCommonSecret,
	}
	result := make(map[string]*contract.CanonicalKind, len(values))
	for label, kind := range values {
		value := kind
		result[label] = &value
	}
	return result
}

func TestPrivacyWorkerHelper(t *testing.T) {
	if os.Getenv(helperEnvironment) != "1" {
		return
	}
	mode := os.Getenv("ASTRLINK_PRIVACY_WORKER_TEST_MODE")
	modelDirectory := helperModelDirectory(os.Args)
	if mode == "startup_fail" {
		os.Exit(9)
	}
	if mode == "missing_ready" {
		os.Exit(0)
	}
	readyPayload, err := json.Marshal(workerReady{
		Version: protocolVersion,
		Ready:   true,
	})
	if mode == "malformed_ready" {
		readyPayload = []byte(`{"version":1,"ready":true,"unexpected":true}`)
	}
	if err != nil || writeFrame(os.Stdout, readyPayload) != nil {
		os.Exit(4)
	}
	requestsServed := 0
	for {
		payload, err := readFrame(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		var request workerRequest
		if json.Unmarshal(payload, &request) != nil || len(request.Texts) == 0 {
			os.Exit(2)
		}
		requestsServed++
		if mode == "hang" {
			_, _ = readFrame(os.Stdin)
			os.Exit(0)
		}
		if mode == "crash_once" {
			marker := filepath.Join(modelDirectory, "crashed-once")
			if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
				if os.WriteFile(marker, []byte("1"), 0o600) != nil {
					os.Exit(3)
				}
				os.Exit(9)
			}
		}
		if mode == "crash_after_success_once" && requestsServed > 1 {
			marker := filepath.Join(modelDirectory, "crashed-after-success-once")
			if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
				if os.WriteFile(marker, []byte("1"), 0o600) != nil {
					os.Exit(3)
				}
				os.Exit(9)
			}
		}
		label := "email"
		if mode == "invalid_label" {
			label = "not_official"
		}
		if mode == "missing_spans" || mode == "null_spans" {
			spans := ""
			if mode == "null_spans" {
				spans = `,"spans":null`
			}
			payload := []byte(fmt.Sprintf(
				`{"version":%d,"id":%d%s}`,
				protocolVersion,
				request.ID,
				spans,
			))
			if writeFrame(os.Stdout, payload) != nil {
				os.Exit(4)
			}
			continue
		}
		if mode == "missing_score" {
			payload := []byte(fmt.Sprintf(
				`{"version":%d,"id":%d,"spans":[{"text_id":%d,"label":"email","start":0,"end":%d}]}`,
				protocolVersion,
				request.ID,
				request.Texts[0].ID,
				len(request.Texts[0].Text),
			))
			if writeFrame(os.Stdout, payload) != nil {
				os.Exit(4)
			}
			continue
		}
		score := 0.99
		if mode == "invalid_score" {
			score = 1.01
		}
		responseSpans := []workerSpan{{
			TextID: request.Texts[0].ID,
			Label:  label,
			Start:  0,
			End:    len(request.Texts[0].Text),
			Score:  &score,
		}}
		if mode == "context_values" || mode == "context_crossing" {
			const value = "秘密🔑same-value"
			text := request.Texts[0].Text
			start := strings.LastIndex(text, value)
			if start < 0 || (start > 0 && !strings.HasPrefix(text, "Field: ")) {
				os.Exit(8)
			}
			responseSpans[0].Start = start
			responseSpans[0].End = start + len(value)
			if mode == "context_crossing" {
				if start <= 0 {
					os.Exit(8)
				}
				responseSpans[0].Start = start - 1
				responseSpans[0].Label = "common_secret"
			} else if start > 0 {
				responseSpans = append(responseSpans,
					workerSpan{TextID: 0, Label: "email", Start: 0, End: start, Score: &score},
				)
			}
		}
		response := workerResponse{
			Version: protocolVersion,
			ID:      request.ID,
			Spans:   &responseSpans,
		}
		responsePayload, err := json.Marshal(response)
		if err != nil || writeFrame(os.Stdout, responsePayload) != nil {
			os.Exit(4)
		}
	}
}

func helperModelDirectory(arguments []string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--model-dir" {
			return arguments[index+1]
		}
	}
	return os.TempDir()
}

func waitForTestProcess(t *testing.T, client *Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		started := client.process != nil
		client.mu.Unlock()
		if started {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not start")
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(value []byte) (int, error) {
	return len(value), nil
}

func (discardWriteCloser) Close() error {
	return nil
}

type stuckReadCloser struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func (reader *stuckReadCloser) Read([]byte) (int, error) {
	reader.startOnce.Do(func() { close(reader.started) })
	<-reader.release
	return 0, io.EOF
}

func (reader *stuckReadCloser) Close() error {
	// Model a pipe that stays readable because another inherited writer still
	// owns the underlying stream. The client must return without waiting for
	// this Read to complete.
	return nil
}

func (reader *stuckReadCloser) unblock() {
	reader.releaseOnce.Do(func() { close(reader.release) })
}

func newStuckWorkerProcess() (*workerProcess, *stuckReadCloser) {
	reader := &stuckReadCloser{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	done := make(chan struct{})
	close(done)
	return &workerProcess{
		command: &exec.Cmd{},
		stdin:   discardWriteCloser{},
		stdout:  reader,
		reader:  bufio.NewReader(reader),
		done:    done,
	}, reader
}
