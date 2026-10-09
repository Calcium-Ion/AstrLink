// Package console serves the server edition's browser console: the control
// API behind a password session, a few console endpoints, and the embedded
// web assets. It shares one listener with the inference plane; Owns says
// which requests are the console's. There is one password: the console
// signs in with the raw password the control API's raw vault keeps.
package console

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
)

const (
	StatusPath = "/console/v1/status"
	SetupPath  = "/console/v1/setup"
	LoginPath  = "/console/v1/login"
	LogoutPath = "/console/v1/logout"

	consoleAPIPrefix = "/console/v1/"
	controlAPIPrefix = "/control/v1/"

	SessionCookie = "astrlink_session"

	// RequestHeader must carry RequestHeaderValue on every state-changing
	// request, setup, sign-in and sign-out included. A page on another
	// origin can only add it after a CORS preflight, and Core approves none,
	// so the header shows the request came from the console's own page. It
	// is local to the console and never forwarded upstream.
	RequestHeader      = "X-AstrLink-Console"
	RequestHeaderValue = "1"

	// ResetVariable is the deploy configuration switch that clears a
	// forgotten raw password at start.
	ResetVariable = "ASTRLINK_RESET_PASSWORD"
)

// SetupWindow is how long after start the console accepts its first
// password while none is set. A restart opens it again.
const SetupWindow = 10 * time.Minute

// Status values of GET StatusPath.
const (
	// StatusSetupRequired: no raw password is set and the setup window is
	// open; the page asks for the first password.
	StatusSetupRequired = "setup_required"
	// StatusSetupExpired: no raw password is set and the window closed;
	// restarting AstrLink opens it again.
	StatusSetupExpired = "setup_expired"
	// StatusLoginRequired: a raw password is set and signs in.
	StatusLoginRequired = "login_required"
)

// React controls, Radix positioning, charts and toast animations use inline styles.
// Scripts remain same-origin only; no inline scripts or eval are permitted.
const contentSecurityPolicy = "default-src 'self'; style-src 'self' 'unsafe-inline'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"

// maxBodyBytes bounds every console request, matching the control API's own
// limit. The shared listener keeps the inference plane's timeouts, so
// streaming responses are never cut; console bodies are bounded by size.
const maxBodyBytes = 1 << 20

// RawPassword is the raw vault, whose password is the console password.
// *controlapi.Vault implements it.
type RawPassword interface {
	Status(context.Context) (controlapi.RawVaultStatus, error)
	// Verify checks a password under the vault's backoff without unlocking
	// raw content.
	Verify(context.Context, controlapi.RawProof) (controlapi.RawVaultStatus, error)
	ChangePassword(context.Context, controlapi.RawPasswordAction, []byte, controlapi.RawProof) (controlapi.RawPasswordOutcome, error)
}

type Config struct {
	RawPassword RawPassword
	// Control is the control API handler. Signed-in requests reach it as
	// operator.
	Control http.Handler
	// Sessions is shared with the control API, which ends the other
	// sessions after a password change. Nil starts an empty set.
	Sessions *Sessions
	// PasswordReset reports that the reset switch cleared the raw password
	// at this start.
	PasswordReset bool
	// Now is for tests; nil uses time.Now.
	Now func() time.Time
}

type Handler struct {
	rawPassword   RawPassword
	control       http.Handler
	sessions      *Sessions
	passwordReset bool
	setupUntil    time.Time
	assets        fs.FS
	now           func() time.Time
}

// New builds the console. The setup window starts now.
func New(config Config) (*Handler, error) {
	if config.Control == nil || config.RawPassword == nil {
		return nil, errors.New("control handler and raw password are required")
	}
	handler := &Handler{
		rawPassword:   config.RawPassword,
		control:       config.Control,
		sessions:      config.Sessions,
		passwordReset: config.PasswordReset,
		assets:        assets,
		now:           config.Now,
	}
	if handler.now == nil {
		handler.now = time.Now
	}
	if handler.sessions == nil {
		handler.sessions = NewSessions()
	}
	handler.setupUntil = handler.now().Add(SetupWindow)
	return handler, nil
}

// Owns reports whether a request on the shared listener is the console's:
// its API, the control API, the page, and files embedded with it. Every
// other path stays with the inference plane and gets its own answers, so
// /v1/models and unknown paths never reach the console.
func (handler *Handler) Owns(request *http.Request) bool {
	path := request.URL.Path
	switch {
	case path == "/" || path == "/index.html":
		return true
	case strings.HasPrefix(path, consoleAPIPrefix) || strings.HasPrefix(path, controlAPIPrefix):
		return true
	}
	return handler.assetExists(path)
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer.Header())
	request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
	path := request.URL.Path
	switch {
	case path == StatusPath:
		handler.status(writer, request)
	case path == SetupPath:
		handler.setup(writer, request)
	case path == LoginPath:
		handler.login(writer, request)
	case path == LogoutPath:
		handler.logout(writer, request)
	case strings.HasPrefix(path, controlAPIPrefix):
		handler.signedIn(handler.control.ServeHTTP)(writer, request)
	case strings.HasPrefix(path, consoleAPIPrefix):
		controlapi.WriteError(writer, http.StatusNotFound, "not_found", "console API path not found")
	default:
		handler.serveAsset(writer, request)
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

type statusResponse struct {
	Status string `json:"status"`
	// SignedIn is true when the request carries a live session.
	SignedIn bool `json:"signed_in"`
	// SetupSecondsLeft counts down the setup window; 0 outside setup.
	SetupSecondsLeft int `json:"setup_seconds_left"`
	// PasswordReset is true for the whole run whose start used the reset
	// switch: saved raw content was discarded and the switch can go.
	PasswordReset     bool   `json:"password_reset"`
	ResetVariable     string `json:"reset_variable"`
	PasswordMinLength int    `json:"password_min_length"`
	PasswordMaxLength int    `json:"password_max_length"`
}

func (handler *Handler) statusFor(ctx context.Context, signedIn bool) (statusResponse, error) {
	raw, err := handler.rawPassword.Status(ctx)
	if err != nil {
		return statusResponse{}, err
	}
	response := statusResponse{
		Status:            StatusLoginRequired,
		SignedIn:          signedIn,
		PasswordReset:     handler.passwordReset,
		ResetVariable:     ResetVariable,
		PasswordMinLength: rawseal.MinPasswordRunes,
		PasswordMaxLength: rawseal.MaxPasswordRunes,
	}
	if !raw.PasswordSet {
		response.Status = StatusSetupExpired
		if left := handler.setupUntil.Sub(handler.now()); left > 0 {
			response.Status = StatusSetupRequired
			response.SetupSecondsLeft = int((left + time.Second - 1) / time.Second)
		}
	}
	return response, nil
}

func (handler *Handler) status(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		controlapi.WriteError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	response, err := handler.statusFor(request.Context(), handler.sessionValid(request))
	if err != nil {
		controlapi.WriteError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "the password state is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

// setup sets the first raw password while none is set and the window is
// open, then signs the caller in. The vault admits only the first set.
func (handler *Handler) setup(writer http.ResponseWriter, request *http.Request) {
	if !handler.postOnly(writer, request) || !handler.allowWrite(writer, request) {
		return
	}
	current, err := handler.statusFor(request.Context(), false)
	if err != nil {
		controlapi.WriteError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "the password state is unavailable")
		return
	}
	switch current.Status {
	case StatusLoginRequired:
		controlapi.WriteError(writer, http.StatusConflict, "already_configured", "a password is already set; sign in with it")
		return
	case StatusSetupExpired:
		controlapi.WriteError(writer, http.StatusForbidden, StatusSetupExpired,
			"first-run setup closes 10 minutes after AstrLink starts; restart AstrLink to open it again")
		return
	}
	password, ok := controlapi.DecodePasswordJSON(writer, request)
	if !ok {
		return
	}
	defer clear(password)
	_, err = handler.rawPassword.ChangePassword(request.Context(), controlapi.RawPasswordSet, password, controlapi.RawProof{})
	switch {
	case err == nil:
		handler.startSession(writer, request)
	case errors.Is(err, controlapi.ErrRawPasswordAlreadySet):
		controlapi.WriteError(writer, http.StatusConflict, "already_configured", "a password is already set; sign in with it")
	case errors.Is(err, controlapi.ErrRawPasswordRequired), errors.Is(err, controlapi.ErrRawPasswordPolicy):
		controlapi.WriteError(writer, http.StatusBadRequest, "invalid_password",
			"the password must have "+strconv.Itoa(rawseal.MinPasswordRunes)+" to "+strconv.Itoa(rawseal.MaxPasswordRunes)+" characters")
	default:
		controlapi.WriteError(writer, http.StatusInternalServerError, "setup_failed", "the password could not be saved")
	}
}

// login checks the raw password under the vault's backoff. It never
// unlocks raw content; viewing that still asks for the password.
func (handler *Handler) login(writer http.ResponseWriter, request *http.Request) {
	if !handler.postOnly(writer, request) || !handler.allowWrite(writer, request) {
		return
	}
	password, ok := controlapi.DecodePasswordJSON(writer, request)
	if !ok {
		return
	}
	defer clear(password)
	_, err := handler.rawPassword.Verify(request.Context(), controlapi.RawProof{Password: password})
	var backoff *controlapi.RawBackoffError
	switch {
	case err == nil:
		handler.startSession(writer, request)
	case errors.As(err, &backoff):
		writer.Header().Set("Retry-After", strconv.Itoa(int((backoff.Remaining+time.Second-1)/time.Second)))
		controlapi.WriteError(writer, http.StatusTooManyRequests, "too_many_attempts", "too many wrong passwords; wait before trying again")
	case errors.Is(err, controlapi.ErrRawPasswordInvalid):
		controlapi.WriteError(writer, http.StatusUnauthorized, "invalid_password", "the password is wrong")
	case errors.Is(err, controlapi.ErrRawNotConfigured):
		controlapi.WriteError(writer, http.StatusConflict, StatusSetupRequired, "no password is set yet; finish first-run setup")
	default:
		controlapi.WriteError(writer, http.StatusInternalServerError, "login_failed", "the password could not be checked")
	}
}

func (handler *Handler) startSession(writer http.ResponseWriter, request *http.Request) {
	token, err := handler.sessions.create(handler.now())
	if err != nil {
		controlapi.WriteError(writer, http.StatusInternalServerError, "session_unavailable", "could not start a session")
		return
	}
	response, err := handler.statusFor(request.Context(), true)
	if err != nil {
		handler.sessions.remove(token)
		controlapi.WriteError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "the password state is unavailable")
		return
	}
	http.SetCookie(writer, sessionCookie(request, token, int(sessionLifetime/time.Second)))
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) logout(writer http.ResponseWriter, request *http.Request) {
	if !handler.postOnly(writer, request) || !handler.allowWrite(writer, request) {
		return
	}
	if cookie, err := request.Cookie(SessionCookie); err == nil {
		handler.sessions.remove(cookie.Value)
	}
	http.SetCookie(writer, sessionCookie(request, "", -1))
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

// signedIn admits a request with a live session as operator.
func (handler *Handler) signedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if !handler.allowWrite(writer, request) {
			return
		}
		if !handler.sessionValid(request) {
			controlapi.WriteError(writer, http.StatusUnauthorized, "unauthorized", "sign in to the web console")
			return
		}
		next(writer, request.WithContext(controlapi.ContextWithConsoleSession(request.Context())))
	}
}

func (handler *Handler) sessionValid(request *http.Request) bool {
	cookie, err := request.Cookie(SessionCookie)
	return err == nil && handler.sessions.touch(cookie.Value, handler.now())
}

func (handler *Handler) postOnly(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodPost {
		return true
	}
	writer.Header().Set("Allow", http.MethodPost)
	controlapi.WriteError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
	return false
}

// allowWrite refuses a state-changing request without RequestHeader. It
// does not compare Origin with Host, because reverse proxies such as nginx
// rewrite Host by default. A CORS preflight is refused here too, without any
// Access-Control-Allow-* header.
func (handler *Handler) allowWrite(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead ||
		request.Header.Get(RequestHeader) == RequestHeaderValue {
		return true
	}
	controlapi.WriteError(writer, http.StatusForbidden, "console_header_required",
		"state-changing console requests must carry "+RequestHeader+": "+RequestHeaderValue)
	return false
}

// sessionCookie is Secure when the browser reached the console over HTTPS,
// directly or through a TLS-terminating reverse proxy.
func sessionCookie(request *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   overHTTPS(request),
		SameSite: http.SameSiteStrictMode,
	}
}

func overHTTPS(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(request.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
