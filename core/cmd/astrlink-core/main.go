package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/parentwatch"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			os.Exit(runServeCommand(os.Args[2:], os.Stderr))
		case "healthcheck":
			os.Exit(runHealthcheck(os.Args[2:], os.LookupEnv, os.Stderr))
		}
		commandCtx, stopCommand := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code, handled := runOfflineCommand(commandCtx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		stopCommand()
		if handled {
			os.Exit(code)
		}
	}
	config := coreapp.DefaultConfig(buildinfo.Version, buildinfo.Commit)
	parentPID := 0
	dataDirectory := ""
	privacyWorkerPath := ""
	classifierWorkerPath := ""
	controlTokenStdin := false
	observerTokenStdin := false
	localKeyStdin := false
	localKeyFile := ""
	outboundProxy := "environment"
	maxConcurrentInspections := ingress.DefaultMaxConcurrentInspections
	var maxRequestBodyMiB uint64
	responseStartTimeoutSeconds := ingress.DefaultResponseStartTimeoutSeconds
	flag.StringVar(&config.InferenceListen, "inference-listen", config.InferenceListen, "inference listen address: 127.0.0.1:<port> answers this machine only, "+coreapp.NetworkInferenceHost+":<port> answers every interface")
	flag.BoolVar(&config.InferencePortFallback, "inference-port-fallback", false, "use an ephemeral loopback port when the inference port is occupied; ignored with "+coreapp.NetworkInferenceHost)
	flag.StringVar(&config.ControlListen, "control-listen", config.ControlListen, "loopback control listen address")
	flag.IntVar(&parentPID, "parent-pid", 0, "optional desktop parent PID to watch on Unix")
	flag.StringVar(&dataDirectory, "data-dir", "", "optional persistent application data directory")
	flag.StringVar(&privacyWorkerPath, "privacy-worker", "", "optional bundled privacy worker executable")
	flag.StringVar(&classifierWorkerPath, "classifier-worker", "", "optional bundled classifier worker executable")
	flag.BoolVar(&controlTokenStdin, "control-token-stdin", false, "read the per-start control token from stdin")
	flag.BoolVar(&observerTokenStdin, "observer-token-stdin", false, "read the per-start observer token from the third stdin line")
	flag.BoolVar(&localKeyStdin, "kek-stdin", false, "read the local key as 64 hex digits from the second stdin line")
	flag.StringVar(&localKeyFile, "kek-file", "", "local key file path; defaults to $"+localkey.EnvKeyFile+", then <data-dir>/"+localkey.FileName)
	flag.IntVar(&maxConcurrentInspections, "max-concurrent-inspections", maxConcurrentInspections, "maximum requests that may parse and classify at once")
	flag.Uint64Var(&maxRequestBodyMiB, "max-request-body-mib", 0, "maximum inference request body size in MiB; 0 means unlimited")
	flag.IntVar(&responseStartTimeoutSeconds, "response-start-timeout-seconds", responseStartTimeoutSeconds, "seconds to wait for upstream response headers before failing over; 0 waits indefinitely")
	flag.StringVar(&outboundProxy, "outbound-proxy", outboundProxy, "outbound proxy mode: environment, system, or direct")
	flag.CommandLine.SetOutput(os.Stderr)
	flag.Parse()

	logger := log.New(os.Stderr, "astrlink-core: ", log.LstdFlags)
	if err := ingress.ValidateMaxConcurrentInspections(maxConcurrentInspections); err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	if err := ingress.ValidateResponseStartTimeoutSeconds(responseStartTimeoutSeconds); err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	if maxRequestBodyMiB > 1<<32-1 {
		logger.Printf("max-request-body-mib must be at most 4294967295 (0 means unlimited)")
		os.Exit(2)
	}
	if err := installOutboundProxy(outboundProxy); err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, stopParentWatch, err := parentwatch.NotifyContext(signalCtx, parentPID, time.Second)
	if err != nil {
		logger.Printf("parent watchdog: %v", err)
		os.Exit(2)
	}
	defer stopParentWatch()

	dependencies := coreapp.Dependencies{
		InferenceHandler: ingress.NewWithDependencies(ingress.Dependencies{
			MaxRequestBodyMiB: uint32(maxRequestBodyMiB),
		}),
	}
	if (localKeyStdin || localKeyFile != "") && !controlTokenStdin {
		logger.Printf("--kek-stdin and --kek-file require persistent mode")
		os.Exit(2)
	}
	if dataDirectory != "" || controlTokenStdin {
		if dataDirectory == "" || !controlTokenStdin {
			logger.Printf("persistent mode requires both --data-dir and --control-token-stdin")
			os.Exit(2)
		}
		tokens, err := readStdinTokens(os.Stdin, stdinLineCount(observerTokenStdin, localKeyStdin), localKeyStdin)
		if err != nil {
			logger.Printf("read local control tokens: %v", err)
			os.Exit(2)
		}
		if localKeyFile == "" {
			localKeyFile = os.Getenv(localkey.EnvKeyFile)
		}
		core, err := openPersistentCore(ctx, persistentOptions{
			version:                  config.Version,
			dataDirectory:            dataDirectory,
			controlToken:             tokens.control,
			observerToken:            tokens.observer,
			stdinLocalKey:            tokens.localKey,
			localKeyFile:             localKeyFile,
			privacyWorkerPath:        privacyWorkerPath,
			maxConcurrentInspections: maxConcurrentInspections,
			maxRequestBodyMiB:        uint32(maxRequestBodyMiB),
			responseStartTimeout:     time.Duration(responseStartTimeoutSeconds) * time.Second,
			shutdown:                 stopSignals,
			logf:                     logger.Printf,
		})
		if err != nil {
			logger.Printf("%v", err)
			os.Exit(1)
		}
		defer core.close()
		if runtime.GOOS != "windows" {
			config.ControlSocketPath = filepath.Join(dataDirectory, "control.sock")
		}
		dependencies.ControlHandler = core.control
		dependencies.RetentionSweep = core.retentionSweep
		dependencies.NewInferenceHandler = func(address string, networkExposed bool) (http.Handler, error) {
			production := core.gateway
			if networkExposed {
				// Other machines reach this port by any name, so there is no
				// Host gate; access tokens stay mandatory and unauthenticated
				// requests are not recorded. See ingress.NewNetworkProduction.
				return ingress.NewNetworkProduction(production)
			}
			production.AllowedHost = address
			return ingress.NewProduction(production)
		}
	}

	if config.NetworkExposed() {
		logger.Printf("inference plane answers every interface on %s; requests need an access token", config.InferenceListen)
	}
	if err := coreapp.RunWithDependencies(ctx, config, os.Stdout, dependencies); err != nil {
		logger.Printf("%v", err)
		os.Exit(1)
	}
}

// Stdin carries one secret per line, in a fixed order, so a newer desktop and
// an older Core agree on positions: line 1 is the control token, line 2 is
// reserved for the injected local key, and line 3 is the observer token.
const (
	stdinControlLine = iota
	stdinLocalKeyLine
	stdinObserverLine
	maxStdinLines
)

// maxStdinLineBytes bounds one line including its newline.
const maxStdinLineBytes = 129

// rawStatusReader is the part of the raw vault the start-up warning reads.
type rawStatusReader interface {
	Status(context.Context) (controlapi.RawVaultStatus, error)
}

// warnWithoutRawPassword says once at start that raw content is not kept:
// a headless Core has no dialog to ask for the password.
func warnWithoutRawPassword(ctx context.Context, vault rawStatusReader, dataDirectory string, logf func(string, ...any)) {
	status, err := vault.Status(ctx)
	if err != nil {
		logf("read raw sealing state: %v", err)
		return
	}
	if status.PasswordSet {
		return
	}
	logf("WARNING: no raw password is set, so raw request and response content is not recorded "+
		"(shareable content and forwarding are unaffected). Set one while Core is stopped with "+
		"`astrlink-core raw-password set --data-dir %s --password-stdin`, or through POST %s",
		dataDirectory, controlapi.RawPasswordPath)
}

type stdinTokens struct {
	control  string
	observer string
	// localKey is the decoded key from line 2; the caller clears it.
	localKey []byte
}

// stdinLineCount is how many lines the desktop promised via flags. Core never
// reads past them, because the desktop keeps stdin open for the process
// lifetime and an unpromised read would block startup.
func stdinLineCount(observerTokenStdin, localKeyStdin bool) int {
	switch {
	case observerTokenStdin:
		return stdinObserverLine + 1
	case localKeyStdin:
		return stdinLocalKeyLine + 1
	}
	return stdinControlLine + 1
}

// readStdinTokens reads the promised lines. The local key line is required
// when localKeyStdin is set and ignored otherwise.
func readStdinTokens(input io.Reader, lines int, localKeyStdin bool) (tokens stdinTokens, err error) {
	values, buffer, err := readTokenLines(input, lines)
	defer clear(buffer)
	if err != nil {
		return stdinTokens{}, err
	}
	defer func() {
		if err != nil {
			clear(tokens.localKey)
			tokens = stdinTokens{}
		}
	}()
	if tokens.control, err = validateToken(string(values[stdinControlLine])); err != nil {
		return tokens, fmt.Errorf("control token: %w", err)
	}
	if localKeyStdin {
		if lines <= stdinLocalKeyLine {
			return tokens, fmt.Errorf("local key: stdin line %d was not requested", stdinLocalKeyLine+1)
		}
		if tokens.localKey, err = localkey.Parse(values[stdinLocalKeyLine]); err != nil {
			return tokens, fmt.Errorf("local key: %w", err)
		}
	}
	if lines > stdinObserverLine && len(values[stdinObserverLine]) > 0 {
		if tokens.observer, err = validateToken(string(values[stdinObserverLine])); err != nil {
			return tokens, fmt.Errorf("observer token: %w", err)
		}
		if tokens.observer == tokens.control {
			return tokens, fmt.Errorf("observer token must differ from the control token")
		}
	}
	return tokens, nil
}

// readTokenLines reads exactly count newline-terminated lines. Only the first
// line is mandatory; a later line that is empty or absent at end of input is
// returned empty. A non-empty line cut off before its newline is an error so
// a truncated secret is never accepted. It reads one byte at a time into a
// single buffer, so nothing past the promised lines is consumed and no copy
// of a secret outlives the buffer the caller clears.
func readTokenLines(input io.Reader, count int) (values [][]byte, buffer []byte, err error) {
	if input == nil {
		return nil, nil, fmt.Errorf("stdin is unavailable")
	}
	if count < 1 || count > maxStdinLines {
		return nil, nil, fmt.Errorf("stdin line count %d is out of range", count)
	}
	// Each line holds at most maxStdinLineBytes-1 bytes, so appends never
	// reallocate and leave a stray copy behind.
	buffer = make([]byte, 0, count*maxStdinLineBytes)
	defer func() {
		if err != nil {
			clear(buffer)
			buffer = nil
		}
	}()
	values = make([][]byte, count)
	var next [1]byte
	defer clear(next[:])
	for index := range values {
		start := len(buffer)
		complete := false
		var readErr error
		for !complete && readErr == nil {
			var read int
			read, readErr = input.Read(next[:])
			if read == 0 {
				continue
			}
			switch {
			case next[0] == '\n':
				complete = true
			case len(buffer)-start == maxStdinLineBytes-1:
				readErr = errStdinLineTooLong
			default:
				buffer = append(buffer, next[0])
			}
		}
		if !complete {
			if index > 0 && len(buffer) == start && errors.Is(readErr, io.EOF) {
				break
			}
			if index == 0 {
				return nil, nil, fmt.Errorf("control token: token line is incomplete")
			}
			return nil, nil, fmt.Errorf("stdin line %d is incomplete", index+1)
		}
		values[index] = buffer[start:len(buffer):len(buffer)]
	}
	return values, buffer, nil
}

var errStdinLineTooLong = errors.New("stdin line is too long")

func validateToken(token string) (string, error) {
	if len(token) < 32 || len(token) > 128 {
		return "", fmt.Errorf("token must contain 32 to 128 characters")
	}
	for _, character := range token {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_') {
			return "", fmt.Errorf("token must use base64url-safe characters")
		}
	}
	return token, nil
}

func newSubscriptionManager(
	ctx context.Context,
	store *sqlite.Store,
	identities *accountauth.IdentityRegistry,
	noLoopbackCallback bool,
	logf func(string, ...any),
) (*subscription.Manager, error) {
	oauth := accountauth.OAuthConfig{
		ResolveProxy:       networkproxy.Resolver(store, store),
		ClientID:           accountauth.DefaultCodexOAuthClientID,
		Identities:         identities,
		NoLoopbackCallback: noLoopbackCallback,
	}
	if clientID := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_OAUTH_CLIENT_ID")); clientID != "" {
		oauth.ClientID = clientID
	}
	if issuer := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_OAUTH_ISSUER")); issuer != "" {
		oauth.Issuer = issuer
	}
	if apiBase := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_API_BASE_URL")); apiBase != "" {
		oauth.APIBaseURL = apiBase
	}
	grok := accountauth.OAuthConfig{Provider: contract.SubscriptionProviderXAIGrok}
	if issuer := strings.TrimSpace(os.Getenv("ASTRLINK_GROK_OAUTH_ISSUER")); issuer != "" {
		grok.Issuer = issuer
	}
	if apiBase := strings.TrimSpace(os.Getenv("ASTRLINK_GROK_API_BASE_URL")); apiBase != "" {
		grok.APIBaseURL = apiBase
	}
	// OAuth tokens live in the database, sealed under the local key (plan
	// §5.9). Accounts connected by an earlier release move out of the OS
	// keystore here, before any request can read them.
	accounts := subscription.StorageAccountStore{Store: store}
	credentials := subscription.NewStorageAccountCredentialStore(store)
	if moved := credentials.MigrateKeyringCredentials(ctx, accounts, accountauth.NewKeyringCredentialStore(), logf); moved > 0 {
		logf("astrlink subscription: moved %d account credential(s) from the OS keystore into local storage", moved)
	}
	return subscription.NewManager(accounts, credentials, oauth, grok)
}

// subscriptionRiskReporter lets the inference plane pause subscription
// accounts through the manager that owns their persisted state.
type subscriptionRiskReporter struct{ manager *subscription.Manager }

func (reporter subscriptionRiskReporter) ReportSubscriptionRisk(
	ctx context.Context, id contract.ServiceID, observation contract.SubscriptionRiskObservation,
) error {
	return reporter.manager.ReportRisk(ctx, id, observation)
}

func (reporter subscriptionRiskReporter) ClearExpiredSubscriptionRisk(ctx context.Context, id contract.ServiceID) error {
	return reporter.manager.ClearExpiredRisk(ctx, id)
}

func (reporter subscriptionRiskReporter) RefreshRejectedSubscriptionToken(
	ctx context.Context, id contract.ServiceID, rejectedAccessToken string,
) error {
	return reporter.manager.HandleUnauthorized(ctx, id, rejectedAccessToken)
}
