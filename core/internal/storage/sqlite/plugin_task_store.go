package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const pluginTaskColumns = `id, plugin_key, plugin_version, service_id, owner, surface, operation, action, model, upstream_model, upstream_task_id,
    status, progress, fail_reason, retain, data_json, state_json, usage_json, request_json, result_url, poll_failures, request_id,
    created_at, updated_at, finished_at, next_poll_at`

// pluginTaskTime is how task times are kept: sortable, so that the due
// index compares them as text.
const pluginTaskTime = "2006-01-02T15:04:05.000000000Z"

func formatTaskTime(value time.Time) string { return value.UTC().Format(pluginTaskTime) }

func nullableTaskTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return formatTaskTime(value)
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func checkPluginTask(task storage.PluginTask) error {
	switch {
	case len(task.ID) < 8 || len(task.ID) > 64:
		return fmt.Errorf("%w: plugin task id", storage.ErrInvalidArgument)
	case task.PluginKey == "" || len(task.PluginKey) > 32:
		return fmt.Errorf("%w: plugin task plugin", storage.ErrInvalidArgument)
	case len(task.Data) > storage.MaxPluginTaskDataBytes:
		return fmt.Errorf("%w: plugin task data is larger than %d MiB", storage.ErrInvalidArgument, storage.MaxPluginTaskDataBytes>>20)
	case len(task.State) > storage.MaxPluginTaskStateBytes:
		return fmt.Errorf("%w: plugin task state is larger than %d MiB", storage.ErrInvalidArgument, storage.MaxPluginTaskStateBytes>>20)
	case len(task.Usage) > storage.MaxPluginTaskUsageBytes:
		return fmt.Errorf("%w: plugin task usage", storage.ErrInvalidArgument)
	case len(task.Request) > storage.MaxPluginTaskRequestBytes:
		return fmt.Errorf("%w: plugin task request", storage.ErrInvalidArgument)
	case len(task.ResultURL) > storage.MaxPluginTaskResultURL:
		return fmt.Errorf("%w: plugin task result", storage.ErrInvalidArgument)
	}
	for _, value := range []json.RawMessage{task.Data, task.State, task.Usage, task.Request} {
		if len(value) > 0 && !json.Valid(value) {
			return fmt.Errorf("%w: plugin task JSON", storage.ErrInvalidArgument)
		}
	}
	return nil
}

func (store *Store) CreatePluginTask(ctx context.Context, task storage.PluginTask) error {
	if err := checkPluginTask(task); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO plugin_tasks (`+pluginTaskColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.PluginKey, task.PluginVersion, string(task.ServiceID), task.Owner, task.Surface, task.Operation, task.Action, task.Model,
		task.UpstreamModel, task.UpstreamTaskID, task.Status, task.Progress, task.FailReason, task.Retain,
		nullableJSON(task.Data), nullableJSON(task.State), nullableJSON(task.Usage), nullableJSON(task.Request), task.ResultURL,
		task.PollFailures, task.RequestID, formatTaskTime(task.CreatedAt), formatTaskTime(task.UpdatedAt),
		nullableTaskTime(task.FinishedAt), nullableTaskTime(task.NextPollAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("%w: plugin task %s", storage.ErrConflict, task.ID)
		}
		return fmt.Errorf("create plugin task: %w", err)
	}
	return nil
}

func (store *Store) GetPluginTask(ctx context.Context, id string) (storage.PluginTask, error) {
	row := store.db.QueryRowContext(ctx, `SELECT `+pluginTaskColumns+` FROM plugin_tasks WHERE id = ?`, id)
	task, err := scanPluginTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.PluginTask{}, fmt.Errorf("%w: plugin task %s", storage.ErrNotFound, id)
	}
	return task, err
}

func (store *Store) UpdatePluginTask(ctx context.Context, task storage.PluginTask) error {
	if err := checkPluginTask(task); err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE plugin_tasks SET plugin_version = ?, operation = ?, action = ?, model = ?, upstream_model = ?,
    upstream_task_id = ?, status = ?, progress = ?, fail_reason = ?, retain = ?, data_json = ?, state_json = ?, usage_json = ?, request_json = ?,
    result_url = ?, poll_failures = ?, request_id = ?, updated_at = ?, finished_at = ?, next_poll_at = ?
WHERE id = ?`,
		task.PluginVersion, task.Operation, task.Action, task.Model, task.UpstreamModel, task.UpstreamTaskID, task.Status, task.Progress,
		task.FailReason, task.Retain, nullableJSON(task.Data), nullableJSON(task.State), nullableJSON(task.Usage), nullableJSON(task.Request),
		task.ResultURL, task.PollFailures, task.RequestID, formatTaskTime(task.UpdatedAt), nullableTaskTime(task.FinishedAt),
		nullableTaskTime(task.NextPollAt), task.ID)
	if err != nil {
		return fmt.Errorf("update plugin task: %w", err)
	}
	if changed, err := result.RowsAffected(); err == nil && changed == 0 {
		return fmt.Errorf("%w: plugin task %s", storage.ErrNotFound, task.ID)
	}
	return nil
}

func (store *Store) DuePluginTasks(ctx context.Context, now time.Time, limit int) ([]storage.PluginTask, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+pluginTaskColumns+` FROM plugin_tasks
WHERE next_poll_at IS NOT NULL AND next_poll_at <= ? ORDER BY next_poll_at LIMIT ?`, formatTaskTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("read due plugin tasks: %w", err)
	}
	return scanPluginTasks(rows)
}

func (store *Store) ListPluginTasks(ctx context.Context, plugin, cursor string, limit int) ([]storage.PluginTask, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `SELECT ` + pluginTaskColumns + ` FROM plugin_tasks WHERE plugin_key = ?`
	args := []any{plugin}
	if cursor != "" {
		created, id, ok := strings.Cut(cursor, "|")
		if !ok {
			return nil, "", fmt.Errorf("%w: plugin task cursor", storage.ErrInvalidArgument)
		}
		query += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, created, created, id)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("read plugin tasks: %w", err)
	}
	tasks, err := scanPluginTasks(rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(tasks) > limit {
		tasks = tasks[:limit]
		last := tasks[len(tasks)-1]
		next = formatTaskTime(last.CreatedAt) + "|" + last.ID
	}
	return tasks, next, nil
}

func (store *Store) CountPluginTasks(ctx context.Context, plugin string) (int, int, error) {
	var total, running int
	err := store.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status IN ('SUCCESS', 'FAILURE') THEN 0 ELSE 1 END), 0)
FROM plugin_tasks WHERE plugin_key = ?`, plugin).Scan(&total, &running)
	if err != nil {
		return 0, 0, fmt.Errorf("count plugin tasks: %w", err)
	}
	return total, running, nil
}

func (store *Store) DeletePluginTasks(ctx context.Context, plugin string) (int, error) {
	result, err := store.db.ExecContext(ctx, `DELETE FROM plugin_tasks WHERE plugin_key = ? AND status IN ('SUCCESS', 'FAILURE')`, plugin)
	if err != nil {
		return 0, fmt.Errorf("delete plugin tasks: %w", err)
	}
	deleted, _ := result.RowsAffected()
	return int(deleted), nil
}

func scanPluginTasks(rows *sql.Rows) ([]storage.PluginTask, error) {
	defer rows.Close()
	var tasks []storage.PluginTask
	for rows.Next() {
		task, err := scanPluginTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read plugin tasks: %w", err)
	}
	return tasks, nil
}

func scanPluginTask(row scanner) (storage.PluginTask, error) {
	var task storage.PluginTask
	var service, created, updated string
	var data, state, usage, request, finished, next sql.NullString
	if err := row.Scan(&task.ID, &task.PluginKey, &task.PluginVersion, &service, &task.Owner, &task.Surface, &task.Operation, &task.Action,
		&task.Model, &task.UpstreamModel, &task.UpstreamTaskID, &task.Status, &task.Progress, &task.FailReason, &task.Retain,
		&data, &state, &usage, &request, &task.ResultURL, &task.PollFailures, &task.RequestID, &created, &updated, &finished, &next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return task, err
		}
		return task, fmt.Errorf("read plugin task: %w", err)
	}
	task.ServiceID = contract.ServiceID(service)
	for _, field := range []struct {
		from sql.NullString
		to   *json.RawMessage
	}{{data, &task.Data}, {state, &task.State}, {usage, &task.Usage}, {request, &task.Request}} {
		if field.from.Valid {
			*field.to = json.RawMessage(field.from.String)
		}
	}
	invalid := fmt.Errorf("%w: plugin task %s", storage.ErrInvalidRecord, task.ID)
	var err error
	if task.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return task, invalid
	}
	if task.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return task, invalid
	}
	if finished.Valid {
		if task.FinishedAt, err = time.Parse(time.RFC3339Nano, finished.String); err != nil {
			return task, invalid
		}
	}
	if next.Valid {
		if task.NextPollAt, err = time.Parse(time.RFC3339Nano, next.String); err != nil {
			return task, invalid
		}
	}
	return task, nil
}
