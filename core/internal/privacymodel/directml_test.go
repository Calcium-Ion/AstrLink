package privacymodel

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestDirectMLVariantsRequireWindowsX64Build19041(t *testing.T) {
	for _, platform := range []struct {
		goos, goarch string
		build        uint32
		want         bool
	}{
		{"windows", "amd64", 19041, true},
		{"windows", "amd64", 19045, true},
		{"windows", "amd64", 26100, true},
		{"windows", "amd64", 18363, false},
		{"windows", "amd64", 0, false},
		{"windows", "arm64", 26100, false},
		{"windows", "386", 26100, false},
		{"linux", "amd64", 26100, false},
		{"darwin", "arm64", 26100, false},
	} {
		if got := directMLPlatformSupport(platform.goos, platform.goarch, platform.build); got != platform.want {
			t.Fatalf("directMLPlatformSupport(%s, %s, %d)=%v, want %v",
				platform.goos, platform.goarch, platform.build, got, platform.want)
		}
	}
}

func TestWindowsBuildNumberIsReadOnWindows(t *testing.T) {
	build := windowsBuildNumber()
	if runtime.GOOS != "windows" {
		if build != 0 {
			t.Fatalf("windowsBuildNumber off Windows=%d", build)
		}
		return
	}
	// Windows 10 1507 is build 10240; Go supports nothing older.
	if build < 10240 {
		t.Fatalf("windowsBuildNumber=%d", build)
	}
}

func TestLocalProbeOffersDirectMLVariantForStaticFP16Models(t *testing.T) {
	source := writeLocalFP16Fixture(t, staticFP16Inputs(512), true)
	root := filepath.Join(t.TempDir(), "registry")
	store := openRegistryStore(t)

	setLocalDirectMLSupport(t, false)
	registry := newLocalTestRegistry(t, root, store)
	cpuOnly, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal without DirectML: %v", err)
	}
	assertVariantIDs(t, cpuOnly.Variants, "gpu_f16", "cpu_int8")

	setLocalDirectMLSupport(t, true)
	probe, err := registry.ProbeLocal(
		context.Background(),
		contract.PrivacyModelLocalProbeRequest{Path: source},
	)
	if err != nil {
		t.Fatalf("ProbeLocal with DirectML: %v", err)
	}
	if probe.RepoID != cpuOnly.RepoID || probe.Revision != cpuOnly.Revision {
		t.Fatalf("DirectML changed local identity: %s@%s vs %s@%s",
			probe.RepoID, probe.Revision, cpuOnly.RepoID, cpuOnly.Revision)
	}
	assertVariantIDs(t, probe.Variants, "directml_fp16_gpu", "cpu_int8")
	variant := probe.Variants[0]
	if !variant.Supported || variant.UnsupportedReason != nil || variant.Recommended ||
		variant.Name != "DirectML FP16 · GPU" || variant.Quantization != "f16" ||
		variant.BytesTotal != cpuOnly.Variants[0].BytesTotal ||
		variant.EstimatedRAMBytes != acceleratedEstimatedRAM(variant.BytesTotal, directMLVariantChoices[0].ramMultiplier) {
		t.Fatalf("DirectML variant=%#v", variant)
	}
	if probe.Variants[1] != cpuOnly.Variants[1] || !probe.Variants[1].Recommended {
		t.Fatalf("INT8 variant changed: %#v vs %#v", probe.Variants[1], cpuOnly.Variants[1])
	}
	if err := contract.ValidatePrivacyModelProbeResponse(probe); err != nil {
		t.Fatalf("probe response is invalid: %v", err)
	}

	started, err := registry.Install(
		context.Background(),
		contract.PrivacyModelInstallRequest{
			RepoID: probe.RepoID, Revision: probe.Revision,
			VariantID: "directml_fp16_gpu", LabelMapping: emailMapping(),
		},
	)
	if err != nil {
		t.Fatalf("Install DirectML: %v", err)
	}
	if ready := waitForInstallation(t, registry, started.ID); ready.Status != contract.PrivacyModelStatusReady ||
		ready.VariantName != "DirectML FP16 · GPU" {
		t.Fatalf("DirectML installation=%#v", ready)
	}
	installed, exists := registry.ReadyInstallation(started.ID)
	if !exists {
		t.Fatal("DirectML installation is not ready")
	}
	document, err := os.ReadFile(filepath.Join(installed.Directory, InstallationManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if want := `,"execution":{"provider":"directml","model_format":null,"compute_units":null,"static_sequence_length":512,"pad_token_id":1}}`; !bytes.HasSuffix(document, []byte(want)) {
		t.Fatalf("DirectML manifest=%s", document)
	}
	var manifest normalizedManifest
	if err := strictDecodeJSON(document, &manifest); err != nil ||
		manifest.ModelPath != "model_fp16_static512.onnx" ||
		len(manifest.ExternalData) != 0 || len(manifest.Files) != 3 {
		t.Fatalf("DirectML manifest=%#v err=%v", manifest, err)
	}

	// Installed DirectML models load from the registry as they are, even on
	// a system that no longer offers the variant.
	setLocalDirectMLSupport(t, false)
	restarted := newLocalTestRegistry(t, root, store)
	if _, ok := restarted.ReadyInstallation(started.ID); !ok {
		t.Fatal("DirectML installation is not ready after restart")
	}
}

// setLocalDirectMLSupport also turns Core ML off, so the DirectML tests see
// the same variants on every platform.
func setLocalDirectMLSupport(t *testing.T, supported bool) {
	t.Helper()
	previous := localDirectMLSupport
	localDirectMLSupport = func() bool { return supported }
	t.Cleanup(func() { localDirectMLSupport = previous })
	previousCoreML := localCoreMLSupport
	localCoreMLSupport = func() coreMLSupport { return coreMLUnavailable }
	t.Cleanup(func() { localCoreMLSupport = previousCoreML })
}
