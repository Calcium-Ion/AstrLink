package control

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ValidateAuthorizationURL checks that value is the page of an
// authorization provider the desktop may open in the browser.
func ValidateAuthorizationURL(value string) error { return validateAuthorizationURL(value) }

func validateAuthorizationURL(value string) error {
	if value == "" || len(value) > 4096 {
		return errors.New("authorization URL is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" {
		return errors.New("authorization URL is invalid")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		if parsed.Hostname() != "" {
			return nil
		}
		return errors.New("authorization URL is invalid")
	case "http":
		switch strings.ToLower(parsed.Hostname()) {
		case "127.0.0.1", "localhost":
			return nil
		}
		return errors.New("authorization URL http is only allowed on loopback")
	}
	return errors.New("authorization URL must use https")
}

func validateSubscriptionErrorCode(value string) error {
	if !queryIDOK(value) {
		return errors.New("subscription error code is invalid")
	}
	return nil
}

func validateSubscriptionErrorMessage(value string) error {
	if value == "" || runeCount(value) > 240 {
		return errors.New("subscription error message is invalid")
	}
	lower := strings.ToLower(value)
	for _, secret := range []string{"bearer ", "access_token", "refresh_token", "id_token", "device_auth_id", "code_verifier", "authorization_code"} {
		if strings.Contains(lower, secret) {
			return errors.New("subscription error message must not contain credential material")
		}
	}
	if strings.Contains(value, "eyJ") {
		return errors.New("subscription error message must not contain credential material")
	}
	return nil
}

func parseSubscriptionError(value any) (object, error) {
	o, ok := asObject(value)
	if !ok {
		return nil, errors.New("subscription error must be an object")
	}
	_, hasCode := o["code"]
	_, hasMessage := o["message"]
	if len(o) != 2 || !hasCode || !hasMessage {
		return nil, errors.New("subscription error has missing or unexpected fields")
	}
	code, ok := stringField(o, "code")
	if !ok {
		return nil, errors.New("subscription error omitted code")
	}
	message, ok := stringField(o, "message")
	if !ok {
		return nil, errors.New("subscription error omitted message")
	}
	if err := validateSubscriptionErrorCode(code); err != nil {
		return nil, err
	}
	if err := validateSubscriptionErrorMessage(message); err != nil {
		return nil, err
	}
	return object{"code": code, "message": message}, nil
}

func parseAuthorizationDeviceCode(value any) (object, error) {
	o, ok := asObject(value)
	if !ok {
		return nil, errors.New("authorization device_code must be an object")
	}
	_, hasURL := o["verification_url"]
	_, hasCode := o["user_code"]
	if len(o) != 2 || !hasURL || !hasCode {
		return nil, errors.New("authorization device_code has missing or unexpected fields")
	}
	verificationURL, ok := stringField(o, "verification_url")
	if !ok {
		return nil, errors.New("authorization device_code omitted verification_url")
	}
	if err := validateAuthorizationURL(verificationURL); err != nil {
		return nil, err
	}
	userCode, ok := stringField(o, "user_code")
	if !ok {
		return nil, errors.New("authorization device_code omitted user_code")
	}
	if strings.TrimSpace(userCode) == "" || runeCount(userCode) > 128 || strings.ContainsAny(userCode, "\r\n\x00") {
		return nil, errors.New("authorization device_code user_code is invalid")
	}
	return object{"verification_url": verificationURL, "user_code": userCode}, nil
}

// parseAuthorizationSession checks a subscription sign-in session and
// returns only the fields the frontend reads.
func parseAuthorizationSession(raw []byte) (object, error) {
	value, err := decodeValue(raw)
	if err != nil {
		return nil, fmt.Errorf("authorization session returned invalid JSON: %w", err)
	}
	return parseAuthorizationSessionValue(value)
}

func parseAuthorizationSessionValue(value any) (object, error) {
	o, ok := asObject(value)
	if !ok {
		return nil, errors.New("authorization session must be an object")
	}
	allowed := []string{"id", "provider", "status", "flow", "authorization_url", "device_code", "service_id", "expires_at", "error", "created_at", "updated_at"}
	for key := range o {
		if !slices.Contains(allowed, key) {
			return nil, errors.New("authorization session has unexpected fields")
		}
	}
	for _, key := range []string{"id", "provider", "status", "flow", "service_id", "expires_at", "created_at", "updated_at"} {
		if _, ok := o[key]; !ok {
			return nil, fmt.Errorf("authorization session omitted %s", key)
		}
	}
	text := func(key string) (string, error) {
		value, ok := stringField(o, key)
		if !ok {
			return "", fmt.Errorf("authorization session omitted %s", key)
		}
		return value, nil
	}
	id, err := text("id")
	if err != nil {
		return nil, err
	}
	if err := ValidateResourceID(id); err != nil {
		return nil, err
	}
	provider, err := text("provider")
	if err != nil {
		return nil, err
	}
	if !slices.Contains(subscriptionProviders, provider) {
		return nil, errors.New("unknown subscription provider")
	}
	status, err := text("status")
	if err != nil {
		return nil, err
	}
	if !slices.Contains(authorizationSessionStatuses, status) {
		return nil, errors.New("unknown authorization session status")
	}
	flow, err := text("flow")
	if err != nil {
		return nil, err
	}
	if err := validateAuthorizationFlow(flow); err != nil {
		return nil, err
	}
	unsupported := errors.New("authorization flow is unsupported by provider")
	if (provider == "claude_code") != (flow == "authorization_code" || flow == "claude_cli") {
		return nil, unsupported
	}
	if provider == "antigravity" && flow != "browser" {
		return nil, unsupported
	}
	if (provider == "xai_grok" || provider == "github_copilot" || provider == "factory_droid") && flow != "device_code" {
		return nil, unsupported
	}
	serviceID, err := text("service_id")
	if err != nil {
		return nil, err
	}
	if err := ValidateResourceID(serviceID); err != nil {
		return nil, err
	}
	for _, key := range []string{"expires_at", "created_at", "updated_at"} {
		if err := validateRFC3339(o[key], "authorization session "+key); err != nil {
			return nil, err
		}
	}
	session := object{
		"id": id, "provider": provider, "status": status, "flow": flow, "service_id": serviceID,
		"expires_at": o["expires_at"], "created_at": o["created_at"], "updated_at": o["updated_at"],
	}
	authorizationURL, hasURL := stringField(o, "authorization_url")
	if hasURL {
		if err := validateAuthorizationURL(authorizationURL); err != nil {
			return nil, err
		}
		session["authorization_url"] = authorizationURL
	} else if _, ok := o["authorization_url"]; ok {
		return nil, errors.New("authorization_url must be a string")
	}
	deviceCode, hasDeviceCode := o["device_code"]
	if hasDeviceCode {
		parsed, err := parseAuthorizationDeviceCode(deviceCode)
		if err != nil {
			return nil, err
		}
		session["device_code"] = parsed
	}
	switch {
	case status == "pending" && (flow == "browser" || flow == "authorization_code" || flow == "claude_cli"):
		if !hasURL {
			return nil, errors.New("pending browser authorization session requires authorization_url")
		}
		if hasDeviceCode {
			return nil, errors.New("browser authorization session must not include device_code")
		}
	case status == "pending" && flow == "device_code":
		if !hasDeviceCode {
			return nil, errors.New("pending Device Code authorization session requires device_code")
		}
		if hasURL {
			return nil, errors.New("Device Code authorization session must not include authorization_url")
		}
	case hasURL || hasDeviceCode:
		return nil, errors.New("terminal authorization session must not include login instructions")
	}
	if value, ok := o["error"]; ok {
		parsed, err := parseSubscriptionError(value)
		if err != nil {
			return nil, err
		}
		session["error"] = parsed
	}
	return session, nil
}
