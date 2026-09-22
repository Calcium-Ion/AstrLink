package contract

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ServiceProxy contains only public configuration. Authentication is stored separately.
type ServiceProxy struct {
	Mode          string `json:"mode"`
	URL           string `json:"url,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func (proxy *ServiceProxy) Validate(id ServiceID) error {
	if proxy == nil {
		return nil
	}
	switch proxy.Mode {
	case "inherit", "direct":
		if proxy.URL != "" || proxy.CredentialRef != "" {
			return fmt.Errorf("only custom proxy may specify an address or credential")
		}
	case "custom":
		if err := ValidateProxyURL(proxy.URL); err != nil {
			return err
		}
		if proxy.CredentialRef != "" && proxy.CredentialRef != "local://service-proxy/"+string(id) {
			return fmt.Errorf("invalid proxy credential reference")
		}
	default:
		return fmt.Errorf("proxy mode must be inherit, direct, or custom")
	}
	return nil
}

func ValidateProxyURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || len(value) > 2048 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "@?#\r\n\t") || u == nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("proxy URL must contain only a scheme, host and optional port")
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" {
		return fmt.Errorf("proxy scheme must be http, https, or socks5")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid proxy port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("invalid proxy port")
	}
	return nil
}

// ProxyCredential is accepted only on writes and never embedded in a Service.
type ProxyCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (credential ProxyCredential) Validate() error {
	if credential.Username == "" || len(credential.Username) > 255 || len(credential.Password) > 255 || strings.ContainsAny(credential.Username, ":\r\n\x00") || strings.ContainsAny(credential.Password, "\r\n\x00") {
		return fmt.Errorf("invalid proxy authentication")
	}
	return nil
}
