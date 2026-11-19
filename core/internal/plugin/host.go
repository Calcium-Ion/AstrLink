package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
)

// Budgets of astrlink.http.fetch within one hook call.
const (
	maxFetchesPerCall = 16
	// maxAsyncFetches is the count of an apiVersion 2 call, whose requests
	// may overlap.
	maxAsyncFetches    = 32
	maxParallelFetches = 8
	// MaxFetchTimeout bounds one request; the call's network budget and
	// deadline bound it further.
	MaxFetchTimeout     = 30 * time.Second
	defaultFetchTimeout = 10 * time.Second
	// MaxFetchBodyBytes bounds a request body, and MaxFetchResponseBytes a
	// response body; a Fetcher answers FetchTooLarge past it.
	MaxFetchBodyBytes     = 1 << 20
	MaxFetchResponseBytes = 1 << 20
)

// Limits of astrlink.store and astrlink.log.
const (
	maxStoreKeyLength = 256
	// MaxStoreValueBytes bounds one stored value as JSON; a Store bounds
	// the total of a plugin's values.
	MaxStoreValueBytes = 64 << 10
	maxLogEntries      = 20
	maxLogBytes        = 1 << 10
)

// maxArgNodes bounds the values of one host function argument.
const maxArgNodes = 1 << 16

// Errors of astrlink.http.fetch, in HTTPResponse.Error.
const (
	// FetchDenied: the address, a header or the credential is not allowed.
	FetchDenied = "denied"
	// FetchTimeout: the request's own timeout passed.
	FetchTimeout = "timeout"
	// FetchNetwork: no response arrived.
	FetchNetwork = "network"
	// FetchTooLarge: the response passed MaxFetchResponseBytes.
	FetchTooLarge = "too_large"
	// FetchBudget: the call's request count or network time ran out.
	FetchBudget = "budget"
)

// HTTPRequest is one request of astrlink.http.fetch.
type HTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	// JSON is sent as an application/json body; Body as text; BodyBase64,
	// which fetch sends for binary bodies, as the bytes it encodes. A
	// request has at most one of them.
	JSON       json.RawMessage `json:"json,omitempty"`
	Body       *string         `json:"body,omitempty"`
	BodyBase64 *string         `json:"bodyBase64,omitempty"`
	// BodyBytes are the bytes of BodyBase64, decoded once the request is
	// checked.
	BodyBytes []byte `json:"-"`
	// Credential names a slot of meta.credentials, which the host writes
	// into the request when it goes to the address the slot is bound to.
	Credential string `json:"credential,omitempty"`
	TimeoutMS  int    `json:"timeoutMs,omitempty"`
}

// HTTPResponse is the outcome of one HTTPRequest. Status is 0 when no
// response arrived; Error then says why.
type HTTPResponse struct {
	Status int
	// Header holds the first value of each response header, keyed in
	// lowercase, and Values all of them, for fetch and node:http.
	Header map[string]string
	Values map[string][]string
	Body   []byte
	Error  string
	Took   time.Duration
}

// Fetcher sends the HTTP requests of a plugin. The host has checked their
// shape; the Fetcher enforces the binding of credentials to the servers
// the user gave them and the transport rules.
type Fetcher interface {
	Fetch(ctx context.Context, plugin *Plugin, request HTTPRequest) HTTPResponse
}

// Store keeps the values of astrlink.store, each plugin apart.
type Store interface {
	Get(ctx context.Context, plugin, key string) (value json.RawMessage, found bool, err error)
	Set(ctx context.Context, plugin, key string, value json.RawMessage) error
	Delete(ctx context.Context, plugin, key string) error
	Keys(ctx context.Context, plugin, prefix string) ([]string, error)
}

// Services are what the host functions reach outside the sandbox. A nil
// service makes its functions throw.
type Services struct {
	Fetcher Fetcher
	Store   Store
	// Metadata serves astrlink.meta.values.
	Metadata MetadataStats
	// Now is the clock of astrlink.clock.now; nil means time.Now.
	Now func() time.Time
	// Zone is the time zone of astrlink.clock.offset; nil means the
	// gateway's local one.
	Zone *time.Location
}

// RequestInfo is what astrlink.request.info returns: the request a hook runs
// for, without its body or any credential.
type RequestInfo struct {
	Protocol string `json:"protocol"`
	Model    string `json:"model"`
	Client   string `json:"client"`
	Stream   bool   `json:"stream"`
	// Meta holds the request's resolved metadata by attribute key.
	Meta    map[string]MetaValue `json:"meta"`
	Session *SessionRef          `json:"session,omitempty"`
}

// MetaValue is one resolved metadata value and where it came from: header,
// session or token.
type MetaValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// SessionRef names the session and turn of a request. First marks the turn
// the plugin first took part in: the session's first turn, or the first
// since the plugin was enabled.
type SessionRef struct {
	ID    string `json:"id"`
	Turn  int    `json:"turn"`
	First bool   `json:"first,omitempty"`
}

// MetadataStats reads how sessions use the values of an attribute, for
// astrlink.meta.values.
type MetadataStats interface {
	ValueStats(ctx context.Context, key string, limit int) ([]ValueStat, error)
}

// ValueStat is a value sessions are bound to: how many, and when one of them
// last made a request, in milliseconds since the Unix epoch, 0 for never.
type ValueStat struct {
	Value      string `json:"value"`
	Sessions   int    `json:"sessions"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

// Bounds of astrlink.meta.values.
const (
	defaultMetaValues = 50
	maxMetaValues     = 200
)

// CallOptions describe one hook call.
type CallOptions struct {
	// Request is what astrlink.request.info returns; nil outside a request.
	Request *RequestInfo
	// NetworkBudget bounds the time the call may wait on http.fetch in all;
	// zero allows none. An apiVersion 2 call's requests may overlap: it
	// bounds how long after the call started they may still run.
	NetworkBudget time.Duration
	// DryRun marks a call the plugin page runs by hand: its journal lines
	// say so, and it does not count in the hook's timing.
	DryRun bool
	// TaskFetch lets a driver hook of a task plugin read upstream data with
	// utils.fetch; without it utils.fetch throws.
	TaskFetch *TaskFetch
	// RequestFiles is what a request processing hook reads of its
	// request's files and keyword lists; without it those utils throw.
	RequestFiles *RequestFiles
}

// CallInfo is what the host observed during a call, for request records and
// the plugin page. It never holds request or response bodies.
type CallInfo struct {
	Elapsed time.Duration
	// Script is the JavaScript time; waits on the host do not count.
	Script      time.Duration
	Network     NetworkStats
	Logs        []LogEntry
	DroppedLogs int
}

// NetworkStats count a call's use of http.fetch: the requests it made, of
// them those denied and those that failed otherwise, and the time it waited.
type NetworkStats struct {
	Requests int
	Denied   int
	Errors   int
	Wait     time.Duration
}

// LogEntry is one astrlink.log line, cleaned of control characters.
type LogEntry struct {
	Level   string
	Message string
	Time    time.Time
}

// call is the state of one hook call, which the host functions read: what
// the plugin may do, its settings and budgets, and what it did.
type call struct {
	ctx context.Context
	// end cancels ctx, which ends the requests and host work the call
	// started, once the call ended.
	end    context.CancelCauseFunc
	plugin *Plugin
	// hook names the hook the call runs.
	hook     string
	options  CallOptions
	pool     *pool
	runtime  *moejs.Runtime
	instance *instance
	watchdog *watchdog
	// async marks an apiVersion 2 call: its hook runs on the runtime's
	// loop and may return a promise, and the host functions that wait on
	// the host return promises. An apiVersion 1 call's return their
	// results.
	async bool
	// slot is whether the call holds an execution slot of its pool, which
	// it gives up while it waits on the host.
	slot bool
	// timers are the timers the call set that are still to fire.
	timers map[*moejs.Object]callTimer
	// queued is set once the call gave the loop work, which may outlive
	// it.
	queued bool

	networkLeft time.Duration
	// networkDeadline ends the requests of an apiVersion 2 call, which may
	// overlap; fetchSlots bounds how many run at once.
	networkDeadline time.Time
	fetchSlots      chan struct{}
	// network guards what the call's requests count: fetch's transport
	// counts on goroutines of its own.
	network sync.Mutex
	fetched int
	// fetchSeq numbers the requests of fetch, and failures keeps why
	// those the host refused or that failed on its terms did, by id.
	fetchSeq int
	failures map[string]string
	stats    NetworkStats
	// taskFetches and taskFetchTime count the utils.fetch requests of a
	// task plugin's hook call and the time they took.
	taskFetches   int
	taskFetchTime time.Duration
	info          CallInfo
}

func (c *call) granted(capability string) bool { return slices.Contains(c.plugin.granted, capability) }

func (c *call) now() time.Time {
	if c.plugin.services.Now != nil {
		return c.plugin.services.Now()
	}
	return time.Now()
}

// wait runs fn, which waits on the host. The wait does not count as script
// time, and the call's execution slot goes to another call meanwhile.
func (c *call) wait(fn func()) error {
	c.leave()
	fn()
	return c.enter()
}

// io runs work, which waits on the host, and returns how it settles: at once
// in an apiVersion 1 call, as a promise in an apiVersion 2 call.
func (c *call) io(work func(ctx context.Context) settle) (moejs.Value, error) {
	if c.async {
		return c.later(work), nil
	}
	var result settle
	if err := c.wait(func() { result = work(c.ctx) }); err != nil {
		return moejs.Undefined(), err
	}
	return result(c.runtime)
}

type hostFunc func(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error)

// install sets the astrlink global. Its functions work only while the host
// runs a hook on this runtime: a module's top level has no call to serve.
func (bound *instance) install() error {
	runtime := bound.runtime
	define := func(name string, length int, fn hostFunc) moejs.Value {
		short := name[strings.LastIndex(name, ".")+1:]
		return runtime.Function(short, length, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				return moejs.Undefined(), realm.TypeError("astrlink.%s works only while the host runs a hook", name)
			}
			return fn(c, realm, args)
		})
	}
	err := runtime.SetGlobal("astrlink", map[string]any{
		"request": map[string]any{"info": define("request.info", 0, hostRequestInfo)},
		"http":    map[string]any{"fetch": define("http.fetch", 1, hostFetch)},
		"store": map[string]any{
			"get":    define("store.get", 1, hostStoreGet),
			"set":    define("store.set", 2, hostStoreSet),
			"delete": define("store.delete", 1, hostStoreDelete),
			"keys":   define("store.keys", 1, hostStoreKeys),
		},
		"config": map[string]any{"get": define("config.get", 0, hostConfigGet)},
		"meta":   map[string]any{"values": define("meta.values", 2, hostMetaValues)},
		"util":   map[string]any{"sha256": define("util.sha256", 1, hostSHA256)},
		"log":    define("log", 2, hostLog),
		"clock": map[string]any{
			"now":    define("clock.now", 0, hostClockNow),
			"offset": define("clock.offset", 1, hostClockOffset),
		},
	})
	if err != nil {
		return err
	}
	return bound.installWeb()
}

func hostRequestInfo(c *call, realm *moejs.Realm, _ []moejs.Value) (moejs.Value, error) {
	if !c.granted(CapabilityRequest) {
		return moejs.Undefined(), realm.TypeError("astrlink.request.info needs the %s capability", CapabilityRequest)
	}
	if c.options.Request == nil {
		return moejs.Null(), nil
	}
	return realm.FromGo(c.options.Request)
}

// hostMetaValues returns the values of one of the plugin's own attributes
// that sessions are bound to, the most recently used first.
func hostMetaValues(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	if !c.granted(CapabilityRequest) {
		return moejs.Undefined(), realm.TypeError("astrlink.meta.values needs the %s capability", CapabilityRequest)
	}
	if c.plugin.services.Metadata == nil {
		return moejs.Undefined(), realm.TypeError("astrlink.meta.values is unavailable")
	}
	key, err := stringArg(realm, args, 0, "key")
	if err != nil {
		return moejs.Undefined(), err
	}
	if !slices.ContainsFunc(c.plugin.Meta.Metadata, func(attribute MetadataAttribute) bool { return attribute.Key == key }) {
		return moejs.Undefined(), realm.RangeError("astrlink.meta.values reads only the attributes in meta.metadata")
	}
	limit := defaultMetaValues
	if argument := moejs.Arg(args, 1); !argument.IsUndefined() {
		number, err := realm.ToNumber(argument)
		if err != nil {
			return moejs.Undefined(), err
		}
		if number != number || number < 1 || number > maxMetaValues {
			return moejs.Undefined(), realm.RangeError("astrlink.meta.values takes a limit of 1 to %d", maxMetaValues)
		}
		limit = int(number)
	}
	return c.io(func(ctx context.Context) settle {
		stats, err := c.plugin.services.Metadata.ValueStats(ctx, key, limit)
		return func(runtime *moejs.Runtime) (moejs.Value, error) {
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("astrlink.meta.values: %w", err)
			}
			if stats == nil {
				stats = []ValueStat{}
			}
			return runtime.Realm().FromGo(stats)
		}
	})
}

func hostConfigGet(c *call, realm *moejs.Realm, _ []moejs.Value) (moejs.Value, error) {
	config := c.plugin.config
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}
	return realm.JSONParseGoString(string(config))
}

func hostSHA256(_ *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	text, err := stringArg(realm, args, 0, "text")
	if err != nil {
		return moejs.Undefined(), err
	}
	sum := sha256.Sum256([]byte(text))
	return moejs.String(hex.EncodeToString(sum[:])), nil
}

func hostClockNow(c *call, _ *moejs.Realm, _ []moejs.Value) (moejs.Value, error) {
	return moejs.Number(float64(c.now().UnixMilli())), nil
}

// hostClockOffset returns how many minutes the gateway's time zone is ahead
// of UTC at a moment, in milliseconds since the Unix epoch, now by default.
// Date in a plugin keeps to UTC; this is what writes a date as the user
// reads it.
func hostClockOffset(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	at := c.now()
	if argument := moejs.Arg(args, 0); !argument.IsUndefined() {
		number, err := realm.ToNumber(argument)
		if err != nil {
			return moejs.Undefined(), err
		}
		if math.IsNaN(number) || math.Abs(number) > maxClockMillis {
			return moejs.Undefined(), realm.RangeError("astrlink.clock.offset takes milliseconds since the Unix epoch")
		}
		at = time.UnixMilli(int64(number))
	}
	zone := c.plugin.services.Zone
	if zone == nil {
		zone = time.Local
	}
	_, offset := at.In(zone).Zone()
	return moejs.Number(float64(offset / 60)), nil
}

// maxClockMillis is the furthest moment a JavaScript Date holds.
const maxClockMillis = 8.64e15

func hostLog(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	level, err := stringArg(realm, args, 0, "level")
	if err != nil {
		return moejs.Undefined(), err
	}
	if level != "debug" && level != "info" && level != "warn" {
		return moejs.Undefined(), realm.TypeError("astrlink.log level must be debug, info or warn")
	}
	message, err := stringArg(realm, args, 1, "message")
	if err != nil {
		return moejs.Undefined(), err
	}
	if len(c.info.Logs) == maxLogEntries {
		c.info.DroppedLogs++
		return moejs.Undefined(), nil
	}
	message = cleanText(message, maxLogBytes, "")
	for len(message) > maxLogBytes {
		_, size := utf8.DecodeLastRuneInString(message)
		message = message[:len(message)-size]
	}
	c.info.Logs = append(c.info.Logs, LogEntry{Level: level, Message: message, Time: c.now()})
	return moejs.Undefined(), nil
}

func hostStoreGet(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	store, key, err := storeKey(c, realm, args)
	if err != nil {
		return moejs.Undefined(), err
	}
	return c.io(func(ctx context.Context) settle {
		value, found, err := store.Get(ctx, c.plugin.Key, key)
		return func(runtime *moejs.Runtime) (moejs.Value, error) {
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("astrlink.store.get: %w", err)
			}
			if !found {
				return moejs.Null(), nil
			}
			return runtime.Realm().JSONParseGoString(string(value))
		}
	})
}

func hostStoreSet(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	store, key, err := storeKey(c, realm, args)
	if err != nil {
		return moejs.Undefined(), err
	}
	argument := moejs.Arg(args, 1)
	if argument.IsUndefined() {
		return moejs.Undefined(), realm.TypeError("astrlink.store.set needs a value; astrlink.store.delete removes a key")
	}
	data, err := plainArg(realm, argument, MaxStoreValueBytes, "value")
	if err != nil {
		return moejs.Undefined(), err
	}
	value, err := json.Marshal(data)
	if err != nil || len(value) > MaxStoreValueBytes {
		return moejs.Undefined(), realm.RangeError("astrlink.store.set value must be JSON of at most %d KiB", MaxStoreValueBytes>>10)
	}
	return c.io(func(ctx context.Context) settle {
		err := store.Set(ctx, c.plugin.Key, key, value)
		return func(*moejs.Runtime) (moejs.Value, error) {
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("astrlink.store.set: %w", err)
			}
			return moejs.Undefined(), nil
		}
	})
}

func hostStoreDelete(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	store, key, err := storeKey(c, realm, args)
	if err != nil {
		return moejs.Undefined(), err
	}
	return c.io(func(ctx context.Context) settle {
		err := store.Delete(ctx, c.plugin.Key, key)
		return func(*moejs.Runtime) (moejs.Value, error) {
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("astrlink.store.delete: %w", err)
			}
			return moejs.Undefined(), nil
		}
	})
}

func hostStoreKeys(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	if !c.granted(CapabilityStorage) {
		return moejs.Undefined(), realm.TypeError("astrlink.store needs the %s capability", CapabilityStorage)
	}
	if c.plugin.services.Store == nil {
		return moejs.Undefined(), realm.TypeError("astrlink.store is unavailable")
	}
	prefix := ""
	if !moejs.Arg(args, 0).IsUndefined() {
		var err error
		if prefix, err = stringArg(realm, args, 0, "prefix"); err != nil {
			return moejs.Undefined(), err
		}
	}
	return c.io(func(ctx context.Context) settle {
		keys, err := c.plugin.services.Store.Keys(ctx, c.plugin.Key, prefix)
		return func(runtime *moejs.Runtime) (moejs.Value, error) {
			if err != nil {
				return moejs.Undefined(), fmt.Errorf("astrlink.store.keys: %w", err)
			}
			if keys == nil {
				keys = []string{}
			}
			return runtime.Realm().FromGo(keys)
		}
	})
}

func storeKey(c *call, realm *moejs.Realm, args []moejs.Value) (Store, string, error) {
	if !c.granted(CapabilityStorage) {
		return nil, "", realm.TypeError("astrlink.store needs the %s capability", CapabilityStorage)
	}
	if c.plugin.services.Store == nil {
		return nil, "", realm.TypeError("astrlink.store is unavailable")
	}
	key, err := stringArg(realm, args, 0, "key")
	if err != nil {
		return nil, "", err
	}
	if key == "" || utf8.RuneCountInString(key) > maxStoreKeyLength {
		return nil, "", realm.RangeError("astrlink.store keys are 1 to %d characters", maxStoreKeyLength)
	}
	return c.plugin.services.Store, key, nil
}

func hostFetch(c *call, realm *moejs.Realm, args []moejs.Value) (moejs.Value, error) {
	if !c.granted(CapabilityNetwork) {
		return moejs.Undefined(), realm.TypeError("astrlink.http.fetch needs the %s capability", CapabilityNetwork)
	}
	if c.plugin.services.Fetcher == nil {
		return moejs.Undefined(), realm.TypeError("astrlink.http.fetch is unavailable")
	}
	argument := moejs.Arg(args, 0)
	batch, err := realm.IsArray(argument)
	if err != nil {
		return moejs.Undefined(), err
	}
	data, err := plainArg(realm, argument, maxParallelFetches*(MaxFetchBodyBytes+64<<10), "request")
	if err != nil {
		return moejs.Undefined(), err
	}
	items := []any{data}
	if batch {
		items = data.([]any)
		if len(items) > maxParallelFetches {
			return moejs.Undefined(), realm.RangeError("astrlink.http.fetch takes at most %d requests at once", maxParallelFetches)
		}
	}
	requests := make([]HTTPRequest, len(items))
	for index, item := range items {
		if requests[index], err = decodeRequest(c, item); err != nil {
			return moejs.Undefined(), realm.TypeError("astrlink.http.fetch: %v", err)
		}
	}
	render := func(realm *moejs.Realm, responses []HTTPResponse) (moejs.Value, error) {
		values := make([]any, len(responses))
		for index, response := range responses {
			values[index] = responseValue(response)
		}
		if batch {
			return realm.FromGo(values)
		}
		return realm.FromGo(values[0])
	}
	if c.async {
		return c.fetchAsync(requests, render), nil
	}
	var responses []HTTPResponse
	if err := c.wait(func() { responses = c.fetch(requests) }); err != nil {
		return moejs.Undefined(), err
	}
	return render(realm, responses)
}

// decodeRequest reads one request strictly and checks its shape. What the
// user must allow, such as the address, is left to the Fetcher.
func decodeRequest(c *call, item any) (HTTPRequest, error) {
	text, err := json.Marshal(item)
	if err != nil {
		return HTTPRequest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	var request HTTPRequest
	if err := decoder.Decode(&request); err != nil {
		return HTTPRequest{}, errors.New(strings.TrimPrefix(err.Error(), "json: "))
	}
	switch request.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	case "HEAD", "OPTIONS":
		if !c.async {
			return HTTPRequest{}, fmt.Errorf("method %q is not one of GET, POST, PUT, PATCH, DELETE", request.Method)
		}
	default:
		return HTTPRequest{}, fmt.Errorf("method %q is not one of GET, HEAD, OPTIONS, POST, PUT, PATCH, DELETE", request.Method)
	}
	target, err := url.Parse(request.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil {
		return HTTPRequest{}, fmt.Errorf("url %q must be an absolute http or https URL without user information", request.URL)
	}
	for name, value := range request.Headers {
		if !headerNamePattern.MatchString(name) || strings.ContainsAny(value, "\r\n\x00") {
			return HTTPRequest{}, fmt.Errorf("header %q is not a valid header", name)
		}
	}
	bodies := 0
	for _, set := range []bool{len(request.JSON) > 0, request.Body != nil, request.BodyBase64 != nil} {
		if set {
			bodies++
		}
	}
	if bodies > 1 {
		return HTTPRequest{}, errors.New("a request has one of json, body and bodyBase64")
	}
	if (request.Method == "GET" || request.Method == "HEAD") && bodies > 0 {
		return HTTPRequest{}, fmt.Errorf("a %s request has no body", request.Method)
	}
	if request.BodyBase64 != nil {
		decoded, err := base64.StdEncoding.DecodeString(*request.BodyBase64)
		if err != nil {
			return HTTPRequest{}, errors.New("bodyBase64 is not base64")
		}
		request.BodyBytes = decoded
	}
	if len(request.JSON) > MaxFetchBodyBytes || (request.Body != nil && len(*request.Body) > MaxFetchBodyBytes) || len(request.BodyBytes) > MaxFetchBodyBytes {
		return HTTPRequest{}, fmt.Errorf("the body is larger than %d MiB", MaxFetchBodyBytes>>20)
	}
	if request.Credential != "" {
		if _, declared := c.plugin.Meta.Credential(request.Credential); !declared {
			return HTTPRequest{}, fmt.Errorf("credential %q is not declared in meta.credentials", request.Credential)
		}
	}
	if request.TimeoutMS < 0 || time.Duration(request.TimeoutMS)*time.Millisecond > MaxFetchTimeout {
		return HTTPRequest{}, fmt.Errorf("timeoutMs must be between 1 and %d", MaxFetchTimeout.Milliseconds())
	}
	return request, nil
}

// fetchAsync sends the requests of an apiVersion 2 call and returns the
// promise of what render makes of their responses. The call's request count
// and network deadline bound them; at most maxParallelFetches of a call run
// at once, and a request past a budget answers FetchBudget.
func (c *call) fetchAsync(requests []HTTPRequest, render func(*moejs.Realm, []HTTPResponse) (moejs.Value, error)) moejs.Value {
	responses := make([]HTTPResponse, len(requests))
	var sent []int
	c.network.Lock()
	for index, request := range requests {
		requests[index].Headers = ownHeaders(request.Headers)
		switch {
		case c.fetched >= maxAsyncFetches || !time.Now().Before(c.networkDeadline):
			responses[index] = HTTPResponse{Error: FetchBudget}
		default:
			c.fetched++
			sent = append(sent, index)
		}
	}
	c.network.Unlock()
	return c.later(func(ctx context.Context) settle {
		started := time.Now()
		if len(sent) > 0 {
			ctx, cancel := context.WithDeadlineCause(ctx, c.networkDeadline, errNetworkBudget)
			var group sync.WaitGroup
			for _, index := range sent {
				group.Go(func() {
					select {
					case c.fetchSlots <- struct{}{}:
					case <-ctx.Done():
						responses[index] = HTTPResponse{Error: FetchBudget}
						return
					}
					defer func() { <-c.fetchSlots }()
					responses[index] = c.send(ctx, requests[index])
				})
			}
			group.Wait()
			cancel()
		}
		elapsed := time.Since(started)
		return func(runtime *moejs.Runtime) (moejs.Value, error) {
			c.count(responses, elapsed)
			return render(runtime.Realm(), responses)
		}
	})
}

// send sends one request within ctx and its own timeout.
func (c *call) send(ctx context.Context, request HTTPRequest) HTTPResponse {
	timeout := defaultFetchTimeout
	if request.TimeoutMS > 0 {
		timeout = time.Duration(request.TimeoutMS) * time.Millisecond
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response := c.plugin.services.Fetcher.Fetch(requestCtx, c.plugin, request)
	if response.Error == FetchTimeout && errors.Is(context.Cause(ctx), errNetworkBudget) {
		response.Error = FetchBudget
	}
	return response
}

// count adds a batch of requests to what the call did. It may run on any
// goroutine.
func (c *call) count(responses []HTTPResponse, elapsed time.Duration) {
	c.network.Lock()
	defer c.network.Unlock()
	c.stats.Wait += elapsed
	c.stats.Requests += len(responses)
	for _, response := range responses {
		switch response.Error {
		case "":
		case FetchDenied:
			c.stats.Denied++
		default:
			c.stats.Errors++
		}
	}
}

// networkStats returns what the call's requests counted so far.
func (c *call) networkStats() NetworkStats {
	c.network.Lock()
	defer c.network.Unlock()
	return c.stats
}

// fetch runs requests in parallel within what is left of the call's
// budgets; a request past them answers FetchBudget.
func (c *call) fetch(requests []HTTPRequest) []HTTPResponse {
	responses := make([]HTTPResponse, len(requests))
	var sent []int
	c.network.Lock()
	for index, request := range requests {
		requests[index].Headers = ownHeaders(request.Headers)
		switch {
		case c.fetched == maxFetchesPerCall || c.networkLeft <= 0:
			responses[index] = HTTPResponse{Error: FetchBudget}
		default:
			c.fetched++
			sent = append(sent, index)
		}
	}
	c.network.Unlock()
	started := time.Now()
	if len(sent) > 0 {
		ctx, cancel := context.WithTimeoutCause(c.ctx, c.networkLeft, errNetworkBudget)
		var group sync.WaitGroup
		for _, index := range sent {
			group.Go(func() { responses[index] = c.send(ctx, requests[index]) })
		}
		group.Wait()
		cancel()
	}
	elapsed := time.Since(started)
	c.networkLeft -= elapsed
	c.count(responses, elapsed)
	return responses
}

var errNetworkBudget = errors.New("plugin network budget exhausted")

// ownHeaders drops the headers of a plugin's request that the host keeps
// for itself: the transport's, the proxy's, and the gateway's own, which
// upstream must not see. A plugin sets the rest, User-Agent and
// Authorization included; a credential's header goes over the plugin's.
func ownHeaders(headers map[string]string) map[string]string {
	for name := range headers {
		if transportHeader(name) {
			delete(headers, name)
		}
	}
	return headers
}

func responseValue(response HTTPResponse) map[string]any {
	header := response.Header
	if header == nil {
		header = map[string]string{}
	}
	value := map[string]any{
		"status":  response.Status,
		"headers": header,
		"took_ms": response.Took.Milliseconds(),
	}
	if response.Error != "" {
		value["error"] = response.Error
	}
	if len(response.Body) > 0 {
		if json.Valid(response.Body) {
			value["json"] = json.RawMessage(response.Body)
		} else {
			value["text"] = strings.ToValidUTF8(string(response.Body), "�")
		}
	}
	return value
}

// plainArg converts a host function argument to Go data. It refuses what is
// not plain data, which converting could run JavaScript for, and what is
// larger than maxBytes as JSON, before converting anything.
func plainArg(realm *moejs.Realm, value moejs.Value, maxBytes int, name string) (any, error) {
	measured, err := realm.MeasureToGo(value, moejs.DecodeOptions{MaxBytes: maxBytes, MaxNodes: maxArgNodes})
	if errors.Is(err, moejs.ErrTooLarge) {
		return nil, realm.RangeError("the %s is larger than %d KiB or %d values as JSON", name, maxBytes>>10, maxArgNodes)
	}
	if err != nil {
		return nil, err
	}
	if !measured {
		return nil, realm.TypeError("the %s must be plain data: objects, arrays, strings, numbers, booleans and null", name)
	}
	return realm.ToGoStrict(value)
}

func stringArg(realm *moejs.Realm, args []moejs.Value, index int, name string) (string, error) {
	value := moejs.Arg(args, index)
	if !value.IsString() {
		return "", realm.TypeError("the %s must be a string", name)
	}
	text, err := realm.ToString(value)
	if err != nil {
		return "", err
	}
	return text.GoString(), nil
}
