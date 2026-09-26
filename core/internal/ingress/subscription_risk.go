package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// SubscriptionRiskReporter records upstream ban and quota signals for
// subscription accounts. Implementations persist them so the resolver keeps
// paused accounts out of scheduling.
type SubscriptionRiskReporter interface {
	ReportSubscriptionRisk(context.Context, contract.ServiceID, contract.SubscriptionRiskObservation) error
	ClearExpiredSubscriptionRisk(context.Context, contract.ServiceID) error
	RefreshRejectedSubscriptionToken(context.Context, contract.ServiceID, string) error
}

type subscriptionRiskScope uint8

const (
	subscriptionRiskNone subscriptionRiskScope = iota
	// subscriptionRiskModel cools only the failing model route.
	subscriptionRiskModel
	// subscriptionRiskAccount pauses every route of the account.
	subscriptionRiskAccount
)

const (
	subscriptionRiskReportTimeout  = 5 * time.Second
	subscriptionRefreshTimeout     = 30 * time.Second
	subscriptionForbiddenCooldown  = 10 * time.Minute
	subscriptionRiskFallbackPause  = 5 * time.Minute
	subscriptionRiskMaximumPause   = 8 * 24 * time.Hour
	subscriptionRiskMaxJitterUnits = 30
	subscriptionRiskInspectBytes   = 64 * 1024
)

type subscriptionRiskSignal struct {
	scope       subscriptionRiskScope
	observation contract.SubscriptionRiskObservation
	// cooldown is the model-route cooldown for model-scoped signals.
	cooldown     time.Duration
	unauthorized bool
}

// Codex reports deactivated plans with codes such as deactivated_workspace or
// account_suspended; both word orders occur.
var codexDeactivatedCode = regexp.MustCompile(
	`^(?:(?:deactivated|disabled|suspended)_(?:workspace|account|organization|org)|(?:workspace|account|organization|org)_(?:deactivated|disabled|suspended))$`,
)

// subscriptionRiskJitter spreads automatic resumption after shared resets.
var subscriptionRiskJitter = func() time.Duration {
	return time.Duration(1+rand.IntN(subscriptionRiskMaxJitterUnits)) * time.Second
}

func subscriptionRiskKind(kind contract.ServiceKind) bool {
	return kind == contract.ServiceKindClaudeSubscription || kind == contract.ServiceKindCodexSubscription
}

func subscriptionRiskStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusPaymentRequired,
		http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// classifySubscriptionRisk maps one upstream error to its account effect.
// Plain request errors, HTML challenge pages and unknown shapes return
// subscriptionRiskNone so a malformed client request never pauses an account.
func classifySubscriptionRisk(
	kind contract.ServiceKind,
	status int,
	header http.Header,
	body []byte,
	now time.Time,
) subscriptionRiskSignal {
	details, structured := parseSubscriptionError(body, now)
	switch kind {
	case contract.ServiceKindClaudeSubscription:
		return classifyClaudeRisk(status, header, details, structured, now)
	case contract.ServiceKindCodexSubscription:
		return classifyCodexRisk(status, header, details, structured, now)
	default:
		return subscriptionRiskSignal{}
	}
}

func classifyClaudeRisk(
	status int,
	header http.Header,
	details subscriptionErrorDetails,
	structured bool,
	now time.Time,
) subscriptionRiskSignal {
	message := strings.ToLower(details.message)
	if structured && status != http.StatusTooManyRequests {
		for _, rule := range []struct {
			fragment string
			code     string
			status   int
		}{
			{"organization has been disabled", contract.RiskCodeOrganizationDisabled, 0},
			{"oauth authentication is currently not allowed", contract.RiskCodeOAuthNotAllowed, 0},
			{"identity verification is required", contract.RiskCodeIdentityVerificationRequired, 0},
			{"credit balance", contract.RiskCodeCreditBalanceLow, http.StatusBadRequest},
		} {
			if strings.Contains(message, rule.fragment) && (rule.status == 0 || rule.status == status) {
				return suspendedRisk(rule.code, details.message, status)
			}
		}
	}
	switch status {
	case http.StatusUnauthorized:
		return subscriptionRiskSignal{unauthorized: true}
	case http.StatusForbidden:
		if !structured || requestScopedForbidden(message) {
			return subscriptionRiskSignal{}
		}
		code := contract.RiskCodeForbidden
		if strings.Contains(message, "only authorized for use with claude code") {
			code = contract.RiskCodeClientIdentityRejected
		}
		return forbiddenRisk(code, details.message, now)
	case http.StatusTooManyRequests:
		return claudeRateLimitRisk(header, details.message, now)
	default:
		return subscriptionRiskSignal{}
	}
}

// claudeRateLimitRisk reads Anthropic's unified subscription windows. A
// rejected 5h or 7d window exhausts the whole account; other rejected claims
// such as overage only affect the requested model.
func claudeRateLimitRisk(header http.Header, message string, now time.Time) subscriptionRiskSignal {
	const prefix = "Anthropic-Ratelimit-Unified-"
	for _, window := range []struct{ name, code string }{
		{"5h", contract.RiskCodeRateLimit5h},
		{"7d", contract.RiskCodeRateLimit7d},
	} {
		if !strings.EqualFold(strings.TrimSpace(header.Get(prefix+window.name+"-Status")), "rejected") {
			continue
		}
		until, ok := parseRiskReset(header.Get(prefix+window.name+"-Reset"), now)
		if !ok {
			until, ok = parseRiskReset(header.Get(prefix+"Reset"), now)
		}
		if !ok {
			until = now.Add(max(parseRetryAfter(header.Get("Retry-After"), now), subscriptionRiskFallbackPause))
		}
		return coolingRisk(window.code, message, http.StatusTooManyRequests, until)
	}
	if !strings.EqualFold(strings.TrimSpace(header.Get(prefix+"Status")), "rejected") {
		return subscriptionRiskSignal{}
	}
	until, ok := parseRiskReset(header.Get(prefix+"Reset"), now)
	if !ok {
		return subscriptionRiskSignal{}
	}
	return subscriptionRiskSignal{scope: subscriptionRiskModel, cooldown: until.Sub(now)}
}

func classifyCodexRisk(
	status int,
	header http.Header,
	details subscriptionErrorDetails,
	structured bool,
	now time.Time,
) subscriptionRiskSignal {
	for _, code := range details.codes {
		if codexDeactivatedCode.MatchString(strings.ToLower(code)) {
			return suspendedRisk(contract.RiskCodeAccountDeactivated, details.message, status)
		}
	}
	if status == http.StatusPaymentRequired && details.detailCode != "" {
		return suspendedRisk(contract.RiskCodeAccountDeactivated, details.message, status)
	}
	switch status {
	case http.StatusUnauthorized:
		return subscriptionRiskSignal{unauthorized: true}
	case http.StatusForbidden:
		if !structured || requestScopedForbidden(strings.ToLower(details.message)) {
			return subscriptionRiskSignal{}
		}
		return forbiddenRisk(contract.RiskCodeForbidden, details.message, now)
	case http.StatusTooManyRequests:
		if !details.usageLimit {
			return subscriptionRiskSignal{}
		}
		until := details.resetAt
		if until.IsZero() || !until.After(now) {
			until = now.Add(max(parseRetryAfter(header.Get("Retry-After"), now), subscriptionRiskFallbackPause))
		}
		return coolingRisk(contract.RiskCodeUsageLimitReached, details.message, status, until)
	default:
		return subscriptionRiskSignal{}
	}
}

// classifySubscriptionStreamError classifies an error event that arrived after
// a successful response start. Only account-wide signals are reported; the
// client already owns the stream, so nothing is retried.
func classifySubscriptionStreamError(kind contract.ServiceKind, event []byte, now time.Time) subscriptionRiskSignal {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(event, &envelope) != nil || (envelope.Type != "error" && envelope.Type != "response.failed") {
		return subscriptionRiskSignal{}
	}
	var status int
	details, _ := parseSubscriptionError(event, now)
	if details.usageLimit {
		status = http.StatusTooManyRequests
	}
	signal := classifySubscriptionRisk(kind, status, nil, event, now)
	if signal.scope != subscriptionRiskAccount || signal.observation.Forbidden {
		return subscriptionRiskSignal{}
	}
	signal.observation.HTTPStatus = 0
	return signal
}

// requestScopedForbidden keeps model entitlement errors from counting toward
// account escalation; retrying a model outside the plan is a request error.
func requestScopedForbidden(message string) bool {
	return strings.Contains(message, "model")
}

func suspendedRisk(code, message string, status int) subscriptionRiskSignal {
	return subscriptionRiskSignal{scope: subscriptionRiskAccount, observation: contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskSuspended, Code: code, Message: message, HTTPStatus: status,
	}}
}

func forbiddenRisk(code, message string, now time.Time) subscriptionRiskSignal {
	// The manager escalates repeated 403s; this pause is the single-hit default.
	until := now.Add(subscriptionForbiddenCooldown)
	return subscriptionRiskSignal{scope: subscriptionRiskAccount, observation: contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskCooling, Code: code, Message: message,
		HTTPStatus: http.StatusForbidden, PausedUntil: &until, Forbidden: true,
	}}
}

func coolingRisk(code, message string, status int, until time.Time) subscriptionRiskSignal {
	return subscriptionRiskSignal{scope: subscriptionRiskAccount, observation: contract.SubscriptionRiskObservation{
		State: contract.SubscriptionRiskCooling, Code: code, Message: message, HTTPStatus: status, PausedUntil: &until,
	}}
}

// parseRiskReset accepts Unix seconds or RFC 3339 and rejects resets that are
// in the past or implausibly far away.
func parseRiskReset(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	var reset time.Time
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		reset = time.Unix(seconds, 0)
	} else if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		reset = parsed
	} else {
		return time.Time{}, false
	}
	if !reset.After(now) || reset.Sub(now) > subscriptionRiskMaximumPause {
		return time.Time{}, false
	}
	return reset.UTC(), true
}

type subscriptionErrorDetails struct {
	message    string
	codes      []string
	detailCode string
	usageLimit bool
	resetAt    time.Time
}

// parseSubscriptionError extracts provider error fields from Anthropic,
// OpenAI Responses, ChatGPT backend and stream error envelopes.
func parseSubscriptionError(body []byte, now time.Time) (subscriptionErrorDetails, bool) {
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		return subscriptionErrorDetails{}, false
	}
	details := subscriptionErrorDetails{}
	objects := []map[string]any{document}
	for _, path := range [][]string{{"error"}, {"detail"}, {"response", "error"}} {
		if object, ok := jsonObjectAt(document, path...); ok {
			objects = append(objects, object)
		}
	}
	for _, object := range objects {
		if details.message == "" {
			if message, ok := object["message"].(string); ok {
				details.message = message
			}
		}
		for _, name := range []string{"code", "type"} {
			if value, ok := object[name].(string); ok && value != "" {
				details.codes = append(details.codes, value)
				if value == "usage_limit_reached" {
					details.usageLimit = true
					details.resetAt = codexUsageReset(object, now)
				}
			}
		}
	}
	if details.message == "" {
		for _, name := range []string{"error", "detail"} {
			if message, ok := document[name].(string); ok {
				details.message = message
				break
			}
		}
	}
	if detail, ok := jsonObjectAt(document, "detail"); ok {
		details.detailCode, _ = detail["code"].(string)
	}
	return details, true
}

func codexUsageReset(object map[string]any, now time.Time) time.Time {
	if value, ok := object["resets_at"].(float64); ok && value > 0 {
		return time.Unix(int64(value), 0).UTC()
	}
	if value, ok := object["resets_in_seconds"].(float64); ok && value > 0 {
		return now.Add(time.Duration(value) * time.Second).UTC()
	}
	return time.Time{}
}

func jsonObjectAt(document map[string]any, path ...string) (map[string]any, bool) {
	current := document
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// inspectSubscriptionRisk classifies an upstream error response without
// changing what the client later receives.
func inspectSubscriptionRisk(
	kind contract.ServiceKind,
	response *http.Response,
	inspect func() ([]byte, bool),
	now time.Time,
) subscriptionRiskSignal {
	var body []byte
	if data, complete := inspect(); complete {
		encoding := strings.Join(response.Header.Values("Content-Encoding"), ",")
		if decoded, err := transport.DecodeBody(data, encoding, subscriptionRiskInspectBytes); err == nil {
			body = decoded
		}
	}
	return classifySubscriptionRisk(kind, response.StatusCode, response.Header, body, now)
}

// applySubscriptionRisk persists an account-wide signal and starts a forced
// credential refresh for rejected tokens. It never changes the response.
func (handler *Handler) applySubscriptionRisk(
	ctx context.Context,
	candidate endpoint.Resolved,
	signal subscriptionRiskSignal,
	upstreamHeaders http.Header,
) {
	reporter := handler.subscriptionRisk
	if reporter == nil {
		return
	}
	id := candidate.CanonicalService().ID
	detached := context.WithoutCancel(ctx)
	if signal.scope == subscriptionRiskAccount {
		observation := signal.observation
		if observation.PausedUntil != nil && !observation.Forbidden {
			until := observation.PausedUntil.Add(subscriptionRiskJitter())
			observation.PausedUntil = &until
		}
		reportCtx, cancel := context.WithTimeout(detached, subscriptionRiskReportTimeout)
		err := reporter.ReportSubscriptionRisk(reportCtx, id, observation)
		cancel()
		if err != nil {
			handler.logSubscriptionRisk("record subscription risk: service_id=%s code=%s: %v", id, observation.Code, err)
		}
	}
	if !signal.unauthorized {
		return
	}
	token, ok := strings.CutPrefix(upstreamHeaders.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(detached, subscriptionRefreshTimeout)
		defer cancel()
		if err := reporter.RefreshRejectedSubscriptionToken(refreshCtx, id, token); err != nil {
			handler.logSubscriptionRisk("refresh rejected subscription credential: service_id=%s: %v", id, err)
		}
	}()
}

// clearExpiredSubscriptionRisk removes a cooling pause after the account
// successfully serves again.
func (handler *Handler) clearExpiredSubscriptionRisk(ctx context.Context, candidate endpoint.Resolved, now time.Time) {
	service := candidate.CanonicalService()
	if handler.subscriptionRisk == nil || service.Subscription == nil || !service.Subscription.Risk.Expired(now) {
		return
	}
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscriptionRiskReportTimeout)
	defer cancel()
	if err := handler.subscriptionRisk.ClearExpiredSubscriptionRisk(clearCtx, service.ID); err != nil {
		handler.logSubscriptionRisk("clear expired subscription risk: service_id=%s: %v", service.ID, err)
	}
}

func (handler *Handler) logSubscriptionRisk(format string, arguments ...any) {
	if handler.recordLogger != nil {
		handler.recordLogger(format, arguments...)
	}
}

// subscriptionStreamRiskReader watches SSE data lines for provider error
// events while passing every byte through unchanged.
type subscriptionStreamRiskReader struct {
	io.ReadCloser
	kind     contract.ServiceKind
	line     []byte
	skipping bool
	once     sync.Once
	report   func(subscriptionRiskSignal)
}

func newSubscriptionStreamRiskReader(
	body io.ReadCloser,
	kind contract.ServiceKind,
	report func(subscriptionRiskSignal),
) *subscriptionStreamRiskReader {
	return &subscriptionStreamRiskReader{ReadCloser: body, kind: kind, report: report}
}

func (reader *subscriptionStreamRiskReader) Read(buffer []byte) (int, error) {
	count, err := reader.ReadCloser.Read(buffer)
	reader.observe(buffer[:count])
	return count, err
}

func (reader *subscriptionStreamRiskReader) observe(data []byte) {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		chunk := data
		if newline >= 0 {
			chunk = data[:newline]
		}
		if !reader.skipping {
			if len(reader.line)+len(chunk) > subscriptionRiskInspectBytes {
				// Error events are small; oversized lines are content.
				reader.line, reader.skipping = reader.line[:0], true
			} else {
				reader.line = append(reader.line, chunk...)
			}
		}
		if newline < 0 {
			return
		}
		if !reader.skipping {
			reader.inspectLine(bytes.TrimSuffix(reader.line, []byte("\r")))
		}
		reader.line, reader.skipping = reader.line[:0], false
		data = data[newline+1:]
	}
}

func (reader *subscriptionStreamRiskReader) inspectLine(line []byte) {
	payload, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok || !bytes.Contains(payload, []byte(`"error"`)) && !bytes.Contains(payload, []byte(`"response.failed"`)) {
		return
	}
	signal := classifySubscriptionStreamError(reader.kind, bytes.TrimSpace(payload), time.Now())
	if signal.scope == subscriptionRiskAccount {
		reader.once.Do(func() { reader.report(signal) })
	}
}

// watchSubscriptionStream wraps successful uncompressed event streams of
// Claude and Codex subscriptions.
func (handler *Handler) watchSubscriptionStream(
	ctx context.Context,
	candidate endpoint.Resolved,
	status int,
	header http.Header,
	body io.ReadCloser,
) io.ReadCloser {
	if handler.subscriptionRisk == nil || !subscriptionRiskKind(candidate.Service.Kind) || status >= 400 ||
		strings.TrimSpace(header.Get("Content-Encoding")) != "" ||
		!strings.HasPrefix(strings.ToLower(header.Get("Content-Type")), "text/event-stream") {
		return body
	}
	return newSubscriptionStreamRiskReader(body, candidate.Service.Kind, func(signal subscriptionRiskSignal) {
		go handler.applySubscriptionRisk(ctx, candidate, signal, nil)
	})
}
