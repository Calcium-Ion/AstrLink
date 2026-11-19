package plugincli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// built is a project built and checked: what the build produced and what
// the plugin declares.
type built struct {
	result  *pluginbuild.Result
	checked *plugin.Checked
}

// buildProject builds the project in dir and checks the plugin as the
// gateway would load it, its files in an install directory of their own.
// A task plugin of new-api's is not built: its plugin.js is checked as it
// is, as the gateway installs it. It reports problems on stderr.
func buildProject(env Environment, dir string) (*built, bool) {
	if text, ok := plugin.ReadTaskSource(dir); ok {
		checked, err := plugin.Check(plugin.Source{File: "plugin.js", Text: text, Dir: dir})
		if err != nil {
			reportCheckError(env, err)
			return nil, false
		}
		return &built{result: &pluginbuild.Result{Plugin: []byte(text)}, checked: checked}, true
	}
	result, err := pluginbuild.Build(dir)
	if err != nil {
		reportBuildError(env, err)
		return nil, false
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(env.Stderr, "warning: %s\n", warning)
	}
	install, err := os.MkdirTemp("", "astrlink-plugin-build-")
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, false
	}
	defer os.RemoveAll(install)
	if _, err := result.Write(install); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return nil, false
	}
	checked, err := plugin.Check(plugin.Source{File: "plugin.js", Text: string(result.Plugin), SourceMap: string(result.SourceMap), Dir: install})
	if err != nil {
		reportCheckError(env, err)
		return nil, false
	}
	return &built{result: result, checked: checked}, true
}

// reportCheckError writes why the plugin does not load, at its line when
// the error has one.
func reportCheckError(env Environment, err error) {
	var loadErr *plugin.LoadError
	if errors.As(err, &loadErr) && loadErr.Line > 0 {
		fmt.Fprintf(env.Stderr, "error: %s:%d:%d: %s\n", loadErr.File, loadErr.Line, loadErr.Column, loadErr.Message)
	} else {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
	}
}

func reportBuildError(env Environment, err error) {
	var failure *pluginbuild.Error
	if errors.As(err, &failure) {
		for _, message := range failure.Messages {
			fmt.Fprintf(env.Stderr, "error: %s\n", message)
		}
		return
	}
	fmt.Fprintf(env.Stderr, "error: %v\n", err)
}

// writeOutputs writes a build's files into dir and lists them.
func writeOutputs(dir string, result *pluginbuild.Result) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	sums, err := result.Write(dir)
	if err != nil {
		return nil, err
	}
	written := slices.Sorted(maps.Keys(sums))
	return written, nil
}

func runBuild(_ context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	out := flags.String("out", "", "where to write the built files (default: <dir>/dist)")
	positional, ok := parse(env, "build", flags, args, 1)
	if !ok {
		return 2
	}
	dir := projectDir(positional)
	project, ok := buildProject(env, dir)
	if !ok {
		return 1
	}
	target := *out
	if target == "" {
		target = filepath.Join(dir, "dist")
	}
	written, err := writeOutputs(target, project.result)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: write the build: %v\n", err)
		return 1
	}
	printJSON(env.Stdout, map[string]any{"key": project.checked.Meta.Key, "out": target, "files": written})
	return 0
}

// checkReport is what astrlink plugin check prints.
type checkReport struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	APIVersion   int      `json:"api_version"`
	Capabilities []string `json:"capabilities"`
	Hooks        []string `json:"hooks"`
	Interface    bool     `json:"interface"`
	PluginBytes  int      `json:"plugin_bytes"`
	ClientBytes  int      `json:"client_bytes,omitempty"`
	Engines      string   `json:"engines,omitempty"`
	// Compatible says whether this AstrLink is in the range the project's
	// engines.astrlink names; absent when it names none or this AstrLink
	// is a development build.
	Compatible *bool    `json:"compatible,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

func runCheck(_ context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	positional, ok := parse(env, "check", flags, args, 1)
	if !ok {
		return 2
	}
	project, ok := buildProject(env, projectDir(positional))
	if !ok {
		return 1
	}
	meta := project.checked.Meta
	report := checkReport{
		Key: meta.Key, Name: meta.Name, Version: meta.Version, APIVersion: meta.APIVersion,
		Capabilities: meta.Capabilities, Hooks: project.checked.Hooks,
		Interface: project.result.Client != nil, PluginBytes: len(project.result.Plugin), ClientBytes: len(project.result.Client),
		Engines: project.result.Manifest.EngineRange(),
	}
	for _, warning := range project.result.Warnings {
		report.Warnings = append(report.Warnings, warning.String())
	}
	if manifest := project.result.Manifest; manifest != nil && manifest.Version != "" && manifest.Version != meta.Version {
		report.Warnings = append(report.Warnings, fmt.Sprintf("package.json says version %s, meta.version says %s", manifest.Version, meta.Version))
	}
	code := 0
	if report.Engines != "" {
		if current, ok := pluginbuild.ParseVersion(buildinfo.Version); ok {
			compatible, err := pluginbuild.Satisfies(current, report.Engines)
			if err != nil {
				fmt.Fprintf(env.Stderr, "error: engines.astrlink %q: %v\n", report.Engines, err)
				return 1
			}
			report.Compatible = &compatible
			if !compatible {
				fmt.Fprintf(env.Stderr, "error: this AstrLink is %s, outside engines.astrlink %q\n", current, report.Engines)
				code = 1
			}
		}
	}
	printJSON(env.Stdout, report)
	return code
}
