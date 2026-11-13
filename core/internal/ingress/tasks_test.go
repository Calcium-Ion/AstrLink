package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginhost"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type taskSettings []string

func (keys taskSettings) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	settings := map[string]plugin.Settings{}
	for _, key := range keys {
		settings[key] = plugin.Settings{Enabled: true}
	}
	return settings, nil
}

type taskStore struct {
	mutex sync.Mutex
	tasks map[string]storage.PluginTask
}

func (store *taskStore) CreatePluginTask(_ context.Context, task storage.PluginTask) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if store.tasks == nil {
		store.tasks = map[string]storage.PluginTask{}
	}
	store.tasks[task.ID] = task
	return nil
}

func (store *taskStore) GetPluginTask(_ context.Context, id string) (storage.PluginTask, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	task, ok := store.tasks[id]
	if !ok {
		return storage.PluginTask{}, storage.ErrNotFound
	}
	return task, nil
}

func (store *taskStore) UpdatePluginTask(_ context.Context, task storage.PluginTask) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.tasks[task.ID] = task
	return nil
}

func (store *taskStore) DuePluginTasks(context.Context, time.Time, int) ([]storage.PluginTask, error) {
	return nil, nil
}

func (store *taskStore) ListPluginTasks(context.Context, string, string, int) ([]storage.PluginTask, string, error) {
	return nil, "", nil
}

func (store *taskStore) CountPluginTasks(context.Context, string) (int, int, error) { return 0, 0, nil }

func (store *taskStore) DeletePluginTasks(context.Context, string) (int, error) { return 0, nil }

type taskServices []contract.Service

func (services taskServices) ListServices(context.Context, storage.ServiceListOptions) (storage.ServicePage, error) {
	page := storage.ServicePage{}
	for _, service := range services {
		page.Items = append(page.Items, storage.ServiceRecord{Service: service})
	}
	return page, nil
}

func (services taskServices) GetService(_ context.Context, id contract.ServiceID) (storage.ServiceRecord, error) {
	for _, service := range services {
		if service.ID == id {
			return storage.ServiceRecord{Service: service}, nil
		}
	}
	return storage.ServiceRecord{}, storage.ErrNotFound
}

type taskSecrets map[secretstore.Ref][]byte

func (secrets taskSecrets) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	value, ok := secrets[ref]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func (secrets taskSecrets) Put(context.Context, secretstore.Ref, []byte) error { return nil }

func (secrets taskSecrets) Delete(context.Context, secretstore.Ref) error { return nil }

// taskRecords keeps the request records of a task gateway; requests may
// end together.
type taskRecords struct {
	mutex sync.Mutex
	store memoryRequestRecordStore
}

func (records *taskRecords) UpsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	records.mutex.Lock()
	defer records.mutex.Unlock()
	return records.store.UpsertRequestRecord(ctx, record)
}

func (records *taskRecords) InsertRequestRecord(ctx context.Context, record contract.RequestRecord) error {
	records.mutex.Lock()
	defer records.mutex.Unlock()
	return records.store.InsertRequestRecord(ctx, record)
}

func (records *taskRecords) FindSessionLink(ctx context.Context, kind contract.SessionCursorKind, values []string, scope storage.SessionCursorScope) (storage.SessionLinkMatch, bool, error) {
	records.mutex.Lock()
	defer records.mutex.Unlock()
	return records.store.FindSessionLink(ctx, kind, values, scope)
}

func (records *taskRecords) list() []contract.RequestRecord {
	records.mutex.Lock()
	defer records.mutex.Unlock()
	return slices.Clone(records.store.records)
}

// taskGateway is the inference handler with the official sora and sunoapi
// plugins, each on a provider of the fake vendor.
type taskGateway struct {
	handler  http.Handler
	engine   *taskplugin.Engine
	store    *taskStore
	records  *taskRecords
	services taskServices
}

func newTaskGateway(t *testing.T, vendor http.Handler) *taskGateway {
	t.Helper()
	return newTaskGatewayOf(t, vendor, func(provider taskProvider) taskServices {
		return taskServices{
			provider("sora", []string{"sora-2"},
				contract.Capability{Protocol: contract.ProtocolOpenAIVideos, Mode: contract.CapabilityModeNative},
				contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}),
			provider("sunoapi", []string{"suno_music", "suno_lyrics"},
				contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true}),
		}
	})
}

// taskProvider makes the provider of a task plugin on the fake vendor, its
// key sk-<plugin>.
type taskProvider func(key string, models []string, capabilities ...contract.Capability) contract.Service

// newTaskGatewayOf is the inference handler with the official plugins the
// providers name, each on a provider of the fake vendor.
func newTaskGatewayOf(t *testing.T, vendor http.Handler, providers func(taskProvider) taskServices) *taskGateway {
	t.Helper()
	server := httptest.NewServer(vendor)
	t.Cleanup(server.Close)
	provider := func(key string, models []string, capabilities ...contract.Capability) contract.Service {
		return contract.Service{
			ID: contract.ServiceID("svc_" + key), Name: key, Kind: contract.ServiceKindPlugin, Enabled: true, Models: models,
			Capabilities: append(capabilities, contract.Capability{Protocol: contract.ProtocolPluginNative, Mode: contract.CapabilityModeNative}),
			HTTP:         &contract.HTTPConnection{BaseURL: server.URL, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}, CredentialRef: "local://service/svc_" + key},
			Plugin:       &contract.ServicePlugin{Key: key, Provider: contract.PluginProviderTask},
		}
	}
	services := providers(provider)
	keys := taskSettings{}
	secrets := taskSecrets{}
	for _, service := range services {
		keys = append(keys, service.Plugin.Key)
		secrets[secretstore.Ref(service.HTTP.CredentialRef)] = []byte("sk-" + service.Plugin.Key)
	}
	manager := plugin.NewManager(plugin.ManagerOptions{Dir: "../plugin/testdata/newapi", Settings: keys})
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if err := service.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := endpoint.NewStoreResolver(services)
	if err != nil {
		t.Fatal(err)
	}
	store := &taskStore{}
	engine := taskplugin.New(taskplugin.Options{
		Plugins: manager, Store: store, Services: services, Send: transport.New(server.Client().Transport).RoundTrip,
		Secrets: secrets, Dir: t.TempDir(), SigningKey: []byte("key"), Logf: t.Logf,
	})
	records := &taskRecords{}
	return &taskGateway{
		handler: NewWithDependencies(Dependencies{Resolver: resolver, Tasks: engine, RequestRecords: records, Plugins: taskRequestPlugins}),
		engine:  engine, store: store, records: records, services: services,
	}
}

// taskRequestPlugins, when a test sets it, runs request processing
// instances in front of the task gateways it makes.
var taskRequestPlugins *pluginhost.Host

func (gateway *taskGateway) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	gateway.handler.ServeHTTP(recorder, request)
	return recorder
}

// soraVendor serves OpenAI's video API: a video that is done when asked.
func soraVendor(t *testing.T) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer sk-sora" {
			http.Error(writer, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
			return
		}
		for name := range request.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-astrlink") {
				t.Errorf("the vendor got the gateway's header %s", name)
			}
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/videos":
			_, _ = io.WriteString(writer, `{"id":"vid_up","status":"queued"}`)
		case request.URL.Path == "/v1/videos/vid_up":
			_, _ = io.WriteString(writer, `{"id":"vid_up","status":"completed","progress":100}`)
		case request.URL.Path == "/v1/videos/vid_up/content":
			writer.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(writer, "MP4DATA")
		default:
			http.NotFound(writer, request)
		}
	})
}

func TestVideosRunThroughTheirTaskPlugin(t *testing.T) {
	gateway := newTaskGateway(t, soraVendor(t))
	created := gateway.do(t, http.MethodPost, "/v1/videos", `{"model":"sora-2","prompt":"a paper boat","seconds":"4"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	var video map[string]any
	_ = json.Unmarshal(created.Body.Bytes(), &video)
	id, _ := video["id"].(string)
	if !strings.HasPrefix(id, "video_") || video["status"] != "queued" || video["object"] != "video" {
		t.Fatalf("video = %s", created.Body)
	}
	if missing := gateway.do(t, http.MethodGet, "/v1/videos/"+id+"/content", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("content before the video is ready = %d %s", missing.Code, missing.Body)
	}
	if _, err := gateway.engine.Poll(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	retrieved := gateway.do(t, http.MethodGet, "/v1/videos/"+id, "")
	_ = json.Unmarshal(retrieved.Body.Bytes(), &video)
	if retrieved.Code != http.StatusOK || video["status"] != "completed" || video["id"] != id {
		t.Fatalf("retrieve = %d %s", retrieved.Code, retrieved.Body)
	}
	content := gateway.do(t, http.MethodGet, "/v1/videos/"+id+"/content", "")
	if content.Code != http.StatusOK || content.Body.String() != "MP4DATA" || content.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content = %d %q", content.Code, content.Body)
	}
	if other := gateway.do(t, http.MethodGet, "/v1/videos/video_00000000000000000000000000000000", ""); other.Code != http.StatusNotFound {
		t.Fatalf("an unknown video = %d", other.Code)
	}
}

func TestResponsesOfATaskModelAnswerInTheBackgroundAndRetrieveLater(t *testing.T) {
	gateway := newTaskGateway(t, soraVendor(t))
	created := gateway.do(t, http.MethodPost, "/v1/responses", `{"model":"sora-2","input":"a paper boat","background":true}`)
	var response map[string]any
	_ = json.Unmarshal(created.Body.Bytes(), &response)
	id, _ := response["id"].(string)
	if created.Code != http.StatusOK || !strings.HasPrefix(id, "resp_") || response["status"] != "queued" || response["object"] != "response" {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	if _, err := gateway.engine.Poll(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	retrieved := gateway.do(t, http.MethodGet, "/v1/responses/"+id, "")
	if retrieved.Code != http.StatusOK || !strings.Contains(retrieved.Body.String(), `"status":"completed"`) {
		t.Fatalf("retrieve = %d %s", retrieved.Code, retrieved.Body)
	}
	// The output links the video through an artifact link, which opens
	// without a token.
	start := strings.Index(retrieved.Body.String(), "http://example.com/v1/tasks/")
	if start < 0 {
		t.Fatalf("no artifact link in %s", retrieved.Body)
	}
	link := retrieved.Body.String()[start:]
	link = link[:strings.IndexAny(link, `"\)`)]
	link = strings.ReplaceAll(link, `\u0026`, "&")
	content := gateway.do(t, http.MethodGet, strings.TrimPrefix(link, "http://example.com"), "")
	if content.Code != http.StatusOK || content.Body.String() != "MP4DATA" {
		t.Fatalf("artifact link %s = %d %q", link, content.Code, content.Body)
	}
	forged := gateway.do(t, http.MethodGet, "/v1/tasks/"+id+"/artifacts/video/content?access=forged", "")
	if forged.Code != http.StatusNotFound {
		t.Fatalf("a forged link = %d", forged.Code)
	}
}

func TestResponsesOfATaskModelStreamTheTask(t *testing.T) {
	gateway := newTaskGateway(t, soraVendor(t))
	streamed := gateway.do(t, http.MethodPost, "/v1/responses", `{"model":"sora-2","input":"a paper boat","stream":true}`)
	if streamed.Code != http.StatusOK || streamed.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("stream = %d %s", streamed.Code, streamed.Body)
	}
	var kinds []string
	for _, line := range strings.Split(streamed.Body.String(), "\n") {
		if kind, ok := strings.CutPrefix(line, "event: "); ok {
			kinds = append(kinds, kind)
		}
	}
	for _, want := range []string{"response.created", "response.output_text.delta", "response.output_item.done", "response.completed"} {
		if !slices.Contains(kinds, want) {
			t.Fatalf("events %v lack %s:\n%s", kinds, want, streamed.Body)
		}
	}
	if kinds[len(kinds)-1] != "response.completed" {
		t.Fatalf("the stream ends with %s", kinds[len(kinds)-1])
	}
}

func TestATaskPluginsProviderIsTestedThroughItsTask(t *testing.T) {
	gateway := newTaskGateway(t, soraVendor(t))
	handler := gateway.handler.(*Handler)
	sora := gateway.services[0]
	var media []contract.ServiceTestMedia
	test := func(host string, stream bool) (*httptest.ResponseRecorder, []int, bool) {
		t.Helper()
		body := fmt.Sprintf(`{"model":"sora-2","input":[{"role":"user","content":[{"type":"input_text","text":"a paper boat"}]}],"stream":%t,"store":false}`, stream)
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
		request.Host = host
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		var statuses []int
		outbound := false
		err := handler.ServeServiceTest(recorder, request, sora, contract.ServiceTestRequest{
			Model: "sora-2", Protocol: contract.ProtocolOpenAIResponses, Stream: stream,
		}, "", ServiceTestObserver{
			Outbound: func() { outbound = true },
			Response: func(status int) { statuses = append(statuses, status) },
			Media:    func(found []contract.ServiceTestMedia) { media = found },
		})
		if err != nil {
			t.Fatal(err)
		}
		return recorder, statuses, outbound
	}
	streamed, statuses, outbound := test("127.0.0.1:18417", true)
	if streamed.Code != http.StatusOK || !outbound || !slices.Equal(statuses, []int{http.StatusOK}) {
		t.Fatalf("test = %d %v %t\n%s", streamed.Code, statuses, outbound, streamed.Body)
	}
	// The video it generated is reported as a video at the gateway's
	// address, and the answer links it too.
	if len(media) != 1 || media[0].Kind != "video" || media[0].Name != "video" || !strings.HasPrefix(media[0].URL, "http://127.0.0.1:18417/v1/tasks/resp_") {
		t.Fatalf("media = %+v", media)
	}
	if !strings.Contains(streamed.Body.String(), "http://127.0.0.1:18417/v1/tasks/resp_") || !strings.Contains(streamed.Body.String(), "event: response.completed") {
		t.Fatalf("stream:\n%s", streamed.Body)
	}
	// Made inside the gateway without a host, its links stay relative.
	answered, _, _ := test("", false)
	if answered.Code != http.StatusOK || !strings.Contains(answered.Body.String(), `src=\"/v1/tasks/resp_`) || strings.Contains(answered.Body.String(), "http:/") {
		t.Fatalf("sync test = %d %s", answered.Code, answered.Body)
	}
}

func TestAStreamOfATaskDoneAtOnceOpensInProgress(t *testing.T) {
	// DashScope answers qwen-image-2.0 at once, with the image.
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer sk-alibaba" || request.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"image":"https://images.example/cat.png"}]}}]},"request_id":"req-1","usage":{"image_count":1}}`)
	})
	gateway := newTaskGatewayOf(t, vendor, func(provider taskProvider) taskServices {
		return taskServices{provider("alibaba", []string{"qwen-image-2.0"},
			contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			contract.Capability{Protocol: contract.ProtocolOpenAIImages, Mode: contract.CapabilityModeNative})}
	})
	streamed := gateway.do(t, http.MethodPost, "/v1/responses", `{"model":"qwen-image-2.0","input":"a cat under cherry blossoms","stream":true}`)
	events := strings.Split(strings.TrimSpace(streamed.Body.String()), "\n\n")
	if streamed.Code != http.StatusOK || len(events) < 2 {
		t.Fatalf("stream = %d %s", streamed.Code, streamed.Body)
	}
	if !strings.Contains(events[0], "event: response.created") || !strings.Contains(events[0], `"status":"in_progress"`) || strings.Contains(events[0], "completed_at") {
		t.Fatalf("the stream opens with %s", events[0])
	}
	last := events[len(events)-1]
	if !strings.Contains(last, "event: response.completed") || !strings.Contains(last, `"status":"completed"`) || !strings.Contains(last, "/artifacts/image-1/content?access=") {
		t.Fatalf("the stream ends with %s", last)
	}
}

func TestATaskThatSucceedsFinishesItsRecord(t *testing.T) {
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"image":"https://images.example/cat.png"}]}}]},"request_id":"req-1","usage":{"image_count":1}}`)
	})
	gateway := newTaskGatewayOf(t, vendor, func(provider taskProvider) taskServices {
		return taskServices{provider("alibaba", []string{"qwen-image-2.0"},
			contract.Capability{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true},
			contract.Capability{Protocol: contract.ProtocolOpenAIImages, Mode: contract.CapabilityModeNative})}
	})
	for _, call := range []struct{ path, body string }{
		{"/v1/images/generations", `{"model":"qwen-image-2.0","prompt":"a cat under cherry blossoms"}`},
		{"/v1/responses", `{"model":"qwen-image-2.0","input":"a cat under cherry blossoms"}`},
		{"/v1/responses", `{"model":"qwen-image-2.0","input":"a cat under cherry blossoms","stream":true}`},
	} {
		if answered := gateway.do(t, http.MethodPost, call.path, call.body); answered.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", call.path, answered.Code, answered.Body)
		}
	}
	// A provider's test runs the same task, and keeps a record too.
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"qwen-image-2.0","input":"a cat","store":false}`))
	request.Header.Set("Content-Type", "application/json")
	tested := httptest.NewRecorder()
	if err := gateway.handler.(*Handler).ServeServiceTest(tested, request, gateway.services[0], contract.ServiceTestRequest{
		Model: "qwen-image-2.0", Protocol: contract.ProtocolOpenAIResponses,
	}, "", ServiceTestObserver{}); err != nil || tested.Code != http.StatusOK {
		t.Fatalf("test = %d %v %s", tested.Code, err, tested.Body)
	}
	// Each ends as it answered, on the provider that served it, never left
	// waiting for a provider.
	records := gateway.records.list()
	if len(records) != 4 {
		t.Fatalf("%d records", len(records))
	}
	for _, record := range records {
		if record.Status != contract.RequestStatusSucceeded || record.ServiceID == nil || *record.ServiceID != "svc_alibaba" || record.Plan == nil {
			t.Fatalf("record %s = %s on %v, plan %+v", record.InputProtocol, record.Status, record.ServiceID, record.Plan)
		}
	}
}

func TestSunoNativeRoutesSubmitAndQuery(t *testing.T) {
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/suno/submit/MUSIC":
			_, _ = io.WriteString(writer, `{"code":"success","data":"suno-up-1"}`)
		case "/suno/fetch":
			_, _ = io.WriteString(writer, `{"code":"success","data":[{"task_id":"suno-up-1","status":"SUCCESS","data":[{"audio_url":"https://cdn.example/1.mp3"}]}]}`)
		default:
			http.NotFound(writer, request)
		}
	})
	gateway := newTaskGateway(t, vendor)
	submitted := gateway.do(t, http.MethodPost, "/suno/submit/music", `{"prompt":"a song about tea","mv":"chirp-v3-5"}`)
	var answer struct {
		Code string `json:"code"`
		Data string `json:"data"`
	}
	_ = json.Unmarshal(submitted.Body.Bytes(), &answer)
	if submitted.Code != http.StatusOK || answer.Code != "success" || !strings.HasPrefix(answer.Data, "task_") {
		t.Fatalf("submit = %d %s", submitted.Code, submitted.Body)
	}
	if _, err := gateway.engine.Poll(context.Background(), answer.Data); err != nil {
		t.Fatal(err)
	}
	queried := gateway.do(t, http.MethodGet, "/suno/fetch/"+answer.Data, "")
	if queried.Code != http.StatusOK || !strings.Contains(queried.Body.String(), "SUCCESS") || !strings.Contains(queried.Body.String(), answer.Data) || strings.Contains(queried.Body.String(), "suno-up-1") {
		t.Fatalf("query = %d %s", queried.Code, queried.Body)
	}
	listed := gateway.do(t, http.MethodPost, "/suno/fetch", fmt.Sprintf(`{"ids":[%q]}`, answer.Data))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), answer.Data) {
		t.Fatalf("batch query = %d %s", listed.Code, listed.Body)
	}
	task := gateway.do(t, http.MethodGet, "/v1/tasks/"+answer.Data, "")
	if task.Code != http.StatusOK || !strings.Contains(task.Body.String(), `"status":"SUCCESS"`) {
		t.Fatalf("/v1/tasks = %d %s", task.Code, task.Body)
	}
	refused := gateway.do(t, http.MethodPost, "/suno/submit/dance", `{"prompt":"x"}`)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "invalid_action") {
		t.Fatalf("an unknown action = %d %s", refused.Code, refused.Body)
	}
}
