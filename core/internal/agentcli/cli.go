package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Run executes one CLI invocation and returns the process exit code. Results
// are JSON on stdout; errors and progress go to stderr.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	name := args[0]
	switch name {
	case "help", "-h", "-help", "--help":
		if len(args) > 1 {
			found, ok := findCommand(args[1])
			if !ok {
				_, _ = fmt.Fprintf(stderr, "astrlink: unknown command %q\n", args[1])
				return 2
			}
			printCommandHelp(stdout, found)
			return 0
		}
		printUsage(stdout)
		return 0
	}
	found, ok := findCommand(name)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "astrlink: unknown command %q\n\n", name)
		printUsage(stderr)
		return 2
	}

	flags := flag.NewFlagSet("astrlink "+found.Name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options DialOptions
	flags.StringVar(&options.Socket, "socket", "", "local control socket path")
	flags.StringVar(&options.ControlURL, "control-url", "", "loopback control URL (tests and Windows fallback)")
	flags.StringVar(&options.ControlToken, "control-token", "", "control token used only with --control-url")
	flags.StringVar(&options.SessionPath, "session", "", "path to control-session.json")
	pretty := flags.Bool("pretty", false, "indent the JSON output")
	values := map[string]func() any{}
	for _, spec := range found.Flags {
		switch spec.Kind {
		case flagInt:
			value := flags.Int(spec.Name, 0, spec.Usage)
			values[spec.Name] = func() any { return *value }
		case flagBool:
			value := flags.Bool(spec.Name, false, spec.Usage)
			values[spec.Name] = func() any { return *value }
		case flagDuration:
			value := flags.Duration(spec.Name, 0, spec.Usage)
			values[spec.Name] = func() any { return *value }
		default:
			value := flags.String(spec.Name, "", spec.Usage)
			values[spec.Name] = func() any { return *value }
		}
	}
	positional, err := parseInterspersed(flags, args[1:])
	if errors.Is(err, flag.ErrHelp) {
		printCommandHelp(stdout, found)
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "astrlink %s: %v\nRun \"astrlink help %s\" for usage.\n", found.Name, err, found.Name)
		return 2
	}
	wantArgs := 0
	if found.Arg != "" {
		wantArgs = 1
	}
	if len(positional) != wantArgs {
		_, _ = fmt.Fprintf(stderr, "astrlink %s: usage: %s\n", found.Name, commandUsage(found))
		return 2
	}

	arguments := map[string]any{}
	if found.Arg != "" {
		arguments[found.ArgKey] = positional[0]
	}
	keys := map[string]string{}
	for _, spec := range found.Flags {
		keys[spec.Name] = spec.Key
	}
	// Only flags given on the command line become arguments, so an absent
	// filter stays absent rather than turning into a zero value.
	flags.Visit(func(set *flag.Flag) {
		if key, ok := keys[set.Name]; ok {
			arguments[key] = values[set.Name]()
		}
	})

	client, err := Dial(options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "astrlink: %v\n", err)
		return 1
	}
	client.Agent = detectAgent(os.Getenv)
	client.Progress = stderr
	payload, err := found.Call(ctx, client, arguments)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "astrlink: %v\n", err)
		return 1
	}
	if err := printJSON(stdout, payload, *pretty || isTerminal(stdout)); err != nil {
		_, _ = fmt.Fprintf(stderr, "astrlink: %v\n", err)
		return 1
	}
	return 0
}

// parseInterspersed lets flags follow the positional argument, as in
// `astrlink raw-audit <id> --reason ...`.
func parseInterspersed(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		rest := flags.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		// flag.Parse consumed a "--" terminator: the rest is positional.
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// detectAgent names the agent host from the environment it sets for the
// shell commands it runs; an explicit --agent flag takes precedence.
func detectAgent(getenv func(string) string) string {
	switch {
	case getenv("CLAUDECODE") != "":
		return "Claude Code"
	case getenv("CODEX_SANDBOX") != "":
		return "Codex"
	}
	return ""
}

func printJSON(output io.Writer, payload json.RawMessage, indent bool) error {
	if indent {
		var buffer strings.Builder
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return err
		}
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(decoded); err != nil {
			return err
		}
		_, err := io.WriteString(output, buffer.String())
		return err
	}
	_, err := fmt.Fprintf(output, "%s\n", bytes.TrimSpace(payload))
	return err
}

func isTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func commandUsage(found command) string {
	usage := "astrlink " + found.Name
	if found.Arg != "" {
		usage += " <" + found.Arg + ">"
	}
	if len(found.Flags) > 0 {
		usage += " [flags]"
	}
	return usage
}

func printUsage(output io.Writer) {
	var builder strings.Builder
	builder.WriteString("Usage: astrlink <command> [arguments] [flags]\n\n")
	builder.WriteString("Read-only access to the local AstrLink gateway's request records, for debugging.\n")
	builder.WriteString("Results are JSON on stdout. The AstrLink desktop must be running.\n\nCommands:\n")
	for _, found := range commandCatalog() {
		label := found.Name
		if found.Arg != "" {
			label += " <" + found.Arg + ">"
		}
		fmt.Fprintf(&builder, "  %-20s %s\n", label, found.Summary)
	}
	builder.WriteString("\nRun \"astrlink help <command>\" for a command's details and flags.\n")
	builder.WriteString("Run \"astrlink plugin help\" for the tools that write, build and test plugins.\n")
	_, _ = io.WriteString(output, builder.String())
}

func printCommandHelp(output io.Writer, found command) {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Usage: %s\n\n%s\n", commandUsage(found), found.Detail)
	if len(found.Flags) > 0 {
		builder.WriteString("\nFlags:\n")
		for _, spec := range found.Flags {
			label := "--" + spec.Name
			switch spec.Kind {
			case flagInt:
				label += " <n>"
			case flagDuration:
				label += " <duration>"
			case flagString:
				label += " <value>"
			}
			fmt.Fprintf(&builder, "  %-26s %s\n", label, spec.Usage)
		}
	}
	builder.WriteString("\nCommon flags:\n  --pretty                   indent the JSON output\n")
	_, _ = io.WriteString(output, builder.String())
}
