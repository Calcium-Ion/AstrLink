//go:build tools

// Package main pins the modules the desktop app is about to use, so that
// go.mod lists them once and `go mod tidy` keeps them until code imports
// them: the comment-preserving config editors (hujson, go-toml, goccy/yaml),
// the Windows and macOS system calls (x/sys, purego) and D-Bus on Linux.
package main

import (
	_ "github.com/ebitengine/purego"
	_ "github.com/goccy/go-yaml"
	_ "github.com/godbus/dbus/v5"
	_ "github.com/pelletier/go-toml/v2"
	_ "github.com/tailscale/hujson"
	_ "golang.org/x/sys/windows"
)
