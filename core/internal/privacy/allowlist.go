package privacy

import (
	"net"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// allowlist decides which detected spans keep their original text.
type allowlist struct {
	literals []string
	suffixes []string
	blocks   []*net.IPNet
}

func newAllowlist(rules []contract.PolicyAllowlistRule) *allowlist {
	result := &allowlist{}
	for _, rule := range rules {
		switch rule.Type {
		case contract.PolicyAllowlistTypeLiteral:
			result.literals = append(result.literals, strings.ToLower(rule.Value))
		case contract.PolicyAllowlistTypeDomainSuffix:
			result.suffixes = append(
				result.suffixes,
				strings.ToLower(strings.TrimPrefix(rule.Value, ".")),
			)
		case contract.PolicyAllowlistTypeCIDR:
			if _, block, err := net.ParseCIDR(rule.Value); err == nil {
				result.blocks = append(result.blocks, block)
			}
		}
	}
	return result
}

// allows reports whether a detected value is exempt from redaction.
//
// Host matching resolves the value to a hostname first, so a single
// domain_suffix rule covers a bare host, a URL on that host, and an address at
// that domain without the operator having to enumerate the forms.
func (list *allowlist) allows(kind Kind, value string) bool {
	if list == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, literal := range list.literals {
		if normalized == literal {
			return true
		}
	}
	if host := allowlistHost(kind, value); host != "" {
		for _, suffix := range list.suffixes {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
		}
	}
	if len(list.blocks) > 0 {
		if address := allowlistAddress(kind, value); address != nil {
			for _, block := range list.blocks {
				if block.Contains(address) {
					return true
				}
			}
		}
	}
	return false
}

func allowlistHost(kind Kind, value string) string {
	switch kind {
	case KindURL:
		return strings.ToLower(strings.TrimSuffix(placeholderURLHost(value), "."))
	case KindEmail:
		at := strings.LastIndex(value, "@")
		if at < 0 {
			return ""
		}
		return strings.ToLower(strings.TrimSuffix(value[at+1:], "."))
	default:
		return ""
	}
}

// allowlistAddress resolves the value to an IP so that a CIDR rule also covers a
// URL whose host is a bare address, such as a loopback development endpoint.
func allowlistAddress(kind Kind, value string) net.IP {
	switch kind {
	case KindIPAddress:
		return net.ParseIP(strings.TrimSpace(value))
	case KindURL:
		return net.ParseIP(placeholderURLHost(value))
	default:
		return nil
	}
}
