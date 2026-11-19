package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// hailuo serves MiniMax's own paths and the same under /hailuo; its v1
// queries read the task id from the query string, and /v1/files/retrieve
// answers with the gateway's download link of the video.
func TestHailuoServesMiniMaxPathsAndTheFileRetrieve(t *testing.T) {
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/video_generation":
			_, _ = io.WriteString(writer, `{"task_id":"up-1","base_resp":{"status_code":0,"status_msg":"success"}}`)
		case request.URL.Path == "/v1/query/video_generation" && request.URL.Query().Get("task_id") == "up-1":
			_, _ = io.WriteString(writer, `{"task_id":"up-1","status":"Success","file_id":"file-9","base_resp":{"status_code":0,"status_msg":"success"}}`)
		default:
			http.NotFound(writer, request)
		}
	})
	gateway := newTaskGatewayOf(t, vendor, func(provider taskProvider) taskServices {
		return taskServices{provider("hailuo", []string{"MiniMax-Hailuo-02"})}
	})
	body := `{"model":"MiniMax-Hailuo-02","prompt":"waves at night","duration":6,"resolution":"768P"}`
	var ids []string
	for _, path := range []string{"/v1/video_generation", "/hailuo/v1/video_generation"} {
		submitted := gateway.do(t, http.MethodPost, path, body)
		var answer struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(submitted.Body.Bytes(), &answer); err != nil || submitted.Code != http.StatusOK || !strings.HasPrefix(answer.TaskID, "task_") {
			t.Fatalf("POST %s = %d %s", path, submitted.Code, submitted.Body)
		}
		ids = append(ids, answer.TaskID)
	}
	id := ids[0]
	if _, err := gateway.engine.Poll(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	queried := gateway.do(t, http.MethodGet, "/v1/query/video_generation?task_id="+id, "")
	if queried.Code != http.StatusOK || !strings.Contains(queried.Body.String(), `"status":"Success"`) || !strings.Contains(queried.Body.String(), `"file_id":"`+id+`"`) || strings.Contains(queried.Body.String(), "up-1") {
		t.Fatalf("query = %d %s", queried.Code, queried.Body)
	}
	retrieved := gateway.do(t, http.MethodGet, "/v1/files/retrieve?file_id="+id, "")
	var file struct {
		File struct {
			FileID      string `json:"file_id"`
			DownloadURL string `json:"download_url"`
		} `json:"file"`
	}
	if err := json.Unmarshal(retrieved.Body.Bytes(), &file); err != nil || retrieved.Code != http.StatusOK || file.File.FileID != id ||
		!strings.Contains(file.File.DownloadURL, "/v1/tasks/"+id+"/artifacts/video/content?access=") {
		t.Fatalf("retrieve = %d %s", retrieved.Code, retrieved.Body)
	}
	// A task still running has no video: its file is not found.
	pending := gateway.do(t, http.MethodGet, "/v1/files/retrieve?file_id="+ids[1], "")
	if pending.Code != http.StatusOK || !strings.Contains(pending.Body.String(), `"status_code":2013`) {
		t.Fatalf("retrieve before the video = %d %s", pending.Code, pending.Body)
	}
}

// xAI's GET /v1/videos/:request_id is the gateway's own path: a task its
// native route took is answered by its query, a pending video with 202, as
// xAI does; the same query also answers under /xai.
func TestXAIAnswersItsVideosAtTheGatewaysVideoPath(t *testing.T) {
	vendor := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/videos/generations":
			_, _ = io.WriteString(writer, `{"request_id":"xreq-1"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/videos/xreq-1":
			_, _ = io.WriteString(writer, `{"status":"pending","progress":40}`)
		default:
			http.NotFound(writer, request)
		}
	})
	gateway := newTaskGatewayOf(t, vendor, func(provider taskProvider) taskServices {
		return taskServices{provider("xai", []string{"grok-imagine-video"},
			contract.Capability{Protocol: contract.ProtocolOpenAIVideos, Mode: contract.CapabilityModeNative})}
	})
	submitted := gateway.do(t, http.MethodPost, "/v1/videos/generations", `{"model":"grok-imagine-video","prompt":"a lighthouse"}`)
	var answer struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &answer); err != nil || submitted.Code != http.StatusOK || !strings.HasPrefix(answer.RequestID, "task_") {
		t.Fatalf("submit = %d %s", submitted.Code, submitted.Body)
	}
	if _, err := gateway.engine.Poll(context.Background(), answer.RequestID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/videos/" + answer.RequestID, "/xai/v1/videos/" + answer.RequestID} {
		status := gateway.do(t, http.MethodGet, path, "")
		if status.Code != http.StatusAccepted || !strings.Contains(status.Body.String(), `"status":"pending"`) || strings.Contains(status.Body.String(), "xreq-1") {
			t.Fatalf("GET %s = %d %s", path, status.Code, status.Body)
		}
	}
	// The gateway's own video routes keep their tasks: an id that is not a
	// task falls to the plugin, which does not know it.
	unknown := gateway.do(t, http.MethodGet, "/v1/videos/not-a-task", "")
	if unknown.Code == http.StatusOK || unknown.Code == http.StatusAccepted {
		t.Fatalf("an unknown id = %d %s", unknown.Code, unknown.Body)
	}
}

func TestGatewayAnswersItsOwnPathsBeforeAVendors(t *testing.T) {
	for _, test := range []struct {
		method, path string
		gateway      bool
	}{
		{http.MethodPost, "/v1/chat/completions", true},
		{http.MethodPost, "/v1beta/models/gemini-3-pro:generateContent", true},
		{http.MethodPost, "/v1beta/models/veo-3.1:predictLongRunning", false},
		{http.MethodPost, "/v1/videos", true},
		{http.MethodPost, "/v1/videos/text2video", false},
		{http.MethodGet, "/v1/videos/text2video/task_0123456789abcdef0123456789abcdef", false},
		{http.MethodGet, "/v1/videos/task_0123456789abcdef0123456789abcdef/content", true},
		{http.MethodGet, "/api/v3/contents/generations/tasks/abc", false},
	} {
		if got := gatewayAnswers(test.method, test.path); got != test.gateway {
			t.Errorf("gatewayAnswers(%s %s) = %v, want %v", test.method, test.path, got, test.gateway)
		}
	}
}
