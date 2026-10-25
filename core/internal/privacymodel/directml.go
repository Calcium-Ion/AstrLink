package privacymodel

import "runtime"

// DirectML execution for FP16 ONNX exports with a static 1×window input.
//
// On Windows x64 a local FP16 model is offered as a DirectML variant instead
// of the unsupported gpu_f16 entry. The worker registers the DirectML
// execution provider on the high-performance GPU (CPU after it for any node
// DirectML does not take), pads every window to the static length and drops
// the padded positions from the logits, exactly as for Core ML. INT8 on the
// CPU stays the recommended variant.
const (
	executionProviderDirectML = "directml"

	// ONNX Runtime picks the high-performance GPU through DXCore, which ships
	// with Windows 10 version 2004 (build 19041) and later. Every Windows
	// release still in service is newer; older systems are not offered the
	// variant at all.
	directMLMinimumWindowsBuild = 19041
)

type directMLVariantChoice struct {
	suffix string
	name   string
	// ramMultiplier scales the asset size into the worker's resident
	// memory, as for the Core ML GPU variant. The weights themselves live
	// in GPU memory.
	ramMultiplier int64
}

var directMLVariantChoices = []directMLVariantChoice{
	{suffix: "gpu", name: "DirectML FP16 · GPU", ramMultiplier: 5},
}

// localDirectMLSupport reports whether this build and system can run
// DirectML variants. Tests override it to cover every outcome.
var localDirectMLSupport = func() bool {
	return directMLPlatformSupport(runtime.GOOS, runtime.GOARCH, windowsBuildNumber())
}

// AstrLink ships Windows builds for x64 only.
func directMLPlatformSupport(goos, goarch string, build uint32) bool {
	return goos == "windows" && goarch == "amd64" && build >= directMLMinimumWindowsBuild
}
