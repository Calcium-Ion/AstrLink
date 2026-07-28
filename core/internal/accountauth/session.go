package accountauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const maxPendingAuthorizationSessions = 3

type sessionSecrets struct {
	state       string
	pkce        PkceCodes
	redirectURI string
	listener    net.Listener
	cancel      context.CancelFunc
}

type trackedSession struct {
	public  contract.AuthorizationSession
	secrets *sessionSecrets
}

// SessionManager owns targeted interactive login windows. A service can have
// at most one current session while independent services may authorize in
// parallel.
type SessionManager struct {
	mu         sync.Mutex
	config     OAuthConfig
	tokens     *TokenClient
	store      AccountCredentialStore
	sessions   map[contract.AuthorizationSessionID]*trackedSession
	byService  map[contract.ServiceID]contract.AuthorizationSessionID
	onComplete func(context.Context, contract.AuthorizationSession, AccountTokens) error
	newID      func() (contract.AuthorizationSessionID, error)
}

func NewSessionManager(
	config OAuthConfig,
	store AccountCredentialStore,
	onComplete func(context.Context, contract.AuthorizationSession, AccountTokens) error,
) *SessionManager {
	config = config.normalized()
	return &SessionManager{
		config: config, tokens: NewTokenClient(config), store: store,
		sessions:   make(map[contract.AuthorizationSessionID]*trackedSession),
		byService:  make(map[contract.ServiceID]contract.AuthorizationSessionID),
		onComplete: onComplete, newID: randomAuthorizationSessionID,
	}
}

func randomAuthorizationSessionID() (contract.AuthorizationSessionID, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return contract.AuthorizationSessionID("authorization_" + hex.EncodeToString(raw[:])), nil
}

func (manager *SessionManager) Begin(
	ctx context.Context,
	serviceID contract.ServiceID,
	flow contract.AuthorizationFlow,
) (contract.AuthorizationSession, error) {
	if err := serviceID.Validate(); err != nil {
		return contract.AuthorizationSession{}, err
	}
	if !flow.Valid() {
		return contract.AuthorizationSession{}, fmt.Errorf("unknown authorization flow %q", flow)
	}
	if err := manager.store.Available(ctx); err != nil {
		return contract.AuthorizationSession{}, fmt.Errorf("%w", ErrCredentialStoreUnavailable)
	}

	switch flow {
	case contract.AuthorizationFlowBrowser:
		session, err := manager.beginBrowserAuthorization(serviceID)
		if !errors.Is(err, ErrCallbackPortsUnavailable) {
			return session, err
		}
		fallback, fallbackErr := manager.beginDeviceCodeAuthorization(ctx, serviceID)
		if fallbackErr != nil {
			return contract.AuthorizationSession{}, errors.Join(err, fallbackErr)
		}
		return fallback, nil
	case contract.AuthorizationFlowDeviceCode:
		return manager.beginDeviceCodeAuthorization(ctx, serviceID)
	default:
		return contract.AuthorizationSession{}, fmt.Errorf("unknown authorization flow %q", flow)
	}
}

func (manager *SessionManager) beginBrowserAuthorization(
	serviceID contract.ServiceID,
) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.canStartLocked(serviceID); err != nil {
		return contract.AuthorizationSession{}, err
	}

	state, err := generateState()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	pkce, err := generatePKCE()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	listener, port, err := manager.listenLoopback()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	browserRedirect := fmt.Sprintf("http://localhost:%d%s", port, manager.config.RedirectPath)
	authURL, err := manager.buildAuthorizeURL(browserRedirect, state, pkce.Challenge)
	if err != nil {
		_ = listener.Close()
		return contract.AuthorizationSession{}, err
	}
	sessionID, err := manager.newID()
	if err != nil {
		_ = listener.Close()
		return contract.AuthorizationSession{}, err
	}
	now := manager.config.Now().UTC()
	public := contract.AuthorizationSession{
		ID: sessionID, Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.AuthorizationSessionStatusPending,
		Flow:   contract.AuthorizationFlowBrowser, AuthorizationURL: authURL,
		ServiceID: serviceID, ExpiresAt: now.Add(manager.config.SessionTTL),
		CreatedAt: now, UpdatedAt: now,
	}
	sessionContext, cancel := context.WithCancel(context.Background())
	secrets := &sessionSecrets{
		state: state, pkce: pkce, redirectURI: browserRedirect,
		listener: listener, cancel: cancel,
	}
	server := &http.Server{
		Handler:           manager.callbackHandler(sessionID, secrets),
		ReadHeaderTimeout: 5 * time.Second,
	}
	manager.sessions[sessionID] = &trackedSession{public: public, secrets: secrets}
	manager.byService[serviceID] = sessionID
	go func() {
		_ = server.Serve(listener)
	}()
	go manager.expireAfter(sessionContext, sessionID, manager.config.SessionTTL)
	return public, nil
}

func (manager *SessionManager) beginDeviceCodeAuthorization(
	ctx context.Context,
	serviceID contract.ServiceID,
) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	if err := manager.canStartLocked(serviceID); err != nil {
		manager.mu.Unlock()
		return contract.AuthorizationSession{}, err
	}
	manager.mu.Unlock()

	device, err := manager.requestDeviceAuthorization(ctx)
	if err != nil {
		return contract.AuthorizationSession{}, err
	}
	sessionID, err := manager.newID()
	if err != nil {
		return contract.AuthorizationSession{}, err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := manager.canStartLocked(serviceID); err != nil {
		return contract.AuthorizationSession{}, err
	}
	now := manager.config.Now().UTC()
	public := contract.AuthorizationSession{
		ID: sessionID, Provider: contract.SubscriptionProviderOpenAICodex,
		Status: contract.AuthorizationSessionStatusPending,
		Flow:   contract.AuthorizationFlowDeviceCode,
		DeviceCode: &contract.AuthorizationDeviceCode{
			VerificationURL: device.VerificationURL,
			UserCode:        device.UserCode,
		},
		ServiceID: serviceID, ExpiresAt: now.Add(manager.config.DeviceCodeTTL),
		CreatedAt: now, UpdatedAt: now,
	}
	sessionContext, cancel := context.WithCancel(context.Background())
	manager.sessions[sessionID] = &trackedSession{
		public: public,
		secrets: &sessionSecrets{
			cancel: cancel,
		},
	}
	manager.byService[serviceID] = sessionID
	go manager.expireAfter(sessionContext, sessionID, manager.config.DeviceCodeTTL)
	go manager.pollDeviceAuthorization(sessionContext, sessionID, device)
	return cloneAuthorizationSession(public), nil
}

func (manager *SessionManager) canStartLocked(serviceID contract.ServiceID) error {
	manager.expirePendingLocked()
	if existingID, ok := manager.byService[serviceID]; ok {
		existing := manager.sessions[existingID]
		if existing != nil && existing.public.Status == contract.AuthorizationSessionStatusPending {
			return fmt.Errorf("an authorization session is already active for service %q", serviceID)
		}
		manager.removeLocked(existingID)
	}
	if manager.pendingCountLocked() >= maxPendingAuthorizationSessions {
		return fmt.Errorf("too many authorization sessions are active")
	}
	return nil
}

func (manager *SessionManager) expireAfter(
	ctx context.Context,
	sessionID contract.AuthorizationSessionID,
	ttl time.Duration,
) {
	timer := time.NewTimer(ttl)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		manager.mu.Lock()
		defer manager.mu.Unlock()
		session := manager.sessions[sessionID]
		if session == nil || session.public.Status != contract.AuthorizationSessionStatusPending {
			return
		}
		manager.shutdownLocked(session, contract.AuthorizationSessionStatusExpired, &contract.SubscriptionError{
			Code: ErrCodeSessionExpired, Message: "authorization session expired",
		})
	}
}

func (manager *SessionManager) Get(serviceID contract.ServiceID) (contract.AuthorizationSession, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expirePendingLocked()
	sessionID, ok := manager.byService[serviceID]
	if !ok {
		return contract.AuthorizationSession{}, false
	}
	session := manager.sessions[sessionID]
	if session == nil {
		delete(manager.byService, serviceID)
		return contract.AuthorizationSession{}, false
	}
	return cloneAuthorizationSession(session.public), true
}

func (manager *SessionManager) Cancel(_ context.Context, serviceID contract.ServiceID) (contract.AuthorizationSession, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expirePendingLocked()
	sessionID, ok := manager.byService[serviceID]
	if !ok {
		return contract.AuthorizationSession{}, ErrSessionNotFound
	}
	session := manager.sessions[sessionID]
	if session == nil {
		delete(manager.byService, serviceID)
		return contract.AuthorizationSession{}, ErrSessionNotFound
	}
	if session.public.Status != contract.AuthorizationSessionStatusPending {
		return session.public, ErrSessionNotPending
	}
	manager.shutdownLocked(session, contract.AuthorizationSessionStatusCancelled, &contract.SubscriptionError{
		Code: ErrCodeSessionCancelled, Message: "authorization cancelled",
	})
	return session.public, nil
}

func (manager *SessionManager) CancelAllForService(serviceID contract.ServiceID) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	sessionID, ok := manager.byService[serviceID]
	if !ok {
		return
	}
	session := manager.sessions[sessionID]
	if session != nil && session.public.Status == contract.AuthorizationSessionStatusPending {
		manager.shutdownLocked(session, contract.AuthorizationSessionStatusCancelled, &contract.SubscriptionError{
			Code: ErrCodeSessionCancelled, Message: "authorization cancelled",
		})
	}
}

func (manager *SessionManager) listenLoopback() (net.Listener, int, error) {
	candidates := []int{manager.config.PreferredPort, manager.config.FallbackPort}
	var lastErr error
	seen := make(map[int]struct{}, len(candidates))
	for _, port := range candidates {
		if port <= 0 {
			continue
		}
		if _, duplicate := seen[port]; duplicate {
			continue
		}
		seen[port] = struct{}{}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			lastErr = err
			continue
		}
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			_ = listener.Close()
			return nil, 0, fmt.Errorf("callback listener address is not TCP")
		}
		return listener, address.Port, nil
	}
	if lastErr == nil {
		lastErr = ErrCallbackPortsUnavailable
	}
	return nil, 0, fmt.Errorf("%w: %v", ErrCallbackPortsUnavailable, lastErr)
}

func (manager *SessionManager) buildAuthorizeURL(redirectURI, state, challenge string) (string, error) {
	endpoint, err := url.Parse(strings.TrimRight(manager.config.Issuer, "/") + "/oauth/authorize")
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("response_type", "code")
	query.Set("client_id", manager.config.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", strings.Join(manager.config.Scopes, " "))
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("state", state)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", manager.config.Originator)
	for key, values := range manager.config.ExtraAuthQuery {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (manager *SessionManager) callbackHandler(sessionID contract.AuthorizationSessionID, secrets *sessionSecrets) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(manager.config.RedirectPath, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query := request.URL.Query()
		if query.Get("error") != "" {
			manager.failSession(sessionID, &contract.SubscriptionError{
				Code: ErrCodeCallbackFailed, Message: "authorization provider returned an error",
			})
			writeCallbackPage(writer, false, "Authorization failed.")
			return
		}
		state, code := query.Get("state"), query.Get("code")
		if code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(secrets.state)) != 1 {
			manager.failSession(sessionID, &contract.SubscriptionError{
				Code: ErrCodeStateMismatch, Message: "authorization state validation failed",
			})
			writeCallbackPage(writer, false, "Authorization state mismatch.")
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		tokens, err := manager.tokens.ExchangeCode(ctx, code, secrets.pkce.Verifier, secrets.redirectURI)
		if err != nil {
			manager.failSession(sessionID, &contract.SubscriptionError{
				Code: ErrCodeCallbackFailed, Message: "authorization code exchange failed",
			})
			writeCallbackPage(writer, false, "Authorization code exchange failed.")
			return
		}
		if err := manager.completeSession(ctx, sessionID, tokens); err != nil {
			manager.failSession(sessionID, &contract.SubscriptionError{
				Code: ErrCodeStoreUnavailable, Message: "failed to persist connected account",
			})
			writeCallbackPage(writer, false, "Connected, but AstrLink could not store credentials.")
			return
		}
		writeCallbackPage(writer, true, "AstrLink is connected. You can close this window.")
	})
	mux.HandleFunc("/cancel", func(writer http.ResponseWriter, request *http.Request) {
		manager.cancelBySessionID(sessionID)
		writer.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (manager *SessionManager) completeSession(ctx context.Context, sessionID contract.AuthorizationSessionID, tokens AccountTokens) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	session := manager.sessions[sessionID]
	if session == nil || session.public.Status != contract.AuthorizationSessionStatusPending {
		return ErrSessionNotPending
	}
	public := cloneAuthorizationSession(session.public)
	public.AuthorizationURL = ""
	public.DeviceCode = nil
	if manager.onComplete != nil {
		if err := manager.onComplete(ctx, public, tokens); err != nil {
			return err
		}
	}
	session.public.Status = contract.AuthorizationSessionStatusCompleted
	session.public.AuthorizationURL = ""
	session.public.DeviceCode = nil
	session.public.UpdatedAt = manager.config.Now().UTC()
	session.public.Error = nil
	manager.closeListenerLocked(session)
	return nil
}

func (manager *SessionManager) failSession(sessionID contract.AuthorizationSessionID, failure *contract.SubscriptionError) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	session := manager.sessions[sessionID]
	if session == nil || session.public.Status != contract.AuthorizationSessionStatusPending {
		return
	}
	manager.shutdownLocked(session, contract.AuthorizationSessionStatusFailed, failure)
}

func (manager *SessionManager) cancelBySessionID(sessionID contract.AuthorizationSessionID) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	session := manager.sessions[sessionID]
	if session == nil || session.public.Status != contract.AuthorizationSessionStatusPending {
		return
	}
	manager.shutdownLocked(session, contract.AuthorizationSessionStatusCancelled, &contract.SubscriptionError{
		Code: ErrCodeSessionCancelled, Message: "authorization cancelled",
	})
}

func (manager *SessionManager) expirePendingLocked() {
	now := manager.config.Now()
	for _, session := range manager.sessions {
		if session.public.Status == contract.AuthorizationSessionStatusPending && now.After(session.public.ExpiresAt) {
			manager.shutdownLocked(session, contract.AuthorizationSessionStatusExpired, &contract.SubscriptionError{
				Code: ErrCodeSessionExpired, Message: "authorization session expired",
			})
		}
	}
}

func (manager *SessionManager) pendingCountLocked() int {
	count := 0
	for _, session := range manager.sessions {
		if session.public.Status == contract.AuthorizationSessionStatusPending {
			count++
		}
	}
	return count
}

func (manager *SessionManager) shutdownLocked(
	session *trackedSession,
	status contract.AuthorizationSessionStatus,
	failure *contract.SubscriptionError,
) {
	if session == nil {
		return
	}
	session.public.Status = status
	session.public.AuthorizationURL = ""
	session.public.DeviceCode = nil
	session.public.Error = failure
	session.public.UpdatedAt = manager.config.Now().UTC()
	manager.closeListenerLocked(session)
}

func (manager *SessionManager) removeLocked(sessionID contract.AuthorizationSessionID) {
	session := manager.sessions[sessionID]
	if session == nil {
		return
	}
	manager.closeListenerLocked(session)
	delete(manager.byService, session.public.ServiceID)
	delete(manager.sessions, sessionID)
}

func (manager *SessionManager) closeListenerLocked(session *trackedSession) {
	if session == nil || session.secrets == nil {
		return
	}
	if session.secrets.cancel != nil {
		session.secrets.cancel()
	}
	if session.secrets.listener != nil {
		_ = session.secrets.listener.Close()
	}
	session.secrets = nil
}

func cloneAuthorizationSession(session contract.AuthorizationSession) contract.AuthorizationSession {
	if session.DeviceCode != nil {
		device := *session.DeviceCode
		session.DeviceCode = &device
	}
	if session.Error != nil {
		failure := *session.Error
		session.Error = &failure
	}
	return session
}

func writeCallbackPage(writer http.ResponseWriter, ok bool, message string) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	title := "AstrLink authorization"
	if !ok {
		writer.WriteHeader(http.StatusBadRequest)
	}
	_, _ = io.WriteString(writer, "<!doctype html><html><head><meta charset=\"utf-8\"><title>"+title+
		"</title></head><body><h1>"+title+"</h1><p>"+htmlEscape(message)+"</p></body></html>")
}

func htmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return replacer.Replace(value)
}
