package taskplugin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func allowLoopback(context.Context, *url.URL) error { return nil }

func TestAlibabaImageAnswersAtOnceAndKeepsNoResult(t *testing.T) {
	var vendor string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/services/aigc/multimodal-generation/generation":
			if request.Header.Get("X-DashScope-Async") != "" {
				t.Errorf("a qwen image went asynchronous")
			}
			_, _ = fmt.Fprintf(writer, `{"request_id":"r1","output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"image":"%s/img/1.png"}]}}]},"usage":{"image_count":1,"width":1024,"height":1024}}`, vendor)
		case "/img/1.png":
			_, _ = io.WriteString(writer, "PNGDATA")
		default:
			http.NotFound(writer, request)
		}
	})
	h := newHarness(t, handler, "alibaba")
	vendor = h.vendor.URL
	h.engine.checkAddress = allowLoopback
	service := h.provider("alibaba", "sk-ali", "qwen-image")
	ctx := context.Background()
	request := Request{Method: http.MethodPost, Path: "/v1/images/generations", Body: jsonBody(`{"model":"qwen-image","prompt":"a red fox","response_format":"b64_json"}`), Protocol: plugin.TaskProtocolImage, Operation: "generate", Model: "qwen-image"}
	intent, err := h.engine.Decode(ctx, "alibaba", "protocols.openai_image.decodeRequest", request)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	done, err := h.engine.Submit(ctx, Submission{PluginKey: "alibaba", Surface: plugin.TaskProtocolImage, Operation: "generate", Intent: intent, Owner: "tok", Retain: false}, []Candidate{{Service: service}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	task := done.Task
	if task.Status != contract.PluginTaskSuccess || task.Retain || task.Data != nil || task.UpstreamTaskID != task.ID {
		t.Fatalf("task = %+v", task)
	}
	if !strings.Contains(string(task.Usage), `"image_count":1`) {
		t.Fatalf("usage = %s", task.Usage)
	}
	if _, err := h.engine.Task(ctx, task.ID, "tok"); err == nil {
		t.Fatal("a task that keeps no result was found")
	}
	rendered, err := h.engine.RenderImages(ctx, task, request, done.Data, "b64_json")
	if err != nil {
		t.Fatalf("RenderImages: %v", err)
	}
	var images struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rendered, &images); err != nil || images.Created == 0 || len(images.Data) != 1 {
		t.Fatalf("images = %s, %v", rendered, err)
	}
	if images.Data[0].B64JSON != base64.StdEncoding.EncodeToString([]byte("PNGDATA")) || images.Data[0].URL != vendor+"/img/1.png" {
		t.Fatalf("image = %+v", images.Data[0])
	}
}

func TestAlibabaInterleavedImagesBuildTheirAnswerFromEvents(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" || request.Header.Get("X-DashScope-Sse") != "enable" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		for _, event := range []string{
			`{"request_id":"r2","output":{"choices":[{"finish_reason":"null","message":{"role":"assistant","content":[{"text":"Here "}]}}]}}`,
			`{"output":{"choices":[{"finish_reason":"null","message":{"role":"assistant","content":[{"text":"it is"}]}}]}}`,
			`{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"image":"https://cdn.example/fox.png"}]}}]},"usage":{"image_count":1}}`,
		} {
			_, _ = fmt.Fprintf(writer, ": ping\r\nid: 1\r\ndata: %s\r\n\r\n", event)
			flusher.Flush()
		}
		// The plugin is done before the stream ends; what follows is never
		// read.
		_, _ = io.WriteString(writer, "data: {\"output\":{}}\n\n")
	})
	h := newHarness(t, handler, "alibaba")
	service := h.provider("alibaba", "sk-ali", "wan2.6-image")
	ctx := context.Background()
	// Interleaved output streams when the client asks DashScope for it.
	sse := http.Header{"X-Dashscope-Sse": {"enable"}}
	request := Request{
		Method: http.MethodPost, Path: "/ali/api/v1/services/aigc/multimodal-generation/generation", Headers: sse,
		Body: jsonBody(`{"model":"wan2.6-image","input":{"messages":[{"role":"user","content":[{"text":"draw a fox and say something"}]}]},"parameters":{"enable_interleave":true}}`),
	}
	intent, err := h.engine.Decode(ctx, "alibaba", "native.createImageTask", request)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	done, err := h.engine.Submit(ctx, Submission{PluginKey: "alibaba", Surface: "native", Operation: request.Path, Intent: intent, RequestHeaders: sse, Owner: "tok", Retain: false}, []Candidate{{Service: service}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if done.Task.Status != contract.PluginTaskSuccess || done.Task.ResultURL != "https://cdn.example/fox.png" {
		t.Fatalf("task = %+v", done.Task)
	}
	var built struct {
		RequestID string `json:"request_id"`
		Output    struct {
			Finished bool `json:"finished"`
			Choices  []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					Content []map[string]string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
	}
	if err := json.Unmarshal(done.Data, &built); err != nil {
		t.Fatalf("built %s: %v", done.Data, err)
	}
	content := built.Output.Choices[0].Message.Content
	if built.RequestID != "r2" || !built.Output.Finished || len(content) != 2 || content[0]["text"] != "Here it is" || content[1]["image"] != "https://cdn.example/fox.png" {
		t.Fatalf("built = %s", done.Data)
	}
	presented, err := h.engine.Present(ctx, "alibaba", "imageCreated", request, []storage.PluginTask{done.Task}, false, done.Data, nil)
	if err != nil || !strings.Contains(string(presented), "Here it is") {
		t.Fatalf("Present = %s, %v", presented, err)
	}
}

func TestSunoPollsItsTasksInOneBatch(t *testing.T) {
	var mutex sync.Mutex
	var fetched [][]string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		switch request.URL.Path {
		case "/suno/submit/MUSIC":
			var body map[string]any
			_ = json.NewDecoder(request.Body).Decode(&body)
			_, _ = fmt.Fprintf(writer, `{"code":"success","data":"suno-%d"}`, len(fetched)+int(time.Now().UnixNano()%1000))
		case "/suno/fetch":
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(request.Body).Decode(&body)
			fetched = append(fetched, body.IDs)
			var items []string
			for _, id := range body.IDs {
				items = append(items, fmt.Sprintf(`{"task_id":%q,"status":"SUCCESS","action":"MUSIC","data":[{"audio_url":"https://cdn.example/%s.mp3"}]}`, id, id))
			}
			_, _ = fmt.Fprintf(writer, `{"code":"success","data":[%s]}`, strings.Join(items, ","))
		default:
			http.NotFound(writer, request)
		}
	})
	h := newHarness(t, handler, "sunoapi")
	service := h.provider("sunoapi", "sk-suno", "suno_music")
	ctx := context.Background()
	var ids []string
	for index := range 2 {
		request := Request{Method: http.MethodPost, Path: "/suno/submit/music", Params: map[string]string{"action": "music"}, Body: jsonBody(fmt.Sprintf(`{"prompt":"song %d","mv":"chirp-v3-5"}`, index))}
		intent, err := h.engine.Decode(ctx, "sunoapi", "native.decodeSubmit", request)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		done, err := h.engine.Submit(ctx, Submission{PluginKey: "sunoapi", Surface: "native", Operation: request.Path, Intent: intent, Owner: "tok", Retain: true}, []Candidate{{Service: service}})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		ids = append(ids, done.Task.ID)
	}
	h.advance(10 * time.Second)
	h.engine.pollDue(ctx)
	if len(fetched) != 1 || len(fetched[0]) != 2 {
		t.Fatalf("the vendor was asked %v", fetched)
	}
	for _, id := range ids {
		task, err := h.engine.Task(ctx, id, "tok")
		if err != nil || task.Status != contract.PluginTaskSuccess || !strings.Contains(string(task.Data), "audio_url") {
			t.Fatalf("task %s = %+v, %v", id, task, err)
		}
	}
}

func TestVertexExchangesItsServiceAccountAndKeepsTheInlineVideo(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(private)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	var exchanges int
	var mutex sync.Mutex
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if request.URL.Path == "/token" {
			exchanges++
			_ = request.ParseForm()
			if request.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || strings.Count(request.Form.Get("assertion"), ".") != 2 {
				http.Error(writer, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `{"access_token":"ya29.token","expires_in":3600,"token_type":"Bearer"}`)
			return
		}
		if request.Header.Get("Authorization") != "Bearer ya29.token" || request.Header.Get("X-Goog-User-Project") != "my-project" {
			http.Error(writer, `{"error":{"message":"unauthenticated"}}`, http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(request.URL.Path, ":predictLongRunning"):
			_, _ = io.WriteString(writer, `{"name":"projects/my-project/locations/global/publishers/google/models/veo-3.0-generate-001/operations/op-1"}`)
		case strings.HasSuffix(request.URL.Path, ":fetchPredictOperation"):
			_, _ = fmt.Fprintf(writer, `{"name":"projects/my-project/locations/global/publishers/google/models/veo-3.0-generate-001/operations/op-1","done":true,"response":{"videos":[{"bytesBase64Encoded":%q,"mimeType":"video/mp4"}]}}`, base64.StdEncoding.EncodeToString([]byte("VERTEXMP4")))
		default:
			http.NotFound(writer, request)
		}
	})
	h := newHarness(t, handler, "vertex-ai")
	account, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "my-project", "private_key_id": "k1", "private_key": pemKey,
		"client_email": "veo@my-project.iam.gserviceaccount.com", "token_uri": h.vendor.URL + "/token",
	})
	service := h.provider("vertex-ai", string(account), "veo-3.0-generate-001")
	ctx := context.Background()
	request := Request{Method: http.MethodPost, Path: "/v1/videos", Body: jsonBody(`{"model":"veo-3.0-generate-001","prompt":"waves","seconds":8}`), Protocol: plugin.TaskProtocolVideo, Operation: "create", Model: "veo-3.0-generate-001"}
	intent, err := h.engine.Decode(ctx, "vertex-ai", "protocols.openai_video.decodeRequest", request)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	done, err := h.engine.Submit(ctx, Submission{PluginKey: "vertex-ai", Surface: plugin.TaskProtocolVideo, Operation: "create", Intent: intent, Owner: "tok", Retain: true}, []Candidate{{Service: service}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	h.advance(10 * time.Second)
	task, err := h.engine.Poll(ctx, done.Task.ID)
	if err != nil || task.Status != contract.PluginTaskSuccess {
		t.Fatalf("poll = %+v, %v", task, err)
	}
	if exchanges != 1 {
		t.Fatalf("the service account was exchanged %d times, want once", exchanges)
	}
	// Since 1.2.0 the plugin lists the inline video and fetches it again
	// from the operation, its bytes at a contentPath of the reply.
	artifacts, err := h.engine.Artifacts(ctx, task)
	if err != nil || len(artifacts) != 1 || artifacts[0].Key != "video" || artifacts[0].Type != "video" {
		t.Fatalf("artifacts = %+v, %v", artifacts, err)
	}
	recorder := httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.ID+"/content", nil)
	get.Header.Set("Range", "bytes=0-5")
	if err := h.engine.ServeContent(ctx, recorder, get, task, "video"); err != nil {
		t.Fatalf("ServeContent: %v", err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "VERTEXMP4" || recorder.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("content = %d %q %v", recorder.Code, recorder.Body.String(), recorder.Header())
	}
	recorder = httptest.NewRecorder()
	head := httptest.NewRequest(http.MethodHead, "/v1/videos/"+task.ID+"/content", nil)
	if err := h.engine.ServeContent(ctx, recorder, head, task, "video"); err != nil || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Length") != "9" {
		t.Fatalf("HEAD = %d %q %v, %v", recorder.Code, recorder.Body.String(), recorder.Header(), err)
	}
}

func TestPublicDataHidesTheVendorsTaskID(t *testing.T) {
	data := publicData(json.RawMessage(`{"id":"up-1","output":{"task_id":"up-1","note":"up-1 stays"},"list":[{"taskId":"up-1"}]}`), "up-1", "task_public")
	if string(data) != `{"id":"task_public","list":[{"taskId":"task_public"}],"output":{"note":"up-1 stays","task_id":"task_public"}}` {
		t.Fatalf("publicData = %s", data)
	}
}
