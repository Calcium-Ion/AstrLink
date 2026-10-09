package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
	"github.com/QuantumNous/astrlink/core/internal/privacyworker"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/servicetest"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

// installOutboundProxy configures the outbound proxy once per process,
// before any clients or transport clones are created. OAuth, subscriptions,
// discovery, downloads and inference share this policy.
func installOutboundProxy(mode string) error {
	proxy, err := networkproxy.New(mode)
	if err != nil {
		return err
	}
	outboundTransport := http.DefaultTransport.(*http.Transport).Clone()
	outboundTransport.Proxy = proxy
	http.DefaultTransport = networkproxy.WrapTransport(outboundTransport)
	return nil
}

// persistentOptions configure the Core shared by the desktop sidecar and the
// server edition.
type persistentOptions struct {
	version       contract.VersionResponse
	dataDirectory string
	controlToken  string
	observerToken string
	// stdinLocalKey is the key the desktop injected, or nil. It is cleared
	// once resolved.
	stdinLocalKey            []byte
	localKeyFile             string
	privacyWorkerPath        string
	maxConcurrentInspections int
	maxRequestBodyMiB        uint32
	responseStartTimeout     time.Duration
	// noLoopbackCallback is set when sign-in browsers run on other machines.
	noLoopbackCallback bool
	// consoleSessions is set by the server edition; see
	// controlapi.ConsoleSessions.
	consoleSessions controlapi.ConsoleSessions
	// rawBackoffCap raises the raw vault's wrong-password backoff cap;
	// zero keeps the desktop's.
	rawBackoffCap time.Duration
	// testNetwork is set only by tests; production leaves it nil.
	testNetwork *testNetwork
	shutdown    context.CancelFunc
	logf        func(string, ...any)
}

// testNetwork keeps a Core started inside a test off the network, also when
// CI sets ASTRLINK_CI_NO_REMOTE_MODELS=1: privacy model metadata comes from
// a loopback stub through the registry's test-only loopback mode, and price
// syncs go through a client that never dials out.
type testNetwork struct {
	privacyModelURL    string
	privacyModelClient *http.Client
	pricingClient      *http.Client
}

// persistentCore is the opened store and everything wired to it.
type persistentCore struct {
	control  *controlapi.Handler
	rawVault *controlapi.Vault
	// gateway configures the inference plane; the caller adds its host gate.
	gateway        ingress.Dependencies
	retentionSweep func(context.Context) error
	// close stops the background monitors, zeroes raw keys, closes the
	// store and then the privacy worker.
	close func() error
}

func openPersistentCore(ctx context.Context, options persistentOptions) (*persistentCore, error) {
	logf := options.logf
	localKey, _, err := localkey.Resolve(localkey.Options{
		StdinKey: options.stdinLocalKey,
		KeyFile:  options.localKeyFile,
		DataDir:  options.dataDirectory,
		Logf:     logf,
	})
	clear(options.stdinLocalKey)
	if err != nil {
		return nil, fmt.Errorf("load local key: %w", err)
	}
	store, err := sqlite.Open(ctx, filepath.Join(options.dataDirectory, "astrlink.db"),
		sqlite.WithLocalKey(localKey), sqlite.WithLogger(logf))
	clear(localKey)
	if err != nil {
		return nil, fmt.Errorf("open persistent store: %w", err)
	}
	var privacyWorker *privacyworker.Client
	fail := func(format string, err error) (*persistentCore, error) {
		if privacyWorker != nil {
			privacyWorker.Close()
		}
		_ = store.Close()
		return nil, fmt.Errorf(format, err)
	}
	if recovered, recoverErr := store.RecoverPendingRequestRecords(ctx); recoverErr != nil {
		logf("recover interrupted request records: %v", recoverErr)
	} else if recovered > 0 {
		logf("recovered %d interrupted request record(s)", recovered)
	}
	rawVault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{Logf: logf, BackoffCap: options.rawBackoffCap})
	// The server edition sets the password on its web page and says so once
	// it listens; the offline command this warning names is the desktop's.
	if options.consoleSessions == nil {
		warnWithoutRawPassword(ctx, rawVault, options.dataDirectory, logf)
	}
	accessTokenManager, err := accesstoken.NewManager(store)
	if err != nil {
		return fail("configure persistent access tokens: %w", err)
	}
	modelConfig := privacymodel.RegistryConfig{
		RootDirectory: filepath.Join(options.dataDirectory, "privacy-model"),
		Store:         store,
		Logf:          logf,
	}
	var pricingClient *http.Client
	if network := options.testNetwork; network != nil {
		modelConfig.MetadataBaseURL = network.privacyModelURL
		modelConfig.HTTPClient = network.privacyModelClient
		modelConfig.TestOnlyLoopbackMode = true
		pricingClient = network.pricingClient
	}
	privacyModel, err := privacymodel.NewRegistry(ctx, modelConfig)
	if err != nil {
		return fail("configure local privacy model: %w", err)
	}
	privacyWorkerPath := options.privacyWorkerPath
	if privacyWorkerPath == "" {
		privacyWorkerPath, err = privacyworker.SiblingExecutablePath()
		if err != nil {
			return fail("locate local privacy worker: %w", err)
		}
	}
	privacyWorker, err = privacyworker.New(privacyworker.Config{
		ExecutablePath: privacyWorkerPath,
		Model:          privacyModel,
	})
	if err != nil {
		return fail("configure local privacy worker: %w", err)
	}
	policyRecord, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
	if err != nil {
		return fail("load local privacy policy: %w", err)
	}
	privacyWorker.ApplyPolicy(policyRecord.Policy)
	policyProvider, err := privacy.NewStorePolicyProvider(store)
	if err != nil {
		return fail("configure local privacy policy: %w", err)
	}
	privacyFilter, err := privacy.New(policyProvider, privacyWorker)
	if err != nil {
		return fail("configure local privacy filter: %w", err)
	}
	conversionEngine := relaykitbridge.NewEngine()
	// One registry serves inference, gateway-initiated requests and
	// learning, so a learned identity applies everywhere at once.
	identities := accountauth.NewIdentityRegistry(store, store)
	if err := identities.Hydrate(ctx); err != nil {
		logf("load learned client identities: %v", err)
	}
	subscriptionManager, err := newSubscriptionManager(ctx, store, identities, options.noLoopbackCallback, logf)
	if err != nil {
		return fail("configure subscription manager: %w", err)
	}
	subscriptionManager.SetRiskEventStore(store)
	pricingManager := pricing.NewManager(store, pricingClient)
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
		return fail("configure persistent endpoint resolver: %w", err)
	}
	resolver.WithRuntimeProfile(contract.RuntimeProfile{RelayKitAvailable: true, Edges: conversionEngine.Edges()})
	resolver.WithSubscriptionBaseURL(subscriptionManager.APIBaseURL())
	gatewayDependencies := ingress.Dependencies{
		ProxyCredentials: store,
		Resolver:         resolver,
		Authorizer: endpoint.NewServiceAuthorizer(store, subscriptionManager, subscriptionManager.Provider().IdentityPolicy()).
			WithRoutingSettings(store).WithIdentities(identities),
		AccessTokenAuthenticator: ingress.AccessTokenAuthenticatorFunc(
			func(ctx context.Context, raw string) (contract.AccessTokenID, error) {
				return accessTokenManager.Authenticate(ctx, raw)
			},
		),
		PrivacyFilter: privacyFilter,
		PolicyWarningReporter: ingress.PolicyWarningReporterFunc(
			func(protocol contract.ProtocolID, endpointID contract.ServiceID, summary string) {
				logf(
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
		RecordLogger:             logf,
		ConversionEngine:         conversionEngine,
		MaxConcurrentInspections: options.maxConcurrentInspections,
		MaxRequestBodyMiB:        options.maxRequestBodyMiB,
		ResponseStartTimeout:     options.responseStartTimeout,
		SubscriptionRisk:         subscriptionRiskReporter{manager: subscriptionManager},
		Identities:               identities,
	}
	handler, err := controlapi.NewWithDependencies(options.version, controlapi.Dependencies{
		ServiceStore: store,
		PricingStore: store, PricingManager: pricingManager,
		AccessTokenManager: accessTokenManager,
		PolicyStore:        store,
		PrivacyModels:      privacyModel,
		PrivacyFilter:      privacyFilter,
		PolicyChanged:      privacyWorker.ApplyPolicy,
		RequestRecords:     store,
		AuditSettings:      store,
		AuditKeys:          store,
		AuditBlobs:         store,
		RawVault:           rawVault,
		LocalData:          store,
		ClientIdentities:   identities,
		Subscriptions:      subscriptionManager,
		CodingPlans:        codingplan.New(store, nil),
		ServiceModels:      servicemodel.New(store, subscriptionManager, nil),
		ServiceTester:      servicetest.NewWithDependencies(gatewayDependencies, subscriptionManager.APIBaseURLFor),
		BuiltinToolTester:  ingress.NewWithDependencies(gatewayDependencies),
		ControlToken:       options.controlToken,
		ObserverToken:      options.observerToken,
		ConversionEngine:   conversionEngine,
		Shutdown:           options.shutdown,
		ConsoleSessions:    options.consoleSessions,
	})
	if err != nil {
		return fail("configure persistent control API: %w", err)
	}
	monitorCtx, stopMonitors := context.WithCancel(ctx)
	var monitors sync.WaitGroup
	monitors.Add(3)
	go func() { defer monitors.Done(); pricingManager.Run(monitorCtx, logf) }()
	go func() { defer monitors.Done(); subscriptionManager.RunUsageMonitor(monitorCtx) }()
	go func() { defer monitors.Done(); rawVault.Run(monitorCtx) }()
	return &persistentCore{
		control:  handler,
		rawVault: rawVault,
		gateway:  gatewayDependencies,
		retentionSweep: func(ctx context.Context) error {
			_, err := store.SweepExpiredAuditData(ctx)
			return err
		},
		close: func() error {
			// Zero every key an agent grant or the unlock session holds.
			handler.RevokeRawGrants()
			rawVault.Lock()
			stopMonitors()
			monitors.Wait()
			err := store.Close()
			privacyWorker.Close()
			return err
		},
	}, nil
}
