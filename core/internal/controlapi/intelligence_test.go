package controlapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/intelligence"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

type intelligenceTester struct {
	mu    sync.Mutex
	calls []contract.IntelligenceRequest
	block bool
}

func (t *intelligenceTester) Test(context.Context, contract.Service, contract.ServiceTestRequest) contract.ServiceTestResult {
	return contract.ServiceTestResult{}
}
func (t *intelligenceTester) TestIntelligence(ctx context.Context, _ contract.Service, in contract.IntelligenceRequest) contract.ServiceTestResult {
	t.mu.Lock()
	t.calls = append(t.calls, in)
	block := t.block
	t.mu.Unlock()
	if block {
		<-ctx.Done()
		return contract.ServiceTestResult{Message: ctx.Err().Error()}
	}
	output := "21"
	if strings.Contains(in.Prompt, "SVG") {
		output = `<svg xmlns="http://www.w3.org/2000/svg"><circle r="10"/></svg>`
	}
	if len(in.Images) > 0 {
		output = `{"passed":true,"reason":"鹈鹕正在骑自行车"}`
	}
	return contract.ServiceTestResult{OK: true, Output: output}
}
func (t *intelligenceTester) inputs() []contract.IntelligenceRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]contract.IntelligenceRequest(nil), t.calls...)
}

func TestIntelligenceAPIWorkflow(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/test.db"
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	tester := &intelligenceTester{}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc"), Dependencies{ServiceStore: store, ServiceTester: tester, ControlToken: testControlToken, NewServiceID: func() (contract.ServiceID, error) { return "service_iq", nil }})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, v any) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if v != nil {
			raw, _ := json.Marshal(v)
			body = string(raw)
		}
		return controlRequest(t, handler, method, IntelligencePath+path, "application/json", body, "")
	}
	created := controlRequest(t, handler, "POST", ServicesPath, "application/json", `{"name":"IQ provider","kind":"openai","enabled":true,"http":{"base_url":"https://example.com/v1","auth":{"scheme":"none"}},"models":["deepseek-v4.1-flash"],"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":false}]}`, "")
	if created.Code != 201 {
		t.Fatal(created.Body)
	}
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest("GET", IntelligencePath+"/settings", nil))
	if unauth.Code != 401 {
		t.Fatal("settings must require authentication")
	}
	response := request("POST", "/channels/service_iq/runs", intelligence.StartRequest{Mode: "all"})
	if response.Code != 400 || !strings.Contains(response.Body.String(), "没有指定模型") || len(tester.inputs()) != 0 {
		t.Fatalf("missing model = %d %s, calls=%d", response.Code, response.Body, len(tester.inputs()))
	}
	config := intelligence.ChannelConfig{Model: "deepseek-v4.1-flash", QuestionIDs: []string{"candy"}}
	if response = request("PUT", "/channels/service_iq", config); response.Code != 200 {
		t.Fatal(response.Body)
	}
	var run intelligence.Run
	response = request("POST", "/channels/service_iq/runs", intelligence.StartRequest{Mode: "all"})
	if response.Code != 200 {
		t.Fatal(response.Body)
	}
	decode(t, response, &run)
	wait := func(status string) intelligence.Run {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			v, e := handler.intelligence.Get(ctx, run.ID)
			if e != nil {
				t.Fatal(e)
			}
			if v.Status == status || v.Items[0].Status == status {
				return v
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("run did not reach " + status)
		return intelligence.Run{}
	}
	done := wait("completed")
	if len(done.Items) != 1 || done.Items[0].Status != "passed" || tester.inputs()[0].Model != config.Model {
		t.Fatalf("run=%+v", done)
	}
	var pngBytes bytes.Buffer
	_ = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	picture := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	settings := intelligence.DefaultSettings()
	settings.JudgeServiceID = "service_iq"
	settings.JudgeModel = config.Model
	settings.Questions[0].ReferencePNG = picture
	if response = request("PUT", "/settings", settings); response.Code != 200 {
		t.Fatal(response.Body)
	}
	config.QuestionIDs = []string{"pelican"}
	if response = request("PUT", "/channels/service_iq", config); response.Code != 200 {
		t.Fatal(response.Body)
	}
	decode(t, request("POST", "/channels/service_iq/runs", intelligence.StartRequest{Mode: "all"}), &run)
	wait("rendering")
	input := intelligence.RenderInput{QuestionID: "pelican", PNG: picture}
	if response = request("POST", "/runs/"+run.ID+"/render", input); response.Code != 200 {
		t.Fatal(response.Body)
	}
	if response = request("POST", "/runs/"+run.ID+"/render", input); response.Code != 400 {
		t.Fatal("duplicate render accepted")
	}
	done = wait("completed")
	calls := tester.inputs()
	judge := calls[len(calls)-1]
	if done.Items[0].Status != "passed" || len(judge.Images) != 2 || judge.Images[0] != picture || judge.Images[1] != picture || done.Items[0].PNG != picture {
		t.Fatalf("image workflow failed: %+v", done)
	}
	tester.mu.Lock()
	tester.block = true
	tester.mu.Unlock()
	decode(t, request("POST", "/channels/service_iq/runs", intelligence.StartRequest{Mode: "all"}), &run)
	wait("generating")
	if response = request("POST", "/runs/"+run.ID+"/cancel", nil); response.Code != 200 {
		t.Fatal(response.Body)
	}
	wait("cancelled")
	// Reopening the database checks actual persistence, not an in-memory mock.
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	manager := intelligence.New(store, store, tester)
	saved, e := manager.Channel(ctx, "service_iq")
	if e != nil || saved.Model != config.Model || saved.QuestionIDs[0] != "pelican" {
		t.Fatalf("saved channel=%+v %v", saved, e)
	}
	savedSettings, e := manager.Settings(ctx)
	if e != nil || savedSettings.Questions[0].ReferencePNG != picture {
		t.Fatal("settings not persisted", e)
	}
	history, e := manager.History(ctx, "service_iq")
	if e != nil || len(history) != 3 || history[0].Status != "cancelled" || history[1].Items[0].Output != "" {
		t.Fatalf("history=%+v %v", history, e)
	}
}
