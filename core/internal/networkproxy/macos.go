package networkproxy

import (
	"fmt"
	"strings"
)

// scutil reads the same system proxy dictionary used by macOS applications,
// without requiring cgo. Ignore scoped/supplemental nested dictionaries.
func parseMacSettings(output string) (settings, error) {
	values := map[string]string{}
	var exceptions []string
	depth := 0
	inExceptions := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "{") {
			if depth == 1 {
				inExceptions = strings.HasPrefix(line, "ExceptionsList :")
			}
			depth++
			continue
		}
		if line == "}" {
			depth--
			if depth < 2 {
				inExceptions = false
			}
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if ok && depth == 1 {
			values[key] = value
		} else if ok && depth == 2 && inExceptions {
			exceptions = append(exceptions, value)
		}
	}
	if depth != 0 || !strings.HasPrefix(strings.TrimSpace(output), "<dictionary> {") {
		return settings{}, fmt.Errorf("invalid macOS system proxy response")
	}
	config := settings{
		bypass: strings.Join(exceptions, ","), excludeSimple: values["ExcludeSimpleHostnames"] == "1",
		automatic: values["ProxyAutoConfigEnable"] == "1" || values["ProxyAutoDiscoveryEnable"] == "1",
	}
	for _, protocol := range []struct {
		key, scheme string
		target      *string
	}{
		{"HTTP", "http", &config.http},
		{"HTTPS", "http", &config.https}, // HTTPS destinations use HTTP CONNECT.
		{"SOCKS", "socks5", &config.socks},
	} {
		if values[protocol.key+"Enable"] != "1" {
			continue
		}
		address, err := proxyAddress(protocol.scheme, values[protocol.key+"Proxy"], values[protocol.key+"Port"])
		if err != nil {
			return settings{}, err
		}
		*protocol.target = address
	}
	return config, nil
}
