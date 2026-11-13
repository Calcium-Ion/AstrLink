package clientupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
	"github.com/egoist/mygo/netproxy"
)

const (
	fetchTimeout = 20 * time.Second
	// maxLatestBytes bounds a version service's answer.
	maxLatestBytes = 1024 * 1024
)

// latestSource is where a client's latest version is published.
type latestSource struct {
	url string
	// body, when set, is posted as JSON.
	body map[string]string
}

func latestSourceOf(env *Environment, id ClientID, install *installation) latestSource {
	method := install.method
	switch {
	case method.kind == methodBrew:
		kind := "formula"
		if method.cask {
			kind = "cask"
		}
		return latestSource{url: "https://formulae.brew.sh/api/" + kind + "/" + method.pkg + ".json"}
	case method.kind == methodNative && id == ClientClaude:
		channel := "latest"
		if env.readJSON(filepath.Join(env.home, ".claude", "settings.json"))["autoUpdatesChannel"] == "stable" {
			channel = "stable"
		}
		return latestSource{url: "https://downloads.claude.ai/claude-code-releases/" + channel}
	case id == ClientCursor:
		// The same public endpoint `cursor-agent update` asks for its
		// channel.
		return latestSource{url: cursorLatestURL, body: map[string]string{"channel": cursorChannel(env)}}
	case id == ClientPi:
		return latestSource{url: piLatestURL}
	}
	return latestSource{url: "https://registry.npmjs.org/" + id.packages()[0] + "/latest"}
}

func parseLatest(body string, method installMethod, id ClientID) (clientVersion, *clientError) {
	invalid := newError("version", "Invalid latest version")
	if method == native && id == ClientClaude {
		if v, ok := parseClientVersion(id, strings.TrimSpace(body)); ok {
			return v, nil
		}
		return clientVersion{}, invalid
	}
	value, err := format.DecodeJSON(body)
	if err != nil {
		return clientVersion{}, newError("version", err.Error())
	}
	object, _ := value.(map[string]any)
	var text any = object["version"]
	if method.kind == methodBrew && !method.cask {
		versions, _ := object["versions"].(map[string]any)
		text = versions["stable"]
	}
	raw, ok := text.(string)
	if !ok {
		return clientVersion{}, newError("version", "Missing latest version")
	}
	v, ok := parseClientVersion(id, raw)
	if !ok {
		return clientVersion{}, invalid
	}
	if len(v.order.pre) > 0 {
		return clientVersion{}, newError("version", "Latest endpoint returned a prerelease")
	}
	return v, nil
}

// httpClient asks version services through the system's proxy, or
// straight out.
func httpClient(useSystemProxy bool) *http.Client {
	transport := netproxy.Transport().Clone()
	if !useSystemProxy {
		transport.Proxy = nil
	}
	return &http.Client{Transport: transport, Timeout: fetchTimeout}
}

func fetchLatest(ctx context.Context, client *http.Client, source latestSource, install *installation, id ClientID) (clientVersion, *clientError) {
	method, body := http.MethodGet, io.Reader(nil)
	if source.body != nil {
		encoded, _ := json.Marshal(source.body)
		method, body = http.MethodPost, bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, source.url, body)
	if err != nil {
		return clientVersion{}, newError("network", err.Error())
	}
	// Like the Tauri shell's, the request names no client.
	request.Header.Set("User-Agent", "")
	if source.body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("connect-protocol-version", "1")
	}
	response, err := client.Do(request)
	if err != nil {
		return clientVersion{}, newError("network", err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return clientVersion{}, newError("rate_limit", "Version service rate limit")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return clientVersion{}, newError("network", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxLatestBytes+1))
	if err != nil {
		return clientVersion{}, newError("network", err.Error())
	}
	if len(data) > maxLatestBytes {
		return clientVersion{}, newError("version", "Version response is too large")
	}
	return parseLatest(strings.ToValidUTF8(string(data), "�"), install.method, id)
}
