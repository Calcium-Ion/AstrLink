package contract

import "encoding/json"

// ProtocolPluginNative records a request on a native route of a task
// plugin: the vendor's own wire format, which only that plugin reads. It
// is an extension ID with no descriptor, like ProtocolOpenAISearch.
const ProtocolPluginNative ProtocolID = "plugin.native"

// PluginProviderTask is the provider a task plugin offers: a provider of
// kind plugin whose plugin turns generation requests into the vendor's task
// API.
const PluginProviderTask = "task"

// PluginProviderRelay is the provider a new-api plugin's provider part
// offers: API requests relayed over its meta.relay routes.
const PluginProviderRelay = "relay"

// PluginUpstreamNewAPI marks a task plugin's provider that is another
// new-api gateway with the same plugin installed, rather than the vendor.
const PluginUpstreamNewAPI = "new_api"

// Kinds of plugin.
const (
	PluginKindExtension = "extension"
	PluginKindTask      = "task"
	// PluginKindProvider is a new-api plugin with a provider part and no
	// task part.
	PluginKindProvider = "provider"
)

// PluginTask is what a task plugin, written for new-api's task plugin API,
// declares: the models it generates media with and how clients reach them.
type PluginTask struct {
	// Icon is a LobeHub icon name, such as Sora.Color, or text and
	// text:<label> for a text avatar; the image beside the plugin, when it
	// has one, is the summary's Icon.
	Icon        string   `json:"icon,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	AuthorURL   string   `json:"author_url,omitempty"`
	Website     string   `json:"website,omitempty"`
	Models      []string `json:"models"`
	// FetchMode is per_task or batch: whether the gateway polls each task
	// or several at once.
	FetchMode string `json:"fetch_mode"`
	// Auth is api_key, oauth2_jwt or none: what the provider's key holds.
	Auth string `json:"auth"`
	// BaseURL is the address a new provider starts with, "" when the user
	// names one.
	BaseURL      string   `json:"base_url,omitempty"`
	AllowedHosts []string `json:"allowed_hosts,omitempty"`
	// NewAPI is set when the plugin can also drive another new-api
	// gateway that has it installed.
	NewAPI    bool                 `json:"new_api,omitempty"`
	Protocols []PluginTaskProtocol `json:"protocols"`
	Routes    []PluginTaskRoute    `json:"routes"`
	// SortPriority orders plugins for display, higher first.
	SortPriority int32 `json:"sort_priority,omitempty"`
	// Capabilities are the host capabilities the plugin requires.
	Capabilities []string `json:"capabilities,omitempty"`
	// Usage is the usage facts the plugin reports, as its manifest
	// declares them: usageSchema, usageExamples and usageProfiles.
	Usage *PluginTaskUsage `json:"usage,omitempty"`
}

// PluginTaskUsage is what a task plugin declares of the usage it reports.
type PluginTaskUsage struct {
	Schema   json.RawMessage `json:"schema,omitempty"`
	Examples json.RawMessage `json:"examples,omitempty"`
	Profiles json.RawMessage `json:"profiles,omitempty"`
}

// PluginTaskProtocol is a gateway protocol a task plugin serves:
// openai_image, openai_video or openai_responses, with the modes of the
// last and the models it narrows to, none for all.
type PluginTaskProtocol struct {
	Name     string   `json:"name"`
	Supports []string `json:"supports,omitempty"`
	Models   []string `json:"models,omitempty"`
}

// PluginTaskRoute is a vendor-native URL a task plugin serves on the
// inference port.
type PluginTaskRoute struct {
	Method string   `json:"method"`
	Path   string   `json:"path"`
	Type   string   `json:"type"`
	Models []string `json:"models,omitempty"`
}

// Statuses of a plugin task, as new-api's task plugins report them.
const (
	PluginTaskNotStart   = "NOT_START"
	PluginTaskSubmitted  = "SUBMITTED"
	PluginTaskQueued     = "QUEUED"
	PluginTaskInProgress = "IN_PROGRESS"
	PluginTaskSuccess    = "SUCCESS"
	PluginTaskFailure    = "FAILURE"
	PluginTaskUnknown    = "UNKNOWN"
)

// PluginTaskTerminal reports whether status ends a task.
func PluginTaskTerminal(status string) bool {
	return status == PluginTaskSuccess || status == PluginTaskFailure
}

// PluginTaskRecord is a task a task plugin runs, as the plugin page lists
// it: never its result or the vendor's data.
type PluginTaskRecord struct {
	ID        string    `json:"id"`
	PluginKey string    `json:"plugin_key"`
	ServiceID ServiceID `json:"service_id"`
	// Surface is the protocol or native route the task came in by.
	Surface    string `json:"surface"`
	Action     string `json:"action,omitempty"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	Progress   string `json:"progress,omitempty"`
	FailReason string `json:"fail_reason,omitempty"`
	// Usage are the facts the plugin reported, such as seconds or
	// image_count.
	Usage      json.RawMessage `json:"usage,omitempty"`
	Artifacts  []string        `json:"artifacts,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	FinishedAt string          `json:"finished_at,omitempty"`
	// RequestID is the request record of the submission.
	RequestID string `json:"request_id,omitempty"`
}

// PluginTaskPage is a page of a plugin's tasks, the newest first.
type PluginTaskPage struct {
	Items      []PluginTaskRecord `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
	// Running counts the tasks not yet finished.
	Running int `json:"running"`
}
