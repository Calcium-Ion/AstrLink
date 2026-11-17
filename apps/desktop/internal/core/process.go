package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type processEventKind int

const (
	processStdout processEventKind = iota
	processError
	processTerminated
)

// processEvent is one thing a Core process did. The monitor consumes them
// in order; the last is processError or processTerminated.
type processEvent struct {
	kind processEventKind
	// text is the stdout line, the error, or how the process ended.
	text string
}

// child is a running Core process.
type child interface {
	Pid() int
	// Kill ends the process at once.
	Kill() error
}

// spawnSpec is what one Core start runs.
type spawnSpec struct {
	binary string
	args   []string
	env    []string
	stdin  []byte
}

// spawnFunc starts Core and returns its events. The stdin payload is
// delivered before it returns; a delivery failure is reported with the
// child, which the caller then kills.
type spawnFunc func(spawnSpec) (child, <-chan processEvent, error)

// errStdin wraps a failure to deliver the stdin payload.
type errStdin struct{ err error }

func (e errStdin) Error() string { return e.err.Error() }

type execChild struct {
	cmd   *exec.Cmd
	stdin io.Closer
}

func (c *execChild) Pid() int    { return c.cmd.Process.Pid }
func (c *execChild) Kill() error { return c.cmd.Process.Kill() }

// spawnProcess starts Core with os/exec. Stdout lines become events;
// stderr is copied to the desktop's stderr. Stdin stays open, as Tauri's
// sidecar kept it, until the process ends.
func spawnProcess(spec spawnSpec) (child, <-chan processEvent, error) {
	cmd := exec.Command(spec.binary, spec.args...)
	cmd.Env = append(os.Environ(), spec.env...)
	configureCommand(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	// Plain pipes rather than StdoutPipe: Wait then returns when Core
	// exits, even while a worker it started still holds the write ends.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, nil, err
	}
	cmd.Stdout, cmd.Stderr = stdoutWrite, stderrWrite
	err = cmd.Start()
	stdoutWrite.Close()
	stderrWrite.Close()
	if err != nil {
		stdoutRead.Close()
		stderrRead.Close()
		return nil, nil, err
	}
	process := &execChild{cmd: cmd, stdin: stdin}
	events := make(chan processEvent, 16)
	var readers sync.WaitGroup
	readers.Go(func() {
		defer stdoutRead.Close()
		scanner := bufio.NewScanner(stdoutRead)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			events <- processEvent{kind: processStdout, text: strings.TrimSpace(scanner.Text())}
		}
	})
	go func() {
		defer stderrRead.Close()
		scanner := bufio.NewScanner(stderrRead)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(scanner.Text()))
		}
	}()
	go func() {
		err := cmd.Wait()
		stdin.Close()
		// A ready line printed just before exiting is still read first.
		readers.Wait()
		if cmd.ProcessState == nil {
			events <- processEvent{kind: processError, text: fmt.Sprintf("sidecar event error: %v", err)}
		} else {
			events <- processEvent{kind: processTerminated, text: describeExit(cmd.ProcessState)}
		}
		close(events)
	}()
	if _, err := stdin.Write(spec.stdin); err != nil {
		return process, events, errStdin{err}
	}
	return process, events, nil
}
