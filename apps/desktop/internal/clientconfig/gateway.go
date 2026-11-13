package clientconfig

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// LocalOrigin returns `http://127.0.0.1:<port>` or `http://localhost:<port>`
// for the gateway's root inference URL; Core announces localhost only while
// it serves `[::1]`.
func LocalOrigin(inferenceURL string) (string, error) {
	u, err := url.Parse(inferenceURL)
	if err != nil {
		return "", errors.New("invalid inference URL")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.ToLower(u.Scheme) != "http" || host != "127.0.0.1" && host != "localhost" ||
		u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(inferenceURL, "#") || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid local inference URL")
	}
	origin := "http://" + host
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", errors.New("invalid inference URL")
		}
		if n != 80 {
			origin += ":" + strconv.FormatUint(n, 10)
		}
	}
	return origin, nil
}
