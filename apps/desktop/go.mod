module github.com/QuantumNous/astrlink/desktop

go 1.27.3

// The frontend's build and dependencies are not Go packages of this module;
// keep ./... (and mygo vet) out of them.
ignore (
	./dist
	./node_modules
)

require (
	github.com/QuantumNous/astrlink/core v0.0.0
	github.com/ebitengine/purego v0.11.1
	github.com/egoist/mygo v0.9.0
	github.com/goccy/go-yaml v1.19.2
	github.com/godbus/dbus/v5 v5.2.4
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
	golang.org/x/sys v0.48.0
)

require (
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// core and convo are developed in this repository. core's own replace of
// convo does not apply to a module that requires core, so it is repeated.
replace (
	github.com/QuantumNous/astrlink/convo => ../../convo
	github.com/QuantumNous/astrlink/core => ../../core
)
