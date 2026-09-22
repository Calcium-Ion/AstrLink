package privacyworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

func TestContextProjectionUsesOnlyOriginalUTF8ValueRange(t *testing.T) {
	value := "秘密🔑same-value"
	prefix := "Field: \"/input/0/arguments/秘密🔑same-value\"\nValue: "
	request, modelSegments, lengths, err := contextualWorkerRequest(42, []privacy.Segment{{Value: value, ContextPrefix: prefix}}, toolFieldPathInputContract)
	if err != nil || request.Texts[0].Text != prefix+value {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	start := len(prefix)
	score := 0.99
	spans := []workerSpan{
		{TextID: 0, Label: "common_secret", Start: 0, End: len(prefix), Score: &score},
		{TextID: 0, Label: "common_secret", Start: start, End: start + len(value), Score: &score},
	}
	findings, err := responseFindings(workerResponse{Version: 1, ID: 42, Spans: &spans}, 42, modelSegments)
	if err != nil {
		t.Fatal(err)
	}
	got, err := projectContextFindings(findings, lengths)
	if err != nil || len(got) != 1 || got[0].Start != 0 || got[0].End != len(value) {
		t.Fatalf("projection=%+v err=%v", got, err)
	}
	crossing := append(findings, privacy.Finding{Segment: 0, Start: start - 1, End: start + len(value), Kind: privacy.KindCommonSecret, Confidence: score})
	if got, err := projectContextFindings(crossing, lengths); !errors.Is(err, privacy.ErrDetectorUnavailable) || got != nil {
		t.Fatalf("crossing value prediction must fail closed: findings=%+v err=%v", got, err)
	}
	spans[1].Start++ // middle of a Chinese UTF-8 character must fail, never round.
	if _, err := responseFindings(workerResponse{Version: 1, ID: 42, Spans: &spans}, 42, modelSegments); !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("invalid UTF8 accepted: %v", err)
	}
}

func TestContextRequestDefaultAndLimits(t *testing.T) {
	segments := []privacy.Segment{{Value: "same", ContextPrefix: "Field: key\nValue: "}, {Value: "ordinary user text"}}
	for _, contractName := range []string{"", toolFieldPathInputContract} {
		request, _, lengths, err := contextualWorkerRequest(1, segments, contractName)
		if err != nil {
			t.Fatal(err)
		}
		want := segments[0].Value
		if contractName != "" {
			want = segments[0].ContextPrefix + want
		}
		if request.Texts[0].Text != want || request.Texts[1].Text != segments[1].Value || lengths[1] != 0 {
			t.Fatalf("wrong request: %+v", request)
		}
	}
	if _, _, _, err := contextualWorkerRequest(1, segments, "unknown"); !errors.Is(err, privacy.ErrDetectorUnavailable) {
		t.Fatalf("unknown contract accepted: %v", err)
	}
	segments[0].ContextPrefix = strings.Repeat("x", maxContextPrefixBytes+1)
	if _, _, _, err := contextualWorkerRequest(1, segments, toolFieldPathInputContract); !errors.Is(err, privacy.ErrDetectorLimit) {
		t.Fatalf("prefix limit not enforced: %v", err)
	}
	// Old models do not consume or validate unused prefix metadata.
	if _, _, _, err := contextualWorkerRequest(1, segments, ""); err != nil {
		t.Fatalf("old model behavior changed: %v", err)
	}
}

func TestInstalledInputContractIsBoundAndOptIn(t *testing.T) {
	for _, test := range []struct {
		metadata, want string
		fail           bool
	}{
		{`{}`, "", false},
		{`{"astrlink_guard_input_contract":"tool-field-path-v1"}`, toolFieldPathInputContract, false},
		{`{"astrlink_guard_input_contract":"future"}`, "", true},
		{`{"astrlink_guard_input_contract":true}`, "", true},
		{`{"astrlink_guard_input_contract":null}`, "", true},
	} {
		t.Run(test.metadata, func(t *testing.T) {
			client := newTestClient(t, "success", 5*time.Second)
			installation := setTestInputContract(t, client, []byte(test.metadata))
			got, err := installedInputContract(installation)
			if (err != nil) != test.fail || got != test.want {
				t.Fatalf("contract=%q err=%v", got, err)
			}
			if err := os.WriteFile(filepath.Join(installation.Directory, "config.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			if test.metadata != `{}` {
				if _, err := installedInputContract(installation); !errors.Is(err, privacy.ErrDetectorUnavailable) {
					t.Fatalf("unbound metadata accepted: %v", err)
				}
			}
		})
	}
}

func TestClientUsesContextContractAndResetsItWithModelBinding(t *testing.T) {
	client := newTestClient(t, "context_values", 5*time.Second)
	setTestInputContract(t, client, []byte(`{"astrlink_guard_input_contract":"tool-field-path-v1"}`))
	value := "秘密🔑same-value"
	input := privacy.DetectInput{ExpectedLocalModelID: testInstallationID, Segments: []privacy.Segment{{Value: value, ContextPrefix: "Field: \"/arguments/秘密🔑same-value\"\nValue: "}}}
	findings, err := client.Detect(context.Background(), input)
	if err != nil || len(findings) != 1 || findings[0].Start != 0 || findings[0].End != len(value) {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	client.mu.Lock()
	first := client.process
	client.mu.Unlock()
	if first.inputContract != toolFieldPathInputContract {
		t.Fatal("contract not bound to process")
	}
	// Same installation ID, newly hashed config: the contract must not leak
	// from its previous process. The helper accepts only plain value now.
	setTestInputContract(t, client, []byte(`{}`))
	findings, err = client.Detect(context.Background(), input)
	if err != nil || len(findings) != 1 || findings[0].Start != 0 || findings[0].End != len(value) {
		t.Fatalf("legacy findings=%+v err=%v", findings, err)
	}
	client.mu.Lock()
	second := client.process
	client.mu.Unlock()
	if first == second || second.inputContract != "" {
		t.Fatal("input contract leaked across installation change")
	}
}

func TestContextWorkerEngineRoundTripRewritesOnlyOriginalValue(t *testing.T) {
	client := newTestClient(t, "context_values", 5*time.Second)
	setTestInputContract(t, client, []byte(`{"astrlink_guard_input_contract":"tool-field-path-v1"}`))
	engine, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return privacy.Policy{}, nil
	}), client)
	if err != nil {
		t.Fatal(err)
	}
	const value = "秘密🔑same-value"
	const body = ` {"messages":[{"role":"assistant","content":[{"type":"tool_use","input":{"秘密🔑same-value":"秘密🔑same-value"}}]}]} `
	result, err := engine.Inspect(context.Background(), privacy.Policy{
		Enabled: true, Mode: privacy.ModeLocalModel, LocalModelID: testInstallationID, Action: privacy.ActionRedact,
	}, contract.ProtocolAnthropicMessages, []byte(body))
	if err != nil || len(result.Redactions) != 1 || result.Redactions[0].Value != value {
		t.Fatalf("redactions=%+v err=%v", result.Redactions, err)
	}
	want := strings.Replace(body, `:"`+value+`"`, `:"`+result.Redactions[0].Placeholder+`"`, 1)
	if string(result.Body) != want {
		t.Fatalf("rewritten wrong bytes: %s", result.Body)
	}
}

func TestContextWorkerCrossingSecretFailsClosedThroughEngine(t *testing.T) {
	client := newTestClient(t, "context_crossing", 5*time.Second)
	setTestInputContract(t, client, []byte(`{"astrlink_guard_input_contract":"tool-field-path-v1"}`))
	engine, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return privacy.Policy{}, nil
	}), client)
	if err != nil {
		t.Fatal(err)
	}
	// The helper returns one common_secret prediction covering the entire
	// value plus the preceding separator space. Returning no findings here
	// would incorrectly turn a detected secret into an allowed request.
	const body = `{"messages":[{"role":"assistant","content":[{"type":"tool_use","input":{"value":"秘密🔑same-value"}}]}]}`
	result, err := engine.Inspect(context.Background(), privacy.Policy{
		Enabled: true, Mode: privacy.ModeLocalModel, LocalModelID: testInstallationID, Action: privacy.ActionRedact,
	}, contract.ProtocolAnthropicMessages, []byte(body))
	if !errors.Is(err, privacy.ErrDetectorUnavailable) || result.Decision == privacy.DecisionAllow || result.Body != nil {
		t.Fatalf("crossing secret silently allowed or rewritten: decision=%s err=%v", result.Decision, err)
	}
}

func setTestInputContract(t *testing.T, client *Client, config []byte) InstalledModel {
	t.Helper()
	provider := client.model.(*testModelProvider)
	installation, _ := provider.ReadyInstallation(testInstallationID)
	manifestPath := filepath.Join(installation.Directory, installationManifestName)
	document, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest installationManifest
	if err := json.Unmarshal(document, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installation.Directory, manifest.ConfigPath), config, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(config)
	for index := range manifest.Files {
		if manifest.Files[index].Path == manifest.ConfigPath {
			manifest.Files[index].Size = int64(len(config))
			manifest.Files[index].SHA256 = hex.EncodeToString(digest[:])
		}
	}
	manifest.Adapter = contract.PrivacyModelAdapterHFToken
	manifest.CalibrationPath = nil
	manifest.TagScheme = "bio"
	document, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, document, 0600); err != nil {
		t.Fatal(err)
	}
	digest = sha256.Sum256(document)
	installation.ManifestSHA256 = hex.EncodeToString(digest[:])
	provider.set(testInstallationID, installation)
	return installation
}
