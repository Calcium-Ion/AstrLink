package privacymodel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	guardUpdateSHA  = "1111111111111111111111111111111111111111"
	guardFutureSHA  = "2222222222222222222222222222222222222222"
	guardUnknownSHA = "3333333333333333333333333333333333333333"
)

type fakeGuardRelease struct {
	tag   string
	sha   string
	files map[string][]byte
	// lfsSHA overrides the Hub's blob digest for a file.
	lfsSHA map[string]string
	// unavailable files fail with 503, as from an unreachable Hub.
	unavailable map[string]bool
}

type fakeGuardHub struct {
	server   *httptest.Server
	releases []fakeGuardRelease
	requests atomic.Int64
	// downloadCounts records the HEADs the Hub counts as downloads.
	downloadCounts atomic.Int64
	// fetched records every file path requested from any revision.
	fetched sync.Map
}

func newFakeGuardHub(t *testing.T, releases ...fakeGuardRelease) *fakeGuardHub {
	t.Helper()
	hub := &fakeGuardHub{releases: releases}
	hub.server = httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	t.Cleanup(hub.server.Close)
	return hub
}

func (hub *fakeGuardHub) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	hub.requests.Add(1)
	api := "/api/models/" + astrLinkGuardRepoID + "/"
	resolve := "/" + astrLinkGuardRepoID + "/resolve/"
	switch {
	case request.Method == http.MethodHead &&
		request.URL.Path == resolve+astrLinkGuardCountRevision+"/config.json":
		hub.downloadCounts.Add(1)
	case request.URL.Path == api+"refs":
		tags := []map[string]string{{"name": "v" + astrLinkGuardVersion}}
		for _, release := range hub.releases {
			tags = append(tags, map[string]string{"name": release.tag})
		}
		writeFakeGuardJSON(writer, map[string]any{"tags": tags, "branches": []any{}})
	case strings.HasPrefix(request.URL.Path, api+"revision/"):
		release, exists := hub.release(strings.TrimPrefix(request.URL.Path, api+"revision/"))
		if !exists {
			http.NotFound(writer, request)
			return
		}
		siblings := make([]map[string]any, 0, len(release.files))
		for path, document := range release.files {
			sibling := map[string]any{"rfilename": path, "size": len(document)}
			if strings.Contains(path, ".onnx") {
				digest := sha256Hex(document)
				if override, exists := release.lfsSHA[path]; exists {
					digest = override
				}
				sibling["lfs"] = map[string]any{"sha256": digest, "size": len(document)}
			}
			siblings = append(siblings, sibling)
		}
		writeFakeGuardJSON(writer, map[string]any{
			"id": astrLinkGuardRepoID, "modelId": astrLinkGuardRepoID,
			"sha": release.sha, "siblings": siblings,
		})
	case strings.HasPrefix(request.URL.Path, resolve):
		revision, path, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, resolve), "/")
		hub.fetched.Store(path, true)
		release, exists := hub.release(revision)
		document, found := release.files[path]
		if exists && release.unavailable[path] {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if !exists || !found {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(document)))
		_, _ = writer.Write(document)
	default:
		http.NotFound(writer, request)
	}
}

func (hub *fakeGuardHub) release(revision string) (fakeGuardRelease, bool) {
	for _, release := range hub.releases {
		if release.tag == revision || release.sha == revision {
			return release, true
		}
	}
	return fakeGuardRelease{}, false
}

func writeFakeGuardJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func guardConfigDocument(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	id2label := map[string]string{"0": "O"}
	for index, kind := range astrLinkGuardKinds {
		id2label[strconv.Itoa(1+index*2)] = "B-" + string(kind)
		id2label[strconv.Itoa(2+index*2)] = "I-" + string(kind)
	}
	config := map[string]any{
		"architectures":                   []string{"BertForTokenClassification"},
		"model_type":                      "bert",
		"type_vocab_size":                 2,
		"id2label":                        id2label,
		astrLinkGuardDecoderContractField: astrLinkGuardDecoderContract,
		astrLinkGuardSpanContractField:    astrLinkGuardSpanContract,
	}
	if edit != nil {
		edit(config)
	}
	document, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal Guard config: %v", err)
	}
	return document
}

func guardReleaseFiles(t *testing.T, config []byte, marker string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{astrLinkGuardLicense: []byte("Apache License " + marker)}
	for _, variant := range []string{"cpu_int8", "cpu_fp32"} {
		folder, model, _ := astrLinkGuardLayout(variant)
		folderFiles := map[string][]byte{
			model:                   []byte("guard onnx " + marker + folder),
			model + "_data":         []byte("guard tensors " + marker + folder),
			"config.json":           config,
			"tokenizer.json":        []byte(`{"version":"1.0","model":{"type":"Unigram"}}`),
			"tokenizer_config.json": []byte(`{"tokenizer_class":"XLMRobertaTokenizer"}`),
			"label_mapping.json":    []byte(`{"schema":"astrlink-guard-labels"}`),
		}
		checksums := make(map[string]releaseChecksum, len(folderFiles))
		for name, document := range folderFiles {
			checksums[name] = releaseChecksum{
				SHA256: sha256Hex(document), Size: int64(len(document)),
			}
			files[folder+"/"+name] = document
		}
		document, err := json.Marshal(checksums)
		if err != nil {
			t.Fatalf("marshal checksums: %v", err)
		}
		files[folder+"/"+releaseChecksumsName] = document
	}
	return files
}

func TestAstrLinkGuardConfigMatchesWorkerContract(t *testing.T) {
	if err := validateAstrLinkGuardConfig(guardConfigDocument(t, nil)); err != nil {
		t.Fatalf("published config rejected: %v", err)
	}
	if err := validateAstrLinkGuardConfig(guardConfigDocument(t, func(config map[string]any) {
		delete(config, astrLinkGuardDecoderContractField)
		config[astrLinkGuardSpanContractField] = nil
	})); err != nil {
		t.Fatalf("config without contracts rejected: %v", err)
	}
	cases := map[string]func(map[string]any){
		"reordered labels": func(config map[string]any) {
			labels := config["id2label"].(map[string]string)
			labels["1"], labels["3"] = labels["3"], labels["1"]
		},
		"extra label": func(config map[string]any) {
			config["id2label"].(map[string]string)["21"] = "B-extra"
		},
		"unknown decoder contract": func(config map[string]any) {
			config[astrLinkGuardDecoderContractField] = "bio-offset-consistency-v2"
		},
		"non-string span contract": func(config map[string]any) {
			config[astrLinkGuardSpanContractField] = true
		},
		"no token type ids": func(config map[string]any) {
			delete(config, "type_vocab_size")
		},
		"sequence classifier": func(config map[string]any) {
			config["architectures"] = []string{"BertForSequenceClassification"}
		},
	}
	for name, edit := range cases {
		if err := validateAstrLinkGuardConfig(guardConfigDocument(t, edit)); !errors.Is(err, ErrUnsupportedModel) {
			t.Fatalf("%s: error=%v", name, err)
		}
	}
}

func TestAstrLinkGuardBuiltinManifestSurvivesPersistence(t *testing.T) {
	for _, variant := range []string{"cpu_int8", "cpu_fp32"} {
		plan, exists := builtinVariantPlan(astrLinkGuardRepoID, astrLinkGuardRevision, variant)
		folder, model, _ := astrLinkGuardLayout(variant)
		if !exists || plan.runtime.modelPath != folder+"/"+model ||
			plan.runtime.configPath != folder+"/config.json" ||
			plan.runtime.inputNames.TokenTypeIDs == nil ||
			plan.assets[len(plan.assets)-1].Path != astrLinkGuardLicense {
			t.Fatalf("%s plan=%#v", variant, plan)
		}
		manifest := buildNormalizedManifest(contract.PrivacyModelInstallation{
			ID:     InstallationID(astrLinkGuardRepoID, astrLinkGuardRevision, variant),
			RepoID: astrLinkGuardRepoID, Revision: astrLinkGuardRevision,
			VariantID: variant, Adapter: plan.item.Adapter,
			LabelMapping: defaultAstrLinkGuardLabelMapping(),
		}, plan.runtime, plan.assets)
		document, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var persisted normalizedManifest
		if err := strictDecodeJSON(document, &persisted); err != nil {
			t.Fatal(err)
		}
		if validateNormalizedManifest(persisted) != nil ||
			!manifestMatchesBuiltinPlan(persisted, plan) {
			t.Fatalf("%s persisted manifest no longer matches its plan", variant)
		}
	}
	mapping := defaultAstrLinkGuardLabelMapping()
	if len(mapping) != len(astrLinkGuardKinds) {
		t.Fatalf("mapping=%#v", mapping)
	}
	for label, kind := range mapping {
		if kind == nil || string(*kind) != label {
			t.Fatalf("label %q maps to %v", label, kind)
		}
	}
}

// v0.1.0 installed its pinned Guard release through the built-in plan. Once
// the pin moves on, that record must still load offline as a Guard release;
// a rejected provenance stops the whole registry from starting.
func TestAstrLinkGuardInstallOfAnEarlierPinStaysReady(t *testing.T) {
	assertEarlierGuardPinStaysReady(t, "49e8b7b83d34fd75a86cf07698bd80972d444ff3", []Asset{
		{Path: "int8/model_int8.onnx", Size: 324_054, SHA256: "6cacc676cf1eea9fccc9654c0587484c181102b5835600ffaf1362e53c2641a9"},
		{Path: "int8/model_int8.onnx_data", Size: 37_046_272, SHA256: "fe5918effd2a04684d071aa4e6bf880963318c4ed6edd6189a1ae439f225c83b"},
		{Path: "int8/config.json", Size: 1_804, SHA256: "2c20003590d642e273c03c4c7c82d9849fa75f64bf29a71ec0a0c9a20ba4a150"},
		{Path: "int8/tokenizer.json", Size: 1_502_881, SHA256: "0cf3b235ef125658015a6c8026a5fe6f28088bf825101a990ec18a10200feef4"},
		{Path: "int8/tokenizer_config.json", Size: 205, SHA256: "3a7cf4d70bb7c0376f18cc6ad23c143853de5b4478084c414d5dbbd35ea5c589"},
		{Path: "LICENSE", Size: 11_357, SHA256: "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"},
	})
}

func assertEarlierGuardPinStaysReady(t *testing.T, revision string, assets []Asset) {
	t.Helper()
	if revision == astrLinkGuardRevision {
		t.Fatal("the fixture must predate the pinned release")
	}
	id := InstallationID(astrLinkGuardRepoID, revision, "cpu_int8")
	catalogID := CatalogAstrLinkGuard
	catalogSource := contract.PrivacyModelCatalogSourceOfficial
	license, installedAt := "Apache-2.0", "2026-10-01T00:00:00Z"
	var total int64
	for _, asset := range assets {
		total += asset.Size
	}
	installation := contract.PrivacyModelInstallation{
		ID: id, Source: contract.PrivacyModelSourceCatalog,
		CatalogID: &catalogID, CatalogSource: &catalogSource,
		Name: "AstrLink Guard", License: &license, Languages: []string{"zh", "en"},
		RepoID: astrLinkGuardRepoID, Revision: revision,
		VariantID: "cpu_int8", VariantName: "CPU INT8", Quantization: "int8",
		Adapter:         contract.PrivacyModelAdapterHFToken,
		Status:          contract.PrivacyModelStatusReady,
		BytesDownloaded: total, BytesTotal: total,
		EstimatedRAMBytes: 268_435_456,
		LabelMapping:      defaultAstrLinkGuardLabelMapping(), InstalledAt: &installedAt,
	}
	manifest := buildNormalizedManifest(installation, astrLinkGuardRuntime("cpu_int8"), assets)
	root := filepath.Join(t.TempDir(), "models")
	directory := filepath.Join(root, "installations", string(id))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeNormalizedManifest(directory, manifest); err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(filepath.Join(directory, InstallationManifestName))
	if err != nil {
		t.Fatal(err)
	}
	binding := manifestBinding(manifest.Identity, document)
	store := openRegistryStore(t)
	if err := store.PutPrivacyModelInstallation(context.Background(), storage.PrivacyModelInstallationRecord{
		Installation: installation, Manifest: &binding,
	}); err != nil {
		t.Fatal(err)
	}
	hub := newFakeGuardHub(t)
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        root,
		MetadataBaseURL:      hub.server.URL,
		HTTPClient:           hub.server.Client(),
		Store:                store,
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatalf("restart rejected the earlier pin: %v", err)
	}
	if _, ready := registry.ReadyInstallation(id); !ready || hub.requests.Load() != 0 {
		t.Fatalf("earlier pin ready=%t requests=%d", ready, hub.requests.Load())
	}
}

func TestAstrLinkGuardProbeNeedsNoLabelMappingOrNetwork(t *testing.T) {
	hub := newFakeGuardHub(t)
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        filepath.Join(t.TempDir(), "models"),
		MetadataBaseURL:      hub.server.URL,
		HTTPClient:           hub.server.Client(),
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
		RepoID: astrLinkGuardRepoID, Revision: astrLinkGuardRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.RequiresLabelMapping || response.Revision != astrLinkGuardRevision ||
		len(response.Labels) != len(astrLinkGuardKinds) || hub.requests.Load() != 0 {
		t.Fatalf("probe=%#v requests=%d", response, hub.requests.Load())
	}
	for _, label := range response.Labels {
		if label.SuggestedKind == nil || string(*label.SuggestedKind) != label.Label {
			t.Fatalf("label=%#v", label)
		}
	}
	latest, err := registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
		RepoID: astrLinkGuardRepoID, Revision: "main",
	})
	if err != nil || latest.Revision != astrLinkGuardRevision {
		t.Fatalf("main without newer tags=%#v err=%v", latest, err)
	}
}

func TestAstrLinkGuardReleaseSkipsIncompatibleTagsAndInstallsUpdate(t *testing.T) {
	update := fakeGuardRelease{
		tag: "v0.2.0", sha: guardUpdateSHA,
		files: guardReleaseFiles(t, guardConfigDocument(t, nil), "update"),
	}
	future := fakeGuardRelease{
		tag: "v0.10.0", sha: guardFutureSHA,
		files: guardReleaseFiles(t, guardConfigDocument(t, func(config map[string]any) {
			config[astrLinkGuardDecoderContractField] = "bio-offset-consistency-v2"
		}), "future"),
	}
	hub := newFakeGuardHub(t, update, future)
	store := openRegistryStore(t)
	root := filepath.Join(t.TempDir(), "models")
	config := RegistryConfig{
		RootDirectory:        root,
		MetadataBaseURL:      hub.server.URL,
		HTTPClient:           hub.server.Client(),
		Store:                store,
		TestOnlyLoopbackMode: true,
	}
	registry, err := NewRegistry(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}

	releases, err := registry.CatalogReleases(context.Background(), false)
	if err != nil || len(releases.Items) != 1 {
		t.Fatalf("releases=%#v err=%v", releases, err)
	}
	latest := releases.Items[0]
	if latest.ID != CatalogAstrLinkGuard || latest.Revision != guardUpdateSHA ||
		latest.Version == nil || *latest.Version != "0.2.0" || !latest.Recommended {
		t.Fatalf("latest release=%#v", latest)
	}
	var int8Total int64
	for path, document := range update.files {
		if path == astrLinkGuardLicense ||
			(strings.HasPrefix(path, "int8/") &&
				!strings.HasSuffix(path, releaseChecksumsName) &&
				!strings.HasSuffix(path, "label_mapping.json")) {
			int8Total += int64(len(document))
		}
	}
	if latest.Variants[0].ID != "cpu_int8" || latest.Variants[0].BytesTotal != int8Total {
		t.Fatalf("int8 total=%d want=%d", latest.Variants[0].BytesTotal, int8Total)
	}
	checked := hub.requests.Load()
	if _, err := registry.CatalogReleases(context.Background(), false); err != nil ||
		hub.requests.Load() != checked {
		t.Fatalf("release check was not cached: err=%v requests=%d->%d",
			err, checked, hub.requests.Load())
	}
	if _, err := registry.CatalogReleases(context.Background(), true); err != nil ||
		hub.requests.Load() == checked {
		t.Fatalf("refresh reused the cached release check: err=%v requests=%d",
			err, hub.requests.Load())
	}

	for revision, want := range map[string]error{
		guardFutureSHA:  ErrUnsupportedModel,
		guardUnknownSHA: ErrUnsupportedModel,
		"refs/pr/1":     ErrUnsupportedModel,
	} {
		_, err := registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
			RepoID: astrLinkGuardRepoID, Revision: revision,
		})
		if !errors.Is(err, want) {
			t.Fatalf("probe %s error=%v", revision, err)
		}
	}

	if counted := hub.downloadCounts.Load(); counted != 0 {
		t.Fatalf("release checks counted %d downloads", counted)
	}

	// A fresh registry resolves the release by commit, as the desktop sends it.
	registry, err = NewRegistry(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "cpu_int8",
	})
	if err != nil {
		t.Fatal(err)
	}
	ready := waitForInstallation(t, registry, started.ID)
	if ready.Status != contract.PrivacyModelStatusReady ||
		ready.Source != contract.PrivacyModelSourceCatalog ||
		ready.CatalogID == nil || *ready.CatalogID != CatalogAstrLinkGuard ||
		ready.BytesTotal != int8Total ||
		len(ready.LabelMapping) != len(astrLinkGuardKinds) {
		t.Fatalf("installed release=%#v", ready)
	}
	if counted := hub.downloadCounts.Load(); counted != 1 {
		t.Fatalf("install counted %d downloads, want 1", counted)
	}

	installed := hub.requests.Load()
	restarted, err := NewRegistry(context.Background(), config)
	if err != nil {
		t.Fatalf("restart rejected release provenance: %v", err)
	}
	if reloaded, err := restarted.GetInstallation(ready.ID); err != nil ||
		reloaded.Status != contract.PrivacyModelStatusReady ||
		hub.requests.Load() != installed {
		t.Fatalf("reloaded=%#v err=%v requests=%d->%d",
			reloaded, err, installed, hub.requests.Load())
	}

	custom := cloneInstallation(ready)
	custom.Source = contract.PrivacyModelSourceCustom
	custom.CatalogID, custom.CatalogSource = nil, nil
	renamed := cloneInstallation(ready)
	renamed.Name = "Guard lookalike"
	if validInstallationProvenance(custom) || validInstallationProvenance(renamed) {
		t.Fatal("release provenance accepted a tampered installation")
	}
}

func TestAstrLinkGuardReleaseRejectsChecksumsThatDisagreeWithHub(t *testing.T) {
	tampered := fakeGuardRelease{
		tag: "v0.2.0", sha: guardUpdateSHA,
		files:  guardReleaseFiles(t, guardConfigDocument(t, nil), "tampered"),
		lfsSHA: map[string]string{"int8/model_int8.onnx": strings.Repeat("0", 64)},
	}
	hub := newFakeGuardHub(t, tampered)
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        filepath.Join(t.TempDir(), "models"),
		MetadataBaseURL:      hub.server.URL,
		HTTPClient:           hub.server.Client(),
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CatalogReleases(context.Background(), false); !errors.Is(err, ErrRemoteMetadata) {
		t.Fatalf("tampered release error=%v", err)
	}
	if _, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "cpu_int8",
	}); !errors.Is(err, ErrRemoteMetadata) {
		t.Fatalf("tampered install error=%v", err)
	}
}
