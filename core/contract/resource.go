package contract

import (
	"fmt"
	"regexp"
)

var resourceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func validateResourceID(kind, value string) error {
	if len(value) < 3 || len(value) > 96 || !resourceIDPattern.MatchString(value) {
		return fmt.Errorf("invalid %s id %q", kind, value)
	}
	return nil
}

func (id EndpointID) Validate() error {
	return validateResourceID("endpoint", string(id))
}

func (id RouteID) Validate() error {
	return validateResourceID("route", string(id))
}

func (id PolicyID) Validate() error {
	return validateResourceID("policy", string(id))
}
