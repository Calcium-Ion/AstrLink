package controlapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
)

type fakePrivacyModelRegistry struct {
	catalog       contract.PrivacyModelCatalogResponse
	probeResponse contract.PrivacyModelProbeResponse
	probeErr      error
	installErr    error
	deleteErr     error
	installations map[contract.PrivacyModelID]contract.PrivacyModelInstallation
	ready         map[contract.PrivacyModelID]bool
	installCalls  int
	deleteCalls   int
	lastInstall   contract.PrivacyModelInstallRequest
	lastDeleted   contract.PrivacyModelID
}

func (registry *fakePrivacyModelRegistry) Catalog() contract.PrivacyModelCatalogResponse {
	return registry.catalog
}

func (registry *fakePrivacyModelRegistry) Probe(
	context.Context,
	contract.PrivacyModelProbeRequest,
) (contract.PrivacyModelProbeResponse, error) {
	return registry.probeResponse, registry.probeErr
}

func (registry *fakePrivacyModelRegistry) ListInstallations() []contract.PrivacyModelInstallation {
	result := make([]contract.PrivacyModelInstallation, 0, len(registry.installations))
	for _, installation := range registry.installations {
		result = append(result, installation)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].ID < result[right].ID
	})
	return result
}

func (registry *fakePrivacyModelRegistry) GetInstallation(
	id contract.PrivacyModelID,
) (contract.PrivacyModelInstallation, error) {
	installation, exists := registry.installations[id]
	if !exists {
		return contract.PrivacyModelInstallation{}, privacymodel.ErrNotFound
	}
	return installation, nil
}

func (registry *fakePrivacyModelRegistry) Install(
	_ context.Context,
	request contract.PrivacyModelInstallRequest,
) (contract.PrivacyModelInstallation, error) {
	registry.installCalls++
	registry.lastInstall = request
	if registry.installErr != nil {
		return contract.PrivacyModelInstallation{}, registry.installErr
	}
	id := privacymodel.InstallationID(
		request.RepoID,
		request.Revision,
		request.VariantID,
	)
	installation := registry.installations[id]
	return installation, nil
}

func (registry *fakePrivacyModelRegistry) DeleteInstallation(
	_ context.Context,
	id contract.PrivacyModelID,
) error {
	registry.deleteCalls++
	registry.lastDeleted = id
	if registry.deleteErr != nil {
		return registry.deleteErr
	}
	if _, exists := registry.installations[id]; !exists {
		return privacymodel.ErrNotFound
	}
	delete(registry.installations, id)
	delete(registry.ready, id)
	return nil
}

func (registry *fakePrivacyModelRegistry) ReadyInstallation(
	id contract.PrivacyModelID,
) (contract.ReadyPrivacyModelInstallation, bool) {
	if !registry.ready[id] {
		return contract.ReadyPrivacyModelInstallation{}, false
	}
	installation, exists := registry.installations[id]
	if !exists {
		return contract.ReadyPrivacyModelInstallation{}, false
	}
	return contract.ReadyPrivacyModelInstallation{
		Directory:      "/not-exposed",
		ManifestSHA256: "not-exposed",
		Identity: installation.RepoID + "@" +
			installation.Revision + "#" + installation.VariantID,
	}, true
}

func readyTestInstallation() contract.PrivacyModelInstallation {
	catalogID := privacymodel.CatalogOpenAIPrivacyFilter
	catalogSource := contract.PrivacyModelCatalogSourceOfficial
	license := "Apache-2.0"
	installedAt := "2026-07-24T00:00:00Z"
	return contract.PrivacyModelInstallation{
		ID:            contract.LegacyOpenAIPrivacyFilterInstallationID,
		Source:        contract.PrivacyModelSourceCatalog,
		CatalogID:     &catalogID,
		CatalogSource: &catalogSource,
		Name:          "OpenAI Privacy Filter",
		License:       &license,
		Languages:     []string{"en"},
		RepoID:        "openai/privacy-filter",
		Revision:      privacymodel.DefaultRevision,
		VariantID:     "cpu_q4", VariantName: "CPU Q4",
		Quantization:    "q4",
		Adapter:         contract.PrivacyModelAdapterOpenAIBIOES,
		Status:          contract.PrivacyModelStatusReady,
		BytesDownloaded: 1, BytesTotal: 1,
		EstimatedRAMBytes: 2,
		LabelMapping: map[string]*contract.CanonicalKind{
			"account_number":  nil,
			"private_address": nil,
			"private_date":    nil,
			"private_email":   nil,
			"private_person":  nil,
			"private_phone":   nil,
			"private_url":     nil,
			"secret":          nil,
		},
		InstalledAt: &installedAt,
	}
}

func newFakePrivacyModelRegistry() *fakePrivacyModelRegistry {
	installation := readyTestInstallation()
	return &fakePrivacyModelRegistry{
		catalog: privacymodel.BuiltinCatalog(),
		installations: map[contract.PrivacyModelID]contract.PrivacyModelInstallation{
			installation.ID: installation,
		},
		ready: map[contract.PrivacyModelID]bool{},
	}
}

func TestRemovedSingletonPrivacyModelRoutesReturnNotFound(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	for _, path := range []string{
		"/control/v1/privacy-model",
		"/control/v1/privacy-model/download",
	} {
		response := policyRequest(t, handler, "GET", path, "", "", "")
		if response.Code != 404 {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestPrivacyModelCollectionRoutesAndSelectedDeleteGuard(t *testing.T) {
	store, handler, registry := newPolicyHandler(t)
	response := policyRequest(
		t,
		handler,
		http.MethodGet,
		PrivacyModelCatalogPath,
		"",
		"",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", response.Code, response.Body.String())
	}
	var catalog contract.PrivacyModelCatalogResponse
	decode(t, response, &catalog)
	if len(catalog.Items) != 3 {
		t.Fatalf("catalog=%#v", catalog)
	}

	license := "apache-2.0"
	kind := contract.CanonicalKindEmail
	registry.probeResponse = contract.PrivacyModelProbeResponse{
		RepoID: "acme/model", RequestedRevision: "main",
		Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:     "Model", License: &license, Languages: []string{"en"},
		Adapter: contract.PrivacyModelAdapterHFToken,
		Variants: []contract.PrivacyModelVariant{{
			ID: "q4", Name: "Q4", Quantization: "q4",
			BytesTotal: 1, EstimatedRAMBytes: 2, Supported: true,
		}},
		Labels: []contract.PrivacyModelLabel{{
			Label: "EMAIL", SuggestedKind: &kind,
		}},
	}
	response = policyRequest(
		t,
		handler,
		http.MethodPost,
		PrivacyModelProbePath,
		"application/json",
		`{"repo_id":"acme/model","revision":"main"}`,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", response.Code, response.Body.String())
	}

	response = policyRequest(
		t,
		handler,
		http.MethodGet,
		PrivacyModelsPath,
		"",
		"",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var list contract.PrivacyModelInstallationList
	decode(t, response, &list)
	if len(list.Items) != 1 ||
		list.Items[0].CatalogSource == nil ||
		*list.Items[0].CatalogSource !=
			contract.PrivacyModelCatalogSourceOfficial ||
		list.Items[0].License == nil ||
		*list.Items[0].License != "Apache-2.0" ||
		len(list.Items[0].Languages) != 1 ||
		list.Items[0].Languages[0] != "en" {
		t.Fatalf("list=%#v", list)
	}

	response = policyRequest(
		t,
		handler,
		http.MethodPost,
		PrivacyModelsPath,
		"application/json",
		`{"repo_id":"openai/privacy-filter","revision":"7ffa9a043d54d1be65afb281eddf0ffbe629385b","variant_id":"cpu_q4","label_mapping":{}}`,
		"",
	)
	if response.Code != http.StatusAccepted || registry.installCalls != 1 {
		t.Fatalf("install status=%d calls=%d body=%s", response.Code, registry.installCalls, response.Body.String())
	}

	id := contract.LegacyOpenAIPrivacyFilterInstallationID
	itemPath := PrivacyModelsPath + "/" + string(id)
	response = policyRequest(t, handler, http.MethodGet, itemPath, "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("item status=%d body=%s", response.Code, response.Body.String())
	}
	record, err := store.GetPolicy(
		context.Background(),
		contract.DefaultPrivacyPolicyID,
	)
	if err != nil {
		t.Fatal(err)
	}
	selected := record.Policy
	selected.Detector = contract.PolicyDetectorLocalModel
	selected.LocalModelID = &id
	if _, err := store.UpdatePolicy(
		context.Background(),
		selected,
		record.ETag,
	); err != nil {
		t.Fatal(err)
	}
	response = policyRequest(t, handler, http.MethodDelete, itemPath, "", "", "")
	if response.Code != http.StatusConflict || registry.deleteCalls != 0 {
		t.Fatalf("selected delete status=%d calls=%d body=%s", response.Code, registry.deleteCalls, response.Body.String())
	}
	record, err = store.GetPolicy(context.Background(), contract.DefaultPrivacyPolicyID)
	if err != nil {
		t.Fatal(err)
	}
	unselected := record.Policy
	unselected.Detector = contract.PolicyDetectorRegex
	unselected.LocalModelID = nil
	if _, err := store.UpdatePolicy(
		context.Background(),
		unselected,
		record.ETag,
	); err != nil {
		t.Fatal(err)
	}
	response = policyRequest(t, handler, http.MethodDelete, itemPath, "", "", "")
	if response.Code != http.StatusNoContent || registry.deleteCalls != 1 {
		t.Fatalf("delete status=%d calls=%d body=%s", response.Code, registry.deleteCalls, response.Body.String())
	}
}

func TestPrivacyModelRoutesRejectQueriesUnknownFieldsAndWrongMethods(t *testing.T) {
	_, handler, registry := newPolicyHandler(t)
	tests := []struct {
		method      string
		path        string
		contentType string
		body        string
		status      int
	}{
		{http.MethodGet, PrivacyModelCatalogPath + "?x=1", "", "", http.StatusBadRequest},
		{http.MethodPost, PrivacyModelCatalogPath, "", "", http.StatusMethodNotAllowed},
		{http.MethodGet, PrivacyModelProbePath, "", "", http.StatusMethodNotAllowed},
		{http.MethodPost, PrivacyModelProbePath, "application/json", `{"repo_id":"acme/model","revision":"main","extra":true}`, http.StatusBadRequest},
		{http.MethodPost, PrivacyModelsPath, "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{http.MethodPatch, PrivacyModelsPath, "", "", http.StatusMethodNotAllowed},
		{http.MethodGet, PrivacyModelsPath + "/not-an-id", "", "", http.StatusBadRequest},
	}
	for _, test := range tests {
		response := policyRequest(
			t,
			handler,
			test.method,
			test.path,
			test.contentType,
			test.body,
			"",
		)
		if response.Code != test.status {
			t.Fatalf("%s %s status=%d want=%d body=%s", test.method, test.path, response.Code, test.status, response.Body.String())
		}
	}
	if registry.installCalls != 0 {
		t.Fatalf("invalid requests started %d installs", registry.installCalls)
	}
}

func TestPrivacyModelRegistryErrorDetailsAreSanitized(t *testing.T) {
	_, handler, registry := newPolicyHandler(t)
	registry.probeErr = errors.New("secret /Users/alice/model")
	response := policyRequest(
		t,
		handler,
		"POST",
		PrivacyModelProbePath,
		"application/json",
		`{"repo_id":"owner/model","revision":"main"}`,
		"",
	)
	if response.Code != 500 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); containsPrivateDetail(body) {
		t.Fatalf("registry detail leaked: %s", body)
	}
}

func containsPrivateDetail(value string) bool {
	return contains(value, "secret") || contains(value, "/Users/")
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
