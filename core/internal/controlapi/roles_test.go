package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const testObserverToken = "test-observer-token-0123456789"

type roleCaller struct {
	name string
	role Role
	set  func(*http.Request)
}

var roleCallers = []roleCaller{
	{name: "socket", role: RoleObserver, set: func(request *http.Request) {
		*request = *request.WithContext(ContextWithLocalSocketAuth(request.Context()))
	}},
	{name: "observer token", role: RoleObserver, set: func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
	}},
	{name: "operator token", role: RoleOperator, set: func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+testControlToken)
	}},
	{name: "operator token on socket", role: RoleOperator, set: func(request *http.Request) {
		*request = *request.WithContext(ContextWithLocalSocketAuth(request.Context()))
		request.Header.Set("Authorization", "Bearer "+testControlToken)
	}},
	{name: "console session", role: RoleOperator, set: func(request *http.Request) {
		*request = *request.WithContext(ContextWithConsoleSession(request.Context()))
	}},
	{name: "console session with observer token", role: RoleOperator, set: func(request *http.Request) {
		*request = *request.WithContext(ContextWithConsoleSession(request.Context()))
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
	}},
}

func newRoleMatrixHandler(t *testing.T) *Handler {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatalf("access token manager: %v", err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore:       store,
		PricingStore:       store,
		AccessTokenManager: tokens,
		PolicyStore:        store,
		RequestRecords:     store,
		AuditSettings:      store,
		AuditKeys:          store,
		AuditBlobs:         store,
		LocalData:          store,
		ClientIdentities:   accountauth.NewIdentityRegistry(store, store),
		ControlToken:       testControlToken,
		ObserverToken:      testObserverToken,
		Shutdown:           func() {},
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	return handler
}

func TestControlRoleMatrix(t *testing.T) {
	handler := newRoleMatrixHandler(t)
	// Every route is listed with the least role it accepts. Observer rows are
	// the agent-readable surface; operator rows are the §5.10 "cannot do"
	// column plus every other write.
	routes := []struct {
		method string
		path   string
		body   string
		role   Role
	}{
		{http.MethodGet, ObserversPath, "", RoleObserver},
		{http.MethodGet, RequestsPath, "", RoleObserver},
		{http.MethodGet, RequestsPath + "/req_missing", "", RoleObserver},
		{http.MethodGet, RequestsPath + "/req_missing/audit?view=shareable", "", RoleObserver},
		{http.MethodPost, RequestsPath + "/req_missing/audit/raw-access", `{"reason":"debug"}`, RoleObserver},
		{http.MethodGet, RequestsPath + "/req_missing/children", "", RoleObserver},
		{http.MethodGet, RequestSessionsPath, "", RoleObserver},
		{http.MethodGet, UsageSummaryPath, "", RoleObserver},
		{http.MethodGet, AccessTokenUsagePath, "", RoleObserver},
		{http.MethodGet, AuditSettingsPath, "", RoleObserver},
		// Observers get the reduced raw_available view.
		{http.MethodGet, RawSealingPath, "", RoleObserver},
		{http.MethodGet, RoutingSettingsPath, "", RoleObserver},
		{http.MethodGet, ServicesPath, "", RoleObserver},
		{http.MethodGet, ServicesPath + "/service_missing", "", RoleObserver},
		{http.MethodGet, ServicesPath + "/service_missing/risk-events", "", RoleObserver},
		{http.MethodGet, PoliciesPath, "", RoleObserver},
		{http.MethodGet, AccessTokensPath, "", RoleObserver},
		{http.MethodGet, ServiceOrderPath, "", RoleObserver},
		{http.MethodGet, LocalDataPath, "", RoleObserver},
		{http.MethodGet, ClientIdentitiesPath, "", RoleObserver},

		{http.MethodGet, AccessTokensPath + "/tok_missing/secret", "", RoleOperator},
		// The full view needs the operator role or an approved raw grant.
		{http.MethodGet, RequestsPath + "/req_missing/audit", "", RoleOperator},
		{http.MethodGet, RawAccessPath, "", RoleOperator},
		{http.MethodPost, RawAccessPath + "/rawgrant_0000000000000000/decision", `{"decision":"deny"}`, RoleOperator},
		{http.MethodPost, RawPasswordPath, `{"action":"set","password":"long enough"}`, RoleOperator},
		{http.MethodPost, RawUnlockPath, `{"proof":{"password":"long enough"}}`, RoleOperator},
		{http.MethodPost, RawLockPath, "", RoleOperator},
		{http.MethodPost, RawVerifyPath, `{"proof":{"password":"long enough"}}`, RoleOperator},
		{http.MethodPost, AccessTokensPath, `{"name":"x"}`, RoleOperator},
		{http.MethodDelete, AccessTokensPath + "/tok_missing", "", RoleOperator},
		{http.MethodPatch, AuditSettingsPath, `{}`, RoleOperator},
		{http.MethodPatch, RoutingSettingsPath, `{}`, RoleOperator},
		{http.MethodPost, RequestsPurgePath, `{}`, RoleOperator},
		{http.MethodDelete, RequestsPath + "/req_missing", "", RoleOperator},
		{http.MethodPost, ServicesPath, `{}`, RoleOperator},
		{http.MethodPatch, ServicesPath + "/service_missing", `{}`, RoleOperator},
		{http.MethodDelete, ServicesPath + "/service_missing", "", RoleOperator},
		{http.MethodGet, ServicesPath + "/service_missing/authorization", "", RoleOperator},
		{http.MethodPost, ServicesPath + "/service_missing/authorization", `{}`, RoleOperator},
		{http.MethodPost, ServicesPath + "/service_missing/test", `{}`, RoleOperator},
		{http.MethodPost, ServiceModelProbesPath, `{}`, RoleOperator},
		{http.MethodPost, ServiceProxyProbesPath, `{}`, RoleOperator},
		{http.MethodGet, BuiltinToolsPath + "web_search/credential", "", RoleOperator},
		{http.MethodPut, BuiltinToolsPath + "web_search/credential", `{"secret":"x"}`, RoleOperator},
		{http.MethodPatch, PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID), `{}`, RoleOperator},
		{http.MethodPut, ServiceOrderPath, `{}`, RoleOperator},
		{http.MethodPost, ShutdownPath, "", RoleOperator},
	}
	for _, route := range routes {
		for _, caller := range roleCallers {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			request.Header.Set("Content-Type", "application/json")
			caller.set(request)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			allowed := caller.role >= route.role
			switch {
			case recorder.Code == http.StatusUnauthorized:
				t.Fatalf("%s %s as %s: 401 body=%s", route.method, route.path, caller.name, recorder.Body.String())
			case allowed && recorder.Code == http.StatusForbidden:
				t.Fatalf("%s %s as %s: 403, want access body=%s", route.method, route.path, caller.name, recorder.Body.String())
			case !allowed && recorder.Code != http.StatusForbidden:
				t.Fatalf("%s %s as %s: %d, want 403 body=%s", route.method, route.path, caller.name, recorder.Code, recorder.Body.String())
			}
		}
	}
}

func TestControlRoleRejectsUnknownBearerAndBareHTTP(t *testing.T) {
	handler := newRoleMatrixHandler(t)
	for _, authorization := range []string{"", "Bearer wrong-token-0123456789", "Bearer " + testObserverToken + "x", "Basic " + testControlToken} {
		request := httptest.NewRequest(http.MethodGet, RequestsPath, nil)
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status = %d, want 401", authorization, recorder.Code)
		}
	}
}

func TestObserverTokenIsOptionalAndDistinct(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	version := contract.DefaultVersionResponse("0.1.0-test", "abc1234")
	if _, err := NewWithDependencies(version, Dependencies{ServiceStore: store, ControlToken: testControlToken, ObserverToken: testControlToken}); err == nil {
		t.Fatal("observer token equal to the control token was accepted")
	}
	if _, err := NewWithDependencies(version, Dependencies{ServiceStore: store, ControlToken: testControlToken, ObserverToken: "short"}); err == nil {
		t.Fatal("short observer token was accepted")
	}
	handler, err := NewWithDependencies(version, Dependencies{ServiceStore: store, ControlToken: testControlToken})
	if err != nil {
		t.Fatalf("NewWithDependencies without observer token: %v", err)
	}
	// Without a configured observer token an empty bearer must never match it.
	request := httptest.NewRequest(http.MethodGet, ServicesPath, nil)
	request.Header.Set("Authorization", "Bearer ")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("empty bearer status = %d, want 401", recorder.Code)
	}
}

func TestForbiddenObserverCallsStillCountAsObserved(t *testing.T) {
	handler := newRoleMatrixHandler(t)
	request := httptest.NewRequest(http.MethodPatch, AuditSettingsPath, strings.NewReader(`{}`))
	request = request.WithContext(ContextWithLocalSocketAuth(request.Context()))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if snapshot := handler.observers.snapshot(context.Background()); snapshot.Requests != 1 || snapshot.Client != observerUserAgentPrefix {
		t.Fatalf("observer snapshot = %+v", snapshot)
	}
}

func TestObserversReadPolicySummaries(t *testing.T) {
	handler := newRoleMatrixHandler(t)
	itemPath := PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID)
	call := func(caller roleCaller, method, path, body, etag string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		caller.set(request)
		if body != "" {
			request.Header.Set("Content-Type", "application/merge-patch+json")
			request.Header.Set("If-Match", etag)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s %s = %d %s", caller.name, method, path, response.Code, response.Body.String())
		}
		return response
	}
	operator := roleCallers[2]
	response := call(operator, http.MethodGet, itemPath, "", "")
	call(operator, http.MethodPatch, itemPath, `{
		"regex_source":"custom",
		"custom_regex_rules":[{"kind":"email","pattern":"guarded-pattern@[a-z.]+"},{"kind":"email","pattern":"second@x"}],
		"allowlist_rules":[{"type":"literal","value":"guarded-literal"},{"type":"cidr","value":"10.9.0.0/16"},{"type":"literal","value":"guarded-two"}]
	}`, response.Header().Get("ETag"))
	guarded := []string{"guarded-pattern", "second@x", "guarded-literal", "10.9.0.0/16", "guarded-two", "allowlist_rules", "pattern"}

	for _, caller := range roleCallers {
		t.Run(caller.name, func(t *testing.T) {
			list := call(caller, http.MethodGet, PoliciesPath, "", "")
			item := call(caller, http.MethodGet, itemPath, "", "")
			if caller.role == RoleOperator {
				var page policyPageResponse
				decode(t, list, &page)
				var policy contract.Policy
				decode(t, item, &policy)
				if len(page.Items) != 1 || len(page.Items[0].AllowlistRules) != 3 ||
					policy.AllowlistRules[0].Value != "guarded-literal" || policy.CustomRegexRules[0].Pattern != "guarded-pattern@[a-z.]+" {
					t.Fatalf("operator policy = %+v / %+v", page.Items, policy)
				}
				if item.Header().Get("ETag") == "" {
					t.Fatal("operator read lost the ETag it patches with")
				}
				return
			}
			for _, body := range []string{list.Body.String(), item.Body.String()} {
				for _, value := range guarded {
					if strings.Contains(body, value) {
						t.Fatalf("observer read %q: %s", value, body)
					}
				}
			}
			if etag := item.Header().Get("ETag"); etag != "" {
				t.Fatalf("observer got the document hash %q", etag)
			}
			var page policySummaryPageResponse
			decode(t, list, &page)
			var summary PolicySummary
			decode(t, item, &summary)
			if len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0], summary) {
				t.Fatalf("list %+v differs from item %+v", page.Items, summary)
			}
			if summary.CustomRegexRules.Count != 2 || !reflect.DeepEqual(summary.CustomRegexRules.Kinds, []string{"email"}) ||
				summary.Allowlist.Count != 3 || summary.Allowlist.ByType[contract.PolicyAllowlistTypeLiteral] != 2 ||
				summary.Allowlist.ByType[contract.PolicyAllowlistTypeCIDR] != 1 || summary.RegexSource != contract.PolicyRegexSourceCustom {
				t.Fatalf("observer summary = %+v", summary)
			}
		})
	}
}
