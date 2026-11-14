package clientupdates

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// commandSpec is a program and its literal arguments; no shell parses
// them.
type commandSpec struct {
	program string
	args    []string
}

func command(program string, args ...string) commandSpec {
	return commandSpec{program: program, args: append([]string{}, args...)}
}

type envVar struct{ name, value string }

// Environment is where clients are checked and updated: this computer, or
// a WSL distribution, where home, path and every client path are Linux
// paths.
type Environment struct {
	home           string
	path           string
	useSystemProxy bool
	// proxyEnv is the system proxy for command-line installers, nil when
	// they keep their own.
	proxyEnv []envVar
	wsl      *wslTarget
}

var proxyVariables = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"ALL_PROXY",
	"http_proxy",
	"https_proxy",
	"all_proxy",
	"NO_PROXY",
	"no_proxy",
	"npm_config_proxy",
	"npm_config_https_proxy",
	"npm_config_noproxy",
}

// changes is what every client command changes in its environment:
// variables to remove, then variables to set. PATH comes from the
// environment itself.
func changes(env *Environment) (removed []string, set []envVar) {
	if !env.useSystemProxy || env.proxyEnv != nil {
		removed = proxyVariables
	}
	set = []envVar{
		{"NO_COLOR", "1"},
		{"CI", "1"},
		{"DISABLE_AUTOUPDATER", "1"},
		{"HOMEBREW_NO_INSTALL_CLEANUP", "1"},
	}
	if !env.useSystemProxy {
		set = append(set,
			envVar{"NO_PROXY", "*"},
			envVar{"no_proxy", "*"},
			envVar{"npm_config_proxy", ""},
			envVar{"npm_config_https_proxy", ""},
		)
	} else {
		set = append(set, env.proxyEnv...)
	}
	return removed, set
}

// outputLimit bounds what is kept of each output stream: its end.
const outputLimit = 16_384

type tail struct{ data []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if len(t.data) > outputLimit {
		t.data = append(t.data[:0], t.data[len(t.data)-outputLimit:]...)
	}
	return len(p), nil
}

// windowsImage reports whether path holds a PE image. CreateProcess treats
// any other .exe as a 16-bit DOS program, which 64-bit Windows refuses
// with a modal "Unsupported 16-Bit Application" dialog on top of the
// error. Claude Code's npm package keeps a text stub at bin/claude.exe
// when its postinstall did not run or its platform package was not
// downloaded.
func windowsImage(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	header := make([]byte, 64)
	if n, err := io.ReadFull(file, header); n < 64 {
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return false, err
		}
		return false, nil
	}
	if !bytes.HasPrefix(header, []byte("MZ")) {
		return false, nil
	}
	offset := binary.LittleEndian.Uint32(header[60:64])
	signature := make([]byte, 4)
	n, err := file.ReadAt(signature, int64(offset))
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return n == 4 && string(signature) == "PE\x00\x00", nil
}

// sameName compares environment variable names as the system does.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// childEnvironment is this process's environment with names removed and
// variables set.
func childEnvironment(removed []string, set []envVar) []string {
	var out []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "" {
			// Windows keeps per-drive directories as =C:=C:\....
			out = append(out, entry)
			continue
		}
		dropped := false
		for _, other := range removed {
			if sameName(name, other) {
				dropped = true
				break
			}
		}
		for _, variable := range set {
			if sameName(name, variable.name) {
				dropped = true
				break
			}
		}
		if !dropped {
			out = append(out, entry)
		}
	}
	for _, variable := range set {
		out = append(out, variable.name+"="+variable.value)
	}
	return out
}

// run starts spec with stdin closed, waits up to deadline, and returns its
// output. Every command runs in its own process group (a job object on
// Windows), which is killed when it ends, so a hung npm child cannot
// outlive a timed-out update.
func run(env *Environment, spec commandSpec, deadline time.Duration) (string, *clientError) {
	if env.wsl != nil {
		spec = wslCommand(env, env.wsl, spec)
	}
	if failure := checkProgram(spec.program); failure != nil {
		return "", failure
	}
	cmd := exec.Command(spec.program, spec.args...)
	if env.wsl != nil {
		// wsl.exe writes its own messages in UTF-16 otherwise. The command
		// it runs gets its environment from `env`.
		cmd.Env = childEnvironment(nil, []envVar{{"WSL_UTF8", "1"}})
	} else {
		removed, set := changes(env)
		cmd.Dir = env.home
		cmd.Env = childEnvironment(removed, append([]envVar{{"PATH", env.path}}, set...))
	}
	configure(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", newError("command", err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", newError("command", err.Error())
	}
	if err := cmd.Start(); err != nil {
		return "", newError("command", err.Error())
	}
	kill, err := contain(cmd.Process)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		stdout.Close()
		stderr.Close()
		return "", newError("command", err.Error())
	}
	var out, errOut tail
	var readers sync.WaitGroup
	readers.Add(2)
	for _, stream := range []struct {
		from io.ReadCloser
		to   *tail
	}{{stdout, &out}, {stderr, &errOut}} {
		go func() {
			defer readers.Done()
			_, _ = io.Copy(stream.to, stream.from)
			stream.from.Close()
		}()
	}
	type exit struct {
		state *os.ProcessState
		err   error
	}
	exited := make(chan exit, 1)
	go func() {
		state, err := cmd.Process.Wait()
		exited <- exit{state, err}
	}()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	var result exit
	timedOut := false
	select {
	case result = <-exited:
	case <-timer.C:
		timedOut = true
	}
	// The process tree goes either way: a successful installer may leave
	// a child behind.
	kill()
	_ = cmd.Process.Kill()
	if timedOut {
		<-exited
	}
	readers.Wait()
	if timedOut {
		return "", newError("timeout", "Command timed out")
	}
	if result.err != nil {
		return "", newError("command", result.err.Error())
	}
	stdoutText := strings.ToValidUTF8(string(out.data), "\uFFFD")
	stderrText := strings.ToValidUTF8(string(errOut.data), "\uFFFD")
	if !result.state.Success() {
		return "", newError("command", strings.TrimSpace(stderrText+"\n"+stdoutText))
	}
	// Some upstream installers use pipelines whose final process exits
	// zero even when the download failed. Keep stderr for the check after
	// the update.
	if strings.TrimSpace(stderrText) == "" {
		return stdoutText, nil
	}
	return stdoutText + "\n" + stderrText, nil
}

// clientError is a failure the About page shows: a code it translates and
// the end of the details.
type clientError struct {
	code   string
	detail string
}

func (e *clientError) Error() string { return e.detail }

var (
	networkMarkers = []string{
		"curl: (5)", "curl: (6)", "curl: (7)", "curl: (28)", "curl: (35)", "curl: (60)",
		"ECONNREFUSED", "ECONNRESET", "ENOTFOUND", "ETIMEDOUT",
	}
	permissionMarkers = []string{
		"eacces", "eperm", "permission denied", "operation not permitted", "access is denied",
	}
)

// detailLimit is how many characters of the details are kept: their end.
const detailLimit = 4096

func newError(code, detail string) *clientError {
	if code == "command" || code == "unchanged" {
		lower := strings.ToLower(detail)
		switch {
		// A successful final shell process can mask curl failures in a
		// pipeline.
		case containsAny(detail, networkMarkers):
			code = "network"
		case containsAny(lower, permissionMarkers):
			code = "permission"
		}
	}
	if runes := []rune(detail); len(runes) > detailLimit {
		detail = string(runes[len(runes)-detailLimit:])
	}
	return &clientError{code: code, detail: detail}
}

func newErrorf(code, format string, args ...any) *clientError {
	return newError(code, fmt.Sprintf(format, args...))
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
