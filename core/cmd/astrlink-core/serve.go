package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/console"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// The server edition is configured by these variables only, so a Docker
// deployment needs nothing but its environment. The local key file is
// localkey.EnvKeyFile and the password reset switch console.ResetVariable.
// The console password is the raw password, set on the web page.
const (
	envDataDir       = "ASTRLINK_DATA_DIR"
	envListen        = "ASTRLINK_LISTEN"
	envOutboundProxy = "ASTRLINK_OUTBOUND_PROXY"

	defaultServeDataDir       = "/data"
	defaultServeListen        = "0.0.0.0:8317"
	defaultServeOutboundProxy = "environment"
)

const serveUsage = `usage: astrlink-core serve

Runs AstrLink as a server for browsers and clients on other machines.
It is configured by environment variables only:

  ` + envDataDir + `          data directory (default ` + defaultServeDataDir + `)
  ` + envListen + `            address for the client API and the web console (default ` + defaultServeListen + `)
  ` + localkey.EnvKeyFile + `          local key file (default <data dir>/` + localkey.FileName + `)
  ` + envOutboundProxy + `    environment, system, or direct (default ` + defaultServeOutboundProxy + `)
  ` + console.ResetVariable + `  forgot the password: set a new value and restart to clear
                           it and saved raw content; the same value never resets twice

The web console password is set on the web page within 10 minutes of the
first start; restart to reopen that window.
`

type serveConfig struct {
	dataDirectory string
	listen        string
	outboundProxy string
	localKeyFile  string
	resetPassword string
	// testNetwork is set only by tests; see persistentOptions.
	testNetwork *testNetwork
}

// loadServeConfig reads the environment once.
func loadServeConfig(lookup func(string) (string, bool)) serveConfig {
	value := func(name, fallback string) string {
		if current, ok := lookup(name); ok && current != "" {
			return current
		}
		return fallback
	}
	return serveConfig{
		dataDirectory: value(envDataDir, defaultServeDataDir),
		listen:        value(envListen, defaultServeListen),
		outboundProxy: value(envOutboundProxy, defaultServeOutboundProxy),
		localKeyFile:  value(localkey.EnvKeyFile, ""),
		resetPassword: value(console.ResetVariable, ""),
	}
}

func runServeCommand(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprint(stderr, serveUsage)
		if args[0] == "-h" || args[0] == "-help" || args[0] == "--help" {
			return 0
		}
		return 2
	}
	logger := log.New(stderr, "astrlink-core: ", log.LstdFlags)
	config := loadServeConfig(os.LookupEnv)
	if err := installOutboundProxy(config.outboundProxy); err != nil {
		logger.Printf("%s: %v", envOutboundProxy, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, stop, config, logger.Printf, nil); err != nil {
		logger.Printf("%v", err)
		return 1
	}
	return 0
}

// serve runs the server edition until ctx ends. The data directory lock is
// taken before anything in the directory is opened, so a second instance
// fails without touching the first one's socket or database.
func serve(
	ctx context.Context,
	shutdown context.CancelFunc,
	config serveConfig,
	logf func(string, ...any),
	listening func(net.Addr),
) error {
	if err := os.MkdirAll(config.dataDirectory, 0o700); err != nil {
		return fmt.Errorf("create data directory (%s): %w", envDataDir, err)
	}
	lock, err := coreapp.LockDataDirectory(config.dataDirectory)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Nothing outside this process ever holds the control token: browsers
	// sign in with the raw password and the CLI uses the socket.
	controlToken, err := randomControlToken()
	if err != nil {
		return err
	}
	sessions := console.NewSessions()
	core, err := openPersistentCore(ctx, serveCoreOptions(config, controlToken, sessions, shutdown, logf))
	if err != nil {
		return err
	}
	defer core.close()
	passwordReset, err := consumePasswordReset(ctx, config.dataDirectory, config.resetPassword, core.rawVault, logf)
	if err != nil {
		return err
	}
	inference, err := ingress.NewNetworkProduction(core.gateway)
	if err != nil {
		return fmt.Errorf("configure inference gate: %w", err)
	}
	webConsole, err := console.New(console.Config{
		RawPassword: core.rawVault, Control: core.control, Sessions: sessions, PasswordReset: passwordReset,
	})
	if err != nil {
		return fmt.Errorf("configure web console: %w", err)
	}
	socketPath := ""
	if runtime.GOOS != "windows" {
		socketPath = filepath.Join(config.dataDirectory, "control.sock")
	}
	return coreapp.RunServe(ctx, coreapp.ServeConfig{
		Listen:            config.listen,
		ControlSocketPath: socketPath,
	}, coreapp.ServeDependencies{
		InferenceHandler: inference,
		Console:          webConsole,
		ControlHandler:   core.control,
		RetentionSweep:   core.retentionSweep,
		Listening: func(address net.Addr) {
			logf("listening on %s for the client API and the web console", address)
			if status, err := core.rawVault.Status(ctx); err == nil && !status.PasswordSet {
				logf("no password is set yet: open the web console within %d minutes of start to set it; after that, restart to reopen setup",
					int(console.SetupWindow/time.Minute))
			}
			if listening != nil {
				listening(address)
			}
		},
	})
}

// serveRawBackoffCap bounds the wait after wrong raw passwords. The raw
// password signs in to a console that other machines reach, so guesses wait
// up to 15 minutes instead of the desktop's 30 seconds. The wait is kept in
// memory and a restart clears it.
const serveRawBackoffCap = 15 * time.Minute

// serveCoreOptions configures the persistent Core for the server edition.
func serveCoreOptions(
	config serveConfig,
	controlToken string,
	sessions *console.Sessions,
	shutdown context.CancelFunc,
	logf func(string, ...any),
) persistentOptions {
	return persistentOptions{
		version:                  coreapp.DefaultConfig(buildinfo.Version, buildinfo.Commit).Version,
		dataDirectory:            config.dataDirectory,
		controlToken:             controlToken,
		localKeyFile:             config.localKeyFile,
		maxConcurrentInspections: ingress.DefaultMaxConcurrentInspections,
		noLoopbackCallback:       true,
		consoleSessions:          sessions,
		rawBackoffCap:            serveRawBackoffCap,
		testNetwork:              config.testNetwork,
		shutdown:                 shutdown,
		logf:                     logf,
	}
}

// passwordResetMarker holds the SHA-256 of the last console.ResetVariable
// value acted on, so leaving the switch in the deploy configuration never
// resets twice; a different value resets again.
const passwordResetMarker = "password-reset.sha256"

// passwordClearer is the raw vault's reset for a forgotten password.
type passwordClearer interface {
	ClearPassword(context.Context) (storage.RawResetResult, error)
}

// consumePasswordReset acts on a new reset switch value: the raw password
// and every raw part sealed under it are discarded, so first-run setup opens
// again. Service credentials and settings stay; they are protected by the
// local key, not by the raw password. The value is recorded only after the
// reset, so a failed reset runs again at the next start.
func consumePasswordReset(ctx context.Context, dataDirectory, value string, vault passwordClearer, logf func(string, ...any)) (bool, error) {
	if value == "" {
		return false, nil
	}
	sum := sha256.Sum256([]byte(value))
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(dataDirectory, passwordResetMarker)
	consumed, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", passwordResetMarker, err)
	}
	if strings.TrimSpace(string(consumed)) == digest {
		return false, nil
	}
	result, err := vault.ClearPassword(ctx)
	if err != nil {
		return false, fmt.Errorf("%s: clear the password: %w", console.ResetVariable, err)
	}
	if err := writeFileAtomically(path, []byte(digest+"\n")); err != nil {
		return false, fmt.Errorf("%s: record the value: %w", console.ResetVariable, err)
	}
	logf("%s: cleared the password and discarded %d saved raw part(s); set a new password on the web page, then remove %s",
		console.ResetVariable, result.DeletedParts, console.ResetVariable)
	return true, nil
}

// writeFileAtomically replaces path with content (mode 0600) through a
// synced temporary file and a rename.
func writeFileAtomically(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func randomControlToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate control token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// healthcheckTimeout bounds one probe; Docker's own default is 30 seconds.
const healthcheckTimeout = 5 * time.Second

// runHealthcheck exits 0 when the server edition on this machine answers its
// console status, and 1 otherwise. Docker reserves exit code 2, so usage
// mistakes also exit 1.
func runHealthcheck(args []string, lookup func(string) (string, bool), stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: astrlink-core healthcheck (reads "+envListen+")")
		return 1
	}
	address := defaultServeListen
	if value, ok := lookup(envListen); ok && value != "" {
		address = value
	}
	statusURL, err := healthcheckURL(address)
	if err != nil {
		fmt.Fprintf(stderr, "astrlink-core healthcheck: %s: %v\n", envListen, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		fmt.Fprintf(stderr, "astrlink-core healthcheck: %v\n", err)
		return 1
	}
	// Never through a proxy: the console is on this machine.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		fmt.Fprintf(stderr, "astrlink-core healthcheck: %v\n", err)
		return 1
	}
	defer response.Body.Close()
	var status struct {
		Status string `json:"status"`
	}
	if response.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status) != nil || status.Status == "" {
		fmt.Fprintf(stderr, "astrlink-core healthcheck: %s answered %s\n", statusURL, response.Status)
		return 1
	}
	return 0
}

// healthcheckURL turns a listen address into the console status URL; an
// all-interfaces address is probed on loopback.
func healthcheckURL(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
		if ip != nil && ip.To4() == nil {
			host = "::1"
		}
	}
	return "http://" + net.JoinHostPort(host, port) + console.StatusPath, nil
}
