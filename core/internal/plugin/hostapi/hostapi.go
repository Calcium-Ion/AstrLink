// Package hostapi connects plugins to the gateway: it sends their HTTP
// requests on the user's terms, keeps their values and reads their settings
// from the gateway settings.
package hostapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// CredentialReader reads a plugin's credential and the origin it is bound to.
type CredentialReader interface {
	PluginCredential(ctx context.Context, plugin, name string) (secret []byte, origin string, err error)
}

// Fetcher sends plugin requests. Plugins are trusted code and reach any
// address; what stays the user's is the credentials the gateway keeps, which
// go only to the server each is bound to, over plain http beyond this
// machine only where the user allowed it for that server. It follows no
// redirect, sends no User-Agent of its own and nothing of the gateway's,
// and reads at most plugin.MaxFetchResponseBytes of a response. Fetch reads
// a response whole, for astrlink.http.fetch; Stream hands it back as it
// arrives, for fetch.
type Fetcher struct {
	Credentials CredentialReader
	// Client sends the requests; nil uses the gateway's proxy settings.
	Client *http.Client
}

func (fetcher *Fetcher) Fetch(ctx context.Context, target *plugin.Plugin, request plugin.HTTPRequest) plugin.HTTPResponse {
	started := time.Now()
	fail := func(reason string, status int) plugin.HTTPResponse {
		return plugin.HTTPResponse{Status: status, Error: reason, Took: time.Since(started)}
	}
	var body io.Reader
	contentType := ""
	switch {
	case len(request.JSON) > 0:
		body, contentType = bytes.NewReader(request.JSON), "application/json"
	case request.Body != nil:
		body, contentType = strings.NewReader(*request.Body), "text/plain; charset=utf-8"
	case request.BodyBytes != nil:
		body, contentType = bytes.NewReader(request.BodyBytes), "application/octet-stream"
	}
	outgoing, err := http.NewRequestWithContext(ctx, request.Method, request.URL, body)
	if err != nil {
		return fail(plugin.FetchDenied, 0)
	}
	for name, value := range request.Headers {
		outgoing.Header.Set(name, value)
	}
	if contentType != "" && outgoing.Header.Get("Content-Type") == "" {
		outgoing.Header.Set("Content-Type", contentType)
	}
	if reason := fetcher.authorize(ctx, target, outgoing, request.Credential); reason != "" {
		return fail(reason, 0)
	}
	response, err := fetcher.client().Do(outgoing)
	if err != nil {
		return fail(transportFailure(ctx, err), 0)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, plugin.MaxFetchResponseBytes+1))
	if err != nil {
		return fail(transportFailure(ctx, err), response.StatusCode)
	}
	if len(data) > plugin.MaxFetchResponseBytes {
		return fail(plugin.FetchTooLarge, response.StatusCode)
	}
	header := make(map[string]string, len(response.Header))
	values := make(map[string][]string, len(response.Header))
	for name, list := range response.Header {
		if len(list) > 0 {
			header[strings.ToLower(name)] = list[0]
			values[strings.ToLower(name)] = list
		}
	}
	return plugin.HTTPResponse{Status: response.StatusCode, Header: header, Values: values, Body: data, Took: time.Since(started)}
}

// Stream sends a request of fetch on the same terms as Fetch and hands the
// response back as it arrives. Reading its body past
// plugin.MaxFetchResponseBytes fails with plugin.FetchTooLarge.
func (fetcher *Fetcher) Stream(ctx context.Context, target *plugin.Plugin, outgoing *http.Request, credential string) (*http.Response, error) {
	if reason := fetcher.authorize(ctx, target, outgoing, credential); reason != "" {
		return nil, &plugin.FetchError{Reason: reason}
	}
	response, err := fetcher.client().Do(outgoing)
	if err != nil {
		return nil, &plugin.FetchError{Reason: transportFailure(ctx, err), Err: err}
	}
	if response.ContentLength > plugin.MaxFetchResponseBytes {
		response.Body.Close()
		return nil, &plugin.FetchError{Reason: plugin.FetchTooLarge}
	}
	response.Body = &limitedBody{ReadCloser: response.Body, left: plugin.MaxFetchResponseBytes}
	return response, nil
}

// authorize puts the user's terms on a plugin's request: the credential it
// names goes only to the server it is bound to, and the request carries no
// User-Agent unless the plugin set one. It returns the reason it refuses
// the request with, "" when it goes.
func (fetcher *Fetcher) authorize(ctx context.Context, target *plugin.Plugin, outgoing *http.Request, credential string) string {
	origin, err := plugin.NormalizeOrigin(outgoing.URL.String())
	if err != nil {
		return plugin.FetchDenied
	}
	if credential != "" {
		slot, declared := target.Meta.Credential(credential)
		if !declared || fetcher.Credentials == nil || !allowed(target.Settings.Origins, origin) {
			return plugin.FetchDenied
		}
		secret, bound, err := fetcher.Credentials.PluginCredential(ctx, target.Key, slot.Name)
		if err != nil {
			return plugin.FetchDenied
		}
		value := string(secret)
		clear(secret)
		if bound != origin {
			return plugin.FetchDenied
		}
		if slot.Scheme != "" {
			value = slot.Scheme + " " + value
		}
		outgoing.Header.Set(slot.Header, value)
	}
	if outgoing.Header.Get("User-Agent") == "" {
		// An empty value keeps net/http from adding its own.
		outgoing.Header.Set("User-Agent", "")
	}
	return ""
}

// client sends the plugins' requests, through the gateway's proxy unless
// Client says otherwise, and follows no redirect.
func (fetcher *Fetcher) client() *http.Client {
	client := fetcher.Client
	if client == nil {
		client = networkproxy.WrapClient(&http.Client{})
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &noRedirect
}

// limitedBody fails once a response body passes what a plugin may read.
type limitedBody struct {
	io.ReadCloser
	left int64
}

func (body *limitedBody) Read(p []byte) (int, error) {
	if body.left <= 0 {
		// Whether anything is left to read decides between the end and
		// too much.
		var probe [1]byte
		if n, err := body.ReadCloser.Read(probe[:]); n == 0 {
			return 0, err
		}
		return 0, &plugin.FetchError{Reason: plugin.FetchTooLarge}
	}
	if int64(len(p)) > body.left {
		p = p[:body.left]
	}
	n, err := body.ReadCloser.Read(p)
	body.left -= int64(n)
	return n, err
}

// allowed reports whether a credential may go to origin: one of the
// servers of the plugin's settings, over plain http beyond this machine only
// with the user's consent.
func allowed(origins []plugin.NetworkOrigin, origin string) bool {
	for _, granted := range origins {
		if granted.Origin == origin {
			return granted.AllowPlaintext || !plugin.Plaintext(origin)
		}
	}
	return false
}

func transportFailure(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return plugin.FetchTimeout
	}
	return plugin.FetchNetwork
}

// Store keeps astrlink.store values in the gateway settings.
type Store struct {
	Values storage.PluginStore
}

func (store Store) Get(ctx context.Context, owner, key string) (json.RawMessage, bool, error) {
	return store.Values.GetPluginValue(ctx, owner, key)
}

func (store Store) Set(ctx context.Context, owner, key string, value json.RawMessage) error {
	return store.Values.PutPluginValue(ctx, owner, key, value)
}

func (store Store) Delete(ctx context.Context, owner, key string) error {
	return store.Values.DeletePluginValue(ctx, owner, key)
}

func (store Store) Keys(ctx context.Context, owner, prefix string) ([]string, error) {
	return store.Values.PluginValueKeys(ctx, owner, prefix)
}

// Settings reads the plugins' settings from the gateway settings.
type Settings struct {
	Store storage.PluginStore
}

func (settings Settings) PluginSettings(ctx context.Context) (map[string]plugin.Settings, error) {
	records, err := settings.Store.ListPluginSettings(ctx)
	if err != nil {
		return nil, err
	}
	all := make(map[string]plugin.Settings, len(records))
	for _, record := range records {
		origins := make([]plugin.NetworkOrigin, 0, len(record.Origins))
		for _, origin := range record.Origins {
			origins = append(origins, plugin.NetworkOrigin{Origin: origin.Origin, AllowPlaintext: origin.AllowPlaintext})
		}
		all[record.Key] = plugin.Settings{
			Enabled:          record.Enabled,
			Capabilities:     record.Capabilities,
			Config:           record.Config,
			Origins:          origins,
			Trusted:          record.Trusted,
			TurnWait:         time.Duration(record.TurnWaitMs) * time.Millisecond,
			ExcludedClients:  record.ExcludedClients,
			WorkspaceBinding: record.WorkspaceBinding,
			AutoRefresh:      record.AutoRefresh,
			LookupEnabled:    record.LookupEnabled,
			LimitsMs:         record.HookLimitsMs,
			FailClosed:       record.FailClosed,
			UIEnabled:        record.UIEnabled,
		}
	}
	return all, nil
}

// Metadata reads how sessions use attribute values from the gateway
// settings.
type Metadata struct {
	Store storage.MetadataStore
}

func (metadata Metadata) ValueStats(ctx context.Context, key string, limit int) ([]plugin.ValueStat, error) {
	stats, err := metadata.Store.MetadataValueStats(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	values := make([]plugin.ValueStat, 0, len(stats))
	for _, stat := range stats {
		value := plugin.ValueStat{Value: stat.Value, Sessions: stat.Sessions}
		if stat.LastUsedAt != nil {
			value.LastUsedAt = stat.LastUsedAt.UnixMilli()
		}
		values = append(values, value)
	}
	return values, nil
}
