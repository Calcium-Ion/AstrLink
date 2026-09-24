package contract

import (
	"fmt"
	"strings"
)

// MaxModelRedirects bounds the redirect table kept in routing settings.
const MaxModelRedirects = 200

// ModelRedirect routes a client-requested model name to another model before
// provider selection. Responses keep the client's model name; only the
// upstream request carries To. Redirects are a single hop: To is never looked
// up again.
type ModelRedirect struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Enabled bool   `json:"enabled"`
}

func (redirect ModelRedirect) Validate() error {
	if err := validateRedirectModel("from", redirect.From); err != nil {
		return err
	}
	if err := validateRedirectModel("to", redirect.To); err != nil {
		return err
	}
	if redirect.From == redirect.To {
		return fmt.Errorf("model redirect %q must target a different model", redirect.From)
	}
	if redirect.To == AstrLinkAutoModelID {
		return fmt.Errorf("model redirect %q must not target %s", redirect.From, AstrLinkAutoModelID)
	}
	return nil
}

func (redirect *ModelRedirect) UnmarshalJSON(data []byte) error {
	type document ModelRedirect
	var value document
	if err := decodePolicy(data, &value, []string{"from", "to", "enabled"}, nil); err != nil {
		return err
	}
	if err := ModelRedirect(value).Validate(); err != nil {
		return err
	}
	*redirect = ModelRedirect(value)
	return nil
}

// ValidateModelRedirects checks the whole table: sources are unique and no
// target is another rule's source, so a redirect never depends on rule order
// or on whether a later rule is enabled.
func ValidateModelRedirects(redirects []ModelRedirect) error {
	if len(redirects) > MaxModelRedirects {
		return fmt.Errorf("model_redirects must contain at most %d rules", MaxModelRedirects)
	}
	sources := make(map[string]struct{}, len(redirects))
	for _, redirect := range redirects {
		if err := redirect.Validate(); err != nil {
			return err
		}
		if _, exists := sources[redirect.From]; exists {
			return fmt.Errorf("model redirect source %q is duplicated", redirect.From)
		}
		sources[redirect.From] = struct{}{}
	}
	for _, redirect := range redirects {
		if _, exists := sources[redirect.To]; exists {
			return fmt.Errorf("model redirect target %q must not be another rule's source", redirect.To)
		}
	}
	return nil
}

// ResolveModelRedirect returns the enabled rule whose source exactly matches
// model.
func ResolveModelRedirect(redirects []ModelRedirect, model string) (ModelRedirect, bool) {
	if model == "" {
		return ModelRedirect{}, false
	}
	for _, redirect := range redirects {
		if redirect.Enabled && redirect.From == model {
			return redirect, true
		}
	}
	return ModelRedirect{}, false
}

// RequestModelRedirect records the redirect applied to one request: From is
// the client-requested model and To the model used for routing.
type RequestModelRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (redirect RequestModelRedirect) Validate() error {
	if err := validateBoundedText("model_redirect.from", redirect.From, 256, false); err != nil {
		return err
	}
	if err := validateBoundedText("model_redirect.to", redirect.To, 256, false); err != nil {
		return err
	}
	if redirect.From == redirect.To {
		return fmt.Errorf("model_redirect must change the model")
	}
	return nil
}

func validateRedirectModel(field, value string) error {
	if err := validateBoundedText(field, value, 256, false); err != nil {
		return err
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not start or end with whitespace", field)
	}
	return nil
}
