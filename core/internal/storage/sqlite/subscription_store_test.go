package sqlite

import "testing"

func TestSubscriptionSecretGuardRejectsOAuthInternals(t *testing.T) {
	t.Parallel()
	for _, document := range []string{
		`{"access_token":"access-secret"}`,
		`{"refresh_token":"refresh-secret"}`,
		`{"id_token":"identity-secret"}`,
		`{"device_auth_id":"device-secret"}`,
		`{"code_verifier":"verifier-secret"}`,
		`{"authorization_code":"authorization-secret"}`,
		`{"message":"Bearer bearer-secret-value"}`,
	} {
		if !containsSubscriptionSecret([]byte(document)) {
			t.Fatalf("secret guard accepted %s", document)
		}
	}
	if containsSubscriptionSecret([]byte(`{"user_code":"ABCD-EFGH","status":"pending"}`)) {
		t.Fatal("short-lived public Device Code was classified as a stored credential")
	}
}
