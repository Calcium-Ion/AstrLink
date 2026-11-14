package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
)

// API sends the control API commands the frontend forwards to Core: each
// checks its arguments, calls one route and checks what Core answered, as
// the Tauri shell's CoreManager did. The services the app binds embed it.
type API struct {
	Caller Caller
}

// send calls Core and returns the body of a 2xx answer.
func (a *API) send(ctx context.Context, method, path string, body any, ifMatch string) (Response, error) {
	return Send(ctx, a.Caller, Request{Method: method, Path: path, Body: body, IfMatch: ifMatch})
}

// value calls Core and returns its answer as JSON; label names the answer
// in the error for one that is not JSON.
func (a *API) value(ctx context.Context, method, path string, body any, label string) (json.RawMessage, error) {
	response, err := a.send(ctx, method, path, body, "")
	if err != nil {
		return nil, err
	}
	return checkedJSON(response.Body, label)
}

// loggedValue is value that also logs a reply that is not JSON, for reads
// whose failure the page only summarizes.
func (a *API) loggedValue(ctx context.Context, method, path, label string) (json.RawMessage, error) {
	value, err := a.value(ctx, method, path, nil, label)
	if err != nil && !errors.As(err, new(*invalidJSON)) {
		return nil, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "astrlink: %s %s failed: %v\n", method, path, err)
	}
	return value, err
}

type invalidJSON struct{ message string }

func (e *invalidJSON) Error() string { return e.message }

func checkedJSON(body []byte, label string) (json.RawMessage, error) {
	if !json.Valid(body) {
		var value any
		err := json.Unmarshal(body, &value)
		return nil, &invalidJSON{fmt.Sprintf("%s returned invalid JSON: %v", label, err)}
	}
	return body, nil
}

// optional decodes an IPC argument for the validators, which work on the
// decoded value as the Rust ones worked on serde_json::Value. A missing
// argument is null.
func optional(raw json.RawMessage) (any, error) {
	if !present(raw) {
		return nil, nil
	}
	return decodeValue(raw)
}

// present reports an optional IPC argument that is set: neither missing
// nor null.
func present(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

// ---- Services ----

// Services are the API providers: their records, order, packages, usage,
// tests, probes and authorization.
type Services struct {
	*API
	// Tests registers connection tests so the window that started one can
	// cancel it.
	Tests TestRegistry
	// Owner names the window a call came from.
	Owner func(ctx context.Context) string
	// OpenURL opens a page in the system browser.
	OpenURL func(url string) error
}

// TestRegistry keeps the cancel functions of running connection tests
// (core.Manager).
type TestRegistry interface {
	RegisterServiceTest(owner, serviceID, testID string, cancel func()) (release func(), err error)
	CancelServiceTest(owner, serviceID, testID string) bool
}

// Stable error codes of the connection test. The frontend matches these
// codes instead of human-readable messages.
const (
	ServiceTestCancelled      = "service_test_cancelled"
	ServiceTestAlreadyRunning = "service_test_already_running"
	ServiceTestInvalidID      = "service_test_invalid_id"
)

// ServicePage is every API provider, read page by page.
type ServicePage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// ListServices reads every API provider. Core pages them; the desktop reads
// all pages so the frontend never pages.
func (s Services) ListServices(ctx context.Context) (ServicePage, error) {
	items := []json.RawMessage{}
	cursor := ""
	seen := map[string]bool{}
	for {
		path := ServicesPath + "?limit=200&cursor=" + PercentEncodeQuery(cursor)
		response, err := s.send(ctx, http.MethodGet, path, nil, "")
		if err != nil {
			return ServicePage{}, err
		}
		var page struct {
			Items      *[]json.RawMessage `json:"items"`
			NextCursor json.RawMessage    `json:"next_cursor"`
		}
		if err := json.Unmarshal(response.Body, &page); err != nil {
			return ServicePage{}, fmt.Errorf("invalid service list: %v", err)
		}
		if page.Items == nil {
			return ServicePage{}, errors.New("service list omitted items")
		}
		items = append(items, *page.Items...)
		cursor = ""
		_ = json.Unmarshal(page.NextCursor, &cursor)
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return ServicePage{}, errors.New("service pagination did not advance")
		}
		seen[cursor] = true
	}
	return ServicePage{Items: items}, nil
}

// GetServiceOrder reads the order the providers are tried in.
func (s Services) GetServiceOrder(ctx context.Context) (ServiceOrder, error) {
	response, err := s.send(ctx, http.MethodGet, "/control/v1/service-order", nil, "")
	if err != nil {
		return ServiceOrder{}, err
	}
	return ServiceOrderFrom(response.ETag, response.Body)
}

// UpdateServiceOrder replaces the order of the providers.
func (s Services) UpdateServiceOrder(ctx context.Context, serviceIDs []string, etag string) (ServiceOrder, error) {
	if err := validateStrongETag(etag); err != nil {
		return ServiceOrder{}, err
	}
	seen := map[string]bool{}
	for _, id := range serviceIDs {
		if err := ValidateResourceID(id); err != nil {
			return ServiceOrder{}, err
		}
		if seen[id] {
			return ServiceOrder{}, errors.New("duplicate service ID")
		}
		seen[id] = true
	}
	if serviceIDs == nil {
		serviceIDs = []string{}
	}
	body := struct {
		ServiceIDs []string `json:"service_ids"`
	}{serviceIDs}
	response, err := s.send(ctx, http.MethodPut, "/control/v1/service-order", body, etag)
	if err != nil {
		return ServiceOrder{}, err
	}
	return ServiceOrderFrom(response.ETag, response.Body)
}

// GetServicePackages reads the providers grouped by package.
func (s Services) GetServicePackages(ctx context.Context) (json.RawMessage, error) {
	response, err := s.send(ctx, http.MethodGet, "/control/v1/service-packages", nil, "")
	if err != nil {
		return nil, err
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(response.Body, &value); err != nil {
		return nil, fmt.Errorf("invalid service packages: %v", err)
	}
	var packages []json.RawMessage
	if json.Unmarshal(value["packages"], &packages) != nil || packages == nil {
		return nil, errors.New("service packages omitted packages")
	}
	return response.Body, nil
}

// UpdateServicePackage sets how a package picks among its providers.
func (s Services) UpdateServicePackage(ctx context.Context, packageID, strategy string) (json.RawMessage, error) {
	if err := validatePackageID(packageID); err != nil {
		return nil, err
	}
	if err := validatePackageStrategy(strategy); err != nil {
		return nil, err
	}
	body := struct {
		Strategy string `json:"strategy"`
	}{strategy}
	response, err := s.send(ctx, http.MethodPatch, "/control/v1/service-packages/"+PercentEncodeQuery(packageID), body, "")
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(response.Body, &value); err != nil {
		return nil, fmt.Errorf("invalid service package: %v", err)
	}
	return response.Body, nil
}

func servicePath(serviceID, suffix string) (string, error) {
	if err := ValidateResourceID(serviceID); err != nil {
		return "", err
	}
	return ServicesPath + "/" + serviceID + suffix, nil
}

func (s Services) record(ctx context.Context, method, path string, body any, ifMatch string) (ServiceRecord, error) {
	response, err := s.send(ctx, method, path, body, ifMatch)
	if err != nil {
		return ServiceRecord{}, err
	}
	return ServiceRecordFrom(response.ETag, response.Body)
}

// GetService reads one provider with its ETag.
func (s Services) GetService(ctx context.Context, serviceID string) (ServiceRecord, error) {
	path, err := servicePath(serviceID, "")
	if err != nil {
		return ServiceRecord{}, err
	}
	return s.record(ctx, http.MethodGet, path, nil, "")
}

// checkServiceInput checks the proxy and failure policy of a provider the
// page creates or patches.
func checkServiceInput(value any) error {
	o, _ := asObject(value)
	if proxy, ok := o["proxy"]; ok {
		if err := validateProxy(proxy, "", true); err != nil {
			return err
		}
	}
	if policy, ok := o["failure_policy"]; ok && policy != nil {
		return validateFailurePolicy(policy)
	}
	return nil
}

// CreateService adds a provider.
func (s Services) CreateService(ctx context.Context, input json.RawMessage) (ServiceRecord, error) {
	value, err := decodeValue(input)
	if err != nil {
		return ServiceRecord{}, err
	}
	if err := checkServiceInput(value); err != nil {
		return ServiceRecord{}, err
	}
	return s.record(ctx, http.MethodPost, ServicesPath, input, "")
}

// UpdateService patches a provider the page read at etag.
func (s Services) UpdateService(ctx context.Context, serviceID, etag string, patch json.RawMessage) (ServiceRecord, error) {
	path, err := servicePath(serviceID, "")
	if err != nil {
		return ServiceRecord{}, err
	}
	if err := validateETag(etag); err != nil {
		return ServiceRecord{}, err
	}
	value, err := decodeValue(patch)
	if err != nil {
		return ServiceRecord{}, err
	}
	if err := checkServiceInput(value); err != nil {
		return ServiceRecord{}, err
	}
	return s.record(ctx, http.MethodPatch, path, patch, etag)
}

// DeleteService removes a provider the page read at etag.
func (s Services) DeleteService(ctx context.Context, serviceID, etag string) error {
	path, err := servicePath(serviceID, "")
	if err != nil {
		return err
	}
	if err := validateETag(etag); err != nil {
		return err
	}
	_, err = s.send(ctx, http.MethodDelete, path, nil, etag)
	return err
}

// GetServiceUsage reads a subscription's quota. fresh asks Core to drop its
// 30s snapshot and query the provider now; only operator-initiated
// refreshes set it.
func (s Services) GetServiceUsage(ctx context.Context, serviceID string, fresh *bool) (json.RawMessage, error) {
	suffix := "/usage"
	if fresh != nil && *fresh {
		suffix += "?refresh=1"
	}
	path, err := servicePath(serviceID, suffix)
	if err != nil {
		return nil, err
	}
	return s.loggedValue(ctx, http.MethodGet, path, "service usage")
}

// GetServiceResetCredits reads the credits that can reset a subscription's
// quota.
func (s Services) GetServiceResetCredits(ctx context.Context, serviceID string) (json.RawMessage, error) {
	path, err := servicePath(serviceID, "/usage/reset-credits")
	if err != nil {
		return nil, err
	}
	return s.value(ctx, http.MethodGet, path, nil, "reset credits")
}

// ResetServiceUsage resets a provider's local usage.
func (s Services) ResetServiceUsage(ctx context.Context, serviceID string) (json.RawMessage, error) {
	path, err := servicePath(serviceID, "/usage/reset")
	if err != nil {
		return nil, err
	}
	value, err := s.value(ctx, http.MethodPost, path, nil, "service usage reset")
	if errors.As(err, new(*invalidJSON)) {
		fmt.Fprintf(os.Stderr, "astrlink: POST %s failed: %v\n", path, err)
	}
	return value, err
}

// TestService sends one connection test. With a testID the window can
// cancel it (CancelServiceTest); a test that finished wins over a
// cancellation that came at the same time.
func (s Services) TestService(ctx context.Context, serviceID string, input json.RawMessage, testID *string) (json.RawMessage, error) {
	if testID == nil {
		return s.testService(ctx, serviceID, input)
	}
	if !validTestIDs(serviceID, *testID) {
		return nil, errors.New(ServiceTestInvalidID)
	}
	testCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelled := make(chan struct{})
	release, err := s.Tests.RegisterServiceTest(s.owner(ctx), serviceID, *testID, sync.OnceFunc(func() {
		close(cancelled)
		cancel()
	}))
	if err != nil {
		return nil, err
	}
	defer release()
	value, err := s.testService(testCtx, serviceID, input)
	if err == nil {
		return value, nil
	}
	select {
	case <-cancelled:
		return nil, errors.New(ServiceTestCancelled)
	default:
		return nil, err
	}
}

func (s Services) testService(ctx context.Context, serviceID string, input json.RawMessage) (json.RawMessage, error) {
	path, err := servicePath(serviceID, "/test")
	if err != nil {
		return nil, err
	}
	return s.value(ctx, http.MethodPost, path, input, "service test")
}

// CancelServiceTest cancels a test this window started, reporting whether
// one was running.
func (s Services) CancelServiceTest(ctx context.Context, serviceID, testID string) bool {
	if !validTestIDs(serviceID, testID) {
		return false
	}
	return s.Tests.CancelServiceTest(s.owner(ctx), serviceID, testID)
}

func validTestIDs(serviceID, testID string) bool {
	return serviceID != "" && len(serviceID) <= 128 && testID != "" && len(testID) <= 128
}

func (s Services) owner(ctx context.Context) string {
	if s.Owner == nil {
		return ""
	}
	return s.Owner(ctx)
}

// ProbeServiceModels lists the models a saved provider offers.
func (s Services) ProbeServiceModels(ctx context.Context, serviceID string, input json.RawMessage) (json.RawMessage, error) {
	path, err := servicePath(serviceID, "/probe-models")
	if err != nil {
		return nil, err
	}
	return s.value(ctx, http.MethodPost, path, input, "service model probe")
}

// ProbeDraftServiceModels lists the models of a provider not yet saved.
func (s Services) ProbeDraftServiceModels(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(input)
	if err != nil {
		return nil, err
	}
	if o, _ := asObject(value); o != nil {
		if proxy, ok := o["proxy"]; ok {
			if err := validateProxy(proxy, "", true); err != nil {
				return nil, err
			}
		}
	}
	return s.value(ctx, http.MethodPost, ServiceModelProbesPath, input, "draft service model probe")
}

// ProbeServiceProxy checks that a custom proxy reaches the provider.
func (s Services) ProbeServiceProxy(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(input)
	if err != nil {
		return nil, err
	}
	o, _ := asObject(value)
	proxy := o["proxy"]
	if err := validateProxy(proxy, "", true); err != nil {
		return nil, err
	}
	if p, _ := asObject(proxy); p == nil || p["mode"] != "custom" {
		return nil, errors.New("proxy test requires a custom proxy")
	}
	if id, ok := o["service_id"]; ok {
		text, ok := asString(id)
		if !ok {
			return nil, errors.New("invalid service id")
		}
		if err := ValidateResourceID(text); err != nil {
			return nil, err
		}
	}
	response, err := s.send(ctx, http.MethodPost, ServiceProxyProbesPath, input, "")
	if err != nil {
		return nil, err
	}
	if !json.Valid(response.Body) {
		return nil, errors.New("proxy probe returned invalid JSON")
	}
	return response.Body, nil
}

// AuthorizationStart is a started authorization session.
type AuthorizationStart struct {
	Kind    string          `json:"kind"`
	Session json.RawMessage `json:"session"`
}

// BeginServiceAuthorization starts an OAuth or device code session and
// opens the page Core names in the browser. The session still starts when
// the browser cannot open: the page can be opened again from the app.
// `claude auth login` opens its page itself.
func (s Services) BeginServiceAuthorization(ctx context.Context, serviceID, flow string) (AuthorizationStart, error) {
	path, err := servicePath(serviceID, "/authorization")
	if err != nil {
		return AuthorizationStart{}, err
	}
	if err := validateAuthorizationFlow(flow); err != nil {
		return AuthorizationStart{}, err
	}
	body := struct {
		Flow string `json:"flow"`
	}{flow}
	response, err := s.Caller.Call(ctx, Request{Method: http.MethodPost, Path: path, Body: body})
	if err != nil {
		return AuthorizationStart{}, err
	}
	if response.Status != http.StatusAccepted {
		return AuthorizationStart{}, StatusError(http.MethodPost, path, response.Status, response.Body)
	}
	session, err := parseAuthorizationSession(response.Body)
	if err != nil {
		return AuthorizationStart{}, err
	}
	var url string
	if session["flow"] == "device_code" {
		code, _ := asObject(session["device_code"])
		url, _ = asString(code["verification_url"])
	} else {
		url, _ = asString(session["authorization_url"])
	}
	if session["flow"] != "claude_cli" && s.OpenURL != nil {
		if err := validateAuthorizationURL(url); err == nil {
			_ = s.OpenURL(url)
		}
	}
	return AuthorizationStart{Kind: "session", Session: encodeValue(session)}, nil
}

func (s Services) authorization(ctx context.Context, method, serviceID string, body any) (json.RawMessage, error) {
	path, err := servicePath(serviceID, "/authorization")
	if err != nil {
		return nil, err
	}
	response, err := s.send(ctx, method, path, body, "")
	if err != nil {
		return nil, err
	}
	session, err := parseAuthorizationSession(response.Body)
	if err != nil {
		return nil, err
	}
	return encodeValue(session), nil
}

// GetServiceAuthorization reads a provider's authorization session.
func (s Services) GetServiceAuthorization(ctx context.Context, serviceID string) (json.RawMessage, error) {
	return s.authorization(ctx, http.MethodGet, serviceID, nil)
}

// CompleteServiceAuthorization hands Core the code the operator pasted.
func (s Services) CompleteServiceAuthorization(ctx context.Context, serviceID, sessionID, code string) (json.RawMessage, error) {
	if err := ValidateResourceID(serviceID); err != nil {
		return nil, err
	}
	if err := ValidateResourceID(sessionID); err != nil {
		return nil, err
	}
	if code == "" || len(code) > 8192 {
		return nil, errors.New("authorization code is required and must be at most 8192 bytes")
	}
	body := struct {
		SessionID string `json:"session_id"`
		Code      string `json:"code"`
	}{sessionID, code}
	return s.authorization(ctx, http.MethodPut, serviceID, body)
}

// CancelServiceAuthorization ends a provider's authorization session.
func (s Services) CancelServiceAuthorization(ctx context.Context, serviceID string) (json.RawMessage, error) {
	return s.authorization(ctx, http.MethodDelete, serviceID, nil)
}

// GetClaudeCLI reads the claude CLI official Claude accounts run. The
// desktop never installs it; the status links to the official
// instructions instead.
func (s Services) GetClaudeCLI(ctx context.Context) (json.RawMessage, error) {
	response, err := s.send(ctx, http.MethodGet, "/control/v1/claude-cli", nil, "")
	if err != nil {
		return nil, err
	}
	var status any
	if err := json.Unmarshal(response.Body, &status); err != nil {
		return nil, fmt.Errorf("Claude Code status is invalid JSON: %v", err)
	}
	if _, ok := status.(map[string]any); !ok {
		return nil, errors.New("Claude Code status must be an object")
	}
	return response.Body, nil
}

// LogoutService signs a subscription out.
func (s Services) LogoutService(ctx context.Context, serviceID string) (ServiceRecord, error) {
	path, err := servicePath(serviceID, "/logout")
	if err != nil {
		return ServiceRecord{}, err
	}
	return s.record(ctx, http.MethodPost, path, nil, "")
}

// ClearServiceRisk restores scheduling for a subscription paused by an
// upstream risk signal. Core keeps the credentials and records a cleared
// event.
func (s Services) ClearServiceRisk(ctx context.Context, serviceID string) (ServiceRecord, error) {
	path, err := servicePath(serviceID, "/risk/clear")
	if err != nil {
		return ServiceRecord{}, err
	}
	return s.record(ctx, http.MethodPost, path, nil, "")
}

// ListServiceRiskEvents lists a subscription's recent upstream risk
// events, newest first.
func (s Services) ListServiceRiskEvents(ctx context.Context, serviceID string, limit *uint32) (json.RawMessage, error) {
	if err := ValidateResourceID(serviceID); err != nil {
		return nil, err
	}
	path, err := serviceRiskEventsPath(serviceID, limit)
	if err != nil {
		return nil, err
	}
	return s.loggedValue(ctx, http.MethodGet, path, "service risk events")
}

// ---- Pricing ----

// Pricing is the price catalog, the estimates and the billing of each
// provider.
type Pricing struct{ *API }

// Pricing runs one pricing operation: status, catalog, estimates, model,
// sync, summary, report, configure or backfill.
func (p Pricing) Pricing(ctx context.Context, operation string, serviceID *string, input json.RawMessage) (json.RawMessage, error) {
	const base = "/control/v1/pricing"
	decoded, err := optional(input)
	if err != nil {
		return nil, err
	}
	o, _ := asObject(decoded)
	var method, path string
	var body any
	switch operation {
	case "status", "catalog", "estimates":
		method, path = http.MethodGet, base+"/"+operation
	case "model":
		// The official model behind a gateway model; null when unknown.
		model, ok := asString(o["model"])
		if !ok {
			return nil, errors.New("model is required")
		}
		path = "/control/v1/model-metadata?model=" + PercentEncodeQuery(model)
		response, err := p.Caller.Call(ctx, Request{Method: http.MethodGet, Path: path})
		if err != nil {
			return nil, err
		}
		if response.Status == http.StatusNotFound {
			return json.RawMessage("null"), nil
		}
		if !response.OK() {
			return nil, StatusError(http.MethodGet, path, response.Status, response.Body)
		}
		if !json.Valid(response.Body) {
			return nil, errors.New("model metadata returned invalid JSON")
		}
		return response.Body, nil
	case "sync":
		method, path = http.MethodPost, base+"/sync"
	case "report", "configure", "backfill":
		if serviceID == nil {
			return nil, errors.New("service ID is required")
		}
		if err := ValidateResourceID(*serviceID); err != nil {
			return nil, err
		}
		path = base + "/services/" + *serviceID
		switch operation {
		case "configure":
			if !present(input) {
				return nil, errors.New("pricing configuration is required")
			}
			method, body = http.MethodPut, input
		case "backfill":
			method, path = http.MethodPost, path+"/backfill"
		default:
			method = http.MethodGet
		}
	case "summary":
		if !present(input) {
			return nil, errors.New("billing range is required")
		}
		from, ok := asString(o["from"])
		if !ok {
			return nil, errors.New("from is required")
		}
		to, ok := asString(o["to"])
		if !ok {
			return nil, errors.New("to is required")
		}
		method = http.MethodGet
		path = base + "/summary?from=" + PercentEncodeQuery(from) + "&to=" + PercentEncodeQuery(to)
	default:
		return nil, errors.New("unsupported pricing operation")
	}
	response, err := p.send(ctx, method, path, body, "")
	if err != nil {
		return nil, err
	}
	if !json.Valid(response.Body) {
		return nil, errors.New("pricing returned invalid JSON")
	}
	return response.Body, nil
}

// ---- Sync ----

// Sync is the configuration sync.
type Sync struct{ *API }

// SyncConfig runs one sync operation: configure stores the settings,
// passphrase sets the package passphrase, forget clears it, run syncs now,
// and status and config read the stored state.
func (s Sync) SyncConfig(ctx context.Context, operation string, input json.RawMessage) (json.RawMessage, error) {
	value, err := optional(input)
	if err != nil {
		return nil, err
	}
	req, err := syncRequest(operation, value, present(input))
	if err != nil {
		return nil, err
	}
	response, err := s.send(ctx, req.Method, req.Path, req.Body, "")
	if err != nil {
		return nil, err
	}
	if !json.Valid(response.Body) {
		return nil, errors.New("sync returned invalid JSON")
	}
	return response.Body, nil
}

// ---- Records ----

// Records are the request records, sessions and project memory.
type Records struct {
	*API
	// Changed is told when records were deleted, so the tray counts again.
	Changed func()
}

// ListRequestRecords lists request records matching query.
func (r Records) ListRequestRecords(ctx context.Context, query json.RawMessage) (json.RawMessage, error) {
	value, err := optional(query)
	if err != nil {
		return nil, err
	}
	qs, err := buildRequestRecordQuery(value)
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, "/control/v1/requests"+qs, nil, "request record list")
}

// ListRequestSessions lists request sessions matching query.
func (r Records) ListRequestSessions(ctx context.Context, query json.RawMessage) (json.RawMessage, error) {
	value, err := optional(query)
	if err != nil {
		return nil, err
	}
	qs, err := buildRequestSessionQuery(value)
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, "/control/v1/request-sessions"+qs, nil, "request session list")
}

// GetRequestActivity reads what changed since a timestamp.
func (r Records) GetRequestActivity(ctx context.Context, since string) (json.RawMessage, error) {
	if !timestampCharsetOK(since) {
		return nil, errors.New("request activity since has an invalid timestamp charset")
	}
	return r.value(ctx, http.MethodGet, "/control/v1/request-activity?since="+PercentEncodeQuery(since), nil, "request activity")
}

// GetProjectMemory reads a project's memory view.
func (r Records) GetProjectMemory(ctx context.Context, query json.RawMessage) (json.RawMessage, error) {
	value, err := optional(query)
	if err != nil {
		return nil, err
	}
	qs, err := buildProjectMemoryQuery(value, false)
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, "/control/v1/request-memory"+qs, nil, "request memory")
}

// GetProjectMemoryEntry reads one memory of a project and the turns that
// noted or read it.
func (r Records) GetProjectMemoryEntry(ctx context.Context, query json.RawMessage) (json.RawMessage, error) {
	value, err := optional(query)
	if err != nil {
		return nil, err
	}
	qs, err := buildProjectMemoryQuery(value, true)
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, "/control/v1/request-memory/entry"+qs, nil, "request memory entry")
}

// GetRequestSession reads one session.
func (r Records) GetRequestSession(ctx context.Context, sessionID string) (json.RawMessage, error) {
	if err := ValidateResourceID(sessionID); err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, "/control/v1/request-sessions/"+sessionID, nil, "request session")
}

func (r Records) channelBindings(ctx context.Context, sessionID string, release bool, before *int64) (json.RawMessage, error) {
	if err := ValidateResourceID(sessionID); err != nil {
		return nil, err
	}
	if before != nil && *before < 1 {
		return nil, errors.New("invalid binding event cursor")
	}
	path := "/control/v1/request-sessions/" + sessionID + "/channel-bindings"
	if before != nil {
		path += fmt.Sprintf("?before=%d", *before)
	}
	method := http.MethodGet
	if release {
		method = http.MethodDelete
	}
	return r.value(ctx, method, path, nil, "API provider bindings")
}

// GetSessionChannelBindings reads the providers a session is bound to,
// with the binding events before the cursor.
func (r Records) GetSessionChannelBindings(ctx context.Context, sessionID string, before *int64) (json.RawMessage, error) {
	return r.channelBindings(ctx, sessionID, false, before)
}

// ReleaseSessionChannelBindings releases a session's provider bindings.
func (r Records) ReleaseSessionChannelBindings(ctx context.Context, sessionID string) (json.RawMessage, error) {
	return r.channelBindings(ctx, sessionID, true, nil)
}

func requestPath(requestID, suffix string) (string, error) {
	if err := ValidateResourceID(requestID); err != nil {
		return "", err
	}
	return "/control/v1/requests/" + requestID + suffix, nil
}

// GetRequestRecord reads one request record.
func (r Records) GetRequestRecord(ctx context.Context, requestID string) (json.RawMessage, error) {
	path, err := requestPath(requestID, "")
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, path, nil, "request record")
}

// ListRequestRecordChildren lists the calls a request made.
func (r Records) ListRequestRecordChildren(ctx context.Context, requestID string) (json.RawMessage, error) {
	path, err := requestPath(requestID, "/children")
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, path, nil, "request record children")
}

// DeleteRequestRecord deletes one request record.
func (r Records) DeleteRequestRecord(ctx context.Context, requestID string) error {
	path, err := requestPath(requestID, "")
	if err != nil {
		return err
	}
	if _, err := r.send(ctx, http.MethodDelete, path, nil, ""); err != nil {
		return err
	}
	r.changed()
	return nil
}

// PurgeRequestRecords deletes records in bulk; Core may take minutes.
func (r Records) PurgeRequestRecords(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(input)
	if err != nil {
		return nil, err
	}
	if err := validatePurgeRequestRecordsInput(value); err != nil {
		return nil, err
	}
	purged, err := r.value(ctx, http.MethodPost, RequestPurgePath, input, "request purge")
	if err != nil {
		return nil, err
	}
	r.changed()
	return purged, nil
}

func (r Records) changed() {
	if r.Changed != nil {
		r.Changed()
	}
}

// GetRequestAuditContent reads the captured content of one request.
func (r Records) GetRequestAuditContent(ctx context.Context, requestID string) (json.RawMessage, error) {
	path, err := requestPath(requestID, "/audit")
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, path, nil, "request audit content")
}

// GetRequestContextTexts reads the texts a request's captured body
// carried: rules, memory, summary.
func (r Records) GetRequestContextTexts(ctx context.Context, requestID string) (json.RawMessage, error) {
	path, err := requestPath(requestID, "/audit/context")
	if err != nil {
		return nil, err
	}
	return r.value(ctx, http.MethodGet, path, nil, "request context texts")
}

// ---- Routing ----

// Routing is the global routing settings, the built-in tools and the
// client identities.
type Routing struct{ *API }

// BuiltinToolAction reads, saves, deletes or tests a built-in tool's
// credential.
func (r Routing) BuiltinToolAction(ctx context.Context, kind, action string, input json.RawMessage) (json.RawMessage, error) {
	if kind != "web_search" && kind != "image_generation" {
		return nil, errors.New("invalid builtin tool")
	}
	var method, suffix string
	switch action {
	case "status":
		method, suffix = http.MethodGet, "credential"
	case "save_key":
		method, suffix = http.MethodPut, "credential"
	case "delete_key":
		method, suffix = http.MethodDelete, "credential"
	case "test":
		method, suffix = http.MethodPost, "test"
	default:
		return nil, errors.New("invalid builtin tool action")
	}
	if action == "test" {
		if !present(input) {
			return nil, errors.New("missing configuration")
		}
		value, err := decodeValue(input)
		if err != nil {
			return nil, err
		}
		if err := validateBuiltinTool(kind, value); err != nil {
			return nil, err
		}
	}
	var body any
	if present(input) {
		body = input
	}
	response, err := r.send(ctx, method, "/control/v1/builtin-tools/"+kind+"/"+suffix, body, "")
	if err != nil {
		return nil, err
	}
	if !json.Valid(response.Body) {
		return nil, errors.New("invalid tool response")
	}
	return response.Body, nil
}

func (r Routing) settings(body []byte) (json.RawMessage, error) {
	value, err := decodeValue(body)
	if err != nil {
		return nil, fmt.Errorf("invalid routing settings: %v", err)
	}
	if err := validateRoutingSettings(value, false); err != nil {
		return nil, err
	}
	return body, nil
}

// GetRoutingSettings reads the global routing settings.
func (r Routing) GetRoutingSettings(ctx context.Context) (json.RawMessage, error) {
	response, err := r.send(ctx, http.MethodGet, "/control/v1/routing-settings", nil, "")
	if err != nil {
		return nil, err
	}
	return r.settings(response.Body)
}

// UpdateRoutingSettings patches the global routing settings.
func (r Routing) UpdateRoutingSettings(ctx context.Context, patch json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(patch)
	if err != nil {
		return nil, err
	}
	if err := validateRoutingSettings(value, true); err != nil {
		return nil, err
	}
	response, err := r.send(ctx, http.MethodPatch, "/control/v1/routing-settings", patch, "")
	if err != nil {
		return nil, err
	}
	return r.settings(response.Body)
}

// GetClientIdentities reads the learned and built-in client identity
// versions.
func (r Routing) GetClientIdentities(ctx context.Context) (json.RawMessage, error) {
	return r.value(ctx, http.MethodGet, "/control/v1/client-identities", nil, "client identities")
}

// ---- Audit ----

// Audit is the audit settings and the local data state.
type Audit struct{ *API }

// GetAuditSettings reads what Core captures.
func (a Audit) GetAuditSettings(ctx context.Context) (json.RawMessage, error) {
	return a.value(ctx, http.MethodGet, "/control/v1/audit-settings", nil, "audit settings")
}

// UpdateAuditSettings patches what Core captures. Enabling body capture
// needs the operator's acknowledgement in the patch.
func (a Audit) UpdateAuditSettings(ctx context.Context, patch json.RawMessage) (json.RawMessage, error) {
	value, err := decodeValue(patch)
	if err != nil {
		return nil, err
	}
	if err := validateAuditSettingsPatch(value); err != nil {
		return nil, err
	}
	return a.value(ctx, http.MethodPatch, "/control/v1/audit-settings", patch, "audit settings update")
}

// LocalDataStatus counts saved data this device can no longer decrypt.
func (a Audit) LocalDataStatus(ctx context.Context) (json.RawMessage, error) {
	return a.value(ctx, http.MethodGet, "/control/v1/local-data", nil, "local data status")
}

// ---- Tokens ----

// Tokens are the local access tokens clients use.
type Tokens struct {
	*API
	// WriteClipboard puts text on the system clipboard.
	WriteClipboard func(text string) error
}

// ListAccessTokens lists the tokens.
func (t Tokens) ListAccessTokens(ctx context.Context) (json.RawMessage, error) {
	return t.value(ctx, http.MethodGet, "/control/v1/access-tokens", nil, "access token list")
}

// ListNetworkAddresses lists the addresses other devices reach this
// machine by, read on each call so a changed network shows its current
// address.
func (t Tokens) ListNetworkAddresses(ctx context.Context) (json.RawMessage, error) {
	return t.value(ctx, http.MethodGet, "/control/v1/network-addresses", nil, "network address list")
}

// CreateAccessToken creates a token, limited to some providers when
// serviceScope is set.
func (t Tokens) CreateAccessToken(ctx context.Context, name string, serviceScope *string, serviceIDs []string) (json.RawMessage, error) {
	var body any
	if serviceScope != nil {
		if serviceIDs == nil {
			serviceIDs = []string{}
		}
		body = struct {
			Name         string   `json:"name"`
			ServiceScope string   `json:"service_scope"`
			ServiceIDs   []string `json:"service_ids"`
		}{name, *serviceScope, serviceIDs}
	} else {
		body = struct {
			Name string `json:"name"`
		}{name}
	}
	return t.value(ctx, http.MethodPost, "/control/v1/access-tokens", body, "access token create")
}

// UpdateAccessToken renames a token and replaces the providers it may use.
func (t Tokens) UpdateAccessToken(ctx context.Context, tokenID, name, serviceScope string, serviceIDs []string) (json.RawMessage, error) {
	if err := ValidateResourceID(tokenID); err != nil {
		return nil, err
	}
	for _, id := range serviceIDs {
		if err := ValidateResourceID(id); err != nil {
			return nil, err
		}
	}
	if serviceIDs == nil {
		serviceIDs = []string{}
	}
	body := struct {
		Name         string   `json:"name"`
		ServiceScope string   `json:"service_scope"`
		ServiceIDs   []string `json:"service_ids"`
	}{name, serviceScope, serviceIDs}
	return t.value(ctx, http.MethodPatch, "/control/v1/access-tokens/"+tokenID, body, "access token update")
}

// DeleteAccessToken deletes a token.
func (t Tokens) DeleteAccessToken(ctx context.Context, tokenID string) error {
	if err := ValidateResourceID(tokenID); err != nil {
		return err
	}
	_, err := t.send(ctx, http.MethodDelete, "/control/v1/access-tokens/"+tokenID, nil, "")
	return err
}

// RevealAccessToken reads a token's secret for the desktop's own use, such
// as writing a client config. It is no method of Tokens, so it is never
// bound: the secret does not reach the webview.
func RevealAccessToken(ctx context.Context, caller Caller, tokenID string) (string, error) {
	if err := ValidateResourceID(tokenID); err != nil {
		return "", err
	}
	api := &API{Caller: caller}
	value, err := api.value(ctx, http.MethodGet, "/control/v1/access-tokens/"+tokenID+"/secret", nil, "access token reveal")
	if err != nil {
		return "", err
	}
	var secret struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(value, &secret) != nil || secret.AccessToken == "" {
		return "", errors.New("access token reveal returned no token")
	}
	return secret.AccessToken, nil
}

// CopyAccessToken copies a token from Core straight to the clipboard: the
// token never reaches the webview, and no click gesture has to outlast the
// reveal. false means the clipboard refused it.
func (t Tokens) CopyAccessToken(ctx context.Context, tokenID string) (bool, error) {
	token, err := RevealAccessToken(ctx, t.Caller, tokenID)
	if err != nil {
		return false, err
	}
	if t.WriteClipboard == nil {
		return false, nil
	}
	if err := t.WriteClipboard(token); err != nil {
		fmt.Fprintf(os.Stderr, "unable to copy the access token: %v\n", err)
		return false, nil
	}
	return true, nil
}

// ---- Usage ----

// Usage is the usage and privacy summaries.
type Usage struct{ *API }

func summaryPath(route, from, to, timeZone, bucket string) string {
	return route + "?from=" + PercentEncodeQuery(from) + "&to=" + PercentEncodeQuery(to) +
		"&time_zone=" + PercentEncodeQuery(timeZone) + "&bucket=" + PercentEncodeQuery(bucket)
}

// GetUsageSummary reads the usage over a window in buckets.
func (u Usage) GetUsageSummary(ctx context.Context, from, to, timeZone, bucket string) (json.RawMessage, error) {
	return u.value(ctx, http.MethodGet, summaryPath("/control/v1/usage-summary", from, to, timeZone, bucket), nil, "usage summary")
}

// GetPrivacySummary reads what the privacy policy did over a window, with
// the usage summary's window, time zone and bucket rules.
func (u Usage) GetPrivacySummary(ctx context.Context, from, to, timeZone, bucket string) (json.RawMessage, error) {
	return u.value(ctx, http.MethodGet, summaryPath("/control/v1/privacy-summary", from, to, timeZone, bucket), nil, "privacy summary")
}

// GetImageCompressionTotals reads what request image compression has saved
// since counting began.
func (u Usage) GetImageCompressionTotals(ctx context.Context) (json.RawMessage, error) {
	return u.value(ctx, http.MethodGet, "/control/v1/image-compression-totals", nil, "image compression totals")
}

// ListAccessTokenUsage reads each token's usage today.
func (u Usage) ListAccessTokenUsage(ctx context.Context, todayFrom string) (json.RawMessage, error) {
	return u.value(ctx, http.MethodGet, "/control/v1/access-token-usage?today_from="+PercentEncodeQuery(todayFrom), nil, "access token usage")
}

// ---- Privacy ----

// Privacy is the privacy policy and its local models.
type Privacy struct {
	*API
	// PolicyChanged is told when the policy changed, so the model update
	// reminder settles at once.
	PolicyChanged func()
	// ReleasesRead is told when the page read the model releases, which
	// the reminder may not have seen.
	ReleasesRead func()
}

func (p Privacy) parsed(ctx context.Context, method, path string, body any, parse func([]byte) (json.RawMessage, error)) (json.RawMessage, error) {
	response, err := p.send(ctx, method, path, body, "")
	if err != nil {
		return nil, err
	}
	return parse(response.Body)
}

// ListPrivacyPolicies lists the privacy policies.
func (p Privacy) ListPrivacyPolicies(ctx context.Context) (json.RawMessage, error) {
	return p.parsed(ctx, http.MethodGet, "/control/v1/policies", nil, parsePrivacyPolicyPage)
}

const privacyPolicyPath = "/control/v1/policies/policy_privacy_default"

// GetPrivacyPolicy reads the privacy policy with its ETag.
func (p Privacy) GetPrivacyPolicy(ctx context.Context) (PolicyRecord, error) {
	response, err := p.send(ctx, http.MethodGet, privacyPolicyPath, nil, "")
	if err != nil {
		return PolicyRecord{}, err
	}
	return PolicyRecordFrom(response.ETag, response.Body)
}

// UpdatePrivacyPolicy patches the privacy policy the page read at etag.
func (p Privacy) UpdatePrivacyPolicy(ctx context.Context, etag string, patch json.RawMessage) (PolicyRecord, error) {
	if err := validateStrongETag(etag); err != nil {
		return PolicyRecord{}, err
	}
	value, err := decodeValue(patch)
	if err != nil {
		return PolicyRecord{}, err
	}
	if err := validatePrivacyPolicyPatch(value); err != nil {
		return PolicyRecord{}, err
	}
	response, err := p.send(ctx, http.MethodPatch, privacyPolicyPath, patch, etag)
	if err != nil {
		return PolicyRecord{}, err
	}
	record, err := PolicyRecordFrom(response.ETag, response.Body)
	if err != nil {
		return PolicyRecord{}, err
	}
	if p.PolicyChanged != nil {
		p.PolicyChanged()
	}
	return record, nil
}

func (p Privacy) checked(raw json.RawMessage, validate func(any) error) (json.RawMessage, error) {
	value, err := decodeValue(raw)
	if err != nil {
		return nil, err
	}
	if err := validate(value); err != nil {
		return nil, err
	}
	return raw, nil
}

// DryRunPrivacyPolicy shows what the policy would do to a sample.
func (p Privacy) DryRunPrivacyPolicy(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	body, err := p.checked(input, validatePrivacyDryRunInput)
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, PolicyDryRunPath, body, parsePrivacyDryRunResult)
}

// GetPrivacyRegexBuiltinRules reads the built-in detection rules.
func (p Privacy) GetPrivacyRegexBuiltinRules(ctx context.Context) (json.RawMessage, error) {
	return p.parsed(ctx, http.MethodGet, PrivacyRegexBuiltinRulesPath, nil, parsePrivacyRegexBuiltinRules)
}

// GetPrivacyModelCatalog reads the models the app offers.
func (p Privacy) GetPrivacyModelCatalog(ctx context.Context) (json.RawMessage, error) {
	return p.parsed(ctx, http.MethodGet, PrivacyModelCatalogPath, nil, parsePrivacyModelCatalog)
}

// GetPrivacyModelReleases reads the catalog with the newest releases.
// refresh asks Core to skip its hour-long release cache and query the Hub
// now; only operator-initiated refreshes set it.
func (p Privacy) GetPrivacyModelReleases(ctx context.Context, refresh *bool) (json.RawMessage, error) {
	path := PrivacyModelReleasesPath
	if refresh != nil && *refresh {
		path += "?refresh=1"
	}
	releases, err := p.parsed(ctx, http.MethodGet, path, nil, parsePrivacyModelCatalog)
	if err != nil {
		return nil, err
	}
	if p.ReleasesRead != nil {
		p.ReleasesRead()
	}
	return releases, nil
}

// ProbePrivacyModel reads a Hub model's files before it is installed.
func (p Privacy) ProbePrivacyModel(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	body, err := p.checked(input, validatePrivacyModelProbeInput)
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, PrivacyModelProbePath, body, parsePrivacyModelProbe)
}

// ProbeLocalPrivacyModel reads a model directory on this machine.
func (p Privacy) ProbeLocalPrivacyModel(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	body, err := p.checked(input, validateLocalPrivacyModelProbeInput)
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, LocalPrivacyModelProbePath, body, parsePrivacyModelProbe)
}

// ListPrivacyModelInstallations lists the installed models.
func (p Privacy) ListPrivacyModelInstallations(ctx context.Context) (json.RawMessage, error) {
	return p.parsed(ctx, http.MethodGet, PrivacyModelsPath, nil, parsePrivacyModelInstallationList)
}

// InstallPrivacyModel installs a model; its weights download afterwards.
func (p Privacy) InstallPrivacyModel(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	body, err := p.checked(input, validatePrivacyModelInstallInput)
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, PrivacyModelsPath, body, parsePrivacyModelInstallation)
}

func installationPath(installationID, suffix string) (string, error) {
	if err := validatePrivacyModelID(installationID); err != nil {
		return "", err
	}
	return PrivacyModelsPath + "/" + installationID + suffix, nil
}

// GetPrivacyModelInstallation reads one installed model.
func (p Privacy) GetPrivacyModelInstallation(ctx context.Context, installationID string) (json.RawMessage, error) {
	path, err := installationPath(installationID, "")
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodGet, path, nil, parsePrivacyModelInstallation)
}

// PausePrivacyModelInstallation pauses a model's download.
func (p Privacy) PausePrivacyModelInstallation(ctx context.Context, installationID string) (json.RawMessage, error) {
	path, err := installationPath(installationID, "/pause")
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, path, nil, parsePrivacyModelInstallation)
}

// ResumePrivacyModelInstallation resumes a model's download.
func (p Privacy) ResumePrivacyModelInstallation(ctx context.Context, installationID string) (json.RawMessage, error) {
	path, err := installationPath(installationID, "/resume")
	if err != nil {
		return nil, err
	}
	return p.parsed(ctx, http.MethodPost, path, nil, parsePrivacyModelInstallation)
}

// DeletePrivacyModelInstallation deletes an installed model and its files.
func (p Privacy) DeletePrivacyModelInstallation(ctx context.Context, installationID string) error {
	path, err := installationPath(installationID, "")
	if err != nil {
		return err
	}
	_, err = p.send(ctx, http.MethodDelete, path, nil, "")
	return err
}
