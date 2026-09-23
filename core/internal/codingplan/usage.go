// Package codingplan reads the plan quota that API-key "coding plan"
// providers expose next to their inference endpoints (Kimi For Coding, GLM
// Coding Plan, MiniMax Coding Plan, OpenCode Go), plus the prepaid quota of a
// New API key. Each provider has its own first-party usage route and payload;
// this package maps every one of them onto the same public SubscriptionUsage
// snapshot the subscription meters already render, so a Kimi quota is never
// fetched or labelled as another provider's.
package codingplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

var (
	// ErrUnsupported means the service kind has no first-party quota endpoint.
	ErrUnsupported = errors.New("coding plan usage is unsupported for this service")
	// ErrCredentialUnavailable means the service has no stored API key.
	ErrCredentialUnavailable = errors.New("coding plan credential is unavailable")
	// ErrUsageUnavailable is the provider-neutral sentinel; messages carry the
	// provider label ("kimi usage unavailable: status 401").
	ErrUsageUnavailable = errors.New("usage unavailable")
)

const (
	fiveHourSeconds int64 = 5 * 3600
	weeklySeconds   int64 = 7 * 24 * 3600
	monthlySeconds  int64 = 30 * 24 * 3600
	requestTimeout        = 20 * time.Second
	cacheTTL              = 30 * time.Second
	maxBodyBytes          = 1 << 20
	// New API rate-limits /api/usage to 20 requests per 20 minutes per IP by
	// default, shared by every key on the site.
	newAPICacheTTL = 5 * time.Minute
	// defaultNewAPIQuotaPerUnit is New API's stock quota per USD. A site can
	// change it and publishes the live value on /api/status.
	defaultNewAPIQuotaPerUnit = 500_000
)

// Supports reports whether kind exposes a plan quota endpoint AstrLink knows.
// OpenCode Zen (pay-as-you-go) and other API gateways deliberately return
// false; New API is the exception because its keys carry their own quota.
func Supports(kind contract.ServiceKind) bool {
	switch kind {
	case contract.ServiceKindKimiCoding, contract.ServiceKindGLMCoding,
		contract.ServiceKindMiniMaxCoding, contract.ServiceKindOpenCodeGo,
		contract.ServiceKindNewAPI:
		return true
	default:
		return false
	}
}

// Label is the short provider name used in error text.
func Label(kind contract.ServiceKind) string {
	switch kind {
	case contract.ServiceKindKimiCoding:
		return "kimi"
	case contract.ServiceKindGLMCoding:
		return "glm"
	case contract.ServiceKindMiniMaxCoding:
		return "minimax"
	case contract.ServiceKindOpenCodeGo:
		return "opencode-go"
	case contract.ServiceKindNewAPI:
		return "new-api"
	default:
		return string(kind)
	}
}

type cacheEntry struct {
	usage contract.SubscriptionUsage
	until time.Time
}

// Fetcher resolves the service API key and calls the provider quota route.
type Fetcher struct {
	secrets secretstore.SecretStore
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	cache   map[contract.ServiceID]cacheEntry
}

func New(secrets secretstore.SecretStore, client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return &Fetcher{secrets: secrets, client: networkproxy.WrapClient(client), now: time.Now, cache: make(map[contract.ServiceID]cacheEntry)}
}

// ForgetUsage drops the cached quota snapshot so the next Usage call goes to
// the provider. A nil fetcher has nothing to forget.
func (fetcher *Fetcher) ForgetUsage(id contract.ServiceID) {
	if fetcher == nil {
		return
	}
	fetcher.mu.Lock()
	delete(fetcher.cache, id)
	fetcher.mu.Unlock()
}

// Usage returns the sanitized quota snapshot for a coding plan service.
func (fetcher *Fetcher) Usage(ctx context.Context, service contract.Service) (contract.SubscriptionUsage, error) {
	if fetcher == nil || !Supports(service.Kind) || service.HTTP == nil {
		return contract.SubscriptionUsage{}, ErrUnsupported
	}
	now := fetcher.now().UTC()
	fetcher.mu.Lock()
	if entry, ok := fetcher.cache[service.ID]; ok && now.Before(entry.until) {
		fetcher.mu.Unlock()
		return entry.usage, nil
	}
	fetcher.mu.Unlock()

	ctx, err := networkproxy.Bind(ctx, service, fetcher.secrets)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	secret, err := fetcher.apiKey(ctx, *service.HTTP)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	defer clear(secret)
	usage, err := fetcher.fetch(ctx, service.Kind, service.HTTP.BaseURL, string(secret))
	if err != nil {
		if !errors.Is(err, ErrUsageUnavailable) {
			err = fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
		}
		return contract.SubscriptionUsage{}, fmt.Errorf("%s %w", Label(service.Kind), err)
	}
	usage.ServiceID = service.ID
	usage.FetchedAt = now
	if err := usage.Validate(); err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%s %w: invalid payload", Label(service.Kind), ErrUsageUnavailable)
	}
	ttl := cacheTTL
	if service.Kind == contract.ServiceKindNewAPI {
		ttl = newAPICacheTTL
	}
	fetcher.mu.Lock()
	fetcher.cache[service.ID] = cacheEntry{usage: usage, until: now.Add(ttl)}
	fetcher.mu.Unlock()
	return usage, nil
}

func (fetcher *Fetcher) apiKey(ctx context.Context, connection contract.HTTPConnection) ([]byte, error) {
	if connection.Auth.Scheme == contract.AuthSchemeNone || connection.CredentialRef == "" || fetcher.secrets == nil {
		return nil, ErrCredentialUnavailable
	}
	ref, err := secretstore.ParseRef(connection.CredentialRef)
	if err != nil {
		return nil, ErrCredentialUnavailable
	}
	secret, err := fetcher.secrets.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCredentialUnavailable, err)
	}
	value := strings.TrimSpace(string(secret))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return nil, ErrCredentialUnavailable
	}
	return []byte(value), nil
}

func (fetcher *Fetcher) fetch(ctx context.Context, kind contract.ServiceKind, baseURL, apiKey string) (contract.SubscriptionUsage, error) {
	endpoint, err := UsageURL(kind, baseURL)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	request.Header.Set("Accept", "application/json")
	switch kind {
	case contract.ServiceKindGLMCoding:
		// Zhipu's monitor route takes the raw key, not a Bearer token.
		request.Header.Set("Authorization", apiKey)
		request.Header.Set("Accept-Language", "en-US,en")
	default:
		// Kimi, MiniMax, OpenCode Go and New API all take Bearer here even
		// where the inference side uses x-api-key.
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	status, body, err := fetcher.read(request)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	if status != http.StatusOK {
		if kind == contract.ServiceKindOpenCodeGo && status == http.StatusForbidden {
			return contract.SubscriptionUsage{}, fmt.Errorf("%w: key has no OpenCode Go subscription (status 403)", ErrUsageUnavailable)
		}
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: status %d", ErrUsageUnavailable, status)
	}
	quotaPerUnit := float64(defaultNewAPIQuotaPerUnit)
	if kind == contract.ServiceKindNewAPI {
		quotaPerUnit = fetcher.newAPIQuotaPerUnit(ctx, baseURL)
	}
	return decode(kind, body, fetcher.now().UTC(), quotaPerUnit)
}

func (fetcher *Fetcher) read(request *http.Request) (int, []byte, error) {
	response, err := fetcher.client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrUsageUnavailable, err)
	}
	return response.StatusCode, body, nil
}

// newAPIQuotaPerUnit reads the site's quota per USD from the public
// /api/status, which takes no key. The stock value covers a site that hides or
// fails it.
func (fetcher *Fetcher) newAPIQuotaPerUnit(ctx context.Context, baseURL string) float64 {
	base, err := baseOrigin(baseURL)
	if err != nil {
		return defaultNewAPIQuotaPerUnit
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/api/status", nil)
	if err != nil {
		return defaultNewAPIQuotaPerUnit
	}
	request.Header.Set("Accept", "application/json")
	status, body, err := fetcher.read(request)
	if err != nil || status != http.StatusOK {
		return defaultNewAPIQuotaPerUnit
	}
	var document struct {
		Data struct {
			QuotaPerUnit json.RawMessage `json:"quota_per_unit"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &document) != nil {
		return defaultNewAPIQuotaPerUnit
	}
	value, ok := flexibleNumber(document.Data.QuotaPerUnit)
	if !ok || value <= 0 || math.IsInf(value, 0) {
		return defaultNewAPIQuotaPerUnit
	}
	return value
}

// UsageURL derives the provider quota route from the configured inference
// base URL so a self-hosted proxy or regional host keeps working:
//
//	kimi_coding     {origin}/coding/v1/usages
//	glm_coding      {origin}/api/monitor/usage/quota/limit
//	minimax_coding  https://api.minimaxi.com|api.minimax.io/v1/api/openplatform/coding_plan/remains
//	opencode_go     {origin}/zen/go/v1/usage
//	newapi          {origin}/api/usage/token/
func UsageURL(kind contract.ServiceKind, baseURL string) (string, error) {
	base, err := baseOrigin(baseURL)
	if err != nil {
		return "", err
	}
	origin := base.String()
	switch kind {
	case contract.ServiceKindKimiCoding:
		return origin + "/coding/v1/usages", nil
	case contract.ServiceKindGLMCoding:
		return origin + "/api/monitor/usage/quota/limit", nil
	case contract.ServiceKindMiniMaxCoding:
		host := strings.ToLower(base.Hostname())
		switch {
		case host == "minimax.io" || strings.HasSuffix(host, ".minimax.io"):
			origin = "https://api.minimax.io"
		case host == "minimaxi.com" || strings.HasSuffix(host, ".minimaxi.com") ||
			host == "minimax.cn" || strings.HasSuffix(host, ".minimax.cn"):
			// The quota route is only published on the legacy CN API host; the
			// newer api.minimax.cn inference host shares the same key.
			origin = "https://api.minimaxi.com"
		}
		return origin + "/v1/api/openplatform/coding_plan/remains", nil
	case contract.ServiceKindOpenCodeGo:
		return origin + "/zen/go/v1/usage", nil
	case contract.ServiceKindNewAPI:
		// The trailing slash is the registered route; without it Gin answers
		// with a redirect.
		return origin + "/api/usage/token/", nil
	default:
		return "", ErrUnsupported
	}
}

// baseOrigin trims a configured base URL down to its scheme and host.
func baseOrigin(baseURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%w: invalid base URL", ErrUsageUnavailable)
	}
	return &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}, nil
}

// Decode maps a provider payload onto the public snapshot. New API amounts use
// the stock quota per USD; Fetcher reads the site's own value.
func Decode(kind contract.ServiceKind, body []byte, now time.Time) (contract.SubscriptionUsage, error) {
	return decode(kind, body, now, defaultNewAPIQuotaPerUnit)
}

func decode(kind contract.ServiceKind, body []byte, now time.Time, quotaPerUnit float64) (contract.SubscriptionUsage, error) {
	document, err := decodeObject(body)
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	var usage contract.SubscriptionUsage
	switch kind {
	case contract.ServiceKindKimiCoding:
		usage, err = decodeKimi(document)
	case contract.ServiceKindGLMCoding:
		usage, err = decodeGLM(document)
	case contract.ServiceKindMiniMaxCoding:
		usage, err = decodeMiniMax(document)
	case contract.ServiceKindOpenCodeGo:
		usage, err = decodeOpenCodeGo(document, now)
	case contract.ServiceKindNewAPI:
		// A key quota has no windows; decodeNewAPI sets limit_reached itself.
		return decodeNewAPI(document, quotaPerUnit)
	default:
		return contract.SubscriptionUsage{}, ErrUnsupported
	}
	if err != nil {
		return contract.SubscriptionUsage{}, err
	}
	if usage.Primary == nil && usage.Secondary == nil && len(usage.AdditionalRateLimits) == 0 {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: no usage windows", ErrUsageUnavailable)
	}
	if usage.Primary != nil || usage.Secondary != nil {
		reached := (usage.Primary != nil && usage.Primary.UsedPercent >= 100) ||
			(usage.Secondary != nil && usage.Secondary.UsedPercent >= 100)
		usage.LimitReached = &reached
	}
	return usage, nil
}

// decodeKimi reads GET /coding/v1/usages:
//
//	{"limits":[{"detail":{"limit","remaining","resetTime"}}],
//	 "usage":{"limit","remaining","resetTime"}}
//
// limits[] is the rolling 5-hour window; usage is the weekly allowance.
func decodeKimi(document map[string]json.RawMessage) (contract.SubscriptionUsage, error) {
	usage := contract.SubscriptionUsage{}
	var limits []struct {
		Detail map[string]json.RawMessage `json:"detail"`
	}
	if raw := document["limits"]; jsonArray(raw) && json.Unmarshal(raw, &limits) == nil {
		for _, item := range limits {
			if window := kimiWindow(item.Detail, fiveHourSeconds); window != nil {
				usage.Primary = window
				break
			}
		}
	}
	var weekly map[string]json.RawMessage
	if raw := document["usage"]; jsonObject(raw) && json.Unmarshal(raw, &weekly) == nil {
		usage.Secondary = kimiWindow(weekly, weeklySeconds)
	}
	return usage, nil
}

func kimiWindow(fields map[string]json.RawMessage, seconds int64) *contract.RateLimitWindow {
	if fields == nil {
		return nil
	}
	limit, ok := flexibleNumber(fields["limit"])
	if !ok || limit <= 0 {
		return nil
	}
	remaining, ok := flexibleNumber(fields["remaining"])
	if !ok {
		return nil
	}
	used := math.Max(limit-remaining, 0) / limit * 100
	return newWindow(used, seconds, flexibleTime(fields["resetTime"]))
}

// decodeGLM reads GET /api/monitor/usage/quota/limit:
//
//	{"success":true,"data":{"level":"pro","limits":[
//	   {"type":"TOKENS_LIMIT","percentage":12,"nextResetTime":<ms>,"unit":3,"number":5},
//	   {"type":"TOKENS_LIMIT","percentage":40,"nextResetTime":<ms>,"unit":6,"number":7}]}}
//
// unit 3 is the 5-hour window and unit 6 the weekly one; entries without a
// recognised unit fall back to reset order (no reset first, then ascending).
func decodeGLM(document map[string]json.RawMessage) (contract.SubscriptionUsage, error) {
	if raw, ok := document["success"]; ok {
		var success bool
		if json.Unmarshal(raw, &success) == nil && !success {
			var message string
			_ = json.Unmarshal(document["msg"], &message)
			return contract.SubscriptionUsage{}, fmt.Errorf("%w: %s", ErrUsageUnavailable, sanitizeMessage(message, "provider error"))
		}
	}
	var data struct {
		Level  string `json:"level"`
		Limits []struct {
			Type          string          `json:"type"`
			Percentage    json.RawMessage `json:"percentage"`
			NextResetTime json.RawMessage `json:"nextResetTime"`
			Unit          *int64          `json:"unit"`
		} `json:"limits"`
	}
	if raw := document["data"]; !jsonObject(raw) || json.Unmarshal(raw, &data) != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	type entry struct {
		percent float64
		reset   *time.Time
	}
	var fiveHour, weekly *entry
	var unclassified []entry
	for _, item := range data.Limits {
		kind := strings.ToUpper(strings.TrimSpace(item.Type))
		if kind != "TOKENS_LIMIT" && kind != "CREDIT_LIMIT" {
			continue
		}
		percent, ok := flexibleNumber(item.Percentage)
		if !ok {
			percent = 0
		}
		current := entry{percent: percent, reset: flexibleTime(item.NextResetTime)}
		switch {
		case item.Unit != nil && *item.Unit == 3 && fiveHour == nil:
			fiveHour = &current
		case item.Unit != nil && *item.Unit == 6 && weekly == nil:
			weekly = &current
		default:
			unclassified = append(unclassified, current)
		}
	}
	// Fallback for entries without a recognised unit: no reset time leans
	// towards the 5-hour slot, then earlier resets fill whichever is empty.
	sort.SliceStable(unclassified, func(i, j int) bool {
		a, b := unclassified[i], unclassified[j]
		if (a.reset == nil) != (b.reset == nil) {
			return a.reset == nil
		}
		return a.reset != nil && a.reset.Before(*b.reset)
	})
	for index := range unclassified {
		switch {
		case fiveHour == nil:
			fiveHour = &unclassified[index]
		case weekly == nil:
			weekly = &unclassified[index]
		}
	}
	usage := contract.SubscriptionUsage{PlanType: sanitizeLabel(data.Level, 64)}
	if fiveHour != nil {
		usage.Primary = newWindow(fiveHour.percent, fiveHourSeconds, fiveHour.reset)
	}
	if weekly != nil {
		usage.Secondary = newWindow(weekly.percent, weeklySeconds, weekly.reset)
	}
	return usage, nil
}

// decodeMiniMax reads GET /v1/api/openplatform/coding_plan/remains:
//
//	{"base_resp":{"status_code":0},"model_remains":[{"model_name":"general",
//	  "current_interval_remaining_percent":80,"end_time":<ms>,
//	  "current_weekly_status":1,"current_weekly_remaining_percent":55,"weekly_end_time":<ms>}]}
//
// Only the "general" (coding) entry counts; the weekly bucket exists only
// while current_weekly_status is 1.
func decodeMiniMax(document map[string]json.RawMessage) (contract.SubscriptionUsage, error) {
	var base struct {
		StatusCode *int64 `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	}
	if raw := document["base_resp"]; jsonObject(raw) && json.Unmarshal(raw, &base) == nil && base.StatusCode != nil && *base.StatusCode != 0 {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %s (code %d)", ErrUsageUnavailable, sanitizeMessage(base.StatusMsg, "provider error"), *base.StatusCode)
	}
	var remains []struct {
		ModelName      string          `json:"model_name"`
		IntervalRemain json.RawMessage `json:"current_interval_remaining_percent"`
		EndTime        json.RawMessage `json:"end_time"`
		WeeklyStatus   *int64          `json:"current_weekly_status"`
		WeeklyRemain   json.RawMessage `json:"current_weekly_remaining_percent"`
		WeeklyEndTime  json.RawMessage `json:"weekly_end_time"`
	}
	if raw := document["model_remains"]; !jsonArray(raw) || json.Unmarshal(raw, &remains) != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	usage := contract.SubscriptionUsage{}
	for _, item := range remains {
		if !strings.EqualFold(strings.TrimSpace(item.ModelName), "general") {
			continue
		}
		if remain, ok := flexibleNumber(item.IntervalRemain); ok {
			usage.Primary = newWindow(100-remain, fiveHourSeconds, flexibleTime(item.EndTime))
		}
		if item.WeeklyStatus != nil && *item.WeeklyStatus == 1 {
			if remain, ok := flexibleNumber(item.WeeklyRemain); ok {
				usage.Secondary = newWindow(100-remain, weeklySeconds, flexibleTime(item.WeeklyEndTime))
			}
		}
		break
	}
	return usage, nil
}

// decodeOpenCodeGo reads GET /zen/go/v1/usage:
//
//	{"usage":{"rolling":{"status":"ok","percent":12,"resetsAt":"…"},
//	          "weekly":{…},"monthly":{…}}}
//
// rolling is the 5-hour window, weekly the 7-day one and monthly is surfaced
// as an additional 30-day limit. A 0% window carries a placeholder resetsAt
// that is dropped.
func decodeOpenCodeGo(document map[string]json.RawMessage, now time.Time) (contract.SubscriptionUsage, error) {
	var windows map[string]struct {
		Percent  json.RawMessage `json:"percent"`
		ResetsAt json.RawMessage `json:"resetsAt"`
	}
	if raw := document["usage"]; !jsonObject(raw) || json.Unmarshal(raw, &windows) != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	read := func(key string, seconds int64) *contract.RateLimitWindow {
		window, ok := windows[key]
		if !ok {
			return nil
		}
		percent, ok := flexibleNumber(window.Percent)
		if !ok {
			return nil
		}
		var reset *time.Time
		if percent > 0 {
			if parsed := flexibleTime(window.ResetsAt); parsed != nil && parsed.After(now) {
				reset = parsed
			}
		}
		return newWindow(percent, seconds, reset)
	}
	usage := contract.SubscriptionUsage{Primary: read("rolling", fiveHourSeconds), Secondary: read("weekly", weeklySeconds)}
	if monthly := read("monthly", monthlySeconds); monthly != nil {
		usage.AdditionalRateLimits = []contract.AdditionalRateLimit{{LimitName: "Monthly", MeteredFeature: "monthly", Primary: monthly}}
	}
	return usage, nil
}

// decodeNewAPI reads New API GET /api/usage/token/:
//
//	{"code":true,"message":"ok","data":{"total_granted":…,"total_used":…,
//	  "total_available":…,"unlimited_quota":false,"expires_at":<unix s, 0 = never>}}
//
// Amounts are raw quota; quotaPerUnit converts them to USD. An unlimited key
// still counts total_available down, so only its spend is reported.
func decodeNewAPI(document map[string]json.RawMessage, quotaPerUnit float64) (contract.SubscriptionUsage, error) {
	var ok bool
	if json.Unmarshal(document["code"], &ok) != nil || !ok {
		var message string
		_ = json.Unmarshal(document["message"], &message)
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: %s", ErrUsageUnavailable, sanitizeMessage(message, "provider error"))
	}
	var data map[string]json.RawMessage
	if raw := document["data"]; !jsonObject(raw) || json.Unmarshal(raw, &data) != nil {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	used, ok := flexibleNumber(data["total_used"])
	if !ok {
		return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	used = math.Max(used, 0)
	var unlimited bool
	_ = json.Unmarshal(data["unlimited_quota"], &unlimited)
	quota := &contract.UsageQuota{Unlimited: unlimited, UsedUSD: usdAmount(used, quotaPerUnit), ExpiresAt: flexibleTime(data["expires_at"])}
	reached := false
	if !unlimited {
		remaining, ok := flexibleNumber(data["total_available"])
		if !ok {
			return contract.SubscriptionUsage{}, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
		}
		// An overdrawn key reports negative availability; it is simply empty.
		remaining = math.Max(remaining, 0)
		quota.RemainingUSD = usdAmount(remaining, quotaPerUnit)
		quota.TotalUSD = usdAmount(used+remaining, quotaPerUnit)
		reached = remaining <= 0
	}
	return contract.SubscriptionUsage{LimitReached: &reached, Quota: quota}, nil
}

// usdAmount renders raw quota as a USD decimal with at most six places.
func usdAmount(quota, quotaPerUnit float64) string {
	return strconv.FormatFloat(math.Round(quota/quotaPerUnit*1e6)/1e6, 'f', -1, 64)
}

func newWindow(percent float64, seconds int64, reset *time.Time) *contract.RateLimitWindow {
	if math.IsNaN(percent) || math.IsInf(percent, 0) {
		return nil
	}
	percent = math.Round(percent*100) / 100
	if percent < 0 {
		percent = 0
	}
	if percent > 1000 {
		percent = 1000
	}
	window := &contract.RateLimitWindow{UsedPercent: percent}
	if seconds > 0 {
		limit := seconds
		window.LimitWindowSeconds = &limit
	}
	if reset != nil {
		value := reset.UTC()
		window.ResetAt = &value
	}
	return window
}

func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &document); err != nil || document == nil {
		return nil, fmt.Errorf("%w: invalid payload", ErrUsageUnavailable)
	}
	return document, nil
}

func jsonObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func jsonArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

// flexibleNumber accepts JSON numbers and numeric strings ("100").
func flexibleNumber(raw json.RawMessage) (float64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, false
	}
	var number json.Number
	if json.Unmarshal(trimmed, &number) == nil {
		if value, err := number.Float64(); err == nil {
			return value, true
		}
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		if value, err := strconv.ParseFloat(strings.TrimSpace(text), 64); err == nil {
			return value, true
		}
	}
	return 0, false
}

// flexibleTime accepts RFC 3339 strings and unix seconds or milliseconds.
// Zero and negative timestamps (MiniMax/Volcengine "no active window") are
// treated as absent.
func flexibleTime(raw json.RawMessage) *time.Time {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		text = strings.TrimSpace(text)
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			value := parsed.UTC()
			return &value
		}
		if number, err := strconv.ParseInt(text, 10, 64); err == nil {
			return unixFlexible(number)
		}
		return nil
	}
	var number json.Number
	if json.Unmarshal(trimmed, &number) == nil {
		if value, err := number.Int64(); err == nil {
			return unixFlexible(value)
		}
		if value, err := number.Float64(); err == nil {
			return unixFlexible(int64(value))
		}
	}
	return nil
}

func unixFlexible(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	var parsed time.Time
	if value < 1_000_000_000_000 {
		parsed = time.Unix(value, 0)
	} else {
		parsed = time.UnixMilli(value)
	}
	parsed = parsed.UTC()
	return &parsed
}

func sanitizeLabel(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "@") || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	if runes := []rune(value); len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func sanitizeMessage(value, fallback string) string {
	value = sanitizeLabel(value, 120)
	if value == "" {
		return fallback
	}
	return value
}
