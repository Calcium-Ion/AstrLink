//go:build !linux || !(amd64 || arm64)

package sigstack

// Only Linux's web view installs handlers without SA_ONSTACK.
const supported = false

func fix() {}

func holdChildHandler() error { return nil }
