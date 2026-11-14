package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// Bounds of what a plugin task keeps.
const (
	MaxPluginTaskDataBytes    = 4 << 20
	MaxPluginTaskStateBytes   = 1 << 20
	MaxPluginTaskUsageBytes   = 64 << 10
	MaxPluginTaskRequestBytes = 1 << 20
	MaxPluginTaskResultURL    = 64 << 10
)

// PluginTask is a generation a task plugin runs for a client: submitted to
// the vendor, then polled until it ends.
type PluginTask struct {
	// ID is the public id clients read the task by; UpstreamTaskID the
	// vendor's.
	ID            string
	PluginKey     string
	PluginVersion string
	ServiceID     contract.ServiceID
	// Owner is the access token that submitted the task, "" on a gateway
	// without tokens.
	Owner string
	// Surface is the protocol or native route the task came by, and
	// Operation what it asked of it.
	Surface        string
	Operation      string
	Action         string
	Model          string
	UpstreamModel  string
	UpstreamTaskID string
	Status         string
	Progress       string
	FailReason     string
	// Retain is false for a task whose route keeps no result: its Data is
	// nil and no surface finds it.
	Retain bool
	// Data is the vendor's latest snapshot; State the plugin's own state
	// between polls; Usage the usage facts it reported; Request what the
	// protocol's answers echo.
	Data    json.RawMessage
	State   json.RawMessage
	Usage   json.RawMessage
	Request json.RawMessage
	// ResultURL is the result the vendor named, or "stored:" and a media
	// type for one kept beside the task.
	ResultURL    string
	PollFailures int
	// RequestID is the request record of the submission.
	RequestID string
	CreatedAt time.Time
	UpdatedAt time.Time
	// FinishedAt is zero until the task ends; NextPollAt is zero once it
	// did.
	FinishedAt time.Time
	NextPollAt time.Time
}

// Terminal reports whether the task ended.
func (task PluginTask) Terminal() bool { return contract.PluginTaskTerminal(task.Status) }

// PluginTaskStore keeps the tasks of task plugins.
type PluginTaskStore interface {
	CreatePluginTask(context.Context, PluginTask) error
	// GetPluginTask returns ErrNotFound for an unknown id.
	GetPluginTask(ctx context.Context, id string) (PluginTask, error)
	// UpdatePluginTask writes everything but the id, plugin, service,
	// owner, surface and creation time; ErrNotFound for an unknown id.
	UpdatePluginTask(context.Context, PluginTask) error
	// DuePluginTasks returns up to limit unfinished tasks whose next poll
	// is due at now, the most overdue first.
	DuePluginTasks(ctx context.Context, now time.Time, limit int) ([]PluginTask, error)
	// ListPluginTasks returns a plugin's tasks, the newest first, after
	// cursor; the next cursor is "" on the last page.
	ListPluginTasks(ctx context.Context, plugin, cursor string, limit int) ([]PluginTask, string, error)
	// CountPluginTasks counts a plugin's tasks, and those not finished.
	CountPluginTasks(ctx context.Context, plugin string) (total, running int, err error)
	// DeletePluginTasks removes a plugin's finished tasks and returns how
	// many.
	DeletePluginTasks(ctx context.Context, plugin string) (int, error)
}
