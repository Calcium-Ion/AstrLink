package taskplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// The official task plugins of new-api, the newest version of each.
const officialPlugins = "../plugin/testdata/newapi"

type enabledPlugins map[string]bool

func (keys enabledPlugins) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	settings := map[string]plugin.Settings{}
	for key := range keys {
		settings[key] = plugin.Settings{Enabled: true}
	}
	return settings, nil
}

// memoryTasks is a PluginTaskStore in memory.
type memoryTasks struct {
	mutex sync.Mutex
	tasks map[string]storage.PluginTask
}

func (store *memoryTasks) CreatePluginTask(_ context.Context, task storage.PluginTask) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.tasks == nil {
		store.tasks = map[string]storage.PluginTask{}
	}
	if _, ok := store.tasks[task.ID]; ok {
		return storage.ErrConflict
	}
	store.tasks[task.ID] = task
	return nil
}

func (store *memoryTasks) GetPluginTask(_ context.Context, id string) (storage.PluginTask, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	task, ok := store.tasks[id]
	if !ok {
		return storage.PluginTask{}, storage.ErrNotFound
	}
	return task, nil
}

func (store *memoryTasks) UpdatePluginTask(_ context.Context, task storage.PluginTask) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if _, ok := store.tasks[task.ID]; !ok {
		return storage.ErrNotFound
	}
	store.tasks[task.ID] = task
	return nil
}

func (store *memoryTasks) DuePluginTasks(_ context.Context, now time.Time, limit int) ([]storage.PluginTask, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var due []storage.PluginTask
	for _, task := range store.tasks {
		if !task.NextPollAt.IsZero() && !task.NextPollAt.After(now) {
			due = append(due, task)
		}
	}
	slices.SortFunc(due, func(a, b storage.PluginTask) int { return a.NextPollAt.Compare(b.NextPollAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (store *memoryTasks) ListPluginTasks(context.Context, string, string, int) ([]storage.PluginTask, string, error) {
	return nil, "", nil
}

func (store *memoryTasks) CountPluginTasks(context.Context, string) (int, int, error) {
	return 0, 0, nil
}

func (store *memoryTasks) DeletePluginTasks(context.Context, string) (int, error) { return 0, nil }

type memoryServices map[contract.ServiceID]contract.Service

func (services memoryServices) GetService(_ context.Context, id contract.ServiceID) (storage.ServiceRecord, error) {
	service, ok := services[id]
	if !ok {
		return storage.ServiceRecord{}, storage.ErrNotFound
	}
	return storage.ServiceRecord{Service: service}, nil
}

type memorySecrets map[secretstore.Ref][]byte

func (secrets memorySecrets) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	value, ok := secrets[ref]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (secrets memorySecrets) Put(_ context.Context, ref secretstore.Ref, value []byte) error {
	secrets[ref] = bytes.Clone(value)
	return nil
}

func (secrets memorySecrets) Delete(_ context.Context, ref secretstore.Ref) error {
	delete(secrets, ref)
	return nil
}

// harness is an engine running official plugins against a fake vendor.
type harness struct {
	t        *testing.T
	engine   *Engine
	tasks    *memoryTasks
	services memoryServices
	vendor   *httptest.Server
	now      time.Time
	mutex    sync.Mutex
}

func newHarness(t *testing.T, vendor http.Handler, keys ...string) *harness {
	t.Helper()
	enabled := enabledPlugins{}
	for _, key := range keys {
		enabled[key] = true
	}
	manager := plugin.NewManager(plugin.ManagerOptions{Dir: officialPlugins, Settings: enabled})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	for _, key := range keys {
		if found := manager.Current().Plugin(key); found == nil || !found.Enabled() || found.Task() == nil {
			t.Fatalf("plugin %s = %+v, want an enabled task plugin", key, found)
		}
	}
	server := httptest.NewServer(vendor)
	t.Cleanup(server.Close)
	h := &harness{t: t, tasks: &memoryTasks{}, services: memoryServices{}, vendor: server, now: time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)}
	secrets := memorySecrets{}
	forwarder := transport.New(server.Client().Transport)
	h.engine = New(Options{
		Plugins: manager, Store: h.tasks, Services: h.services, Secrets: secrets, Send: forwarder.RoundTrip,
		Dir: t.TempDir(), SigningKey: []byte("test-signing-key"), Now: h.clock, Logf: t.Logf,
	})
	return h
}

func (h *harness) clock() time.Time {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return h.now
}

func (h *harness) advance(duration time.Duration) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.now = h.now.Add(duration)
}

// provider adds a provider of the plugin key with the API key apiKey,
// on the fake vendor unless base names another address.
func (h *harness) provider(key, apiKey string, models ...string) contract.Service {
	ref := secretstore.Ref("local://service/" + key)
	_ = h.engine.secrets.Put(context.Background(), ref, []byte(apiKey))
	service := contract.Service{
		ID: contract.ServiceID("svc_" + strings.ReplaceAll(key, "-", "_")), Name: key, Kind: contract.ServiceKindPlugin, Enabled: true, Models: models,
		HTTP:   &contract.HTTPConnection{BaseURL: h.vendor.URL, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: string(ref)},
		Plugin: &contract.ServicePlugin{Key: key, Provider: contract.PluginProviderTask},
	}
	h.services[service.ID] = service
	return service
}

func jsonBody(text string) *Body { return &Body{Kind: BodyJSON, JSON: json.RawMessage(text)} }

func TestSoraVideoRunsFromSubmissionToDownload(t *testing.T) {
	var polls int
	var submitted map[string]any
	var mutex sync.Mutex
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if request.Header.Get("Authorization") != "Bearer sk-sora" {
			http.Error(writer, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/videos":
			_ = json.NewDecoder(request.Body).Decode(&submitted)
			_, _ = io.WriteString(writer, `{"id":"video_upstream_1","object":"video","status":"queued"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/videos/video_upstream_1":
			polls++
			if polls == 1 {
				_, _ = io.WriteString(writer, `{"id":"video_upstream_1","status":"in_progress","progress":40}`)
				return
			}
			_, _ = io.WriteString(writer, `{"id":"video_upstream_1","status":"completed","progress":100,"seconds":"8","size":"1280x720"}`)
		case request.URL.Path == "/v1/videos/video_upstream_1/content":
			writer.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(writer, "MP4DATA")
		default:
			http.NotFound(writer, request)
		}
	})
	h := newHarness(t, vendor, "sora")
	service := h.provider("sora", "sk-sora", "sora-2")
	ctx := context.Background()
	request := Request{Method: http.MethodPost, Path: "/v1/videos", Body: jsonBody(`{"model":"sora-2","prompt":"a cat on a boat","seconds":"8","size":"1280x720"}`), Protocol: plugin.TaskProtocolVideo, Operation: "create", Model: "sora-2"}
	intent, err := h.engine.Decode(ctx, "sora", "protocols.openai_video.decodeRequest", request)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if intent.Kind != "submit" || intent.Model != "sora-2" || intent.Action != "text_to_video" {
		t.Fatalf("intent = %+v", intent)
	}
	done, err := h.engine.Submit(ctx, Submission{PluginKey: "sora", Surface: plugin.TaskProtocolVideo, Operation: "create", Intent: intent, Owner: "tok_1", Retain: true}, []Candidate{{Service: service}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	task := done.Task
	if !strings.HasPrefix(task.ID, "video_") || task.UpstreamTaskID != "video_upstream_1" || task.Status != contract.PluginTaskSubmitted || task.ServiceID != service.ID {
		t.Fatalf("task = %+v", task)
	}
	if submitted["prompt"] != "a cat on a boat" || submitted["model"] != "sora-2" {
		t.Fatalf("the vendor got %v", submitted)
	}
	if string(task.Usage) != `{"seconds":8,"size":"1280x720"}` {
		t.Fatalf("usage = %s", task.Usage)
	}
	retrieve := Request{Method: http.MethodGet, Path: "/v1/videos/" + task.ID, Protocol: plugin.TaskProtocolVideo, Operation: "retrieve", Model: task.Model}
	rendered, err := h.engine.RenderVideo(ctx, task, retrieve)
	if err != nil {
		t.Fatalf("RenderVideo: %v", err)
	}
	var video map[string]any
	_ = json.Unmarshal(rendered, &video)
	if video["id"] != task.ID || video["object"] != "video" || video["status"] != "queued" || video["model"] != "sora-2" {
		t.Fatalf("video = %s", rendered)
	}
	h.advance(5 * time.Second)
	task, err = h.engine.Poll(ctx, task.ID)
	if err != nil || task.Status != contract.PluginTaskInProgress || task.Progress != "40%" {
		t.Fatalf("first poll = %+v, %v", task, err)
	}
	h.advance(5 * time.Second)
	task, err = h.engine.Poll(ctx, task.ID)
	if err != nil || task.Status != contract.PluginTaskSuccess || task.FinishedAt.IsZero() || !task.NextPollAt.IsZero() {
		t.Fatalf("second poll = %+v, %v", task, err)
	}
	if string(task.Usage) != `{"seconds":8,"size":"1280x720"}` {
		t.Fatalf("usage after completion = %s", task.Usage)
	}
	rendered, _ = h.engine.RenderVideo(ctx, task, retrieve)
	_ = json.Unmarshal(rendered, &video)
	if video["status"] != "completed" || video["progress"] != float64(100) || video["completed_at"] == nil {
		t.Fatalf("finished video = %s", rendered)
	}
	if _, err := h.engine.Task(ctx, task.ID, "tok_2"); err == nil {
		t.Fatal("another token read the task")
	}
	recorder := httptest.NewRecorder()
	if err := h.engine.ServeContent(ctx, recorder, httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.ID+"/content", nil), task, "video"); err != nil {
		t.Fatalf("ServeContent: %v", err)
	}
	if recorder.Body.String() != "MP4DATA" || recorder.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content = %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	if access := h.engine.Access(task.ID, "video"); !h.engine.VerifyAccess(task.ID, "video", access) || h.engine.VerifyAccess(task.ID, "other", access) {
		t.Fatal("the artifact capability does not verify")
	}
}

func TestRefusedSubmissionMovesOnToTheNextProvider(t *testing.T) {
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") == "Bearer sk-busy" {
			http.Error(writer, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		if request.Header.Get("Authorization") == "Bearer sk-bad-request" {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, `{"error":{"message":"prompt rejected"}}`)
			return
		}
		_, _ = io.WriteString(writer, `{"id":"video_upstream_2","status":"queued"}`)
	})
	h := newHarness(t, vendor, "sora")
	busy := h.provider("sora", "sk-busy", "sora-2")
	busy.ID = "svc_busy"
	h.services[busy.ID] = busy
	good := h.provider("sora", "sk-good", "sora-2")
	_ = h.engine.secrets.Put(context.Background(), secretstore.Ref(busy.HTTP.CredentialRef+"-busy"), []byte("sk-busy"))
	busy.HTTP = &contract.HTTPConnection{BaseURL: h.vendor.URL, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: busy.HTTP.CredentialRef + "-busy"}
	h.services[busy.ID] = busy
	intent := Intent{Kind: "submit", Model: "sora-2", Action: "text_to_video", RequestBody: json.RawMessage(`{"model":"sora-2","prompt":"a cat"}`)}
	done, err := h.engine.Submit(context.Background(), Submission{PluginKey: "sora", Surface: plugin.TaskProtocolVideo, Intent: intent, Retain: true}, []Candidate{{Service: busy}, {Service: good}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if done.Task.ServiceID != good.ID || len(done.Attempts) != 2 || done.Attempts[0].Status != http.StatusServiceUnavailable {
		t.Fatalf("submitted = %+v", done)
	}
	rejecting := h.provider("sora", "sk-bad-request", "sora-2")
	_, err = h.engine.Submit(context.Background(), Submission{PluginKey: "sora", Surface: plugin.TaskProtocolVideo, Intent: intent, Retain: true}, []Candidate{{Service: rejecting}, {Service: good}})
	var refused *Error
	if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest || refused.Message != "prompt rejected" || len(refused.Attempts()) != 1 {
		t.Fatalf("a refused prompt = %v", err)
	}
}
