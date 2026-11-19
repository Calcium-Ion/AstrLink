package pluginhost

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// scheduleTick is how often the scheduler looks for schedules that are due,
// and scheduleUnit what a schedule's minute is; tests shorten both.
var (
	scheduleTick = 15 * time.Second
	scheduleUnit = time.Minute
)

// RunSchedules runs the onSchedule hooks of plugins granted schedule at the
// intervals of their meta.schedules until ctx ends. A schedule first runs
// one interval after the scheduler first sees it, and never twice at once.
func (host *Host) RunSchedules(ctx context.Context) {
	if host == nil || host.plugins == nil {
		return
	}
	scheduler := &scheduler{host: host, last: map[string]time.Time{}, running: map[string]bool{}}
	ticker := time.NewTicker(scheduleTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			scheduler.wait.Wait()
			return
		case now := <-ticker.C:
			scheduler.tick(ctx, now)
		}
	}
}

type scheduler struct {
	host    *Host
	mutex   sync.Mutex
	last    map[string]time.Time
	running map[string]bool
	wait    sync.WaitGroup
}

// tick starts the schedules due at now.
func (scheduler *scheduler) tick(ctx context.Context, now time.Time) {
	if scheduler.host.Settings().Paused {
		return
	}
	seen := map[string]bool{}
	for _, target := range scheduler.host.plugins.Current().Plugins() {
		if !target.Enabled() || !target.Can(plugin.CapabilitySchedule) || !target.Has(plugin.HookOnSchedule) {
			continue
		}
		for _, entry := range target.Meta.Schedules {
			id := target.Key + "/" + entry.Name + "/" + strconv.Itoa(entry.EveryMinutes)
			seen[id] = true
			every := time.Duration(entry.EveryMinutes) * scheduleUnit
			scheduler.mutex.Lock()
			last, known := scheduler.last[id]
			due := known && now.Sub(last) >= every && !scheduler.running[id]
			if !known || due {
				scheduler.last[id] = now
			}
			if due {
				scheduler.running[id] = true
			}
			scheduler.mutex.Unlock()
			if !due {
				continue
			}
			scheduler.wait.Add(1)
			go func() {
				defer scheduler.wait.Done()
				defer func() {
					scheduler.mutex.Lock()
					delete(scheduler.running, id)
					scheduler.mutex.Unlock()
				}()
				scheduler.host.runSchedule(ctx, target, entry)
			}()
		}
	}
	scheduler.mutex.Lock()
	for id := range scheduler.last {
		if !seen[id] && !scheduler.running[id] {
			delete(scheduler.last, id)
		}
	}
	scheduler.mutex.Unlock()
}

// runSchedule runs one schedule's onSchedule hook within its limit. Its
// lines and failures go to the plugin's journal.
func (host *Host) runSchedule(ctx context.Context, target *plugin.Plugin, entry plugin.Schedule) {
	limit := target.Limit(plugin.HookOnSchedule)
	ctx, cancel := context.WithTimeoutCause(ctx, limit, errHookLimit)
	defer cancel()
	options := plugin.CallOptions{}
	if target.Can(plugin.CapabilityNetwork) {
		options.NetworkBudget = limit
	}
	arg := map[string]any{"name": entry.Name, "everyMinutes": entry.EveryMinutes, "language": host.Settings().Language}
	if _, _, err := target.Call(ctx, plugin.HookOnSchedule, arg, options); err != nil {
		host.logf("plugin %s schedule %s: %v", target.Key, entry.Name, err)
	}
}

// PluginHTTPRequest is a request to /plugins/<key>/… on the inference
// listener, as onHttp sees it.
type PluginHTTPRequest struct {
	Method string `json:"method"`
	// Path is what follows /plugins/<key>, "/" at least.
	Path    string            `json:"path"`
	Query   map[string]string `json:"query"`
	Headers map[string]string `json:"headers"`
	// Body is the request body as text; JSON is it parsed, when it is
	// JSON.
	Body string          `json:"body,omitempty"`
	JSON json.RawMessage `json:"json,omitempty"`
}

// PluginHTTPResponse is what onHttp answers.
type PluginHTTPResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

type httpResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
	JSON    json.RawMessage   `json:"json"`
}

// MaxPluginHTTPBytes bounds the bodies of /plugins/<key>/… both ways.
const MaxPluginHTTPBytes = 1 << 20

// ServePluginHTTP answers a request to /plugins/<key>/… with the plugin's
// onHttp hook. found is false when no enabled plugin of key serves HTTP.
func (host *Host) ServePluginHTTP(ctx context.Context, key string, request PluginHTTPRequest) (response PluginHTTPResponse, found bool) {
	if host == nil || host.plugins == nil || host.Settings().Paused {
		return PluginHTTPResponse{}, false
	}
	target := host.plugins.Current().Plugin(key)
	if target == nil || !target.Enabled() || !target.Can(plugin.CapabilityHTTP) || !target.Has(plugin.HookOnHTTP) {
		return PluginHTTPResponse{}, false
	}
	limit := target.Limit(plugin.HookOnHTTP)
	ctx, cancel := context.WithTimeoutCause(ctx, limit, errHookLimit)
	defer cancel()
	options := plugin.CallOptions{}
	if target.Can(plugin.CapabilityNetwork) {
		options.NetworkBudget = limit
	}
	result, _, err := target.Call(ctx, plugin.HookOnHTTP, request, options)
	if err != nil {
		return PluginHTTPResponse{Status: 502, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}, Body: []byte("the plugin could not answer\n")}, true
	}
	var answered httpResult
	if err := decode(result, &answered); err != nil {
		return PluginHTTPResponse{Status: 502, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}, Body: []byte("the plugin's answer is not { status?, headers?, body? | json? }\n")}, true
	}
	response = PluginHTTPResponse{Status: answered.Status, Headers: map[string]string{}}
	if response.Status < 100 || response.Status > 599 {
		response.Status = 200
	}
	for name, value := range answered.Headers {
		if allowedResponseHeader(name, value) {
			response.Headers[name] = value
		}
	}
	switch {
	case len(answered.JSON) > 0 && string(answered.JSON) != "null":
		response.Body = answered.JSON
		if _, set := headerValue(response.Headers, "Content-Type"); !set {
			response.Headers["Content-Type"] = "application/json"
		}
	case answered.Body != nil:
		response.Body = []byte(*answered.Body)
		if _, set := headerValue(response.Headers, "Content-Type"); !set {
			response.Headers["Content-Type"] = "text/plain; charset=utf-8"
		}
	}
	if len(response.Body) > MaxPluginHTTPBytes {
		return PluginHTTPResponse{Status: 502, Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"}, Body: []byte("the plugin's answer is too large\n")}, true
	}
	return response, true
}

// allowedResponseHeader reports whether onHttp may set a response header:
// never a cookie, nor what the transport or the gateway owns.
func allowedResponseHeader(name, value string) bool {
	if !allowedHeader(name, value) {
		return false
	}
	switch lowerASCII(name) {
	case "set-cookie", "access-control-allow-origin", "access-control-allow-credentials", "content-security-policy":
		return false
	}
	return true
}

func headerValue(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if lowerASCII(key) == lowerASCII(name) {
			return value, true
		}
	}
	return "", false
}

func lowerASCII(text string) string {
	out := []byte(text)
	for index, char := range out {
		if char >= 'A' && char <= 'Z' {
			out[index] = char + 'a' - 'A'
		}
	}
	return string(out)
}
