//go:build !linux || !(amd64 || arm64)

package sigstack

// Only Linux's web view installs handlers without SA_ONSTACK.
const supported = false

func suspendSignal() int { return 0 }

func fix(int) {}

func holdChildHandler() error { return nil }
