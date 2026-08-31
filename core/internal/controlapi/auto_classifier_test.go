package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
	"github.com/QuantumNous/astrlink/core/internal/automodel"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

type fakeAutoClassifierRegistry struct {
	probe      contract.AutoClassifierProbeResponse
	probeErr   error
	install    contract.AutoClassifierInstallation
	installErr error
	items      []contract.AutoClassifierInstallation
	lastPath   string
}

func (registry *fakeAutoClassifierRegistry) ProbeLocal(
	_ context.Context,
	request contract.AutoClassifierLocalProbeRequest,
) (contract.AutoClassifierProbeResponse, error) {
	registry.lastPath = request.Path
	return registry.probe, registry.probeErr
}

func (registry *fakeAutoClassifierRegistry) Install(
	_ context.Context,
	request contract.AutoClassifierInstallRequest,
) (contract.AutoClassifierInstallation, error) {
	registry.lastPath = request.Path
	return registry.install, registry.installErr
}

func (registry *fakeAutoClassifierRegistry) ListInstallations() []contract.AutoClassifierInstallation {
	return registry.items
}

func (registry *fakeAutoClassifierRegistry) GetInstallation(
	id contract.AutoClassifierID,
) (contract.AutoClassifierInstallation, error) {
	for _, item := range registry.items {
		if item.ID == id {
			return item, nil
		}
	}
	return contract.AutoClassifierInstallation{}, automodel.ErrNotFound
}

func (registry *fakeAutoClassifierRegistry) ReadyInstallation(
	contract.AutoClassifierID,
) (contract.ReadyAutoClassifierInstallation, bool) {
	return contract.ReadyAutoClassifierInstallation{}, false
}

func (registry *fakeAutoClassifierRegistry) FirstReady() (contract.ReadyAutoClassifierInstallation, bool) {
	return contract.ReadyAutoClassifierInstallation{}, false
}

type fakeAutoClassifier struct {
	outcome  autoclassifier.Outcome
	tier     contract.AutoClassifierArtifactTier
	eligible bool
	lastText string
}

func (classifier *fakeAutoClassifier) Classify(_ context.Context, text string) autoclassifier.Outcome {
	classifier.lastText = text
	return classifier.outcome
}

func (classifier *fakeAutoClassifier) ArtifactTier() contract.AutoClassifierArtifactTier {
	return classifier.tier
}

func (classifier *fakeAutoClassifier) EligibleForRouting() bool {
	return classifier.eligible
}

func newAutoClassifierHandler(
	t *testing.T,
	registry AutoClassifierRegistry,
	classifier AutoClassifier,
) *Handler {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:    store,
			ControlToken:    testControlToken,
			AutoClassifiers: registry,
			AutoClassifier:  classifier,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAutoClassifierControlSurface(t *testing.T) {
	installation := contract.AutoClassifierInstallation{
		ID:                 "classifier_ab0123456789abcdef0123456789abcd",
		Identity:           "local/model-aaaaaaaaaaaa@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:               "Local classifier",
		ArtifactTier:       contract.AutoClassifierArtifactExperimental,
		EligibleForRouting: true,
		TaxonomyID:         autotaxonomy.ID,
		Status:             contract.AutoClassifierStatusReady,
		BytesTotal:         12,
	}
	registry := &fakeAutoClassifierRegistry{
		probe: contract.AutoClassifierProbeResponse{
			RepoID:             "local/model-aaaaaaaaaaaa",
			Revision:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Name:               "Local classifier",
			ArtifactTier:       contract.AutoClassifierArtifactExperimental,
			EligibleForRouting: true,
			TaxonomyID:         autotaxonomy.ID,
			TaxonomySHA256:     autotaxonomy.SHA256,
			BytesTotal:         12,
			ID2Label:           append([]string(nil), autotaxonomy.Labels...),
		},
		install: installation,
		items:   []contract.AutoClassifierInstallation{installation},
	}
	classifier := &fakeAutoClassifier{
		outcome: autoclassifier.Outcome{
			Category: "general",
			Logits:   []float32{1, 0, 0, 0},
		},
		tier:     contract.AutoClassifierArtifactExperimental,
		eligible: true,
	}
	handler := newAutoClassifierHandler(t, registry, classifier)

	response := controlRequest(
		t, handler, http.MethodPost, AutoClassifierLocalProbePath,
		"application/json", `{"path":"/tmp/classifier"}`, "",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", response.Code, response.Body.String())
	}
	if registry.lastPath != "/tmp/classifier" {
		t.Fatalf("probe path = %q", registry.lastPath)
	}

	response = controlRequest(
		t, handler, http.MethodPost, AutoClassifierPath,
		"application/json", `{"path":"/tmp/classifier"}`, "",
	)
	if response.Code != http.StatusAccepted {
		t.Fatalf("install status=%d body=%s", response.Code, response.Body.String())
	}

	response = controlRequest(t, handler, http.MethodGet, AutoClassifierPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	response = controlRequest(
		t, handler, http.MethodPost, AutoClassifierClassifyPreviewPath,
		"application/json",
		`{"protocol":"openai.chat","body":{"messages":[{"role":"system","content":"sys"},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}]}}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", response.Code, response.Body.String())
	}
	if classifier.lastText != "hello\nworld" {
		t.Fatalf("extracted text = %q", classifier.lastText)
	}
	var preview contract.AutoClassifierClassifyPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Category != "general" || !preview.EligibleForRouting || preview.ArtifactTier != contract.AutoClassifierArtifactExperimental {
		t.Fatalf("preview = %+v", preview)
	}

	response = controlRequest(
		t, handler, http.MethodPost, AutoClassifierClassifyPreviewPath,
		"application/json", `{"text":"   "}`, "",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("blank preview status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.FallbackReason != autoclassifier.FallbackEmptyText {
		t.Fatalf("blank preview = %+v", preview)
	}

	registry.probeErr = automodel.ErrUnsupportedModel
	response = controlRequest(
		t, handler, http.MethodPost, AutoClassifierLocalProbePath,
		"application/json", `{"path":"/tmp/classifier"}`, "",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported probe status=%d", response.Code)
	}
}
