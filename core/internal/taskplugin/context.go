package taskplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// upstreamKind is ctx.upstream: the vendor, or another new-api gateway
// with the same plugin installed.
type upstreamKind struct {
	Kind string `json:"kind"`
}

// credentials are what the hooks of one provider authenticate with.
type credentials struct {
	baseURL    string
	apiKey     string
	authHeader string
	auth       map[string]any
	authError  string
	upstream   upstreamKind
	settings   json.RawMessage
}

// channelBaseURLs are the addresses new-api's channel types default to, for
// a plugin that names channel types but no address of its own.
var channelBaseURLs = map[int]string{
	1:  "https://api.openai.com",
	17: "https://dashscope.aliyuncs.com",
	24: "https://generativelanguage.googleapis.com",
	35: "https://api.minimaxi.com",
	45: "https://ark.cn-beijing.volces.com",
	48: "https://api.x.ai",
	50: "https://api-beijing.klingai.com",
	51: "https://visual.volcengineapi.com",
	52: "https://api.vidu.cn",
	54: "https://ark.cn-beijing.volces.com",
	55: "https://api.openai.com",
}

// DefaultBaseURL is the address a new provider of a task plugin starts
// with: its own, else that of the first of its channel types that has one.
func DefaultBaseURL(meta *plugin.TaskMeta) string {
	if meta.BaseURL != "" {
		return meta.BaseURL
	}
	for _, channel := range meta.ChannelTypes {
		if address, ok := channelBaseURLs[channel]; ok {
			return address
		}
	}
	return ""
}

// credentialsFor reads a provider's address and key, and exchanges a
// service account for a token when the plugin authenticates that way.
func (engine *Engine) credentialsFor(ctx context.Context, service contract.Service, meta *plugin.TaskMeta) (credentials, error) {
	var found credentials
	found.upstream = upstreamKind{Kind: "vendor"}
	if service.Plugin != nil {
		if service.Plugin.Upstream == contract.PluginUpstreamNewAPI {
			found.upstream.Kind = contract.PluginUpstreamNewAPI
		}
		found.settings = service.Plugin.Settings
	}
	if service.HTTP == nil {
		return found, failure(http.StatusServiceUnavailable, "provider_unavailable", "the provider has no address")
	}
	found.baseURL = strings.TrimRight(service.HTTP.BaseURL, "/")
	if found.baseURL == "" {
		found.baseURL = strings.TrimRight(DefaultBaseURL(meta), "/")
	}
	var secret []byte
	if service.HTTP.CredentialRef != "" && engine.secrets != nil {
		ref, err := secretstore.ParseRef(service.HTTP.CredentialRef)
		if err != nil {
			return found, failure(http.StatusServiceUnavailable, "provider_unavailable", "the provider's key cannot be read")
		}
		secret, err = engine.secrets.Get(ctx, ref)
		if err != nil {
			return found, failure(http.StatusServiceUnavailable, "provider_unavailable", "the provider's key cannot be read")
		}
		defer clear(secret)
	}
	key := strings.TrimSpace(string(secret))
	auth := meta.Auth.Kind()
	switch {
	case found.upstream.Kind == contract.PluginUpstreamNewAPI:
		// The key of another gateway is its token, whatever the vendor
		// takes: that gateway runs the vendor's authentication itself.
		found.apiKey, found.authHeader = key, "Bearer "+key
	case auth == plugin.TaskAuthNone:
	case auth == plugin.TaskAuthOAuth2JWT:
		token, account, err := engine.tokens.token(ctx, engine, service, key)
		if account != nil {
			found.auth = map[string]any{"projectId": account.ProjectID, "clientEmail": account.ClientEmail}
		}
		if err != nil {
			found.authError = err.Error()
		} else {
			found.authHeader = "Bearer " + token
		}
	default:
		found.apiKey, found.authHeader = key, "Bearer "+key
	}
	return found, nil
}

// fileReference is a file a client uploaded with a multipart request, as
// the hooks see it: never its bytes.
type fileReference struct {
	Ref      string `json:"ref"`
	Field    string `json:"field"`
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// originTask is a task a submission continues, as ctx.originTasks holds
// it.
type originTask struct {
	TaskID         string          `json:"taskId"`
	UpstreamTaskID string          `json:"upstreamTaskId"`
	Action         string          `json:"action"`
	Status         string          `json:"status"`
	Data           json.RawMessage `json:"data"`
}

// driverContext is the DriverContext of new-api's API: what the driver
// hooks of a submission read.
type driverContext struct {
	RequestBody    json.RawMessage   `json:"requestBody"`
	RequestHeaders map[string]string `json:"requestHeaders"`
	Action         string            `json:"action"`
	Model          string            `json:"model"`
	UpstreamModel  string            `json:"upstreamModel"`
	BaseURL        string            `json:"baseUrl"`
	APIKey         string            `json:"apiKey,omitempty"`
	AuthHeader     string            `json:"authHeader"`
	Auth           map[string]any    `json:"auth,omitempty"`
	AuthError      string            `json:"authError,omitempty"`
	Upstream       upstreamKind      `json:"upstream"`
	Files          []fileReference   `json:"files"`
	PublicTaskID   string            `json:"publicTaskId"`
	OriginTasks    []originTask      `json:"originTasks,omitempty"`
	OriginTaskID   string            `json:"originTaskId,omitempty"`
	UserSetting    json.RawMessage   `json:"userSetting"`
	UsagePurpose   string            `json:"usagePurpose,omitempty"`
}

// queryContext is the TaskQueryContext of new-api's API: what the hooks
// that poll a task read, rebuilt from the task.
type queryContext struct {
	TaskID        string          `json:"taskId"`
	PublicTaskID  string          `json:"publicTaskId"`
	Action        string          `json:"action"`
	Model         string          `json:"model"`
	UpstreamModel string          `json:"upstreamModel"`
	BaseURL       string          `json:"baseUrl"`
	APIKey        string          `json:"apiKey,omitempty"`
	AuthHeader    string          `json:"authHeader"`
	Auth          map[string]any  `json:"auth,omitempty"`
	AuthError     string          `json:"authError,omitempty"`
	Upstream      upstreamKind    `json:"upstream"`
	Data          json.RawMessage `json:"data"`
	State         json.RawMessage `json:"state"`
	UserSetting   json.RawMessage `json:"userSetting"`
}

func (found credentials) query(task storage.PluginTask) queryContext {
	upstreamModel := task.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = task.Model
	}
	return queryContext{
		TaskID: upstreamTaskID(task), PublicTaskID: task.ID, Action: task.Action, Model: task.Model, UpstreamModel: upstreamModel,
		BaseURL: found.baseURL, APIKey: found.apiKey, AuthHeader: found.authHeader, Auth: found.auth, AuthError: found.authError,
		Upstream: found.upstream, Data: jsonOrNull(task.Data), State: jsonOrNull(task.State), UserSetting: settingsOrEmpty(found.settings),
	}
}

func upstreamTaskID(task storage.PluginTask) string {
	if task.UpstreamTaskID != "" {
		return task.UpstreamTaskID
	}
	return task.ID
}

func jsonOrNull(value json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage("null")
	}
	return value
}

func settingsOrEmpty(value json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage("{}")
	}
	return value
}

// taskView is the TaskView of new-api's API: a task as presenters and
// renderers see it.
type taskView struct {
	TaskID     string          `json:"task_id"`
	Status     string          `json:"status"`
	Progress   string          `json:"progress,omitempty"`
	FailReason string          `json:"fail_reason,omitempty"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
	FinishedAt int64           `json:"finished_at,omitempty"`
	Action     string          `json:"action,omitempty"`
	Platform   string          `json:"platform"`
	Data       json.RawMessage `json:"data"`
	Properties map[string]any  `json:"properties"`
}

// view is task as presenters see it: its vendor data with the vendor's
// task id replaced by the public one.
func view(task storage.PluginTask) taskView {
	result := taskView{
		TaskID: task.ID, Status: task.Status, Progress: task.Progress, FailReason: task.FailReason,
		CreatedAt: task.CreatedAt.Unix(), UpdatedAt: task.UpdatedAt.Unix(), Action: task.Action, Platform: task.PluginKey,
		Data:       publicData(task.Data, task.UpstreamTaskID, task.ID),
		Properties: map[string]any{"origin_model_name": task.Model, "upstream_model_name": task.UpstreamModel},
	}
	if !task.FinishedAt.IsZero() {
		result.FinishedAt = task.FinishedAt.Unix()
	}
	return result
}

// publicData replaces the vendor's task id with the public one in the
// fields that name a task: id, task_id and taskId, at any depth.
func publicData(data json.RawMessage, private, public string) json.RawMessage {
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage("null")
	}
	if private == "" || private == public || !bytes.Contains(data, []byte(private)) {
		return data
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return data
	}
	var replace func(value any) any
	replace = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			for name, member := range typed {
				if text, ok := member.(string); ok && text == private && (name == "id" || name == "task_id" || name == "taskId") {
					typed[name] = public
					continue
				}
				typed[name] = replace(member)
			}
		case []any:
			for index, item := range typed {
				typed[index] = replace(item)
			}
		}
		return value
	}
	encoded, err := marshalOrdered(replace(value))
	if err != nil {
		return data
	}
	return encoded
}

// marshalOrdered encodes value without escaping HTML, as the vendor's
// data was.
func marshalOrdered(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buffer.Bytes(), "\n")), nil
}

// allowedHost reports whether a request with credentials may go to target:
// the provider's own host or one the plugin declares.
func allowedHost(target *url.URL, baseURL string, meta *plugin.TaskMeta) bool {
	if target == nil || target.Host == "" || target.User != nil {
		return false
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	if base, err := url.Parse(baseURL); err == nil && base.Host != "" {
		basePort := base.Port()
		if basePort == "" {
			basePort = map[string]string{"http": "80", "https": "443"}[strings.ToLower(base.Scheme)]
		}
		if strings.EqualFold(base.Hostname(), host) && basePort == port {
			return true
		}
	}
	for _, allowed := range meta.AllowedHosts {
		name, allowedPort := allowed, ""
		if strings.HasPrefix(allowed, "[") {
			if end := strings.Index(allowed, "]"); end > 0 {
				name, allowedPort = allowed[1:end], strings.TrimPrefix(allowed[end+1:], ":")
			}
		} else if index := strings.LastIndex(allowed, ":"); index > 0 {
			name, allowedPort = allowed[:index], allowed[index+1:]
		}
		if name == host && (allowedPort == "" || allowedPort == port) {
			return true
		}
	}
	return false
}

// pollDelay is how long the poller waits before asking about a task again,
// longer the longer the task has run.
func pollDelay(age time.Duration) time.Duration {
	switch {
	case age < 2*time.Minute:
		return 5 * time.Second
	case age < 10*time.Minute:
		return 15 * time.Second
	}
	return 30 * time.Second
}

// taskDeadline ends a task the vendor never finished.
const taskDeadline = 24 * time.Hour

// maxPollFailures fails a task the vendor answered that many polls in a row
// with an error or an answer the plugin did not recognize.
const maxPollFailures = 20

func describe(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
