package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/hostapi"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/plugininstall"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
	"github.com/QuantumNous/astrlink/core/internal/privacyworker"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/servicetest"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	syncengine "github.com/QuantumNous/astrlink/core/internal/sync"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/astrlink/core/internal/vertexauth"
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
	// installClaudeCLI is set by the server edition, which installs the
	// claude CLI on request into the data directory.
	installClaudeCLI bool
	// consoleSessions is set by the server edition; see
	// controlapi.ConsoleSessions.
	consoleSessions controlapi.ConsoleSessions
	// rawBackoffCap raises the raw vault's wrong-password backoff cap;
	// zero keeps the desktop's.
	rawBackoffCap time.Duration
	// rawUnlockUntilLock keeps the operator's raw unlock open until the
	// desktop locks it, with no idle deadline; see
	// controlapi.RawVaultOptions.UnlockUntilLock.
	rawUnlockUntilLock bool
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
	gateway ingress.Dependencies
	// serviceTester tests providers; it learns the gateway's address once
	// the inference plane listens.
	serviceTester *servicetest.Tester
	// claudeOfficial serves official Claude accounts; the caller reports the
	// inference listener to it. Nil when they are unavailable.
	claudeOfficial *officialClaude
	// pluginHost learns the inference listener for the logins plugins run.
	pluginHost     *pluginhost.Host
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
	var privacyWorker *privacyworker.Handover
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
	rawVault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{
		Logf: logf, BackoffCap: options.rawBackoffCap, UnlockUntilLock: options.rawUnlockUntilLock,
	})
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
	privacyWorker, err = privacyworker.NewHandover(privacyworker.Config{
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
	auditKey, err := store.GetAuditKey(ctx)
	// The workspace key fingerprints the directories sessions run in, so a
	// new session there takes the project of the last.
	var workspaceKey, taskKey []byte
	if err == nil {
		err = privacyFilter.PersistPlaceholders(auditKey, store)
		workspaceKey = pluginhost.WorkspaceKey(auditKey)
		taskKey = taskplugin.SigningKey(auditKey)
		clear(auditKey)
	}
	if err != nil {
		logf("privacy placeholders are renamed on restart: %v", err)
	}
	// The desktop polls this feed to notify about requests the warn action
	// let through; it lives in memory only.
	privacyWarnings := privacy.NewWarningFeed()
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
	claudeOfficial, err := enableOfficialClaude(subscriptionManager, options, networkproxy.Resolver(store, store))
	if err != nil {
		logf("official Claude accounts are unavailable: %v", err)
	}
	pricingManager := pricing.NewManager(store, pricingClient)
	syncEngine, err := syncengine.New(syncengine.Dependencies{Store: store, Logf: logf})
	if err != nil {
		return fail("configure configuration sync: %w", err)
	}
	subscriptionManager.SetUsageObservers(
		func(ctx context.Context, account contract.SubscriptionAccount, usage contract.SubscriptionUsage) error {
			current, err := store.GetService(ctx, account.ID)
			if err != nil {
				return err
			}
			if current.Service.Subscription == nil || current.Service.Subscription.ProviderAccountID != account.ProviderAccountID {
				return nil
			}
			if err := store.ObserveSubscriptionUsage(ctx, current.Service, usage); err != nil {
				return err
			}
			_, err = store.ObserveSubscriptionPlan(ctx, account.ID, account.ProviderAccountID, usage.PlanType)
			return err
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
	resolver.WithUsageCache(subscriptionManager)
	vertexTokens := vertexauth.New(nil)
	requestMetadata := metadata.NewRegistry(store)
	if err := requestMetadata.Refresh(ctx); err != nil {
		return fail("load request metadata: %w", err)
	}
	gatewayDependencies := ingress.Dependencies{
		Metadata:         requestMetadata,
		ProxyCredentials: store,
		Resolver:         resolver,
		Authorizer: endpoint.NewServiceAuthorizer(store, subscriptionManager, subscriptionManager.Provider().IdentityPolicy()).
			WithRoutingSettings(store).WithIdentities(identities).WithVertexTokens(vertexTokens),
		AccessTokenAuthenticator: ingress.AccessTokenAuthenticatorFunc(
			func(ctx context.Context, raw string) (contract.AccessTokenID, error) {
				return accessTokenManager.Authenticate(ctx, raw)
			},
		),
		PrivacyFilter: privacyFilter,
		PolicyWarningReporter: ingress.PolicyWarningReporterFunc(
			func(warning privacy.Warning) {
				logf(
					"privacy policy warning: protocol=%s service_id=%s findings=%s",
					warning.Protocol,
					warning.ServiceID,
					privacy.WarningSummary(warning.Findings),
				)
				privacyWarnings.Report(warning)
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
		ClaudeOfficial:           claudeOfficial.serving(),
	}
	// Plugins load from <data-dir>/plugins in the background; until the first
	// scan ends, requests see none.
	pluginDirectory := filepath.Join(options.dataDirectory, "plugins")
	if err := os.MkdirAll(pluginDirectory, 0o700); err != nil {
		logf("create plugin directory: %v", err)
	}
	// Each plugin reads and writes its own directory under
	// <data-dir>/plugin-data through node:fs.
	pluginManager := plugin.NewManager(plugin.ManagerOptions{
		Dir:      pluginDirectory,
		DataDir:  filepath.Join(options.dataDirectory, "plugin-data"),
		Settings: hostapi.Settings{Store: store},
		Services: plugin.Services{
			Fetcher:  &hostapi.Fetcher{Credentials: store},
			Store:    hostapi.Store{Values: store},
			Metadata: hostapi.Metadata{Store: store},
		},
	})
	// The tasks of task plugins go through providers of kind plugin and
	// are polled in the background; a result a vendor returns inline is
	// kept in <data-dir>/plugin-tasks.
	taskEngine := taskplugin.New(taskplugin.Options{
		Plugins: pluginManager, Store: store, Services: store, Secrets: store, ProxyCredentials: store,
		Send: transport.New(nil).RoundTrip, Dir: filepath.Join(options.dataDirectory, "plugin-tasks"),
		SigningKey: taskKey, Logf: logf,
	})
	gatewayDependencies.Tasks = taskEngine
	pluginHost := pluginhost.New(pluginhost.Options{
		Plugins: pluginManager, Metadata: requestMetadata, Bindings: store, Store: store, Records: store,
		WorkspaceKey: workspaceKey, Logf: logf, Credentials: serviceCredentials{store: store},
		Send: transport.New(nil).RoundTrip, ProxyCredentials: store,
	})
	// Plugins keep their providers' accounts up while Core runs.
	pluginHost.StartMaintenance(ctx, func(ctx context.Context) ([]contract.Service, error) {
		kind := contract.ServiceKindPlugin
		var services []contract.Service
		cursor := ""
		for {
			page, err := store.ListServices(ctx, storage.ServiceListOptions{Limit: 200, Cursor: cursor, Kind: &kind})
			if err != nil {
				return nil, err
			}
			for _, record := range page.Items {
				services = append(services, record.Service)
			}
			if page.NextCursor == "" {
				return services, nil
			}
			cursor = page.NextCursor
		}
	})
	if err := pluginHost.LoadSettings(ctx); err != nil {
		logf("load the settings of the plugins: %v", err)
	}
	// Plugins installed from source come through the outbound proxy, with
	// their packages from the registry the plugin page names, else npm,
	// then npmmirror.
	pluginInstaller := plugininstall.New(plugininstall.Options{
		Dir:    pluginDirectory,
		Client: networkproxy.WrapClient(&http.Client{}),
		Registries: func() []string {
			registries := []string{contract.DefaultNpmRegistry, contract.FallbackNpmRegistry}
			if registry := pluginHost.Settings().NpmRegistry; registry != "" {
				registries = append([]string{registry}, registries...)
			}
			return registries
		},
		Version:  buildinfo.Version,
		Language: func() string { return pluginHost.Settings().Language },
	})
	gatewayDependencies.Plugins = pluginHost
	gatewayTester := ingress.NewWithDependencies(gatewayDependencies)
	serviceTester := servicetest.NewWithDependencies(gatewayDependencies, subscriptionManager.APIBaseURLFor)
	handler, err := controlapi.NewWithDependencies(options.version, controlapi.Dependencies{
		ServiceStore: store,
		PricingStore: store, PricingManager: pricingManager,
		AccessTokenManager: accessTokenManager,
		PolicyStore:        store,
		PrivacyModels:      privacyModel,
		PrivacyFilter:      privacyFilter,
		PrivacyWarnings:    privacyWarnings,
		PolicyChanged:      privacyWorker.ApplyPolicy,
		ModelPreparation:   privacyWorker.Preparation,
		RequestRecords:     store,
		AuditSettings:      store,
		AuditKeys:          store,
		AuditBlobs:         store,
		ContextTexts:       ingress.ContextTexts,
		RawVault:           rawVault,
		LocalData:          store,
		ClientIdentities:   identities,
		Subscriptions:      subscriptionManager,
		ClaudeCLI:          claudeOfficial.controlCLI(),
		RateLimits:         resolver,
		ServicePackages:    resolver,
		CodingPlans:        codingplan.New(store, nil),
		ServiceModels:      servicemodel.New(store, subscriptionManager, nil).WithVertexTokens(vertexTokens).WithOfficialClaudeModels(claudeOfficial.models()).WithPluginProviders(pluginHost),
		ServiceTester:      serviceTester,
		BuiltinToolTester:  gatewayTester,
		ControlToken:       options.controlToken,
		ObserverToken:      options.observerToken,
		ConversionEngine:   conversionEngine,
		Shutdown:           options.shutdown,
		ConsoleSessions:    options.consoleSessions,
		Sync:               syncEngine,
		Plugins:            pluginManager,
		PluginStore:        store,
		Metadata:           requestMetadata,
		MetadataStore:      store,
		PluginHost:         pluginHost,
		PluginInstaller:    pluginInstaller,
		PluginTasks:        taskEngine,
	})
	if err != nil {
		return fail("configure persistent control API: %w", err)
	}
	monitorCtx, stopMonitors := context.WithCancel(ctx)
	var monitors sync.WaitGroup
	monitors.Add(7)
	go func() {
		defer monitors.Done()
		if err := pluginManager.Reload(monitorCtx); err != nil {
			logf("load plugins: %v", err)
			return
		}
		// An enabled plugin's manifest may have changed since it registered
		// its attributes.
		registerAttributes := func() {
			for _, loaded := range pluginManager.Current().Plugins() {
				if !loaded.Enabled() {
					continue
				}
				if err := requestMetadata.RegisterPlugin(monitorCtx, loaded.Key, loaded.Attributes()); err != nil {
					logf("register metadata of plugin %s: %v", loaded.Key, err)
				}
			}
		}
		registerAttributes()
		// A plugin astrlink plugin dev keeps built reloads as it changes.
		pluginManager.WatchDevelopment(monitorCtx, time.Second, registerAttributes)
	}()
	go func() { defer monitors.Done(); pricingManager.Run(monitorCtx, logf) }()
	go func() { defer monitors.Done(); pluginHost.RunSchedules(monitorCtx) }()
	go func() { defer monitors.Done(); subscriptionManager.RunUsageMonitor(monitorCtx) }()
	go func() { defer monitors.Done(); rawVault.Run(monitorCtx) }()
	go func() { defer monitors.Done(); syncEngine.Run(monitorCtx, logf) }()
	go func() { defer monitors.Done(); taskEngine.Run(monitorCtx) }()
	return &persistentCore{
		control:        handler,
		rawVault:       rawVault,
		gateway:        gatewayDependencies,
		serviceTester:  serviceTester,
		pluginHost:     pluginHost,
		claudeOfficial: claudeOfficial,
		retentionSweep: func(ctx context.Context) error {
			_, err := store.SweepExpiredAuditData(ctx)
			return err
		},
		close: func() error {
			// Zero every key an agent grant or the unlock session holds.
			handler.RevokeRawGrants()
			rawVault.Lock()
			claudeOfficial.close()
			stopMonitors()
			monitors.Wait()
			pluginHost.Close()
			err := store.Close()
			privacyWorker.Close()
			return err
		},
	}, nil
}

// serviceCredentials keeps the credentials plugins log providers in with
// where providers keep their keys.
type serviceCredentials struct{ store *sqlite.Store }

func (credentials serviceCredentials) ReadServiceSecret(ctx context.Context, service contract.Service) ([]byte, error) {
	record, err := credentials.store.GetService(ctx, service.ID)
	if err != nil {
		return nil, err
	}
	if record.Service.HTTP == nil || record.Service.HTTP.CredentialRef == "" {
		return nil, errors.New("the provider keeps no credential")
	}
	return credentials.store.Get(ctx, secretstore.Ref(record.Service.HTTP.CredentialRef))
}

func (credentials serviceCredentials) WriteServiceSecret(ctx context.Context, id contract.ServiceID, secret []byte) error {
	record, err := credentials.store.GetService(ctx, id)
	if err != nil {
		return err
	}
	_, err = credentials.store.UpdateService(ctx, record.Service, storage.CredentialMutation{Present: true, Secret: secret}, record.ETag)
	return err
}
