package agentcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// Instance names: the installed AstrLink and the development instance that
// can run beside it.
const (
	instanceDefault = "default"
	instanceDev     = "dev"
)

// probeTimeout bounds how long a liveness check waits for a control
// endpoint to accept a connection.
const probeTimeout = time.Second

// processAlive and dialProbe are replaced by tests.
var (
	processAlive = pidAlive
	dialProbe    = func(network, address string) (net.Conn, error) {
		return net.DialTimeout(network, address, probeTimeout)
	}
)

type instanceStatus int

const (
	// statusAbsent: no readable session file.
	statusAbsent instanceStatus = iota
	// statusStopped: nothing accepts connections at the session's endpoint.
	statusStopped
	// statusUnknown: the endpoint could not be checked (a sandbox may block
	// it) and the process was not seen alive.
	statusUnknown
	statusRunning
)

type instanceState struct {
	name    string
	path    string
	session SessionFile
	loadErr error
	status  instanceStatus
}

func (state instanceState) describe() string {
	switch state.status {
	case statusAbsent:
		return state.name + " has no control session"
	case statusUnknown:
		return state.name + " is not answering"
	default:
		return state.name + " is not running"
	}
}

// instancePaths are the default and dev session files. An explicit session
// path (--session or ASTRLINK_CONTROL_SESSION) stands for the default one;
// the dev file is looked for next to it.
func instancePaths(sessionPath string) (map[string]string, error) {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		var err error
		if path, err = DefaultSessionPath(); err != nil {
			return nil, err
		}
	}
	return map[string]string{instanceDefault: path, instanceDev: devSessionPath(path)}, nil
}

func loadInstance(name, path string) instanceState {
	state := instanceState{name: name, path: path}
	state.session, state.loadErr = LoadSessionFile(path)
	return state
}

func inspectInstance(name, path string) instanceState {
	state := loadInstance(name, path)
	if state.loadErr != nil {
		state.status = statusAbsent
		return state
	}
	state.status = liveness(state.session)
	return state
}

func liveness(session SessionFile) instanceStatus {
	network, address := "unix", strings.TrimSpace(session.ControlSocket)
	if address == "" {
		parsed, err := url.Parse(strings.TrimSpace(session.ControlURL))
		if err != nil || parsed.Host == "" {
			return statusStopped
		}
		network, address = "tcp", parsed.Host
		if parsed.Port() == "" {
			address = net.JoinHostPort(parsed.Hostname(), "80")
		}
	}
	conn, err := dialProbe(network, address)
	if err == nil {
		_ = conn.Close()
		return statusRunning
	}
	if refused(err) || errors.Is(err, fs.ErrNotExist) {
		return statusStopped
	}
	if session.PID > 0 && processAlive(session.PID) {
		return statusRunning
	}
	return statusUnknown
}

func validInstance(name string) bool { return name == instanceDefault || name == instanceDev }

// selection is the instance a command talks to.
type selection struct {
	name    string
	session SessionFile
	// notice is the one stderr line about the choice, if any.
	notice string
}

const sessionUnavailable = "AstrLink control session unavailable (is the desktop gateway running?)"

// selectInstance picks the instance: the named one; otherwise the default
// one when it runs, else the only running one. An instance whose endpoint
// could not be checked is still tried, so that the request reports why it
// fails (a sandbox, for one) instead of claiming nothing runs.
func selectInstance(sessionPath, named string) (selection, error) {
	paths, err := instancePaths(sessionPath)
	if err != nil {
		return selection{}, err
	}
	if named != "" {
		if !validInstance(named) {
			return selection{}, fmt.Errorf("unknown instance %q (use default or dev)", named)
		}
		state := loadInstance(named, paths[named])
		if state.loadErr != nil {
			return selection{}, fmt.Errorf("%s: %s instance: %w", sessionUnavailable, named, state.loadErr)
		}
		return selection{name: named, session: state.session}, nil
	}
	main := inspectInstance(instanceDefault, paths[instanceDefault])
	dev := inspectInstance(instanceDev, paths[instanceDev])
	switch {
	case main.status == statusRunning:
		picked := selection{name: instanceDefault, session: main.session}
		if dev.status == statusRunning {
			picked.notice = "astrlink: the default and dev instances are both running; using default (pass --instance dev for the other)"
		}
		return picked, nil
	case dev.status == statusRunning:
		return selection{name: instanceDev, session: dev.session, notice: "astrlink: the default instance is not running; using the dev instance"}, nil
	case main.status == statusUnknown:
		return selection{name: instanceDefault, session: main.session}, nil
	case dev.status == statusUnknown:
		return selection{name: instanceDev, session: dev.session, notice: "astrlink: the default instance is not running; using the dev instance"}, nil
	}
	return selection{}, fmt.Errorf("%s: looked for the default and dev instances: %s, %s", sessionUnavailable, main.describe(), dev.describe())
}

// explicitEndpoint reports whether the options name the endpoint outright,
// which bypasses the instances.
func explicitEndpoint(options DialOptions) bool {
	return strings.TrimSpace(options.Socket) != "" || strings.TrimSpace(os.Getenv("ASTRLINK_CONTROL_SOCKET")) != "" ||
		strings.TrimSpace(options.ControlURL) != ""
}

type instanceReport struct {
	Name       string  `json:"name"`
	Running    bool    `json:"running"`
	GatewayURL *string `json:"gateway_url"`
}

type instancesReport struct {
	Instances []instanceReport `json:"instances"`
	// Selected is the instance the other commands use with the same flags;
	// null when none is available or --socket / --control-url bypass them.
	Selected *string `json:"selected"`
}

// listInstances reports each instance without its socket, token, or data
// directory.
func listInstances(options DialOptions) (json.RawMessage, error) {
	if options.Instance != "" && !validInstance(options.Instance) {
		return nil, fmt.Errorf("unknown instance %q (use default or dev)", options.Instance)
	}
	paths, err := instancePaths(options.SessionPath)
	if err != nil {
		return nil, err
	}
	report := instancesReport{Instances: []instanceReport{}}
	for _, name := range []string{instanceDefault, instanceDev} {
		state := inspectInstance(name, paths[name])
		entry := instanceReport{Name: name, Running: state.status == statusRunning}
		if gateway := strings.TrimSpace(state.session.GatewayURL); state.loadErr == nil && gateway != "" {
			entry.GatewayURL = &gateway
		}
		report.Instances = append(report.Instances, entry)
	}
	if !explicitEndpoint(options) {
		if picked, err := selectInstance(options.SessionPath, options.Instance); err == nil {
			report.Selected = &picked.name
		}
	}
	return json.Marshal(report)
}
