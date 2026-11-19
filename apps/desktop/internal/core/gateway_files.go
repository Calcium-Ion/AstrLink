package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// gatewayFileTimeout is how long saving a file of the gateway may take: a
// video is large.
const gatewayFileTimeout = 10 * time.Minute

// gatewayFileURL is the link of a file the gateway serves for a task, such
// as a generated image or video, parsed: a link to the gateway's own
// inference address (or the localhost one clients use) under a task's
// artifacts. The link carries its own access; no other address passes.
func gatewayFileURL(ready Ready, raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return nil, false
	}
	sameOrigin := func(base string) bool {
		origin, err := url.Parse(base)
		return err == nil && origin.Scheme == parsed.Scheme &&
			origin.Hostname() == parsed.Hostname() && port(origin) == port(parsed)
	}
	path := parsed.Path
	artifact := strings.HasPrefix(path, "/v1/tasks/") && strings.Contains(path, "/artifacts/") &&
		strings.HasSuffix(path, "/content")
	if !artifact || !(sameOrigin(ready.InferenceURL) || sameOrigin(ready.ClientInferenceURL)) {
		return nil, false
	}
	return parsed, true
}

// port is a URL's port, the scheme's default when it names none.
func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch u.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// GatewayFileURL is the link of a file the running gateway serves for a
// task, parsed; false for any other link, or while Core is not ready.
func (m *Manager) GatewayFileURL(raw string) (*url.URL, bool) {
	m.lock()
	ready := m.s.ready
	m.unlock()
	if ready == nil {
		return nil, false
	}
	return gatewayFileURL(*ready, raw)
}

// gatewayFiles reads files of the local gateway: no proxy, no redirect,
// and no control token, as the link carries its own access.
var gatewayFiles = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}()

// SaveGatewayFile saves the file the gateway serves at link to path, as it
// streams in. A download that breaks off leaves no partial file behind.
func SaveGatewayFile(ctx context.Context, link *url.URL, path string) error {
	ctx, cancel := context.WithTimeout(ctx, gatewayFileTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return fmt.Errorf("could not read the file from the gateway: %w", err)
	}
	response, err := gatewayFiles.Do(request)
	if err != nil {
		return fmt.Errorf("could not read the file from the gateway: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("the gateway answered %s", response.Status)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("could not create the file: %w", err)
	}
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("could not save the file from the gateway: %w", err)
	}
	return nil
}
