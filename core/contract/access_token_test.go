package contract

import "testing"

func TestAccessTokenIDValidate(t *testing.T) {
	for _, test := range []struct {
		id    AccessTokenID
		valid bool
	}{
		{id: "access_token_01", valid: true},
		{id: "tok", valid: true},
		{id: "Access_token_01", valid: false},
		{id: "at", valid: false},
		{id: "access.token", valid: false},
	} {
		if err := test.id.Validate(); (err == nil) != test.valid {
			t.Fatalf("AccessTokenID(%q).Validate() error = %v, valid=%t", test.id, err, test.valid)
		}
	}
}
