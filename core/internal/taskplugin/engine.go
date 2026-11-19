// Package taskplugin runs the tasks of task plugins, plugins written for
// new-api's task plugin API v1: it submits a generation through a provider
// of kind plugin, keeps the task, polls it until it ends, and renders it for
// the protocol or native route it came by. The plugin only transforms data;
// the engine sends every request, through the provider's proxy, to the
// hosts the plugin may address.
package taskplugin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// Plugins is the plugin registry; *plugin.Manager satisfies it.
type Plugins interface {
	Current() *plugin.Generation
}

// Sender sends one upstream request as the gateway sends every attempt:
// through the provider's proxy, without the gateway's headers. The target's
// RequestHeaders carry the plugin's headers, credentials included.
type Sender func(*http.Request, transport.Target) (*http.Response, error)

// Services reads the providers tasks run on.
type Services interface {
	GetService(ctx context.Context, id contract.ServiceID) (storage.ServiceRecord, error)
}

// Options configure an Engine.
type Options struct {
	Plugins  Plugins
	Store    storage.PluginTaskStore
	Services Services
	// Secrets holds the providers' keys; ProxyCredentials their proxies'.
	Secrets          secretstore.SecretStore
	ProxyCredentials secretstore.SecretStore
	Send             Sender
	// Dir keeps results the vendor returned inline, such as a video in
	// base64: <data-dir>/plugin-tasks.
	Dir string
	// SigningKey signs the artifact links the Responses protocol hands
	// out, which a client opens without its access token.
	SigningKey []byte
	Now        func() time.Time
	Logf       func(format string, args ...any)
	// PollInterval is how long the poller sleeps when no task is due;
	// tests shorten it.
	PollInterval time.Duration
}

// Engine runs the tasks of task plugins.
type Engine struct {
	plugins          Plugins
	store            storage.PluginTaskStore
	services         Services
	secrets          secretstore.SecretStore
	proxyCredentials secretstore.SecretStore
	send             Sender
	dir              string
	signingKey       []byte
	now              func() time.Time
	logf             func(format string, args ...any)
	pollInterval     time.Duration
	tokens           *tokenCache
	// checkAddress refuses a credentialless download of a private
	// address; tests reach their own server.
	checkAddress func(context.Context, *url.URL) error
	// locks serialize what changes one task: the poller and a request
	// waiting on the task may both poll it.
	locks sync.Map
	wake  chan struct{}
	// routes indexes the native routes of the current generation.
	routeMutex sync.Mutex
	routes     *routeIndex
}

// New returns an engine for options.
func New(options Options) *Engine {
	engine := &Engine{
		plugins: options.Plugins, store: options.Store, services: options.Services, secrets: options.Secrets,
		proxyCredentials: options.ProxyCredentials, send: options.Send, dir: options.Dir, signingKey: options.SigningKey,
		now: options.Now, logf: options.Logf, pollInterval: options.PollInterval, tokens: newTokenCache(),
		checkAddress: publicAddress, wake: make(chan struct{}, 1),
	}
	if engine.now == nil {
		engine.now = time.Now
	}
	if engine.logf == nil {
		engine.logf = func(string, ...any) {}
	}
	if engine.pollInterval <= 0 {
		engine.pollInterval = 2 * time.Second
	}
	if len(engine.signingKey) == 0 {
		engine.signingKey = make([]byte, 32)
		_, _ = rand.Read(engine.signingKey)
	}
	return engine
}

// Errors the engine reports for a request, with the status and code its
// answer carries.
type Error struct {
	Status  int
	Code    string
	Message string
	// Retryable is set when another provider may serve the request.
	Retryable bool
	// Upstream is the vendor's answer to a submission it refused.
	Upstream *UpstreamAnswer
	err      error
	attempts []Attempt
}

func (err *Error) Error() string { return err.Message }

// Attempts are the tries of a submission that failed.
func (err *Error) Attempts() []Attempt { return err.attempts }

func (err *Error) Unwrap() error { return err.err }

// UpstreamAnswer is what the vendor answered a request with.
type UpstreamAnswer struct {
	Status int
	Header http.Header
	Body   []byte
}

func failure(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// hookFailure is a hook that threw or failed: what it threw is the message
// a client reads.
func hookFailure(hook string, err error) *Error {
	var call *plugin.CallError
	message := err.Error()
	if errors.As(err, &call) {
		message = call.Message
		if call.Thrown != "" {
			message = call.Thrown
		}
	}
	return &Error{Status: http.StatusBadRequest, Code: "plugin_error", Message: message, err: fmt.Errorf("%s: %w", hook, err)}
}

// plugin returns the task plugin key names in the current generation,
// while it is enabled.
func (engine *Engine) plugin(key string) (*plugin.Plugin, *plugin.TaskMeta, error) {
	if engine.plugins == nil {
		return nil, nil, failure(http.StatusServiceUnavailable, "plugin_unavailable", "task plugins are unavailable")
	}
	found := engine.plugins.Current().Plugin(key)
	if found == nil || found.Task() == nil {
		return nil, nil, failure(http.StatusServiceUnavailable, "plugin_unavailable", "the task plugin %s is not installed", key)
	}
	if !found.Enabled() {
		return nil, nil, failure(http.StatusServiceUnavailable, "plugin_unavailable", "the task plugin %s is turned off", key)
	}
	return found, found.Task(), nil
}

// TaskPlugins returns the enabled task plugins of the current generation.
func (engine *Engine) TaskPlugins() []*plugin.Plugin {
	if engine.plugins == nil {
		return nil
	}
	var plugins []*plugin.Plugin
	for _, candidate := range engine.plugins.Current().Plugins() {
		if candidate.Enabled() && candidate.Task() != nil {
			plugins = append(plugins, candidate)
		}
	}
	return plugins
}

// notify wakes the poller for a task submitted now.
func (engine *Engine) notify() {
	select {
	case engine.wake <- struct{}{}:
	default:
	}
}

// lock serializes the changes of one task.
func (engine *Engine) lock(id string) func() {
	value, _ := engine.locks.LoadOrStore(id, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// Prefixes of public task ids by surface: a video and a response read as
// OpenAI's do.
const (
	videoPrefix    = "video_"
	responsePrefix = "resp_"
	taskPrefix     = "task_"
)

// newTaskID returns a public task id for surface.
func newTaskID(surface string) string {
	var random [16]byte
	_, _ = rand.Read(random[:])
	prefix := taskPrefix
	switch surface {
	case plugin.TaskProtocolVideo:
		prefix = videoPrefix
	case plugin.TaskProtocolResponses:
		prefix = responsePrefix
	}
	return prefix + hex.EncodeToString(random[:])
}

// ValidTaskID reports whether id could be a public task id.
func ValidTaskID(id string) bool {
	for _, prefix := range []string{videoPrefix, responsePrefix, taskPrefix} {
		if rest, ok := strings.CutPrefix(id, prefix); ok && len(rest) == 32 {
			_, err := hex.DecodeString(rest)
			return err == nil
		}
	}
	return false
}

// resultPath is where a result kept beside its task lives.
func (engine *Engine) resultPath(id string) string {
	return filepath.Join(engine.dir, id)
}

// SigningKey derives the key artifact links are signed with from a secret
// the gateway keeps, so that links survive a restart.
func SigningKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("astrlink plugin task artifacts v1"))
	return mac.Sum(nil)
}
