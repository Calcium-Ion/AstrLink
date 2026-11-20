package control

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// maxPurgeSessionIDs is Core's contract.MaxPurgeSessionIDs: one sessions
// purge names at most this many.
const maxPurgeSessionIDs = 1000

// ValidateResourceID checks a Core resource ID: 3 to 96 lowercase letters,
// digits, underscores and hyphens, starting with a letter.
func ValidateResourceID(value string) error {
	if len(value) < 3 || len(value) > 96 {
		return errors.New("resource id is invalid")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case (c >= '0' && c <= '9') || c == '_' || c == '-':
			if i == 0 {
				return errors.New("resource id is invalid")
			}
		default:
			return errors.New("resource id is invalid")
		}
	}
	return nil
}

// validateETag checks a quoted resource ETag.
func validateETag(value string) error {
	if len(value) < 3 || len(value) > 128 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		return errors.New("resource ETag is invalid")
	}
	return nil
}

// validateStrongETag checks a "sha256:<64 hex>" ETag.
func validateStrongETag(value string) error {
	const prefix = `"sha256:`
	if len(value) != len(prefix)+64+1 || !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, `"`) {
		return errors.New("policy ETag is invalid")
	}
	for _, c := range []byte(value[len(prefix) : len(value)-1]) {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return errors.New("policy ETag is invalid")
		}
	}
	return nil
}

// PercentEncodeQuery encodes everything but the RFC 3986 unreserved
// characters.
func PercentEncodeQuery(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// queryIDOK accepts a lowercase snake_case ID such as a protocol or client
// type; Core checks the value against its own list.
func queryIDOK(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z':
		case (c >= '0' && c <= '9') || c == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func timestampCharsetOK(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9') && !strings.ContainsRune("TZz:+.-", rune(c)) {
			return false
		}
	}
	return true
}

func buildRequestRecordQuery(query any) (string, error) {
	if query == nil {
		return "", nil
	}
	o, ok := asObject(query)
	if !ok {
		return "", errors.New("request record query must be an object")
	}
	var (
		limit                                                     *uint64
		cursor, from, to, protocol, serviceID, status, clientType string
		tokenIDs, models                                          []string
	)
	for _, key := range sortedKeys(o) {
		value := o[key]
		switch key {
		case "limit":
			parsed, ok := asU64(value)
			if !ok {
				return "", errors.New("request record query limit must be an integer")
			}
			if parsed < 1 || parsed > 200 {
				return "", errors.New("request record query limit must be between 1 and 200")
			}
			limit = &parsed
		case "cursor":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request record query cursor must be a string")
			}
			if text == "" || len(text) > 512 {
				return "", errors.New("request record query cursor must be 1 to 512 characters")
			}
			cursor = text
		case "from", "to":
			text, ok := asString(value)
			if !ok {
				return "", fmt.Errorf("request record query %s must be a string", key)
			}
			if !timestampCharsetOK(text) {
				return "", fmt.Errorf("request record query %s has an invalid timestamp charset", key)
			}
			if key == "from" {
				from = text
			} else {
				to = text
			}
		case "protocol", "client_type":
			text, ok := asString(value)
			if !ok {
				return "", fmt.Errorf("request record query %s must be a string", key)
			}
			if !queryIDOK(text) {
				return "", fmt.Errorf("request record query %s is invalid", key)
			}
			if key == "protocol" {
				protocol = text
			} else {
				clientType = text
			}
		case "service_id":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request record query service_id must be a string")
			}
			if err := ValidateResourceID(text); err != nil {
				return "", err
			}
			serviceID = text
		case "local_access_token_ids":
			const message = "request record query local_access_token_ids must be an array of strings"
			values, ok := asArray(value)
			if !ok {
				return "", errors.New(message)
			}
			for _, value := range values {
				text, ok := asString(value)
				if !ok {
					return "", errors.New(message)
				}
				if err := ValidateResourceID(text); err != nil {
					return "", err
				}
				tokenIDs = append(tokenIDs, text)
			}
		case "models":
			const message = "request record query models must be an array of strings"
			values, ok := asArray(value)
			if !ok {
				return "", errors.New(message)
			}
			if len(values) > 100 {
				return "", errors.New("request record query models has too many entries")
			}
			for _, value := range values {
				text, ok := asString(value)
				if !ok {
					return "", errors.New(message)
				}
				if text == "" || runeCount(text) > 256 || hasControl(text) {
					return "", errors.New("request record query model is invalid")
				}
				models = append(models, text)
			}
		case "status":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request record query status must be a string")
			}
			switch text {
			case "pending", "succeeded", "failed", "cancelled", "blocked":
				status = text
			default:
				return "", errors.New("request record query status is invalid")
			}
		default:
			return "", fmt.Errorf("request record query contains unknown key %s", key)
		}
	}
	var pairs []string
	add := func(name, value string) {
		if value != "" {
			pairs = append(pairs, name+"="+PercentEncodeQuery(value))
		}
	}
	if limit != nil {
		pairs = append(pairs, "limit="+strconv.FormatUint(*limit, 10))
	}
	add("cursor", cursor)
	add("from", from)
	add("to", to)
	add("protocol", protocol)
	add("service_id", serviceID)
	for _, id := range tokenIDs {
		add("local_access_token_id", id)
	}
	for _, model := range models {
		add("model", model)
	}
	add("status", status)
	add("client_type", clientType)
	if len(pairs) == 0 {
		return "", nil
	}
	return "?" + strings.Join(pairs, "&"), nil
}

func buildRequestSessionQuery(query any) (string, error) {
	recordQuery := query
	var kind any
	hasKind := false
	if o, ok := asObject(query); ok {
		// Core filters records by client type, not sessions.
		if _, ok := o["client_type"]; ok {
			return "", errors.New("request session query contains unknown key client_type")
		}
		copied := make(object, len(o))
		for key, value := range o {
			copied[key] = value
		}
		kind, hasKind = copied["kind"]
		delete(copied, "kind")
		recordQuery = copied
	}
	qs, err := buildRequestRecordQuery(recordQuery)
	if err != nil {
		return "", err
	}
	if hasKind {
		text, _ := asString(kind)
		if text != "inference" && text != "discovery" {
			return "", errors.New("request session query kind must be inference or discovery")
		}
		if qs == "" {
			qs = "?"
		} else {
			qs += "&"
		}
		qs += "kind=" + text
	}
	return qs, nil
}

// buildProjectMemoryQuery builds the request memory query: a project or
// the calls without one, a client, the day start and, for one entry, its
// file, each checked before it reaches Core.
func buildProjectMemoryQuery(query any, entry bool) (string, error) {
	if query == nil {
		if entry {
			return "", errors.New("request memory entry query must name a file")
		}
		return "", nil
	}
	o, ok := asObject(query)
	if !ok {
		return "", errors.New("request memory query must be an object")
	}
	var project, clientType, dayStart, file string
	noProject := false
	for _, key := range sortedKeys(o) {
		value := o[key]
		switch {
		case key == "project":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request memory query project must be a string")
			}
			if text == "" || runeCount(text) > 512 || hasControl(text) {
				return "", errors.New("request memory query project is invalid")
			}
			project = text
		case key == "no_project":
			if b, ok := asBool(value); !ok || !b {
				return "", errors.New("request memory query no_project must be true")
			}
			noProject = true
		case key == "client_type":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request memory query client_type must be a string")
			}
			if text == "" || len(text) > 32 || strings.Trim(text, "abcdefghijklmnopqrstuvwxyz_") != "" {
				return "", errors.New("request memory query client_type is invalid")
			}
			clientType = text
		case key == "day_start":
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request memory query day_start must be a string")
			}
			if !timestampCharsetOK(text) {
				return "", errors.New("request memory query day_start has an invalid timestamp charset")
			}
			dayStart = text
		case key == "file" && entry:
			text, ok := asString(value)
			if !ok {
				return "", errors.New("request memory query file must be a string")
			}
			if text == "" || runeCount(text) > 128 || hasControl(text) {
				return "", errors.New("request memory query file is invalid")
			}
			file = text
		default:
			return "", fmt.Errorf("request memory query has an unknown field %s", key)
		}
	}
	if project != "" && noProject {
		return "", errors.New("request memory query cannot name a project and no project")
	}
	if entry && file == "" {
		return "", errors.New("request memory entry query must name a file")
	}
	var pairs []string
	if project != "" {
		pairs = append(pairs, "project="+PercentEncodeQuery(project))
	}
	if noProject {
		pairs = append(pairs, "no_project=true")
	}
	if clientType != "" {
		pairs = append(pairs, "client_type="+clientType)
	}
	if dayStart != "" {
		pairs = append(pairs, "day_start="+PercentEncodeQuery(dayStart))
	}
	if file != "" {
		pairs = append(pairs, "file="+PercentEncodeQuery(file))
	}
	if len(pairs) == 0 {
		return "", nil
	}
	return "?" + strings.Join(pairs, "&"), nil
}

func validatePurgeRequestRecordsInput(input any) error {
	o, ok := asObject(input)
	if !ok {
		return errors.New("request purge input must be an object")
	}
	for _, key := range sortedKeys(o) {
		switch key {
		case "scope", "confirm", "before", "session_ids":
		default:
			return fmt.Errorf("request purge input contains unknown key %s", key)
		}
	}
	scope, ok := stringField(o, "scope")
	if !ok {
		return errors.New("request purge input scope must be a string")
	}
	confirm, ok := field(o, "confirm")
	if !ok {
		return errors.New("request purge input omitted confirm")
	}
	if b, ok := asBool(confirm); !ok || !b {
		return errors.New("request purge input confirm must be true")
	}
	unexpected := errors.New("request purge input has missing or unexpected fields")
	switch scope {
	case "all":
		if _, ok := o["before"]; ok {
			return errors.New("request purge input before is forbidden when scope is all")
		}
		if len(o) != 2 {
			return unexpected
		}
	case "before":
		before, ok := stringField(o, "before")
		if !ok {
			return errors.New("request purge input before is required when scope is before")
		}
		if !timestampCharsetOK(before) {
			return errors.New("request purge input before has an invalid timestamp charset")
		}
		if len(o) != 3 {
			return unexpected
		}
	case "sessions":
		if _, ok := o["before"]; ok {
			return errors.New("request purge input before is forbidden when scope is sessions")
		}
		ids, ok := asArray(o["session_ids"])
		if !ok {
			return errors.New("request purge input session_ids is required when scope is sessions")
		}
		if len(ids) == 0 || len(ids) > maxPurgeSessionIDs {
			return fmt.Errorf("request purge input session_ids must name 1 to %d sessions", maxPurgeSessionIDs)
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			text, ok := asString(id)
			if !ok {
				return errors.New("request purge input session_ids must be strings")
			}
			if err := ValidateResourceID(text); err != nil {
				return err
			}
			if seen[text] {
				return errors.New("request purge input session_ids must not repeat")
			}
			seen[text] = true
		}
		if len(o) != 3 {
			return unexpected
		}
	default:
		return errors.New("request purge input scope must be all, before or sessions")
	}
	return nil
}

func validateAuditSettingsPatch(patch any) error {
	o, ok := asObject(patch)
	if !ok {
		return errors.New("audit settings patch must be an object")
	}
	if len(o) == 0 {
		return errors.New("audit settings patch must change at least one field")
	}
	enablingCapture := false
	inRange := func(key string, value any, low, high uint64) error {
		parsed, ok := asU64(value)
		if !ok {
			return fmt.Errorf("audit settings patch %s must be an integer", key)
		}
		if parsed < low || parsed > high {
			return fmt.Errorf("audit settings patch %s must be between %d and %d", key, low, high)
		}
		return nil
	}
	for _, key := range sortedKeys(o) {
		value := o[key]
		var err error
		switch key {
		case "request_body_enabled", "response_content_enabled":
			enabled, ok := asBool(value)
			if !ok {
				return fmt.Errorf("audit settings patch %s must be a boolean", key)
			}
			enablingCapture = enablingCapture || enabled
		case "request_body_max_bytes", "response_content_max_bytes":
			err = inRange(key, value, 1024, 67_108_864)
		case "metadata_retention_days":
			err = inRange(key, value, 1, 3650)
		case "content_retention_days":
			err = inRange(key, value, 1, 365)
		case "http_meta_enabled", "audit_risk_acknowledged", "agent_raw_access_enabled":
			if _, ok := asBool(value); !ok {
				return fmt.Errorf("audit settings patch %s must be a boolean", key)
			}
		default:
			return fmt.Errorf("audit settings patch contains unknown key %s", key)
		}
		if err != nil {
			return err
		}
	}
	if acknowledged, _ := asBool(o["audit_risk_acknowledged"]); enablingCapture && !acknowledged {
		return errors.New("enabling body audit requires audit_risk_acknowledged=true")
	}
	return nil
}

// syncRequest translates a sync operation into the control request it
// makes. It is kept apart from the request so the mapping is testable
// without a running Core.
func syncRequest(operation string, input any, hasInput bool) (Request, error) {
	const base = "/control/v1/sync"
	switch operation {
	case "status", "config":
		return Request{Method: http.MethodGet, Path: base + "/" + operation}, nil
	case "configure":
		if !hasInput {
			return Request{}, errors.New("sync configuration is required")
		}
		return Request{Method: http.MethodPut, Path: base + "/config", Body: encodeValue(input)}, nil
	case "passphrase":
		// The passphrase travels as the control API's own body, so a caller
		// passing a bare string and one passing the object both reach Core
		// the same way.
		var body any
		switch value := input.(type) {
		case string:
			body = object{"passphrase": value}
		case map[string]any:
			body = value
		default:
			return Request{}, errors.New("sync passphrase is required")
		}
		if !hasInput {
			return Request{}, errors.New("sync passphrase is required")
		}
		return Request{Method: http.MethodPut, Path: base + "/passphrase", Body: encodeValue(body)}, nil
	case "forget":
		// DELETE forgets the passphrase and leaves the remote settings
		// alone, so entering the passphrase again resumes sync.
		return Request{Method: http.MethodDelete, Path: base + "/passphrase"}, nil
	case "run":
		return Request{Method: http.MethodPost, Path: base + "/run"}, nil
	}
	return Request{}, errors.New("unsupported sync operation")
}

func serviceRiskEventsPath(serviceID string, limit *uint32) (string, error) {
	path := ServicesPath + "/" + serviceID + "/risk-events"
	if limit == nil {
		return path, nil
	}
	if *limit < 1 || *limit > 50 {
		return "", errors.New("risk event limit must be between 1 and 50")
	}
	return path + "?limit=" + strconv.FormatUint(uint64(*limit), 10), nil
}

// validatePackageID accepts service:<service id> and
// subscription:<provider>:<plan>; the plan is whatever the provider
// reported, lowercased, so it is only checked for control characters and
// length.
func validatePackageID(value string) error {
	valid := false
	if serviceID, ok := strings.CutPrefix(value, "service:"); ok {
		valid = ValidateResourceID(serviceID) == nil
	} else if rest, ok := strings.CutPrefix(value, "subscription:"); ok {
		if provider, plan, ok := strings.Cut(rest, ":"); ok {
			valid = provider != "" && strings.Trim(provider, "abcdefghijklmnopqrstuvwxyz") == "" &&
				plan != "" && runeCount(plan) <= 128 && !hasControl(plan)
		}
	}
	if !valid {
		return errors.New("package id is invalid")
	}
	return nil
}

func validatePackageStrategy(value string) error {
	switch value {
	case "auto", "soonest_reset", "most_remaining":
		return nil
	}
	return errors.New("package strategy is invalid")
}

var (
	subscriptionProviders        = []string{"openai_codex", "claude_code", "xai_grok", "antigravity", "github_copilot", "factory_droid"}
	authorizationSessionStatuses = []string{"pending", "completed", "cancelled", "expired", "failed"}
	authorizationFlows           = []string{"browser", "device_code", "authorization_code", "claude_cli"}
)

func validateAuthorizationFlow(value string) error {
	if !slices.Contains(authorizationFlows, value) {
		return errors.New("unknown authorization flow")
	}
	return nil
}

func validateString(value any, minimum, maximum int, label string) (string, error) {
	text, ok := asString(value)
	if !ok {
		return "", fmt.Errorf("%s must be a string", label)
	}
	if n := runeCount(text); n < minimum || n > maximum {
		return "", fmt.Errorf("%s must contain %d through %d characters", label, minimum, maximum)
	}
	return text, nil
}

// validateRFC3339 checks the timestamp shape Core writes: a calendar date,
// at most nine fraction digits, and Z or a ±hh:mm offset.
func validateRFC3339(value any, label string) error {
	timestamp, err := validateString(value, 20, 35, label)
	if err != nil {
		return err
	}
	invalid := fmt.Errorf("%s must be a valid RFC3339 timestamp", label)
	b := []byte(timestamp)
	digits := func(from, to int) (int, bool) {
		n := 0
		for _, c := range b[from:to] {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		return n, true
	}
	for _, c := range b {
		if c >= 0x80 {
			return invalid
		}
	}
	if b[4] != '-' || b[7] != '-' || b[10] != 'T' || b[13] != ':' || b[16] != ':' {
		return invalid
	}
	year, ok1 := digits(0, 4)
	month, ok2 := digits(5, 7)
	day, ok3 := digits(8, 10)
	hour, ok4 := digits(11, 13)
	minute, ok5 := digits(14, 16)
	second, ok6 := digits(17, 19)
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return invalid
	}
	leap := year%4 == 0 && (year%100 != 0 || year%400 == 0)
	var daysInMonth int
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		daysInMonth = 31
	case 4, 6, 9, 11:
		daysInMonth = 30
	case 2:
		daysInMonth = 28
		if leap {
			daysInMonth = 29
		}
	default:
		return invalid
	}
	if day == 0 || day > daysInMonth || hour > 23 || minute > 59 || second > 59 {
		return invalid
	}
	cursor := 19
	if cursor < len(b) && b[cursor] == '.' {
		cursor++
		start := cursor
		for cursor < len(b) && b[cursor] >= '0' && b[cursor] <= '9' {
			cursor++
		}
		if cursor == start || cursor-start > 9 {
			return invalid
		}
	}
	switch {
	case cursor < len(b) && b[cursor] == 'Z' && cursor+1 == len(b):
	case cursor < len(b) && (b[cursor] == '+' || b[cursor] == '-') && cursor+6 == len(b) && b[cursor+3] == ':':
		offsetHour, ok1 := digits(cursor+1, cursor+3)
		offsetMinute, ok2 := digits(cursor+4, cursor+6)
		if !ok1 || !ok2 || offsetHour > 23 || offsetMinute > 59 {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}
