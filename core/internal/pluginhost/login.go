package pluginhost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// A plugin that exports login logs a provider in to an account: the host
// walks the steps it returns until done, and keeps the credential in place
// of the provider's key. A redirect step opens the vendor's page, which
// sends the browser back to the gateway's own address; a vendor that only
// redirects to its own address takes the address the browser ended on,
// pasted; a device step shows a code and polls; a fields step asks for
// values. A login expires after 15 minutes.

// LoginCallbackPath is where a vendor sends the browser back:
// /oauth/plugin/<key>/callback.
const LoginCallbackPath = "/oauth/plugin/"

// Bounds of a login.
const (
	loginTime     = 15 * time.Minute
	loginHookTime = time.Minute
	maxLoginState = 16 << 10
	maxLoginURL   = 8 << 10
)

// LoginView is what the provider page shows of a login in progress.
type LoginView struct {
	ID                      string       `json:"id"`
	Step                    string       `json:"step"`
	URL                     string       `json:"url,omitempty"`
	Hint                    string       `json:"hint,omitempty"`
	UserCode                string       `json:"user_code,omitempty"`
	VerificationURI         string       `json:"verification_uri,omitempty"`
	VerificationURIComplete string       `json:"verification_uri_complete,omitempty"`
	Interval                int          `json:"interval,omitempty"`
	Fields                  []LoginField `json:"fields,omitempty"`
	Error                   string       `json:"error,omitempty"`
	ExpiresAt               time.Time    `json:"expires_at"`
	// Account and Label are the account a finished login is.
	Account string `json:"account,omitempty"`
	Label   string `json:"label,omitempty"`
}

// LoginField is a field a fields step asks for.
type LoginField struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Description string `json:"description,omitempty"`
}

// LoginInput is what the provider page sends a login: the address a paste
// step's browser ended on, or a fields step's values.
type LoginInput struct {
	Pasted string            `json:"pasted,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

// loginFlow is a login in progress.
type loginFlow struct {
	mutex    sync.Mutex
	view     LoginView
	service  contract.Service
	target   *plugin.Plugin
	meta     *plugin.TaskMeta
	baseURL  string
	language string
	// login is ctx.login: what the host made for this login.
	state, nonce, verifier, challenge, redirect string
	session                                     json.RawMessage
	issuer                                      string
	replaced                                    json.RawMessage
	nextPoll                                    time.Time
	deadline                                    time.Time
	busy                                        bool
}

type pluginLogins struct {
	mutex sync.Mutex
	flows map[contract.ServiceID]*loginFlow
}

func randomToken(size int) string {
	value := make([]byte, size)
	_, _ = rand.Read(value)
	return base64.RawURLEncoding.EncodeToString(value)
}

// UseGatewayAddress tells the host the inference listener's address,
// which a login's browser comes back to.
func (host *Host) UseGatewayAddress(address string) {
	if host != nil {
		host.gatewayAddress.Store(address)
	}
}

// loginBase is the gateway's own address on this machine, "" before it
// listens.
func (host *Host) loginBase() string {
	address, _ := host.gatewayAddress.Load().(string)
	if address == "" {
		return ""
	}
	if hostName, port, found := strings.Cut(address, ":"); found && (hostName == "" || hostName == "0.0.0.0" || hostName == "[::]") {
		address = "127.0.0.1:" + port
	}
	return "http://" + address
}

// StartLogin starts a login of a provider of a plugin that exports login.
// A login of the provider in progress is replaced.
func (host *Host) StartLogin(ctx context.Context, service contract.Service) (LoginView, error) {
	if host == nil || host.credentials == nil || service.Plugin == nil || service.Plugin.Provider != contract.PluginProviderRelay {
		return LoginView{}, errors.New("this provider does not log in through a plugin")
	}
	target := host.plugins.Current().Plugin(service.Plugin.Key)
	if target == nil || !target.Enabled() || target.NewAPI() == nil || !target.HasTaskHook("login") {
		return LoginView{}, errors.New("the provider's plugin does not log in")
	}
	base := host.loginBase()
	if base == "" {
		return LoginView{}, errors.New("the gateway's address is unknown")
	}
	flow := &loginFlow{
		service: service, target: target, meta: target.NewAPI(), language: host.Settings().Language,
		state: randomToken(24), nonce: randomToken(24), verifier: randomToken(48), deadline: time.Now().Add(loginTime),
	}
	sum := sha256.Sum256([]byte(flow.verifier))
	flow.challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	flow.redirect = base + LoginCallbackPath + target.Key + "/callback"
	if service.HTTP != nil {
		flow.baseURL = strings.TrimRight(service.HTTP.BaseURL, "/")
	}
	if flow.baseURL == "" {
		flow.baseURL = flow.meta.BaseURL
	}
	if secret, err := host.credentials.ReadServiceSecret(ctx, service); err == nil {
		if existing, ok := decodeCredential(string(secret)); ok {
			flow.replaced = existing.Data
		}
	}
	flow.view = LoginView{ID: randomToken(12), ExpiresAt: flow.deadline}
	host.logins.mutex.Lock()
	if host.logins.flows == nil {
		host.logins.flows = map[contract.ServiceID]*loginFlow{}
	}
	host.logins.flows[service.ID] = flow
	host.logins.mutex.Unlock()
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	host.advance(ctx, flow, nil)
	return flow.view, nil
}

// Login is the provider's login in progress. A device or pending step
// polls the plugin when its interval passed.
func (host *Host) Login(ctx context.Context, id contract.ServiceID) (LoginView, bool) {
	flow := host.loginOf(id)
	if flow == nil {
		return LoginView{}, false
	}
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	if time.Now().After(flow.deadline) && flow.view.Step != "done" {
		flow.view.Step, flow.view.Error = "failed", "the login expired"
		return flow.view, true
	}
	if (flow.view.Step == "device" || flow.view.Step == "pending") && time.Now().After(flow.nextPoll) && !flow.busy {
		host.advance(ctx, flow, map[string]any{"poll": true})
	}
	return flow.view, true
}

// SubmitLogin gives a paste or fields step what the user entered.
func (host *Host) SubmitLogin(ctx context.Context, id contract.ServiceID, input LoginInput) (LoginView, error) {
	flow := host.loginOf(id)
	if flow == nil {
		return LoginView{}, errors.New("no login of this provider is in progress")
	}
	flow.mutex.Lock()
	defer flow.mutex.Unlock()
	switch {
	case time.Now().After(flow.deadline):
		return flow.view, errors.New("the login expired")
	case flow.view.Step == "paste" && input.Pasted != "":
		parsed, err := url.Parse(strings.TrimSpace(input.Pasted))
		if err != nil || len(input.Pasted) > maxLoginURL {
			return flow.view, errors.New("that is not the address the browser ended on")
		}
		query := parsed.Query()
		if parsed.Fragment != "" {
			if values, err := url.ParseQuery(parsed.Fragment); err == nil {
				for name, value := range values {
					if !query.Has(name) {
						query[name] = value
					}
				}
			}
		}
		if err := host.callback(ctx, flow, query, false); err != nil {
			return flow.view, err
		}
	case flow.view.Step == "fields" && input.Fields != nil:
		values := map[string]any{}
		for _, field := range flow.view.Fields {
			value := input.Fields[field.Name]
			if field.Required && strings.TrimSpace(value) == "" {
				return flow.view, fmt.Errorf("%s is required", field.Label)
			}
			values[field.Name] = value
		}
		host.advance(ctx, flow, map[string]any{"fields": values})
	default:
		return flow.view, errors.New("the login does not wait for that")
	}
	return flow.view, nil
}

// CancelLogin drops the provider's login in progress.
func (host *Host) CancelLogin(id contract.ServiceID) {
	host.logins.mutex.Lock()
	delete(host.logins.flows, id)
	host.logins.mutex.Unlock()
}

func (host *Host) loginOf(id contract.ServiceID) *loginFlow {
	if host == nil {
		return nil
	}
	host.logins.mutex.Lock()
	defer host.logins.mutex.Unlock()
	return host.logins.flows[id]
}

// ServeLoginCallback answers the browser a vendor sent back to
// /oauth/plugin/<key>/callback: the login of the plugin whose state the
// query carries takes the query.
func (host *Host) ServeLoginCallback(writer http.ResponseWriter, request *http.Request) {
	key, _, _ := strings.Cut(strings.TrimPrefix(request.URL.Path, LoginCallbackPath), "/")
	query := request.URL.Query()
	var found *loginFlow
	host.logins.mutex.Lock()
	for _, flow := range host.logins.flows {
		if flow.target.Key == key && (query.Get("state") == "" || query.Get("state") == flow.state) && flow.view.Step == "redirect" {
			found = flow
		}
	}
	host.logins.mutex.Unlock()
	page := func(status int, title, text string) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(status)
		_, _ = fmt.Fprintf(writer, `<!doctype html><meta charset="utf-8"><title>AstrLink</title><body style="font-family:system-ui;margin:3rem"><h2>%s</h2><p>%s</p></body>`,
			html.EscapeString(title), html.EscapeString(text))
	}
	if found == nil {
		page(http.StatusNotFound, "Login not found", "This login is not in progress in AstrLink. Start it again from the provider page.")
		return
	}
	found.mutex.Lock()
	err := host.callback(request.Context(), found, query, true)
	view := found.view
	found.mutex.Unlock()
	switch {
	case err != nil:
		page(http.StatusBadRequest, "Login failed", err.Error())
	case view.Step == "done":
		page(http.StatusOK, "Logged in", "You can close this page and go back to AstrLink.")
	case view.Step == "failed":
		page(http.StatusBadRequest, "Login failed", view.Error)
	default:
		page(http.StatusOK, "Almost done", "Go back to AstrLink to finish the login.")
	}
}

// callback checks a callback's query before the plugin reads it: its
// state, its issuer, and an error the vendor sent.
func (host *Host) callback(ctx context.Context, flow *loginFlow, query url.Values, redirected bool) error {
	if time.Now().After(flow.deadline) {
		return errors.New("the login expired")
	}
	if state := query.Get("state"); state != "" && state != flow.state {
		return errors.New("the login's state does not match")
	} else if state == "" && redirected {
		return errors.New("the callback carries no state")
	}
	if flow.issuer != "" && query.Get("iss") != flow.issuer {
		return errors.New("the callback comes from another issuer")
	}
	if code := query.Get("error"); code != "" {
		flow.view.Step, flow.view.Error = "failed", "the vendor refused the login: "+truncateRunes(code, 64)
		return nil
	}
	callback := map[string]string{}
	for name, values := range query {
		if len(values) > 0 {
			callback[name] = values[0]
		}
	}
	host.advance(ctx, flow, map[string]any{"callback": callback})
	return nil
}

// advance calls the plugin's login with input and takes the step it
// returns. A failure fails the attempt: the step can be submitted again.
func (host *Host) advance(ctx context.Context, flow *loginFlow, input any) {
	flow.busy = true
	defer func() { flow.busy = false }()
	view := map[string]any{
		"baseUrl": flow.baseURL,
		"login": map[string]any{
			"state": flow.state, "nonce": flow.nonce, "codeVerifier": flow.verifier, "codeChallenge": flow.challenge,
			"codeChallengeMethod": "S256", "redirectUri": flow.redirect, "session": jsonOrNull(flow.session),
		},
	}
	if flow.replaced != nil {
		view["credential"] = flow.replaced
	}
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), loginHookTime)
	defer cancel()
	raw, _, err := flow.target.CallTask(call, "login", []any{view, input}, credentialCallOptions(flow.meta, flow.baseURL))
	if err != nil {
		flow.view.Error = "the plugin's login failed: " + truncateRunes(loginMessage(err), 512)
		return
	}
	var step struct {
		Step                    string            `json:"step"`
		URL                     string            `json:"url"`
		Hint                    json.RawMessage   `json:"hint"`
		Issuer                  string            `json:"issuer"`
		Session                 *json.RawMessage  `json:"session"`
		UserCode                string            `json:"userCode"`
		VerificationURI         string            `json:"verificationUri"`
		VerificationURIComplete string            `json:"verificationUriComplete"`
		Interval                int               `json:"interval"`
		ExpiresIn               int               `json:"expiresIn"`
		Fields                  []map[string]any  `json:"fields"`
		Credential              json.RawMessage   `json:"credential"`
		Headers                 map[string]string `json:"headers"`
		Query                   map[string]string `json:"query"`
		ExpiresAt               int64             `json:"expiresAt"`
		AccountID               string            `json:"accountId"`
		Label                   string            `json:"label"`
	}
	if err := json.Unmarshal(raw, &step); err != nil {
		flow.view.Error = "the plugin's login returned no step"
		return
	}
	if step.Session != nil {
		if len(*step.Session) > maxLoginState {
			flow.view.Error = "the plugin's login kept more than 16 KiB"
			return
		}
		flow.session = *step.Session
	}
	flow.view.Error = ""
	switch step.Step {
	case "redirect", "paste":
		if !loginURL(step.URL) {
			flow.view.Error = "the plugin's login returned an address that is not http or https"
			return
		}
		flow.view.Step, flow.view.URL = step.Step, step.URL
		flow.view.Hint = localizedOf(step.Hint, flow.language)
		flow.issuer = step.Issuer
	case "device", "pending":
		if step.Step == "device" {
			if step.UserCode == "" || !loginURL(step.VerificationURI) || (step.VerificationURIComplete != "" && !loginURL(step.VerificationURIComplete)) {
				flow.view.Error = "the plugin's login returned no code or address"
				return
			}
			flow.view.UserCode, flow.view.VerificationURI, flow.view.VerificationURIComplete = step.UserCode, step.VerificationURI, step.VerificationURIComplete
			if step.ExpiresIn > 0 && step.ExpiresIn <= 3600 {
				flow.deadline = minTime(flow.deadline, time.Now().Add(time.Duration(step.ExpiresIn)*time.Second))
				flow.view.ExpiresAt = flow.deadline
			}
		}
		if step.Interval >= 1 && step.Interval <= 3600 {
			flow.view.Interval = step.Interval
		} else if flow.view.Interval == 0 {
			flow.view.Interval = 5
		}
		flow.view.Step = "device"
		flow.nextPoll = time.Now().Add(time.Duration(flow.view.Interval) * time.Second)
	case "fields":
		if len(step.Fields) == 0 || len(step.Fields) > 32 {
			flow.view.Error = "the plugin's login asked for 1 to 32 fields"
			return
		}
		fields := make([]LoginField, 0, len(step.Fields))
		for _, raw := range step.Fields {
			encoded, _ := json.Marshal(raw)
			var field struct {
				Name        string          `json:"name"`
				Label       json.RawMessage `json:"label"`
				Type        string          `json:"type"`
				Required    bool            `json:"required"`
				Placeholder json.RawMessage `json:"placeholder"`
				Description json.RawMessage `json:"description"`
			}
			if json.Unmarshal(encoded, &field) != nil || field.Name == "" {
				flow.view.Error = "the plugin's login asked for a field without a name"
				return
			}
			if field.Type != "" && field.Type != "text" && field.Type != "password" && field.Type != "textarea" {
				field.Type = "text"
			}
			fields = append(fields, LoginField{
				Name: field.Name, Label: localizedOf(field.Label, flow.language), Type: field.Type, Required: field.Required,
				Placeholder: localizedOf(field.Placeholder, flow.language), Description: localizedOf(field.Description, flow.language),
			})
		}
		flow.view.Step, flow.view.Fields = "fields", fields
	case "done":
		credential := PluginCredential{
			Plugin: flow.target.Key, Data: step.Credential, Headers: step.Headers, Query: step.Query,
			ExpiresAt: step.ExpiresAt, AccountID: step.AccountID, Label: truncateRunes(step.Label, 128),
		}
		for name, value := range step.Headers {
			if !validHeader(name, value) || relayProtected(http.CanonicalHeaderKey(name), "") && http.CanonicalHeaderKey(name) != "Authorization" && http.CanonicalHeaderKey(name) != "Cookie" {
				flow.view.Error = "the plugin's login returned a header a request cannot send: " + name
				return
			}
		}
		secret, err := encodeCredential(credential)
		if err == nil {
			err = host.credentials.WriteServiceSecret(context.WithoutCancel(ctx), flow.service.ID, secret)
		}
		if err != nil {
			flow.view.Error = "the credential could not be kept: " + err.Error()
			return
		}
		flow.view = LoginView{ID: flow.view.ID, Step: "done", ExpiresAt: flow.view.ExpiresAt, Account: credential.AccountID, Label: credential.Label}
	default:
		flow.view.Error = fmt.Sprintf("the plugin's login returned the step %q", step.Step)
	}
}

func loginURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && len(raw) <= maxLoginURL && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func localizedOf(raw json.RawMessage, language string) string {
	if len(raw) == 0 {
		return ""
	}
	var text plugin.LocalizedText
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	if value := text.In(language); value != "" {
		return truncateRunes(value, 512)
	}
	if strings.HasPrefix(language, "zh") {
		return truncateRunes(text.In("zh"), 512)
	}
	return ""
}

// loginMessage is what a failed login hook says: what it threw.
func loginMessage(err error) string {
	var callErr *plugin.CallError
	if errors.As(err, &callErr) {
		if callErr.Thrown != "" {
			return callErr.Thrown
		}
		if callErr.Message != "" {
			return callErr.Message
		}
	}
	message := err.Error()
	if !utf8.ValidString(message) {
		return "the hook failed"
	}
	return message
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
