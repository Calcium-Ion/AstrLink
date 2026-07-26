package contract

// AccessTokenID is the stable, non-secret identifier used to attribute local
// inference requests without persisting or exposing the bearer token itself.
type AccessTokenID string

func (id AccessTokenID) Validate() error {
	return validateResourceID("access token", string(id))
}
