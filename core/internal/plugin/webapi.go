package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/webapi"
)

//go:embed prelude.js
var preludeSource string

// prelude is the script that adds AstrLink's globals to every plugin
// runtime, compiled once.
var prelude = sync.OnceValues(func() (*moejs.Script, error) {
	return moejs.CompileScript(preludeName, preludeSource)
})

// preludeName is the prelude's name in stack traces.
const preludeName = "astrlink:prelude"

// processStart is the zero of process.hrtime and process.uptime.
var processStart = time.Now()

// maxRandomBytes is what crypto.getRandomValues fills at most.
const maxRandomBytes = 65536

// The headers through which fetch tells its transport which call sends a
// request, and the request's credential and timeout. The transport drops
// them, with every other X-AstrLink- header, before the request leaves.
const (
	fetchHeader      = "X-Astrlink-Fetch"
	credentialHeader = "X-Astrlink-Credential"
	timeoutHeader    = "X-Astrlink-Timeout"
)

// installWeb adds the web APIs of moejs (package webapi) on the runtime's
// loop, and what prelude.js adds over them: console, crypto,
// astrlink.textStream, and fetch on the host's terms, with the promises of
// the web APIs settling as tasks of the call. fetch works only while an
// apiVersion 2 hook runs; the rest works anywhere.
func (bound *instance) installWeb() error {
	client := &http.Client{
		Transport: fetchTransport{bound},
		// A redirect comes back as it is, as from astrlink.http.fetch.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	api, err := webapi.Install(bound.loop, webapi.Options{Client: client})
	if err != nil {
		return err
	}
	bound.web = api
	runtime := bound.runtime
	host := map[string]any{
		"fetch": runtime.Function("fetch", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil || !c.async {
				return moejs.Undefined(), realm.TypeError("fetch works only while the host runs a hook of an apiVersion 2 plugin")
			}
			return beginFetch(c, realm, args)
		}),
		"failure": runtime.Function("failure", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			id, err := stringArg(realm, args, 0, "id")
			if err != nil || c == nil {
				return moejs.Undefined(), err
			}
			if reason := c.fetchFailure(id); reason != "" {
				return moejs.String(reason), nil
			}
			return moejs.Undefined(), nil
		}),
		// later calls fn with value in a task of the loop, as the call's
		// script time: how the prelude relays a promise of the web APIs.
		"later": runtime.Function("later", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			fn, value := moejs.Arg(args, 0), moejs.Arg(args, 1)
			bound.queue()
			kept := bound.keep(fn, value)
			bound.post(func(runtime *moejs.Runtime) error {
				bound.release(kept)
				_, err := runtime.CallFunction(fn, moejs.Undefined(), value)
				return err
			})
			return moejs.Undefined(), nil
		}),
		"log": runtime.Function("log", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				// A module's top level has no call to log for.
				return moejs.Undefined(), nil
			}
			return hostLog(c, realm, args)
		}),
		"randomBytes": runtime.Function("randomBytes", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			number, err := realm.ToNumber(moejs.Arg(args, 0))
			if err != nil || number != number || number < 0 || number > maxRandomBytes {
				return moejs.Undefined(), realm.RangeError("crypto.getRandomValues fills at most %d bytes", maxRandomBytes)
			}
			data := make([]byte, int(number))
			_, _ = rand.Read(data)
			return moejs.String(base64.StdEncoding.EncodeToString(data)), nil
		}),
		"randomUUID": runtime.Function("randomUUID", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.String(randomUUID()), nil
		}),
		"digest": runtime.Function("digest", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "algorithm")
			if err != nil {
				return moejs.Undefined(), err
			}
			newHash := hashOf(name)
			if newHash == nil {
				return moejs.Undefined(), realm.TypeError("crypto.subtle.digest supports SHA-1, SHA-256, SHA-384 and SHA-512, not %q", name)
			}
			data, err := base64Arg(realm, args, 1, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			sum := newHash()
			sum.Write(data)
			return moejs.String(base64.StdEncoding.EncodeToString(sum.Sum(nil))), nil
		}),
		"hmac": runtime.Function("hmac", 3, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := stringArg(realm, args, 0, "hash")
			if err != nil {
				return moejs.Undefined(), err
			}
			newHash := hashOf(name)
			if newHash == nil {
				return moejs.Undefined(), realm.TypeError("HMAC supports SHA-1, SHA-256, SHA-384 and SHA-512, not %q", name)
			}
			key, err := base64Arg(realm, args, 1, "key")
			if err != nil {
				return moejs.Undefined(), err
			}
			data, err := base64Arg(realm, args, 2, "data")
			if err != nil {
				return moejs.Undefined(), err
			}
			mac := hmac.New(newHash, key)
			mac.Write(data)
			return moejs.String(base64.StdEncoding.EncodeToString(mac.Sum(nil))), nil
		}),
	}
	script, err := prelude()
	if err != nil {
		return fmt.Errorf("compile the plugin prelude: %w", err)
	}
	if err := runWith(runtime, script, host); err != nil {
		return fmt.Errorf("run the plugin prelude: %w", err)
	}
	return nil
}

// runWith runs script, whose value is a function, and calls it with host,
// so that the host's primitives reach the script without a global: a
// global deleted afterwards would leave the global object in dictionary
// mode, where every global read takes the slow path.
func runWith(runtime *moejs.Runtime, script *moejs.Script, host map[string]any) error {
	fn, err := runtime.RunScript(script)
	if err != nil {
		return err
	}
	value, err := runtime.FromGo(host)
	if err != nil {
		return err
	}
	_, err = runtime.CallFunction(fn, moejs.Undefined(), value)
	return err
}

// beginFetch checks that the call may send a request of fetch with the
// credential and timeout of its init, args[0] and args[1], and returns the
// request's id, which its X-AstrLink-Fetch header carries to the transport.
func beginFetch(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	if !c.granted(CapabilityNetwork) {
		return moejs.Undefined(), realm.TypeError("fetch needs the %s capability", CapabilityNetwork)
	}
	if c.plugin.services.Fetcher == nil {
		return moejs.Undefined(), realm.TypeError("fetch is unavailable")
	}
	if credential := moejs.Arg(args, 0); !credential.IsUndefined() {
		name, err := realm.ToString(credential)
		if err != nil {
			return moejs.Undefined(), err
		}
		if _, declared := c.plugin.Meta.Credential(name.GoString()); !declared {
			return moejs.Undefined(), realm.TypeError("fetch: credential %q is not declared in meta.credentials", name.GoString())
		}
	}
	if timeout := moejs.Arg(args, 1); !timeout.IsUndefined() {
		number, err := realm.ToNumber(timeout)
		if err != nil {
			return moejs.Undefined(), err
		}
		if number != number || number < 0 || time.Duration(number)*time.Millisecond > MaxFetchTimeout {
			return moejs.Undefined(), realm.TypeError("fetch: timeoutMs must be between 1 and %d", MaxFetchTimeout.Milliseconds())
		}
	}
	c.queued = true
	c.network.Lock()
	c.fetchSeq++
	id := c.instance.fetches.token + "." + strconv.Itoa(c.fetchSeq)
	c.network.Unlock()
	return moejs.String(id), nil
}

// fetchCalls is the call whose requests the runtime's fetch sends, for the
// transport, which runs on goroutines of its own. The token of each call is
// new, so a request of a call that ended is never another's.
type fetchCalls struct {
	mutex   sync.Mutex
	current *call
	token   string
	calls   uint64
}

// begin binds the runtime to c: its host functions serve c, and its fetch
// sends c's requests within c's context.
func (bound *instance) begin(c *call) {
	bound.current = c
	if bound.web == nil {
		return
	}
	bound.web.SetContext(c.ctx)
	bound.fetches.mutex.Lock()
	bound.fetches.calls++
	bound.fetches.current, bound.fetches.token = c, strconv.FormatUint(bound.fetches.calls, 36)
	bound.fetches.mutex.Unlock()
}

// finish unbinds the runtime from the call that ended.
func (bound *instance) finish(*call) {
	bound.current = nil
	bound.fetches.mutex.Lock()
	bound.fetches.current, bound.fetches.token = nil, ""
	bound.fetches.mutex.Unlock()
}

// sender returns the call that sends the request of id, nil for a call
// that ended.
func (calls *fetchCalls) sender(id string) *call {
	token, _, _ := strings.Cut(id, ".")
	calls.mutex.Lock()
	defer calls.mutex.Unlock()
	if token == "" || token != calls.token {
		return nil
	}
	return calls.current
}

// fetchFailure takes the reason of the request id when the host refused it
// or it failed on the host's terms.
func (c *call) fetchFailure(id string) string {
	c.network.Lock()
	defer c.network.Unlock()
	reason := c.failures[id]
	delete(c.failures, id)
	return reason
}

// FetchError is why a request of fetch got no response, or why its body
// broke off: Reason is one of the Fetch* errors of HTTPResponse.
type FetchError struct {
	Reason string
	Err    error
}

func (failure *FetchError) Error() string {
	text := map[string]string{
		FetchDenied:   "the request is not allowed",
		FetchTimeout:  "the request timed out",
		FetchNetwork:  "no response arrived",
		FetchTooLarge: fmt.Sprintf("the response is larger than %d MiB", MaxFetchResponseBytes>>20),
		FetchBudget:   "the call's request count or network time ran out",
	}[failure.Reason]
	if text == "" {
		text = failure.Reason
	}
	if failure.Err != nil {
		return text + ": " + failure.Err.Error()
	}
	return text
}

func (failure *FetchError) Unwrap() error { return failure.Err }

// StreamFetcher is a Fetcher that also hands a response of fetch back as it
// arrives, for its body to stream. The host has dropped the headers it
// keeps; credential names the slot of meta.credentials the request asked
// for. The caller reads the body, at most MaxFetchResponseBytes, and
// closes it. A request that got no response is a *FetchError.
type StreamFetcher interface {
	Fetcher
	Stream(ctx context.Context, plugin *Plugin, request *http.Request, credential string) (*http.Response, error)
}

// fetchTransport sends the requests of a runtime's fetch for the call that
// runs, within its budgets: they count with those of astrlink.http.fetch,
// and the call's network deadline and its own timeout bound each. The
// Fetcher sends it on the user's terms. It follows no redirect.
type fetchTransport struct{ bound *instance }

func (transport fetchTransport) RoundTrip(incoming *http.Request) (*http.Response, error) {
	if incoming.Body != nil {
		defer incoming.Body.Close()
	}
	id := incoming.Header.Get(fetchHeader)
	credential := incoming.Header.Get(credentialHeader)
	timeout := defaultFetchTimeout
	if text := incoming.Header.Get(timeoutHeader); text != "" {
		if ms, err := strconv.Atoi(text); err == nil && ms > 0 {
			timeout = min(time.Duration(ms)*time.Millisecond, MaxFetchTimeout)
		}
	}
	c := transport.bound.fetches.sender(id)
	if c == nil {
		return nil, &FetchError{Reason: FetchNetwork, Err: errCallEnded}
	}
	if incoming.ContentLength > MaxFetchBodyBytes {
		return nil, errBodyTooLarge
	}
	fail := func(reason string, err error) (*http.Response, error) {
		c.network.Lock()
		if c.failures == nil {
			c.failures = make(map[string]string)
		}
		c.failures[id] = reason
		c.network.Unlock()
		return nil, &FetchError{Reason: reason, Err: err}
	}
	c.network.Lock()
	allowed := c.fetched < maxAsyncFetches && time.Now().Before(c.networkDeadline)
	if allowed {
		c.fetched++
	}
	c.network.Unlock()
	if !allowed {
		c.count([]HTTPResponse{{Error: FetchBudget}}, 0)
		return fail(FetchBudget, nil)
	}
	started := time.Now()
	ctx, cancel := context.WithDeadlineCause(incoming.Context(), c.networkDeadline, errNetworkBudget)
	select {
	case c.fetchSlots <- struct{}{}:
	case <-ctx.Done():
		cancel()
		c.count([]HTTPResponse{{Error: FetchBudget}}, time.Since(started))
		return fail(FetchBudget, nil)
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, timeout)
	var once sync.Once
	// done ends the request: its context and its place among the call's
	// requests at once.
	done := func() {
		once.Do(func() {
			cancelRequest()
			cancel()
			<-c.fetchSlots
		})
	}
	// The request as it leaves: without the headers the host keeps, to the
	// URL's host.
	request := incoming.Clone(requestCtx)
	for name := range request.Header {
		if transportHeader(name) {
			delete(request.Header, name)
		}
	}
	request.Host = ""
	if request.Body != nil && request.Body != http.NoBody {
		request.Body = &requestBody{ReadCloser: request.Body, left: MaxFetchBodyBytes}
	}
	fetcher := c.plugin.services.Fetcher
	if streamer, ok := fetcher.(StreamFetcher); ok {
		response, err := streamer.Stream(requestCtx, c.plugin, request, credential)
		if err != nil {
			done()
			reason := FetchNetwork
			var failure *FetchError
			if errors.As(err, &failure) {
				reason = failure.Reason
			}
			reason = budgetReason(ctx, reason)
			c.count([]HTTPResponse{{Error: reason}}, time.Since(started))
			return fail(reason, err)
		}
		c.count([]HTTPResponse{{}}, time.Since(started))
		// The body streams within the request's timeout and the call's
		// network deadline; closing it ends the request.
		response.Body = &fetchBody{ReadCloser: response.Body, ctx: ctx, request: requestCtx, done: done}
		return response, nil
	}
	defer done()
	converted, err := bufferedRequest(request, credential, timeout)
	if err != nil {
		c.count([]HTTPResponse{{Error: FetchNetwork}}, time.Since(started))
		return nil, err
	}
	answer := fetcher.Fetch(requestCtx, c.plugin, converted)
	reason := answer.Error
	if reason != "" {
		reason = budgetReason(ctx, reason)
	}
	c.count([]HTTPResponse{{Error: reason}}, time.Since(started))
	if reason != "" {
		return fail(reason, nil)
	}
	return bufferedResponse(request, answer), nil
}

// errBodyTooLarge is the error of a request body past MaxFetchBodyBytes.
var errBodyTooLarge = fmt.Errorf("the body is larger than %d MiB", MaxFetchBodyBytes>>20)

// requestBody is the body of a request of fetch, which may stream: it
// fails past MaxFetchBodyBytes.
type requestBody struct {
	io.ReadCloser
	left int64
}

func (body *requestBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if body.left -= int64(n); body.left < 0 {
		return 0, errBodyTooLarge
	}
	return n, err
}

// budgetReason is FetchBudget for a request the call's network deadline
// cut short, reason otherwise.
func budgetReason(ctx context.Context, reason string) string {
	if (reason == FetchTimeout || reason == FetchNetwork) && errors.Is(context.Cause(ctx), errNetworkBudget) {
		return FetchBudget
	}
	return reason
}

// fetchBody is the body of a response of fetch as it streams: a read the
// request's timeout or the call's network deadline cut short fails with
// the reason, and closing it ends the request.
type fetchBody struct {
	io.ReadCloser
	ctx, request context.Context
	done         func()
}

func (body *fetchBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if err != nil && err != io.EOF && body.request.Err() != nil {
		return n, &FetchError{Reason: budgetReason(body.ctx, FetchTimeout), Err: err}
	}
	return n, err
}

func (body *fetchBody) Close() error {
	err := body.ReadCloser.Close()
	body.done()
	return err
}

// bufferedRequest is a request of fetch for a Fetcher that only sends whole
// requests: its body, at most MaxFetchBodyBytes, read into memory.
func bufferedRequest(request *http.Request, credential string, timeout time.Duration) (HTTPRequest, error) {
	converted := HTTPRequest{Method: request.Method, URL: request.URL.String(), Headers: map[string]string{}, Credential: credential, TimeoutMS: int(timeout.Milliseconds())}
	for name, values := range request.Header {
		converted.Headers[strings.ToLower(name)] = strings.Join(values, ", ")
	}
	if request.Body != nil && request.Body != http.NoBody {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return HTTPRequest{}, err
		}
		switch {
		case len(data) > 0 && strings.Contains(strings.ToLower(request.Header.Get("Content-Type")), "json") && json.Valid(data):
			converted.JSON = data
		case len(data) > 0 || request.Method != http.MethodGet && request.Method != http.MethodHead:
			converted.BodyBytes = data
			encoded := base64.StdEncoding.EncodeToString(data)
			converted.BodyBase64 = &encoded
		}
	}
	return converted, nil
}

// bufferedResponse is the response of a Fetcher that read it whole.
func bufferedResponse(request *http.Request, answer HTTPResponse) *http.Response {
	header := http.Header{}
	if len(answer.Values) > 0 {
		for _, name := range slices.Sorted(maps.Keys(answer.Values)) {
			for _, value := range answer.Values[name] {
				header.Add(name, value)
			}
		}
	} else {
		for name, value := range answer.Header {
			header.Set(name, value)
		}
	}
	return &http.Response{
		Status:        strconv.Itoa(answer.Status) + " " + http.StatusText(answer.Status),
		StatusCode:    answer.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(answer.Body)),
		ContentLength: int64(len(answer.Body)),
		Request:       request,
		// The Fetcher took out any content encoding.
		Uncompressed: true,
	}
}

func hashOf(name string) func() hash.Hash {
	switch strings.ToUpper(name) {
	case "SHA-1":
		return sha1.New
	case "SHA-256":
		return sha256.New
	case "SHA-384":
		return sha512.New384
	case "SHA-512":
		return sha512.New
	}
	return nil
}

func base64Arg(realm *moejs.Realm, args []moejs.Value, index int, name string) ([]byte, error) {
	text, err := stringArg(realm, args, index, name)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, realm.TypeError("the %s is not base64", name)
	}
	return data, nil
}

// randomUUID returns a version 4 UUID.
func randomUUID() string {
	var data [16]byte
	_, _ = rand.Read(data[:])
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	text := fmt.Sprintf("%x", data[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:32]
}
