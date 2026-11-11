package contract

import (
	"fmt"
	"time"
)

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
//
// ServiceScope and ServiceIDs are present only for a token limited to
// selected providers. A build that predates them refuses such a document and
// leaves the token out instead of storing it without its limit.
type AccessTokenDocument struct {
	Name         string      `json:"name"`
	Source       string      `json:"source"`
	CreatedAt    time.Time   `json:"created_at"`
	ServiceScope string      `json:"service_scope,omitempty"`
	ServiceIDs   []ServiceID `json:"service_ids,omitempty"`
}

// AccessTokenServiceScope says which API providers requests made with an
// access token may reach.
type AccessTokenServiceScope string

const (
	// AccessTokenServicesAll lets the token use every enabled provider,
	// including providers added later. Tokens issued before scopes existed
	// read as this.
	AccessTokenServicesAll AccessTokenServiceScope = "all"
	// AccessTokenServicesSelected limits the token to the listed providers.
	// A selection whose providers were all deleted reaches none: it never
	// widens back to every provider.
	AccessTokenServicesSelected AccessTokenServiceScope = "selected"
)

// MaxAccessTokenServices bounds one token's provider list.
const MaxAccessTokenServices = 200

// AccessTokenServices is the provider scope of one access token.
type AccessTokenServices struct {
	Scope      AccessTokenServiceScope `json:"service_scope"`
	ServiceIDs []ServiceID             `json:"service_ids"`
}

// AllAccessTokenServices is the scope of an unrestricted token.
func AllAccessTokenServices() AccessTokenServices {
	return AccessTokenServices{Scope: AccessTokenServicesAll, ServiceIDs: []ServiceID{}}
}

// Restricted reports whether the token is limited to selected providers. An
// empty scope is the zero value of a token issued before scopes existed.
func (services AccessTokenServices) Restricted() bool {
	return services.Scope == AccessTokenServicesSelected
}

// Allows reports whether the token may use the provider.
func (services AccessTokenServices) Allows(id ServiceID) bool {
	if !services.Restricted() {
		return true
	}
	for _, allowed := range services.ServiceIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

// Validate checks a scope a caller asks for. A selected scope must name at
// least one provider; only deleting providers can leave a selection empty.
func (services AccessTokenServices) Validate() error {
	switch services.Scope {
	case AccessTokenServicesAll:
		if len(services.ServiceIDs) != 0 {
			return fmt.Errorf("service_ids must be empty when service_scope is all")
		}
		return nil
	case AccessTokenServicesSelected:
		if len(services.ServiceIDs) == 0 {
			return fmt.Errorf("service_ids must name at least one API provider")
		}
		if len(services.ServiceIDs) > MaxAccessTokenServices {
			return fmt.Errorf("service_ids names more than %d API providers", MaxAccessTokenServices)
		}
		seen := make(map[ServiceID]bool, len(services.ServiceIDs))
		for _, id := range services.ServiceIDs {
			if err := id.Validate(); err != nil {
				return fmt.Errorf("service_ids: %w", err)
			}
			if seen[id] {
				return fmt.Errorf("service_ids repeats %q", id)
			}
			seen[id] = true
		}
		return nil
	default:
		return fmt.Errorf("service_scope must be all or selected")
	}
}
