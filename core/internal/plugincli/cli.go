// Package plugincli is astrlink plugin: it starts, builds, checks and tests
// plugin projects on this computer, and keeps one built into the running
// gateway while its author works on it. Builds use the bundler compiled into
// the command, the same one the gateway installs plugins with, so a project
// needs no Node to build.
package plugincli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Gateway is the running gateway's control API, which astrlink plugin dev
// reads.
type Gateway interface {
	Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error)
}

// Environment is what a command runs with.
type Environment struct {
	Stdout, Stderr io.Writer
	// Dial reaches the running gateway; only dev needs it.
	Dial func() (Gateway, error)
}

type command struct {
	name    string
	usage   string
	summary string
	run     func(ctx context.Context, env Environment, args []string) int
}

func commands() []command {
	return []command{
		{"new", "astrlink plugin new <key> [--template <name>] [--dir <parent>]", "Start a plugin project from a template.", runNew},
		{"build", "astrlink plugin build [dir] [--out <dir>]", "Build a project into plugin.js, its source map and client.js.", runBuild},
		{"check", "astrlink plugin check [dir]", "Build a project and check it as the gateway would load it.", runCheck},
		{"test", "astrlink plugin test [dir | plugin.js] [--run <text>] [--fixture <file>]", "Run the hook fixtures in the project's tests directory, or in a file.", runTest},
		{"dev", "astrlink plugin dev [dir] [--force]", "Keep the project built into the running gateway and show its logs.", runDev},
	}
}

// Run executes astrlink plugin with args, the words after "plugin", and
// returns the process exit code.
func Run(ctx context.Context, args []string, env Environment) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(env.Stdout)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	for _, found := range commands() {
		if found.name == args[0] {
			return found.run(ctx, env, args[1:])
		}
	}
	fmt.Fprintf(env.Stderr, "astrlink plugin: unknown command %q\n\n", args[0])
	printUsage(env.Stderr)
	return 2
}

func printUsage(output io.Writer) {
	var builder strings.Builder
	builder.WriteString("Usage: astrlink plugin <command> [arguments]\n\nWrite, build and test AstrLink plugins.\n\nCommands:\n")
	for _, found := range commands() {
		fmt.Fprintf(&builder, "  %-7s %s\n          %s\n", found.name, found.summary, found.usage)
	}
	_, _ = io.WriteString(output, builder.String())
}

// parse parses a command's flags, which may follow its arguments, and
// returns the arguments.
func parse(env Environment, name string, flags *flag.FlagSet, args []string, most int) ([]string, bool) {
	flags.SetOutput(io.Discard)
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(env.Stdout, "Usage: %s\n", usageOf(name))
				return nil, false
			}
			fmt.Fprintf(env.Stderr, "astrlink plugin %s: %v\nUsage: %s\n", name, err, usageOf(name))
			return nil, false
		}
		rest := flags.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	if len(positional) > most {
		fmt.Fprintf(env.Stderr, "astrlink plugin %s: too many arguments\nUsage: %s\n", name, usageOf(name))
		return nil, false
	}
	return positional, true
}

func usageOf(name string) string {
	for _, found := range commands() {
		if found.name == name {
			return found.usage
		}
	}
	return "astrlink plugin " + name
}

// projectDir is the project a command works on: its argument, the
// directory of the file it names (a task plugin's plugin.js), else the
// current directory.
func projectDir(positional []string) string {
	if len(positional) == 0 {
		return "."
	}
	if info, err := os.Stat(positional[0]); err == nil && !info.IsDir() {
		return filepath.Dir(positional[0])
	}
	return positional[0]
}

// printJSON writes a command's result on stdout.
func printJSON(output io.Writer, value any) {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}
