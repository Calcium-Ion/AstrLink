package contract

import "time"

// AccessTokenID is the stable, non-secret identifier used to attribute local
// inference requests without persisting or exposing the bearer token itself.
type AccessTokenID string

func (id AccessTokenID) Validate() error {
	return validateResourceID("access token", string(id))
}

// AccessTokenDocument is what a sync package carries about an access token
// beside the value a client presents: the name a user recognises the token by,
// where it came from, and when it was issued. The id is the entity id that names
// the token in the package, and the digest and hint an authentication reads are
// derived from the value on the device that stores it, so neither is repeated
// here. Source is "user" for a token the user created and "system_default" for
// the bootstrap token this installation issued.
type AccessTokenDocument struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}
