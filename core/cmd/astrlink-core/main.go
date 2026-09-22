package main

import (
	"bufio"
	"context"
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
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/parentwatch"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
	"github.com/QuantumNous/astrlink/core/internal/privacyworker"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func main() {
	config := coreapp.DefaultConfig(buildinfo.Version, buildinfo.Commit)
	parentPID := 0
	dataDirectory := ""
	privacyWorkerPath := ""
	classifierWorkerPath := ""
	controlTokenStdin := false
	outboundProxy := "environment"
	maxConcurrentInspections := ingress.DefaultMaxConcurrentInspections
	var maxRequestBodyMiB uint64
	responseStartTimeoutSeconds := ingress.DefaultResponseStartTimeoutSeconds
	flag.StringVar(&config.InferenceListen, "inference-listen", config.InferenceListen, "loopback inference listen address")
	flag.BoolVar(&config.InferencePortFallback, "inference-port-fallback", false, "use an ephemeral loopback port when the inference port is occupied")
	flag.StringVar(&config.ControlListen, "control-listen", config.ControlListen, "loopback control listen address")
	flag.IntVar(&parentPID, "parent-pid", 0, "optional desktop parent PID to watch on Unix")
	flag.StringVar(&dataDirectory, "data-dir", "", "optional persistent application data directory")
	flag.StringVar(&privacyWorkerPath, "privacy-worker", "", "optional bundled privacy worker executable")
	flag.StringVar(&classifierWorkerPath, "classifier-worker", "", "optional bundled classifier worker executable")
	flag.BoolVar(&controlTokenStdin, "control-token-stdin", false, "read the per-start control token from stdin")
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
	proxy, err := networkproxy.New(outboundProxy)
	if err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	// Configure once, before any clients or transport clones are created.
	// OAuth, subscriptions, discovery, downloads and inference share this policy.
	outboundTransport := http.DefaultTransport.(*http.Transport).Clone()
	outboundTransport.Proxy = proxy
	http.DefaultTransport = outboundTransport

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
	var closeStore func() error
	if dataDirectory != "" || controlTokenStdin {
		if dataDirectory == "" || !controlTokenStdin {
			logger.Printf("persistent mode requires both --data-dir and --control-token-stdin")
			os.Exit(2)
		}
		controlToken, err := readControlToken(os.Stdin)
		if err != nil {
			logger.Printf("read local control token: %v", err)
			os.Exit(2)
		}
		store, err := sqlite.Open(ctx, filepath.Join(dataDirectory, "astrlink.db"))
		if err != nil {
			logger.Printf("open persistent store: %v", err)
			os.Exit(1)
		}
		if runtime.GOOS != "windows" {
			config.ControlSocketPath = filepath.Join(dataDirectory, "control.sock")
		}
		closeStore = store.Close
		if recovered, recoverErr := store.RecoverPendingRequestRecords(ctx); recoverErr != nil {
			logger.Printf("recover interrupted request records: %v", recoverErr)
		} else if recovered > 0 {
			logger.Printf("recovered %d interrupted request record(s)", recovered)
		}
		accessTokenManager, err := accesstoken.NewManager(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent access tokens: %v", err)
			os.Exit(1)
		}
		privacyModel, err := privacymodel.NewRegistry(ctx, privacymodel.RegistryConfig{
			RootDirectory: filepath.Join(dataDirectory, "privacy-model"),
			Store:         store,
			Logf:          logger.Printf,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy model: %v", err)
			os.Exit(1)
		}
		if privacyWorkerPath == "" {
			privacyWorkerPath, err = privacyworker.SiblingExecutablePath()
			if err != nil {
				_ = store.Close()
				logger.Printf("locate local privacy worker: %v", err)
				os.Exit(1)
			}
		}
		privacyWorker, err := privacyworker.New(privacyworker.Config{
			ExecutablePath: privacyWorkerPath,
			Model:          privacyModel,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy worker: %v", err)
			os.Exit(1)
		}
		defer privacyWorker.Close()
		policyRecord, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
		if err != nil {
			_ = store.Close()
			logger.Printf("load local privacy policy: %v", err)
			os.Exit(1)
		}
		privacyWorker.ApplyPolicy(policyRecord.Policy)
		policyProvider, err := privacy.NewStorePolicyProvider(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy policy: %v", err)
			os.Exit(1)
		}
		privacyFilter, err := privacy.New(policyProvider, privacyWorker)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy filter: %v", err)
			os.Exit(1)
		}
		conversionEngine := relaykitbridge.NewEngine()
		subscriptionManager, err := newSubscriptionManager(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure subscription manager: %v", err)
			os.Exit(1)
		}
		pricingManager := pricing.NewManager(store, nil)
		subscriptionManager.SetUsageObservers(
			func(ctx context.Context, account contract.SubscriptionAccount, usage contract.SubscriptionUsage) error {
				current, err := store.GetService(ctx, account.ID)
				if err != nil {
					return err
				}
				if current.Service.Subscription == nil || current.Service.Subscription.ProviderAccountID != account.ProviderAccountID {
					return nil
				}
				return store.ObserveSubscriptionUsage(ctx, current.Service, usage)
			},
			func(ctx context.Context, account contract.SubscriptionAccount) error {
				current, err := store.GetService(ctx, account.ID)
				if err != nil {
					return err
				}
				if current.Service.Subscription == nil || current.Service.Subscription.ProviderAccountID != account.ProviderAccountID {
					return nil
				}
				return store.ObserveSubscriptionReset(ctx, current.Service)
			},
		)
		resolver, err := endpoint.NewStoreResolver(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent endpoint resolver: %v", err)
			os.Exit(1)
		}
		resolver.WithRuntimeProfile(contract.RuntimeProfile{RelayKitAvailable: true, Edges: conversionEngine.Edges()})
		resolver.WithSubscriptionBaseURL(subscriptionManager.APIBaseURL())
		handler, err := controlapi.NewWithDependencies(config.Version, controlapi.Dependencies{
			ServiceStore: store,
			PricingStore: store, PricingManager: pricingManager,
			RecoveryResolver:   resolver,
			RouteStore:         store,
			AccessTokenManager: accessTokenManager,
			PolicyStore:        store,
			PrivacyModels:      privacyModel,
			PrivacyFilter:      privacyFilter,
			PolicyChanged:      privacyWorker.ApplyPolicy,
			RequestRecords:     store,
			AuditSettings:      store,
			AuditKeys:          store,
			AuditBlobs:         store,
			Subscriptions:      subscriptionManager,
			CodingPlans:        codingplan.New(store, nil),
			ServiceModels:      servicemodel.New(store, subscriptionManager, nil),
			ControlToken:       controlToken,
			ConversionEngine:   conversionEngine,
			Shutdown:           stopSignals,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent control API: %v", err)
			os.Exit(1)
		}
		dependencies.ControlHandler = handler
		dependencies.RetentionSweep = func(ctx context.Context) error {
			_, err := store.SweepExpiredAuditData(ctx)
			return err
		}
		dependencies.NewInferenceHandler = func(address string) (http.Handler, error) {
			return ingress.NewProduction(ingress.Dependencies{
				Resolver:   resolver,
				Authorizer: endpoint.NewServiceAuthorizer(store, subscriptionManager),
				AccessTokenAuthenticator: ingress.AccessTokenAuthenticatorFunc(
					func(ctx context.Context, raw string) (contract.AccessTokenID, error) {
						return accessTokenManager.Authenticate(ctx, raw)
					},
				),
				PrivacyFilter: privacyFilter,
				PolicyWarningReporter: ingress.PolicyWarningReporterFunc(
					func(protocol contract.ProtocolID, endpointID contract.ServiceID, summary string) {
						logger.Printf(
							"privacy policy warning: protocol=%s service_id=%s findings=%s",
							protocol,
							endpointID,
							summary,
						)
					},
				),
				RequestRecords:           store,
				AuditSettings:            store,
				AuditBlobs:               store,
				RecordLogger:             logger.Printf,
				AllowedHost:              address,
				ConversionEngine:         conversionEngine,
				MaxConcurrentInspections: maxConcurrentInspections,
				MaxRequestBodyMiB:        uint32(maxRequestBodyMiB),
				ResponseStartTimeout:     time.Duration(responseStartTimeoutSeconds) * time.Second,
			})
		}
		monitorCtx, stopMonitors := context.WithCancel(ctx)
		var monitors sync.WaitGroup
		monitors.Add(2)
		go func() { defer monitors.Done(); pricingManager.Run(monitorCtx, logger.Printf) }()
		go func() { defer monitors.Done(); subscriptionManager.RunUsageMonitor(monitorCtx) }()
		closeStore = func() error {
			stopMonitors()
			monitors.Wait()
			return store.Close()
		}
	}
	if closeStore != nil {
		defer closeStore()
	}

	if err := coreapp.RunWithDependencies(ctx, config, os.Stdout, dependencies); err != nil {
		logger.Printf("%v", err)
		os.Exit(1)
	}
}

func readControlToken(input io.Reader) (string, error) {
	if input == nil {
		return "", fmt.Errorf("stdin is unavailable")
	}
	reader := bufio.NewReader(io.LimitReader(input, 258))
	control, err := readTokenLine(reader)
	if err != nil {
		return "", fmt.Errorf("control token: %w", err)
	}
	return control, nil
}

func readTokenLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("token line is incomplete")
	}
	token := strings.TrimSuffix(line, "\n")
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

func newSubscriptionManager(store *sqlite.Store) (*subscription.Manager, error) {
	oauth := accountauth.OAuthConfig{
		ClientID: accountauth.DefaultCodexOAuthClientID,
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
	return subscription.NewManager(
		subscription.StorageAccountStore{Store: store},
		accountauth.NewKeyringCredentialStore(),
		oauth,
		grok,
	)
}
