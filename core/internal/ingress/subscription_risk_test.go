package ingress

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
)

func TestClassifySubscriptionRisk(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	claude, codex := contract.ServiceKindClaudeSubscription, contract.ServiceKindCodexSubscription
	anthropicError := func(kind, message string) string {
		return fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, kind, message)
	}
	header := func(pairs ...string) http.Header {
		result := make(http.Header)
		for index := 0; index+1 < len(pairs); index += 2 {
			result.Set(pairs[index], pairs[index+1])
		}
		return result
	}
	tests := []struct {
		name         string
		kind         contract.ServiceKind
		status       int
		header       http.Header
		body         string
		scope        subscriptionRiskScope
		state        contract.SubscriptionRiskState
		code         string
		until        time.Time
		cooldown     time.Duration
		forbidden    bool
		unauthorized bool
	}{
		{name: "claude organization disabled", kind: claude, status: 400, body: anthropicError("invalid_request_error", "This organization has been disabled."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeOrganizationDisabled},
		{name: "claude oauth not allowed", kind: claude, status: 403, body: anthropicError("permission_error", "OAuth authentication is currently not allowed for this organization."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeOAuthNotAllowed},
		{name: "claude identity verification", kind: claude, status: 403, body: anthropicError("permission_error", "Identity verification is required to continue."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeIdentityVerificationRequired},
		{name: "claude credit balance", kind: claude, status: 400, body: anthropicError("invalid_request_error", "Your credit balance is too low to access the Anthropic API."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeCreditBalanceLow},
		{name: "claude credit balance needs 400", kind: claude, status: 403, body: anthropicError("permission_error", "credit balance"),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeForbidden, until: now.Add(subscriptionForbiddenCooldown), forbidden: true},
		{name: "claude client identity rejected", kind: claude, status: 403, body: anthropicError("permission_error", "This credential is only authorized for use with Claude Code and cannot be used for other API requests."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeClientIdentityRejected, until: now.Add(subscriptionForbiddenCooldown), forbidden: true},
		{name: "claude generic forbidden", kind: claude, status: 403, body: anthropicError("permission_error", "Permission denied."),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeForbidden, until: now.Add(subscriptionForbiddenCooldown), forbidden: true},
		{name: "claude model entitlement forbidden", kind: claude, status: 403, body: anthropicError("permission_error", "Your plan does not include access to model claude-opus-9.")},
		{name: "claude cloudflare html", kind: claude, status: 403, body: `<!DOCTYPE html><html><title>Attention Required! | Cloudflare</title><body>error code: 1010</body></html>`},
		{name: "claude plain text 403", kind: claude, status: 403, body: "error code: 1010"},
		{name: "claude invalid request", kind: claude, status: 400, body: anthropicError("invalid_request_error", "messages: text content blocks must be non-empty")},
		{name: "claude html 400", kind: claude, status: 400, body: `<html><body>organization has been disabled</body></html>`},
		{name: "claude unauthorized", kind: claude, status: 401, body: anthropicError("authentication_error", "invalid x-api-key"), unauthorized: true},
		{name: "claude five hour window", kind: claude, status: 429, body: anthropicError("rate_limit_error", "rate limited"),
			header: header("Anthropic-Ratelimit-Unified-5h-Status", "rejected", "Anthropic-Ratelimit-Unified-5h-Reset", fmt.Sprint(reset.Unix())),
			scope:  subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeRateLimit5h, until: reset},
		{name: "claude seven day window uses unified reset", kind: claude, status: 429, body: anthropicError("rate_limit_error", "rate limited"),
			header: header("Anthropic-Ratelimit-Unified-7d-Status", "rejected", "Anthropic-Ratelimit-Unified-Reset", reset.Format(time.RFC3339)),
			scope:  subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeRateLimit7d, until: reset},
		{name: "claude window without reset falls back", kind: claude, status: 429,
			header: header("Anthropic-Ratelimit-Unified-5h-Status", "rejected", "Retry-After", "60"),
			scope:  subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeRateLimit5h, until: now.Add(subscriptionRiskFallbackPause)},
		{name: "claude window ignores implausible reset", kind: claude, status: 429,
			header: header("Anthropic-Ratelimit-Unified-5h-Status", "rejected", "Anthropic-Ratelimit-Unified-5h-Reset", fmt.Sprint(now.Add(30*24*time.Hour).Unix()), "Retry-After", "900"),
			scope:  subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeRateLimit5h, until: now.Add(15 * time.Minute)},
		{name: "claude overage only cools model", kind: claude, status: 429,
			header: header("Anthropic-Ratelimit-Unified-Status", "rejected", "Anthropic-Ratelimit-Unified-Reset", fmt.Sprint(reset.Unix())),
			scope:  subscriptionRiskModel, cooldown: 3 * time.Hour},
		{name: "claude plain rate limit", kind: claude, status: 429, header: header("Retry-After", "30"), body: anthropicError("rate_limit_error", "slow down")},
		{name: "codex deactivated workspace", kind: codex, status: 402, body: `{"detail":{"code":"deactivated_workspace"}}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeAccountDeactivated},
		{name: "codex account suspended", kind: codex, status: 403, body: `{"error":{"code":"account_suspended","message":"Your account has been suspended."}}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeAccountDeactivated},
		{name: "codex payment detail", kind: codex, status: 402, body: `{"detail":{"code":"workspace_plan_expired","message":"Plan expired"}}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskSuspended, code: contract.RiskCodeAccountDeactivated},
		{name: "codex usage limit seconds", kind: codex, status: 429, body: `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","resets_in_seconds":10800}}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeUsageLimitReached, until: reset},
		{name: "codex usage limit timestamp", kind: codex, status: 429, body: fmt.Sprintf(`{"error":{"type":"usage_limit_reached","resets_at":%d}}`, reset.Unix()),
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeUsageLimitReached, until: reset},
		{name: "codex usage limit without reset", kind: codex, status: 429, body: `{"error":{"type":"usage_limit_reached"}}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeUsageLimitReached, until: now.Add(subscriptionRiskFallbackPause)},
		{name: "codex plain rate limit", kind: codex, status: 429, body: `{"error":{"type":"rate_limit_exceeded","message":"Rate limit reached"}}`},
		{name: "codex generic forbidden", kind: codex, status: 403, body: `{"detail":"Forbidden"}`,
			scope: subscriptionRiskAccount, state: contract.SubscriptionRiskCooling, code: contract.RiskCodeForbidden, until: now.Add(subscriptionForbiddenCooldown), forbidden: true},
		{name: "codex cloudflare html", kind: codex, status: 403, body: `<html><body>Just a moment...</body></html>`},
		{name: "codex unsupported parameter", kind: codex, status: 400, body: `{"error":{"message":"Unsupported parameter: temperature","type":"invalid_request_error","param":"temperature"}}`},
		{name: "codex unauthorized", kind: codex, status: 401, body: `{"error":{"code":"token_invalidated"}}`, unauthorized: true},
		{name: "grok is not classified", kind: contract.ServiceKindGrokSubscription, status: 403, body: anthropicError("permission_error", "This organization has been disabled.")},
		{name: "api key service is not classified", kind: contract.ServiceKindAnthropic, status: 400, body: anthropicError("invalid_request_error", "This organization has been disabled.")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			responseHeader := test.header
			if responseHeader == nil {
				responseHeader = make(http.Header)
			}
			signal := classifySubscriptionRisk(test.kind, test.status, responseHeader, []byte(test.body), now)
			if signal.scope != test.scope || signal.unauthorized != test.unauthorized {
				t.Fatalf("signal = %+v, want scope %d unauthorized %t", signal, test.scope, test.unauthorized)
			}
			if signal.scope == subscriptionRiskModel && signal.cooldown != test.cooldown {
				t.Fatalf("model cooldown = %s, want %s", signal.cooldown, test.cooldown)
			}
			if signal.scope != subscriptionRiskAccount {
				return
			}
			observation := signal.observation
			if observation.State != test.state || observation.Code != test.code || observation.Forbidden != test.forbidden ||
				observation.HTTPStatus != test.status {
				t.Fatalf("observation = %+v, want %s/%s forbidden=%t status=%d", observation, test.state, test.code, test.forbidden, test.status)
			}
			if test.until.IsZero() != (observation.PausedUntil == nil) ||
				observation.PausedUntil != nil && !observation.PausedUntil.Equal(test.until) {
				t.Fatalf("paused_until = %v, want %v", observation.PausedUntil, test.until)
			}
		})
	}
}

func TestClassifySubscriptionStreamError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		kind  contract.ServiceKind
		event string
		state contract.SubscriptionRiskState
		code  string
	}{
		{"codex usage limit", contract.ServiceKindCodexSubscription,
			`{"type":"response.failed","response":{"error":{"code":"usage_limit_reached","message":"limit","resets_in_seconds":60}}}`,
			contract.SubscriptionRiskCooling, contract.RiskCodeUsageLimitReached},
		{"codex deactivated", contract.ServiceKindCodexSubscription,
			`{"type":"error","error":{"type":"invalid_request_error","code":"deactivated_workspace"}}`,
			contract.SubscriptionRiskSuspended, contract.RiskCodeAccountDeactivated},
		{"claude organization disabled", contract.ServiceKindClaudeSubscription,
			`{"type":"error","error":{"type":"permission_error","message":"This organization has been disabled."}}`,
			contract.SubscriptionRiskSuspended, contract.RiskCodeOrganizationDisabled},
		{"claude overloaded", contract.ServiceKindClaudeSubscription,
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "", ""},
		{"codex request failure", contract.ServiceKindCodexSubscription,
			`{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`, "", ""},
		{"content mentioning an error", contract.ServiceKindCodexSubscription,
			`{"type":"response.output_text.delta","delta":"deactivated_workspace error"}`, "", ""},
		{"stream 403 is never counted", contract.ServiceKindClaudeSubscription,
			`{"type":"error","error":{"type":"permission_error","message":"Permission denied."}}`, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			signal := classifySubscriptionStreamError(test.kind, []byte(test.event), now)
			if test.code == "" {
				if signal.scope != subscriptionRiskNone {
					t.Fatalf("signal = %+v, want none", signal)
				}
				return
			}
			if signal.scope != subscriptionRiskAccount || signal.observation.State != test.state ||
				signal.observation.Code != test.code || signal.observation.HTTPStatus != 0 {
				t.Fatalf("signal = %+v, want %s/%s", signal, test.state, test.code)
			}
		})
	}
}

func TestSubscriptionStreamRiskReaderPassesBytesAndReportsOnce(t *testing.T) {
	t.Parallel()
	oversized := `data: {"type":"response.output_text.delta","delta":"` + strings.Repeat("x", subscriptionRiskInspectBytes) + `","error":"deactivated_workspace"}`
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		"",
		oversized,
		"",
		"event: error",
		"data: {\"type\":\"error\",\"error\":{\"code\":\"deactivated_workspace\"}}\r",
		"",
		"event: error",
		`data: {"type":"response.failed","response":{"error":{"code":"usage_limit_reached"}}}`,
		"",
	}, "\n")
	var reports []subscriptionRiskSignal
	reader := newSubscriptionStreamRiskReader(
		io.NopCloser(iotest.HalfReader(strings.NewReader(stream))),
		contract.ServiceKindCodexSubscription,
		func(signal subscriptionRiskSignal) { reports = append(reports, signal) },
	)
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stream {
		t.Fatal("stream reader changed the response bytes")
	}
	if len(reports) != 1 || reports[0].observation.Code != contract.RiskCodeAccountDeactivated {
		t.Fatalf("reports = %+v, want one account_deactivated", reports)
	}

	var oversizedReports atomic.Int32
	reader = newSubscriptionStreamRiskReader(
		io.NopCloser(strings.NewReader(oversized+"\n\n")),
		contract.ServiceKindCodexSubscription,
		func(subscriptionRiskSignal) { oversizedReports.Add(1) },
	)
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if oversizedReports.Load() != 0 {
		t.Fatal("oversized content line was classified")
	}
}

type recordedSubscriptionRisk struct {
	id          contract.ServiceID
	observation contract.SubscriptionRiskObservation
}

type recordingSubscriptionRisk struct {
	mu        sync.Mutex
	reports   []recordedSubscriptionRisk
	cleared   []contract.ServiceID
	reported  chan struct{}
	refreshed chan string
}

func newRecordingSubscriptionRisk() *recordingSubscriptionRisk {
	return &recordingSubscriptionRisk{reported: make(chan struct{}, 8), refreshed: make(chan string, 8)}
}

func (reporter *recordingSubscriptionRisk) ReportSubscriptionRisk(
	_ context.Context,
	id contract.ServiceID,
	observation contract.SubscriptionRiskObservation,
) error {
	reporter.mu.Lock()
	reporter.reports = append(reporter.reports, recordedSubscriptionRisk{id: id, observation: observation})
	reporter.mu.Unlock()
	reporter.reported <- struct{}{}
	return nil
}

func (reporter *recordingSubscriptionRisk) ClearExpiredSubscriptionRisk(_ context.Context, id contract.ServiceID) error {
	reporter.mu.Lock()
	reporter.cleared = append(reporter.cleared, id)
	reporter.mu.Unlock()
	return nil
}

func (reporter *recordingSubscriptionRisk) RefreshRejectedSubscriptionToken(_ context.Context, id contract.ServiceID, token string) error {
	reporter.refreshed <- string(id) + ":" + token
	return nil
}

func (reporter *recordingSubscriptionRisk) snapshot() ([]recordedSubscriptionRisk, []contract.ServiceID) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	return append([]recordedSubscriptionRisk(nil), reporter.reports...), append([]contract.ServiceID(nil), reporter.cleared...)
}

type riskUpstream struct {
	hits atomic.Int32
	url  string
}

func newRiskUpstream(t *testing.T, status int, contentType, body string, header http.Header) *riskUpstream {
	t.Helper()
	upstream := &riskUpstream{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstream.hits.Add(1)
		for name, values := range header {
			writer.Header()[name] = values
		}
		writer.Header().Set("Content-Type", contentType)
		writer.WriteHeader(status)
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(server.Close)
	upstream.url = server.URL
	return upstream
}

func riskCodexCandidate(id contract.ServiceID, baseURL string) endpoint.Resolved {
	candidate := codexVersionCandidate(baseURL + "/backend-api/codex")
	candidate.Service.ID = id
	candidate.Service.Subscription.CredentialRef = accountauth.CredentialRefFor(id)
	policy := contract.DefaultFailurePolicy()
	policy.MaxRetries, policy.InitialDelayMS = 2, 0
	candidate.FailurePolicy = &policy
	return candidate
}

func serveRiskRequest(t *testing.T, reporter SubscriptionRiskReporter, stream bool, candidates ...endpoint.Resolved) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewWithDependencies(Dependencies{
		Resolver:         candidateResolver{candidates: candidates},
		Authorizer:       endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		SubscriptionRisk: reporter,
	})
	body := fmt.Sprintf(`{"model":"gpt-6-astra","input":"hello","stream":%t}`, stream)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func waitForRiskSignal[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subscription risk signal")
		var zero T
		return zero
	}
}

func TestSubscriptionUsageLimitPausesAccountAndFailsOver(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	limited := newRiskUpstream(t, http.StatusTooManyRequests, "application/json",
		fmt.Sprintf(`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached","resets_at":%d}}`, reset.Unix()), nil)
	healthy := newRiskUpstream(t, http.StatusOK, "application/json", `{"id":"resp_ok","object":"response","output":[]}`, nil)
	reporter := newRecordingSubscriptionRisk()
	first := riskCodexCandidate("service_codex_limited", limited.url)
	// A second route of the paused account must not be attempted either.
	sameAccount := first
	sameAccount.UpstreamModel = "gpt-6-astra"
	response := serveRiskRequest(t, reporter, false, first, sameAccount, riskCodexCandidate("service_codex_healthy", healthy.url))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "resp_ok") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if limited.hits.Load() != 1 || healthy.hits.Load() != 1 {
		t.Fatalf("limited hits = %d, healthy hits = %d; want 1 and 1", limited.hits.Load(), healthy.hits.Load())
	}
	reports, _ := reporter.snapshot()
	if len(reports) != 1 || reports[0].id != "service_codex_limited" {
		t.Fatalf("reports = %+v", reports)
	}
	observation := reports[0].observation
	if observation.State != contract.SubscriptionRiskCooling || observation.Code != contract.RiskCodeUsageLimitReached ||
		observation.PausedUntil == nil || observation.PausedUntil.Before(reset.Add(time.Second)) ||
		observation.PausedUntil.After(reset.Add(time.Duration(subscriptionRiskMaxJitterUnits)*time.Second)) {
		t.Fatalf("observation = %+v, want usage limit until %s plus jitter", observation, reset)
	}
}

func TestSubscriptionSuspensionFailsOverAndLeavesLastErrorIntact(t *testing.T) {
	t.Parallel()
	body := `{"error":{"code":"deactivated_workspace","message":"Workspace deactivated"}}`
	deactivated := newRiskUpstream(t, http.StatusPaymentRequired, "application/json", body, nil)
	reporter := newRecordingSubscriptionRisk()
	response := serveRiskRequest(t, reporter, false, riskCodexCandidate("service_codex_deactivated", deactivated.url))

	// With no other account, the client receives the provider error unchanged.
	if response.Code != http.StatusPaymentRequired || response.Body.String() != body {
		t.Fatalf("response = %d %q, want provider error", response.Code, response.Body.String())
	}
	if deactivated.hits.Load() != 1 {
		t.Fatalf("deactivated account retried: %d hits", deactivated.hits.Load())
	}
	reports, _ := reporter.snapshot()
	if len(reports) != 1 || reports[0].observation.State != contract.SubscriptionRiskSuspended ||
		reports[0].observation.Code != contract.RiskCodeAccountDeactivated || reports[0].observation.PausedUntil != nil {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestSubscriptionRequestErrorsDoNotPenalizeAccount(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"unsupported parameter", "application/json", `{"error":{"message":"Unsupported parameter: temperature","type":"invalid_request_error"}}`, http.StatusBadRequest},
		{"cloudflare challenge", "text/html", `<html><body>error code: 1010</body></html>`, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failing := newRiskUpstream(t, test.status, test.contentType, test.body, nil)
			healthy := newRiskUpstream(t, http.StatusOK, "application/json", `{"id":"resp_ok","output":[]}`, nil)
			reporter := newRecordingSubscriptionRisk()
			response := serveRiskRequest(t, reporter, false,
				riskCodexCandidate("service_codex_request", failing.url), riskCodexCandidate("service_codex_other", healthy.url))
			reports, _ := reporter.snapshot()
			if len(reports) != 0 {
				t.Fatalf("request error reported account risk: %+v", reports)
			}
			if test.status == http.StatusBadRequest {
				if response.Code != http.StatusBadRequest || response.Body.String() != test.body || healthy.hits.Load() != 0 {
					t.Fatalf("400 response = %d %q, healthy hits = %d", response.Code, response.Body.String(), healthy.hits.Load())
				}
			}
		})
	}
}

func TestSubscriptionUnauthorizedStartsForcedRefresh(t *testing.T) {
	t.Parallel()
	rejected := newRiskUpstream(t, http.StatusUnauthorized, "application/json", `{"error":{"code":"token_invalidated"}}`, nil)
	reporter := newRecordingSubscriptionRisk()
	serveRiskRequest(t, reporter, false, riskCodexCandidate("service_codex_rejected", rejected.url))
	if got := waitForRiskSignal(t, reporter.refreshed); got != "service_codex_rejected:subscription-token" {
		t.Fatalf("forced refresh = %q", got)
	}
	if reports, _ := reporter.snapshot(); len(reports) != 0 {
		t.Fatalf("401 recorded an account pause: %+v", reports)
	}
}

func TestSubscriptionSuccessClearsExpiredCooling(t *testing.T) {
	t.Parallel()
	healthy := newRiskUpstream(t, http.StatusOK, "application/json", `{"id":"resp_ok","output":[]}`, nil)
	reporter := newRecordingSubscriptionRisk()
	candidate := riskCodexCandidate("service_codex_cooled", healthy.url)
	elapsed := time.Now().Add(-time.Minute)
	candidate.Service.Subscription.Risk = &contract.SubscriptionRisk{
		State: contract.SubscriptionRiskCooling, Code: contract.RiskCodeRateLimit5h,
		ObservedAt: elapsed.Add(-time.Hour), PausedUntil: &elapsed,
	}
	if response := serveRiskRequest(t, reporter, false, candidate); response.Code != http.StatusOK {
		t.Fatalf("response = %d", response.Code)
	}
	if _, cleared := reporter.snapshot(); len(cleared) != 1 || cleared[0] != "service_codex_cooled" {
		t.Fatalf("cleared = %v", cleared)
	}
}

func TestSubscriptionStreamErrorIsReportedWithoutChangingStream(t *testing.T) {
	t.Parallel()
	stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"usage_limit_reached\",\"message\":\"limit\",\"resets_in_seconds\":600}}}\n\n"
	upstream := newRiskUpstream(t, http.StatusOK, "text/event-stream", stream, nil)
	reporter := newRecordingSubscriptionRisk()
	response := serveRiskRequest(t, reporter, true, riskCodexCandidate("service_codex_stream", upstream.url))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"usage_limit_reached"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	waitForRiskSignal(t, reporter.reported)
	reports, _ := reporter.snapshot()
	if len(reports) != 1 || reports[0].observation.Code != contract.RiskCodeUsageLimitReached || reports[0].observation.HTTPStatus != 0 {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestAttemptHealthOutcomeCountsSubscriptionRejections(t *testing.T) {
	t.Parallel()
	subscription := codexVersionCandidate("https://chatgpt.example/backend-api/codex")
	apiKey := endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, true)}
	for _, test := range []struct {
		name      string
		candidate endpoint.Resolved
		status    int
		protected bool
		want      string
	}{
		{"subscription 401", subscription, http.StatusUnauthorized, true, "failure"},
		{"subscription 402", subscription, http.StatusPaymentRequired, true, "failure"},
		{"subscription 403", subscription, http.StatusForbidden, true, "failure"},
		{"subscription 403 without risk protection", subscription, http.StatusForbidden, false, "success"},
		{"subscription 400", subscription, http.StatusBadRequest, true, "success"},
		{"subscription 429", subscription, http.StatusTooManyRequests, true, "abandon"},
		{"api key 403", apiKey, http.StatusForbidden, true, "success"},
		{"api key 503", apiKey, http.StatusServiceUnavailable, true, "failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver := &healthRecordingResolver{
				successes: make(chan struct{}, 1), failures: make(chan struct{}, 1), abandons: make(chan struct{}, 1),
			}
			outcome := newAttemptHealthOutcome(resolver, test.candidate, true)
			outcome.accountRejections = test.protected
			outcome.RecordStatus(test.status)
			got := ""
			select {
			case <-resolver.successes:
				got = "success"
			case <-resolver.failures:
				got = "failure"
			case <-resolver.abandons:
				got = "abandon"
			default:
			}
			if got != test.want {
				t.Fatalf("RecordStatus(%d) = %q, want %q", test.status, got, test.want)
			}
		})
	}
}
