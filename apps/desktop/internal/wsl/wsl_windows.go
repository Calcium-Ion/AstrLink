package wsl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	listTimeout = 10 * time.Second
	// homeTimeout covers starting a stopped distribution, which may boot
	// the WSL VM.
	homeTimeout = 60 * time.Second
	// modeTimeout is short: the write through its share started the
	// distribution.
	modeTimeout = 10 * time.Second
)

func settleShareMode(temporary, target string, secret bool) (bool, error) {
	distribution, staged, ok := insideShare(temporary)
	_, replaced, targetOK := insideShare(target)
	if !ok || !targetOK {
		return false, nil
	}
	mode := "600"
	if !secret {
		if _, err := os.Stat(target); err != nil {
			// A new file keeps the distribution's default mode, as it would
			// anywhere.
			return true, nil
		}
		output, err := run([]string{"--distribution", distribution, "--exec", "stat", "-c", "%a", replaced}, modeTimeout)
		if err != nil {
			return false, err
		}
		if mode, ok = fileMode(output); !ok {
			return false, fmt.Errorf("%s reported an unusable mode %q", replaced, output)
		}
	}
	_, err := run([]string{"--distribution", distribution, "--exec", "chmod", mode, staged}, modeTimeout)
	return err == nil, err
}

func distributions() []Distribution {
	all, err := run([]string{"--list", "--quiet"}, listTimeout)
	if err != nil {
		return nil
	}
	names := parseNames(all)
	// wsl.exe exits non-zero when none is running.
	var running []string
	if output, err := run([]string{"--list", "--running", "--quiet"}, listTimeout); err == nil {
		running = parseNames(output)
	}
	found := make([]Distribution, len(names))
	for i, name := range names {
		found[i] = Distribution{Name: name, Running: slices.Contains(running, name)}
	}
	return found
}

func program() (string, error) {
	system := os.Getenv("SystemRoot")
	if system == "" {
		system = `C:\Windows`
	}
	path := filepath.Join(system, "System32", "wsl.exe")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path, nil
	}
	return "", errors.New("WSL is not installed")
}

func defaultHome(distribution string) (string, error) {
	home, err := run([]string{"--distribution", distribution, "--exec", "printenv", "HOME"}, homeTimeout)
	if err != nil {
		return "", err
	}
	share, ok := sharePath(distribution, home)
	if !ok {
		return "", fmt.Errorf("WSL distribution %s reported an unusable home %q", distribution, home)
	}
	return share, nil
}

// capped keeps the first 64 KiB written to it.
type capped struct{ bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := 64*1024 - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

var _ io.Writer = (*capped)(nil)

// run runs wsl.exe without a console window and returns its decoded output.
func run(args []string, timeout time.Duration) (string, error) {
	path, err := program()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	var stdout, stderr capped
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", fmt.Errorf("wsl.exe %s timed out", strings.Join(args, " "))
	}
	out := decode(stdout.Bytes())
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", fmt.Errorf("wsl.exe %s failed: %s", strings.Join(args, " "), strings.TrimSpace(decode(stderr.Bytes())+"\n"+out))
	}
	if err != nil {
		return "", fmt.Errorf("unable to run wsl.exe: %v", err)
	}
	return out, nil
}
