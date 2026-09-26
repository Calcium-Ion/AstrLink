package intelligence

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type runStore struct {
	sync.Mutex
	values map[string]json.RawMessage
}

func (s *runStore) LoadIntelligence(_ context.Context, key string) (json.RawMessage, error) {
	s.Lock()
	defer s.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return append(json.RawMessage(nil), raw...), nil
}
func (s *runStore) SaveIntelligence(_ context.Context, key, _, _ string, raw json.RawMessage) error {
	s.Lock()
	defer s.Unlock()
	s.values[key] = append(json.RawMessage(nil), raw...)
	return nil
}
func (s *runStore) ListIntelligenceRuns(context.Context, string) ([]json.RawMessage, error) {
	return nil, nil
}

type runServices struct {
	storage.ServiceStore
	service contract.Service
}

func (s runServices) GetService(context.Context, contract.ServiceID) (storage.ServiceRecord, error) {
	return storage.ServiceRecord{Service: s.service}, nil
}

type runExecutor func(context.Context, contract.Service, contract.IntelligenceRequest) contract.ServiceTestResult

func (f runExecutor) TestIntelligence(ctx context.Context, s contract.Service, r contract.IntelligenceRequest) contract.ServiceTestResult {
	return f(ctx, s, r)
}

func testManager(t *testing.T, settings Settings, executor runExecutor) *Manager {
	t.Helper()
	service := contract.Service{ID: "service_iq", Models: []string{DefaultModel}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIChat}}}
	m := New(&runStore{values: map[string]json.RawMessage{}}, runServices{service: service}, executor)
	if err := m.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return m
}
func awaitRun(t *testing.T, m *Manager, id string, ready func(Run) bool) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(run) {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run did not reach expected state")
	return Run{}
}

func TestParallelQuestionsKeepRenderRepliesAndDurationsSeparate(t *testing.T) {
	settings := DefaultSettings()
	settings.Questions[2] = Question{ID: "second_svg", Name: "second", Kind: "svg", Prompt: "second image", Answer: "second image"}
	settings.DefaultQuestionIDs[2] = "second_svg"
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	m := testManager(t, settings, func(ctx context.Context, _ contract.Service, in contract.IntelligenceRequest) contract.ServiceTestResult {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return contract.ServiceTestResult{}
		}
		return contract.ServiceTestResult{OK: true, Output: "29"}
	})
	run, err := m.Start(context.Background(), "service_iq", StartRequest{Mode: "all"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Cancel(run.ID) })
	for range 3 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("questions were serialized")
		}
	}
	active := awaitRun(t, m, run.ID, func(r Run) bool {
		return r.Items[0].StartedAt != "" && r.Items[1].StartedAt != "" && r.Items[2].StartedAt != ""
	})
	for _, item := range active.Items {
		if item.Status != "generating" {
			t.Fatalf("unexpected state: %s", item.Status)
		}
	}
	time.Sleep(15 * time.Millisecond)
	close(release)
	awaitRun(t, m, run.ID, func(r Run) bool { return r.Items[0].Status == "rendering" && r.Items[2].Status == "rendering" })
	// Reply in reverse order: each image must reach its own worker.
	if err := m.Render(run.ID, RenderInput{QuestionID: "second_svg", Error: "second failed"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Render(run.ID, RenderInput{QuestionID: "pelican", Error: "first failed"}); err != nil {
		t.Fatal(err)
	}
	done := awaitRun(t, m, run.ID, func(r Run) bool { return r.Status == "completed" })
	if done.Items[0].Reason != "first failed" || done.Items[2].Reason != "second failed" || done.Items[1].Status != "failed" {
		t.Fatalf("mixed results: %+v", done.Items)
	}
	for _, item := range done.Items {
		if item.DurationMS <= 0 {
			t.Fatal("elapsed time not persisted")
		}
	}
}

func TestCancelParallelRunDoesNotStartQueuedQuestion(t *testing.T) {
	settings := DefaultSettings()
	settings.Questions = append(settings.Questions, Question{ID: "fourth", Name: "fourth", Kind: "text", Prompt: "fourth", Answer: "yes"})
	settings.DefaultQuestionIDs = append(settings.DefaultQuestionIDs, "fourth")
	started := make(chan struct{}, 4)
	m := testManager(t, settings, func(ctx context.Context, _ contract.Service, _ contract.IntelligenceRequest) contract.ServiceTestResult {
		started <- struct{}{}
		<-ctx.Done()
		return contract.ServiceTestResult{Message: "cancelled"}
	})
	run, err := m.Start(context.Background(), "service_iq", StartRequest{Mode: "all"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Cancel(run.ID) })
	for range 3 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("parallel workers did not start")
		}
	}
	if err = m.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	done := awaitRun(t, m, run.ID, func(r Run) bool { return r.Status == "cancelled" })
	if len(started) != 0 {
		t.Fatal("queued question ran after cancellation")
	}
	for _, item := range done.Items {
		if pending(item.Status) {
			t.Fatal("unfinished item left after cancellation")
		}
	}
}
