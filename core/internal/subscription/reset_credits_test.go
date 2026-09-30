package subscription

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeResetCreditsFiltersAndSanitizes(t *testing.T) {
	result, err := decodeResetCredits([]byte(`{"available_count":2,"credits":[
{"id":"private-credit","reset_type":"codex_rate_limits","status":"available","expires_at":"2026-10-15T12:00:00Z","profile_user_id":"private-user"},
{"reset_type":"codex_rate_limits","status":"available","expires_at":null},
{"reset_type":"codex_rate_limits","status":"redeemed","expires_at":null},
{"reset_type":"another_product","status":"available","expires_at":null}]}`))
	if err != nil || result.AvailableCount != 2 || len(result.Credits) != 2 || result.Credits[0].ExpiresAt == nil || result.Credits[1].ExpiresAt != nil {
		t.Fatalf("details=%+v error=%v", result, err)
	}
	body, err := json.Marshal(result)
	if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "profile") {
		t.Fatalf("unsafe public details: %s %v", body, err)
	}
	for _, body := range []string{`null`, `{}`, `{"available_count":-1,"credits":[]}`, `{"available_count":1001,"credits":[]}`, `{"available_count":1,"credits":null}`, `{"available_count":1,"credits":[{"status":"available","reset_type":"codex_rate_limits","expires_at":"invalid"}]}`} {
		if _, err := decodeResetCredits([]byte(body)); err == nil {
			t.Fatalf("accepted invalid details: %s", body)
		}
	}
}
