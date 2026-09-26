package accountauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ClaudeCodeSessionHeader carries the session of metadata.user_id; Claude Code
// sends it since 2.1.87.
const ClaudeCodeSessionHeader = "X-Claude-Code-Session-Id"

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// applyClaudeCodeClientHeaders replaces another SDK's fingerprint with the
// one of identity; nil overlay values delete the caller's extras, including
// client markers the identity does not send.
func applyClaudeCodeClientHeaders(header, clientHeaders http.Header, identity ClientIdentity) {
	clearClientHeaderPrefix(header, clientHeaders, "x-stainless-")
	for _, name := range []string{"X-App", "Anthropic-Dangerous-Direct-Browser-Access"} {
		if len(clientHeaders.Values(name)) > 0 {
			header[name] = nil
		}
	}
	for name, value := range claudeIdentityOrDefault(identity).Headers {
		header.Set(name, value)
	}
}

// claudeIdentityOrDefault treats the zero identity as the baseline.
func claudeIdentityOrDefault(identity ClientIdentity) ClientIdentity {
	if identity.UserAgent == "" {
		return DefaultClaudeIdentity()
	}
	return identity
}

// ScopedSessionID maps a caller's session into one account's namespace. The
// result is stable for that account, so upstream caching and grouping still
// work, but does not link accounts that one conversation fails over between.
func ScopedSessionID(serviceID contract.ServiceID, session string) string {
	sum := sha256.Sum256([]byte(string(serviceID) + "::" + session))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// ClaudeMetadataUserID returns the metadata.user_id of one account: a single
// device per account, so an account shared by several callers still presents
// one installation. Claude Code 2.1.78 replaced the legacy
// user_{device}_account_{uuid}_session_{id} form with JSON.
func ClaudeMetadataUserID(serviceID contract.ServiceID, accountUUID, session string, legacy bool) string {
	sum := sha256.Sum256([]byte("device:" + string(serviceID) + ":" + accountUUID))
	device := hex.EncodeToString(sum[:])
	if !uuidPattern.MatchString(accountUUID) {
		accountUUID = ""
	}
	if legacy {
		return "user_" + device + "_account_" + accountUUID + "_session_" + session
	}
	encoded, _ := json.Marshal(struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}{device, accountUUID, session})
	return string(encoded)
}

// ClaudeUserIDSession extracts the session from either metadata.user_id form.
func ClaudeUserIDSession(userID string) string {
	session, _ := parseClaudeUserID(userID)
	return session
}

// ClaudeLegacyUserID reports a user_{device}_account_..._session_... value.
func ClaudeLegacyUserID(userID string) bool {
	_, legacy := parseClaudeUserID(userID)
	return legacy
}

func parseClaudeUserID(userID string) (session string, legacy bool) {
	userID = strings.TrimSpace(userID)
	if strings.HasPrefix(userID, "{") {
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(userID), &payload) != nil {
			return "", false
		}
		return strings.TrimSpace(payload.SessionID), false
	}
	index := strings.LastIndex(userID, "_session_")
	if !strings.HasPrefix(userID, "user_") || index < 0 {
		return "", false
	}
	return strings.TrimSpace(userID[index+len("_session_"):]), true
}
