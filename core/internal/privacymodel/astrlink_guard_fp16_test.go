package privacymodel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

type guardPlatform struct {
	name     string
	coreML   coreMLSupport
	directML bool
	// accelerated are the variants offered next to the CPU ones.
	accelerated []string
	reason      *string
}

func guardPlatforms() []guardPlatform {
	requiresMacOS26 := unsupportedReasonRequiresMacOS26
	return []guardPlatform{
		{name: "macOS 26 on Apple silicon", coreML: coreMLSupported, accelerated: []string{"coreml_fp16_gpu", "coreml_fp16_ane"}},
		{name: "macOS 15 on Apple silicon", coreML: coreMLRequiresMacOS26, accelerated: []string{"coreml_fp16_gpu", "coreml_fp16_ane"}, reason: &requiresMacOS26},
		{name: "Windows x64 build 19041", directML: true, accelerated: []string{"directml_fp16_gpu"}},
		{name: "Windows x64 before build 19041"},
		{name: "Linux"},
	}
}

func (platform guardPlatform) apply(t *testing.T) {
	t.Helper()
	previousCoreML, previousDirectML := localCoreMLSupport, localDirectMLSupport
	localCoreMLSupport = func() coreMLSupport { return platform.coreML }
	localDirectMLSupport = func() bool { return platform.directML }
	t.Cleanup(func() { localCoreMLSupport, localDirectMLSupport = previousCoreML, previousDirectML })
}

// guardFP16Tokenizer pads with <pad>, as Guard's own tokenizer does. Its
// digest is not the pinned one, so a release check reads the pad id.
func guardFP16Tokenizer(t *testing.T, padID int) []byte {
	t.Helper()
	document, err := json.Marshal(map[string]any{
		"version": "1.0",
		"added_tokens": []map[string]any{
			{"id": 0, "content": "<s>", "special": true},
			{"id": padID, "content": "<pad>", "special": true},
		},
		"model": map[string]any{"type": "Unigram"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

// addGuardFP16Folder adds fp16/ as the v0.1.3 upload lays it out: a single
// static-shape model without external data, next to Guard's metadata files.
func addGuardFP16Folder(t *testing.T, files map[string][]byte, config, tokenizer []byte) {
	t.Helper()
	folder := map[string][]byte{
		astrLinkGuardFP16Model:   onnxModel(staticFP16Inputs(512)...),
		"config.json":            config,
		"tokenizer.json":         tokenizer,
		"tokenizer_config.json":  []byte(`{"tokenizer_class":"XLMRobertaTokenizer"}`),
		"labels.json":            []byte(`["O"]`),
		"label_mapping.json":     []byte(`{"schema":"astrlink-guard-labels"}`),
		InstallationManifestName: []byte(`{"version":1}`),
	}
	for name, document := range folder {
		files[astrLinkGuardFP16Folder+"/"+name] = document
	}
	writeGuardChecksums(t, files, astrLinkGuardFP16Folder)
}

// writeGuardChecksums rewrites a folder's CHECKSUMS.json from its files.
func writeGuardChecksums(t *testing.T, files map[string][]byte, folder string) {
	t.Helper()
	checksums := make(map[string]releaseChecksum)
	for path, document := range files {
		name, inFolder := strings.CutPrefix(path, folder+"/")
		if inFolder && name != releaseChecksumsName {
			checksums[name] = releaseChecksum{SHA256: sha256Hex(document), Size: int64(len(document))}
		}
	}
	document, err := json.Marshal(checksums)
	if err != nil {
		t.Fatal(err)
	}
	files[folder+"/"+releaseChecksumsName] = document
}

func guardFP16Release(t *testing.T) fakeGuardRelease {
	t.Helper()
	config := guardConfigDocument(t, nil)
	files := guardReleaseFiles(t, config, "fp16")
	addGuardFP16Folder(t, files, config, guardFP16Tokenizer(t, 1))
	return fakeGuardRelease{tag: "v0.2.0", sha: guardUpdateSHA, files: files}
}

func newGuardTestRegistry(t *testing.T, hub *fakeGuardHub, root string) *Registry {
	t.Helper()
	registry, err := NewRegistry(context.Background(), RegistryConfig{
		RootDirectory:        root,
		MetadataBaseURL:      hub.server.URL,
		HTTPClient:           hub.server.Client(),
		TestOnlyLoopbackMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func assertGuardVariants(
	t *testing.T,
	platform guardPlatform,
	variants []contract.PrivacyModelVariant,
	fp16Bytes int64,
) {
	t.Helper()
	assertVariantIDs(t, variants, append([]string{"cpu_int8", "cpu_fp32"}, platform.accelerated...)...)
	if !variants[0].Recommended || variants[0].Quantization != "int8" {
		t.Fatalf("INT8 is no longer the recommended variant: %#v", variants[0])
	}
	for _, variant := range variants[2:] {
		choice, _ := astrLinkGuardAccelerated(variant.ID)
		if variant.Recommended || variant.Quantization != "f16" ||
			variant.Name != choice.name || variant.BytesTotal != fp16Bytes ||
			variant.EstimatedRAMBytes != acceleratedEstimatedRAM(astrLinkGuardFP16Bytes, choice.ramMultiplier) ||
			variant.Supported != (platform.reason == nil) ||
			!optionalStringsEqual(variant.UnsupportedReason, platform.reason) {
			t.Fatalf("accelerated variant=%#v", variant)
		}
	}
}

func TestAstrLinkGuardOffersFP16VariantsForEachPlatform(t *testing.T) {
	for _, platform := range guardPlatforms() {
		t.Run(platform.name, func(t *testing.T) {
			platform.apply(t)
			release := guardFP16Release(t)
			hub := newFakeGuardHub(t, release)
			registry := newGuardTestRegistry(t, hub, filepath.Join(t.TempDir(), "models"))
			releases, err := registry.CatalogReleases(context.Background(), false)
			if err != nil || releases.Items[0].Revision != guardUpdateSHA {
				t.Fatalf("releases=%#v err=%v", releases, err)
			}
			var fp16Bytes int64
			for _, path := range astrLinkGuardFolderFiles("coreml_fp16_gpu") {
				fp16Bytes += int64(len(release.files[astrLinkGuardFP16Folder+"/"+path]))
			}
			fp16Bytes += int64(len(release.files[astrLinkGuardLicense]))
			assertGuardVariants(t, platform, releases.Items[0].Variants, fp16Bytes)
			// The probe validates the variants as the desktop receives them.
			probed, err := registry.Probe(context.Background(), contract.PrivacyModelProbeRequest{
				RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA,
			})
			if err != nil {
				t.Fatalf("probe err=%v", err)
			}
			assertGuardVariants(t, platform, probed.Variants, fp16Bytes)
			if _, fetched := hub.fetched.Load(astrLinkGuardFP16Folder + "/" + releaseChecksumsName); fetched !=
				(len(platform.accelerated) > 0) {
				t.Fatalf("fp16/ checked=%t with accelerated=%v", fetched, platform.accelerated)
			}

			// Only the variants this system can run install.
			for _, choice := range allAcceleratedChoices() {
				_, err := registry.prepareInstallation(context.Background(), contract.PrivacyModelInstallRequest{
					RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: choice.id,
				})
				runs := platform.reason == nil && strings.Contains(strings.Join(platform.accelerated, ","), choice.id)
				if runs != (err == nil) || (err != nil && !errors.Is(err, ErrUnsupportedModel)) {
					t.Fatalf("%s err=%v", choice.id, err)
				}
			}
		})
	}
}

func astrLinkGuardVariantName(variant string) string {
	choice, _ := astrLinkGuardAccelerated(variant)
	return choice.name
}

func TestAstrLinkGuardReleaseWithoutFP16ResolvesAsBefore(t *testing.T) {
	guardPlatforms()[0].apply(t)
	release := fakeGuardRelease{
		tag: "v0.1.3", sha: guardUpdateSHA,
		files: guardReleaseFiles(t, guardConfigDocument(t, nil), "cpu only"),
	}
	hub := newFakeGuardHub(t, release)
	registry := newGuardTestRegistry(t, hub, filepath.Join(t.TempDir(), "models"))
	releases, err := registry.CatalogReleases(context.Background(), false)
	if err != nil || releases.Items[0].Revision != guardUpdateSHA {
		t.Fatalf("releases=%#v err=%v", releases, err)
	}
	assertVariantIDs(t, releases.Items[0].Variants, "cpu_int8", "cpu_fp32")
	if _, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "coreml_fp16_gpu",
	}); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("FP16 install from a release without fp16/ err=%v", err)
	}
}

func TestAstrLinkGuardBrokenFP16DropsOnlyTheAcceleratedVariants(t *testing.T) {
	guardPlatforms()[0].apply(t)
	model := astrLinkGuardFP16Folder + "/" + astrLinkGuardFP16Model
	config := astrLinkGuardFP16Folder + "/config.json"
	cases := map[string]func(release *fakeGuardRelease){
		"unreadable checksums": func(release *fakeGuardRelease) {
			release.files[astrLinkGuardFP16Folder+"/"+releaseChecksumsName] = []byte("not json")
		},
		"model missing": func(release *fakeGuardRelease) {
			delete(release.files, model)
			writeGuardChecksums(t, release.files, astrLinkGuardFP16Folder)
		},
		"Hub digest disagrees with the checksums": func(release *fakeGuardRelease) {
			release.lfsSHA = map[string]string{model: strings.Repeat("0", 64)}
		},
		"config differs from its checksum": func(release *fakeGuardRelease) {
			release.files[config] = []byte(strings.Replace(string(release.files[config]), `"bert"`, `"treb"`, 1))
		},
		"config contract unknown": func(release *fakeGuardRelease) {
			release.files[config] = guardConfigDocument(t, func(document map[string]any) {
				document[astrLinkGuardDecoderContractField] = "bio-offset-consistency-v2"
			})
			writeGuardChecksums(t, release.files, astrLinkGuardFP16Folder)
		},
		"tokenizer without a pad token": func(release *fakeGuardRelease) {
			release.files[astrLinkGuardFP16Folder+"/tokenizer.json"] = []byte(`{"version":"1.0","model":{"type":"Unigram"}}`)
			writeGuardChecksums(t, release.files, astrLinkGuardFP16Folder)
		},
		"tokenizer differs from its checksum": func(release *fakeGuardRelease) {
			tokenizer := astrLinkGuardFP16Folder + "/tokenizer.json"
			release.files[tokenizer] = []byte(strings.Replace(string(release.files[tokenizer]), "Unigram", "Unigraf", 1))
		},
	}
	for name, breakFP16 := range cases {
		t.Run(name, func(t *testing.T) {
			release := guardFP16Release(t)
			breakFP16(&release)
			hub := newFakeGuardHub(t, release)
			registry := newGuardTestRegistry(t, hub, filepath.Join(t.TempDir(), "models"))
			releases, err := registry.CatalogReleases(context.Background(), false)
			if err != nil || releases.Items[0].Revision != guardUpdateSHA {
				t.Fatalf("releases=%#v err=%v", releases, err)
			}
			assertVariantIDs(t, releases.Items[0].Variants, "cpu_int8", "cpu_fp32")
			if _, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
				RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "coreml_fp16_gpu",
			}); !errors.Is(err, ErrUnsupportedModel) {
				t.Fatalf("FP16 install from a broken fp16/ err=%v", err)
			}
			if _, err := registry.prepareInstallation(context.Background(), contract.PrivacyModelInstallRequest{
				RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "cpu_int8",
			}); err != nil {
				t.Fatalf("INT8 of the same release err=%v", err)
			}
		})
	}

	// An unreachable Hub is no verdict on fp16/: the release check fails and
	// is retried, instead of remembering the release without its FP16 build.
	release := guardFP16Release(t)
	release.unavailable = map[string]bool{astrLinkGuardFP16Folder + "/" + releaseChecksumsName: true}
	hub := newFakeGuardHub(t, release)
	registry := newGuardTestRegistry(t, hub, filepath.Join(t.TempDir(), "models"))
	if _, err := registry.CatalogReleases(context.Background(), false); !errors.Is(err, ErrRemoteMetadata) {
		t.Fatalf("unreachable fp16/ err=%v", err)
	}
}

func TestAstrLinkGuardInstallsFP16FromARelease(t *testing.T) {
	for _, platform := range guardPlatforms() {
		if platform.reason != nil || len(platform.accelerated) == 0 {
			continue
		}
		t.Run(platform.name, func(t *testing.T) {
			platform.apply(t)
			release := guardFP16Release(t)
			// A release with another tokenizer records its own pad id.
			release.files[astrLinkGuardFP16Folder+"/tokenizer.json"] = guardFP16Tokenizer(t, 3)
			writeGuardChecksums(t, release.files, astrLinkGuardFP16Folder)
			hub := newFakeGuardHub(t, release)
			store := openRegistryStore(t)
			config := RegistryConfig{
				RootDirectory:        filepath.Join(t.TempDir(), "models"),
				MetadataBaseURL:      hub.server.URL,
				HTTPClient:           hub.server.Client(),
				Store:                store,
				TestOnlyLoopbackMode: true,
			}
			registry, err := NewRegistry(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			variant := platform.accelerated[0]
			started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
				RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: variant,
			})
			if err != nil {
				t.Fatal(err)
			}
			ready := waitForInstallation(t, registry, started.ID)
			if ready.Status != contract.PrivacyModelStatusReady ||
				ready.VariantName != astrLinkGuardVariantName(variant) || ready.Quantization != "f16" {
				t.Fatalf("installed=%#v", ready)
			}
			if _, fetched := hub.fetched.Load(astrLinkGuardFP16Folder + "/" + astrLinkGuardFP16Model + "_data"); fetched {
				t.Fatal("the FP16 install asked for external data")
			}
			document, err := os.ReadFile(filepath.Join(
				registry.installationDirectory(ready.ID), InstallationManifestName,
			))
			if err != nil {
				t.Fatal(err)
			}
			var manifest normalizedManifest
			if err := strictDecodeJSON(document, &manifest); err != nil {
				t.Fatal(err)
			}
			paths := make([]string, len(manifest.Files))
			for index, file := range manifest.Files {
				paths[index] = file.Path
			}
			if strings.Join(paths, ",") != "fp16/model_fp16_static512.onnx,fp16/config.json,fp16/tokenizer.json,fp16/tokenizer_config.json,LICENSE" ||
				len(manifest.ExternalData) != 0 || manifest.Execution == nil ||
				manifest.Execution.PadTokenID != 3 || manifest.Execution.StaticSequenceLength != 512 {
				t.Fatalf("manifest=%s", document)
			}
			want, _ := astrLinkGuardAccelerated(variant)
			if manifest.Execution.Provider != want.execution.Provider {
				t.Fatalf("execution=%#v", manifest.Execution)
			}

			// It loads offline on any system, even one that cannot run it.
			guardPlatforms()[4].apply(t)
			requests := hub.requests.Load()
			restarted, err := NewRegistry(context.Background(), config)
			if err != nil {
				t.Fatalf("restart rejected the FP16 release install: %v", err)
			}
			if _, ready := restarted.ReadyInstallation(started.ID); !ready || hub.requests.Load() != requests {
				t.Fatalf("restarted ready=%t requests=%d->%d", ready, requests, hub.requests.Load())
			}
		})
	}
}

// A release can only be checked for a static input shape once its model is
// downloaded; one that does not have it never becomes ready.
func TestAstrLinkGuardRejectsAnFP16ReleaseWithoutStaticInputs(t *testing.T) {
	guardPlatforms()[0].apply(t)
	release := guardFP16Release(t)
	release.files[astrLinkGuardFP16Folder+"/"+astrLinkGuardFP16Model] = onnxModel(batchInputs(-1, 512)...)
	writeGuardChecksums(t, release.files, astrLinkGuardFP16Folder)
	hub := newFakeGuardHub(t, release)
	registry := newGuardTestRegistry(t, hub, filepath.Join(t.TempDir(), "models"))
	started, err := registry.Install(context.Background(), contract.PrivacyModelInstallRequest{
		RepoID: astrLinkGuardRepoID, Revision: guardUpdateSHA, VariantID: "coreml_fp16_gpu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed := waitForInstallation(t, registry, started.ID); failed.Status == contract.PrivacyModelStatusReady {
		t.Fatalf("dynamic FP16 model became ready: %#v", failed)
	}
}
