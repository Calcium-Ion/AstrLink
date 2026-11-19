package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestPluginTasksRoundTrip(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "tasks.db"))
	defer store.Close()
	ctx := context.Background()
	created := time.Date(2026, 10, 25, 8, 0, 0, 123, time.UTC)
	task := storagecontract.PluginTask{
		ID: "video_0123456789abcdef", PluginKey: "sora", PluginVersion: "1.1.0", ServiceID: "svc_1", Owner: "tok_1",
		Surface: "openai_video", Operation: "create", Action: "text_to_video", Model: "sora-2", UpstreamModel: "sora-2",
		UpstreamTaskID: "video_upstream", Status: contract.PluginTaskSubmitted, Retain: true,
		Data: json.RawMessage(`{"id":"video_upstream"}`), Request: json.RawMessage(`{"prompt":"a cat"}`),
		CreatedAt: created, UpdatedAt: created, NextPollAt: created.Add(5 * time.Second),
	}
	if err := store.CreatePluginTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePluginTask(ctx, task); !errors.Is(err, storagecontract.ErrConflict) {
		t.Fatalf("a second create: %v", err)
	}
	if _, err := store.GetPluginTask(ctx, "video_missing0000"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("an unknown task: %v", err)
	}
	got, err := store.GetPluginTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "tok_1" || string(got.Data) != `{"id":"video_upstream"}` || got.State != nil || !got.CreatedAt.Equal(created) || !got.NextPollAt.Equal(task.NextPollAt) || !got.FinishedAt.IsZero() {
		t.Fatalf("task = %+v", got)
	}
	due, err := store.DuePluginTasks(ctx, created.Add(4*time.Second), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("due before its poll: %v, %v", due, err)
	}
	due, err = store.DuePluginTasks(ctx, created.Add(6*time.Second), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("due after its poll: %v, %v", due, err)
	}
	got.Status, got.Progress, got.State = contract.PluginTaskSuccess, "100%", json.RawMessage(`{"n":1}`)
	got.FinishedAt, got.NextPollAt, got.UpdatedAt = created.Add(time.Minute), time.Time{}, created.Add(time.Minute)
	got.ResultURL = "https://cdn.example/v.mp4"
	if err := store.UpdatePluginTask(ctx, got); err != nil {
		t.Fatal(err)
	}
	if due, _ := store.DuePluginTasks(ctx, created.Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("a finished task is due: %v", due)
	}
	if err := store.UpdatePluginTask(ctx, storagecontract.PluginTask{ID: "video_missing0000", PluginKey: "sora"}); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("updating an unknown task: %v", err)
	}
	for index := range 3 {
		other := task
		other.ID = fmt.Sprintf("task_%016d", index)
		other.CreatedAt = created.Add(time.Duration(index+1) * time.Second)
		if err := store.CreatePluginTask(ctx, other); err != nil {
			t.Fatal(err)
		}
	}
	page, next, err := store.ListPluginTasks(ctx, "sora", "", 2)
	if err != nil || len(page) != 2 || page[0].ID != "task_0000000000000002" || next == "" {
		t.Fatalf("first page = %v, %q, %v", page, next, err)
	}
	page, next, err = store.ListPluginTasks(ctx, "sora", next, 2)
	if err != nil || len(page) != 2 || page[1].ID != task.ID || next != "" {
		t.Fatalf("second page = %v, %q, %v", page, next, err)
	}
	total, running, err := store.CountPluginTasks(ctx, "sora")
	if err != nil || total != 4 || running != 3 {
		t.Fatalf("counts = %d, %d, %v", total, running, err)
	}
	if deleted, err := store.DeletePluginTasks(ctx, "sora"); err != nil || deleted != 1 {
		t.Fatalf("deleted %d finished tasks: %v", deleted, err)
	}
	big := task
	big.ID, big.Data = "task_big000000000", json.RawMessage(`"`+string(make([]byte, storagecontract.MaxPluginTaskDataBytes))+`"`)
	if err := store.CreatePluginTask(ctx, big); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("oversized data: %v", err)
	}
}
