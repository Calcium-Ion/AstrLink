package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// Opt in with ASTRLINK_TEST_CODEX_BIN=/absolute/path/to/codex. This runs one
// real Codex turn through the gateway for a Responses Lite model and for a
// regular one, and the model asks for an image and a web search. A Lite
// model reaches both tools from a code-mode exec script, so Codex calls the
// gateway's image and search endpoints. A regular model calls the image tool
// directly, and its hosted web_search goes through the Responses tool
// mapping. With built-in tools off, a turn a Codex subscription served sends
// both tools to that subscription. Every upstream is a local fake, so no
// credentials, user configuration or billable requests are involved.
func TestCodexOwnToolsInstalledClient(t *testing.T) {
	binary := os.Getenv("ASTRLINK_TEST_CODEX_BIN")
	if binary == "" {
		t.Skip("ASTRLINK_TEST_CODEX_BIN is not set")
	}
	for _, test := range []struct {
		model        string
		subscription bool
	}{{"gpt-5.6-sol", false}, {"gpt-5.5", false}, {"gpt-5.6-sol", true}} {
		name := test.model
		if test.subscription {
			name += "/subscription"
		}
		t.Run(name, func(t *testing.T) { runCodexOwnTools(t, binary, test.model, test.subscription) })
	}
}

func runCodexOwnTools(t *testing.T, binary, model string, subscription bool) {
	jpegImage := encodedTestImage(t, "jpeg")
	var imageCalls, searchCalls atomic.Int32
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imageCalls.Add(1)
		_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": jpegImage}}}))
	}))
	defer images.Close()
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls.Add(1)
		_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"results": []any{
			builtintools.Object{"url": "https://example.com/codex-tools", "title": "Codex tools", "content": "Codex can generate images."},
		}}))
	}))
	defer search.Close()

	pngImage := encodedTestImage(t, "png")
	var mu sync.Mutex
	var sawImageTool, actorHeaderLeaked bool
	var imageOutput, searchOutput string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		for name := range r.Header {
			if strings.EqualFold(name, transport.OpenAIActorAuthorizationHeader) {
				actorHeaderLeaked = true
			}
		}
		var body builtintools.Object
		_ = json.Unmarshal(raw, &body)
		// A Codex subscription answers Codex's own tools itself.
		switch r.URL.Path {
		case "/backend-api/codex/images/generations":
			imageCalls.Add(1)
			if r.Header.Get("X-Codex-Image-Turn-Id") == "" || body["model"] != "gpt-image-2" {
				t.Errorf("subscription image request = %v %v", r.Header, body)
			}
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"created": 1, "data": []any{builtintools.Object{"b64_json": pngImage}}}))
			return
		case "/backend-api/codex/alpha/search":
			searchCalls.Add(1)
			if body["model"] != model || builtintools.Map(body["commands"]) == nil {
				t.Errorf("subscription search body = %v", body)
			}
			_, _ = io.WriteString(w, `{"encrypted_output":null,"output":"[turn0search0] Codex tools https://example.com/codex-tools"}`)
			return
		}
		outputs := map[string]string{}
		for _, item := range builtintools.Array(body["input"]) {
			if entry := builtintools.Map(item); entry["type"] == "function_call_output" || entry["type"] == "custom_tool_call_output" {
				outputs[builtintools.String(entry["call_id"])] = builtintools.Text(entry["output"])
			}
		}
		// A regular model's hosted web_search arrives renamed by the gateway.
		var mappedSearch string
		for _, tool := range builtintools.Array(body["tools"]) {
			if name := builtintools.String(builtintools.Map(tool)["name"]); strings.HasPrefix(name, "tool_") {
				mappedSearch = name
			}
		}
		reply := builtinMessage("done")
		switch {
		case outputs["call_exec"] != "":
			imageOutput, searchOutput = outputs["call_exec"], outputs["call_exec"]
		case outputs["call_mapped"] != "":
			searchOutput = outputs["call_mapped"]
		case outputs["call_image"] != "":
			imageOutput = outputs["call_image"]
			reply = codexFunctionCall("", mappedSearch, "call_mapped", builtintools.Object{"action": "search", "query": "codex image tools"})
		case body["tools"] == nil && strings.Contains(string(raw), "image_gen__imagegen") && strings.Contains(string(raw), "web__run"):
			// Responses Lite lists its tools in an input item and runs
			// nested tools from exec.
			sawImageTool = true
			reply = builtintools.Object{"type": "custom_tool_call", "id": builtintools.ID("ctc_"), "name": "exec", "call_id": "call_exec", "status": "completed",
				"input": "generatedImage(await tools.image_gen__imagegen({ prompt: \"a red fox\" }));\ntext(await tools.web__run({ search_query: [{ q: \"codex image tools\" }] }));\n"}
		case strings.Contains(string(raw), `"imagegen"`) && mappedSearch != "":
			sawImageTool = true
			reply = codexFunctionCall("image_gen", "imagegen", "call_image", builtintools.Object{"prompt": "a red fox"})
		}
		writeResponsesSSE(w, builtintools.String(body["model"]), reply)
	}))
	defer upstream.Close()

	main := wsCandidate(upstream.URL)
	main.Service.ResponsesWebSocketEnabled = nil
	main.Service.Models = []string{model}
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = &contract.BuiltinTools{
		ImageGeneration: contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: images.URL + "/v1", Model: "image-model"},
		WebSearch:       contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: search.URL},
	}
	if subscription {
		main = codexToolSubscription(upstream.URL)
		main.Service.Models = []string{model}
		store.settings.BuiltinTools = &contract.BuiltinTools{}
	}
	gateway := httptest.NewServer(NewWithDependencies(Dependencies{
		Resolver:         codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{main}}},
		Authorizer:       endpoint.NewServiceAuthorizer(nil, codingPlanCredentials{}, accountauth.CodexIdentityPolicy{}),
		RequestRecords:   store,
		ProxyCredentials: builtinToolSecrets{},
	}))
	defer gateway.Close()

	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	work := filepath.Join(home, "work")
	for _, dir := range []string{codexHome, work} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The provider table the desktop app writes, including the tools header.
	config := fmt.Sprintf(`model_provider = "astrlink"
model = %q
approval_policy = "never"
sandbox_mode = "read-only"

[model_providers.astrlink]
name = "AstrLink"
base_url = %q
wire_api = "responses"
experimental_bearer_token = "local-test-token"
http_headers = { x-openai-actor-authorization = "codex-imagegen" }

[analytics]
enabled = false
`, model, gateway.URL+"/v1")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "exec", "--skip-git-repo-check", "-C", work, "Draw a red fox, then look up Codex image tools on the web.")
	// Only what the test sets: the parent session's variables must not leak in.
	command.Env = []string{"HOME=" + home, "CODEX_HOME=" + codexHome, "PATH=/usr/bin:/bin", "TERM=dumb"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	if t.Failed() || runErr != nil {
		t.Logf("Codex stdout: %s\nCodex stderr: %s", stdout.String(), stderr.String())
	}
	if runErr != nil {
		t.Fatal(runErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawImageTool || imageCalls.Load() != 1 {
		t.Fatalf("image tool offered=%v gateway image calls=%d", sawImageTool, imageCalls.Load())
	}
	if !strings.Contains(imageOutput, "data:image/png;base64,") {
		t.Fatalf("the generated image did not return to the model: %.300s", imageOutput)
	}
	if searchCalls.Load() != 1 || !strings.Contains(searchOutput, "https://example.com/codex-tools") {
		t.Fatalf("search calls=%d output=%.300s", searchCalls.Load(), searchOutput)
	}
	if actorHeaderLeaked {
		t.Fatal("the Codex tools header reached the upstream provider")
	}
	var saved []string
	_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".png") && strings.Contains(path, "generated_images") {
			data, readErr := os.ReadFile(path)
			if readErr == nil && bytes.HasPrefix(data, pngSignature) {
				saved = append(saved, path)
			}
		}
		return nil
	})
	if len(saved) != 1 {
		t.Fatalf("saved PNG images = %v", saved)
	}
	protocols := map[contract.ProtocolID]int{}
	for _, record := range store.snapshot() {
		protocols[record.InputProtocol]++
	}
	t.Logf("Codex %s: image saved at %s; gateway records by protocol: %v", model, saved[0], protocols)
	lite := model == "gpt-5.6-sol"
	if protocols[contract.ProtocolOpenAIImages] != 1 || (protocols[contract.ProtocolOpenAISearch] == 1) != lite {
		t.Fatalf("records = %v", protocols)
	}
	for _, record := range store.snapshot() {
		if record.Status != contract.RequestStatusSucceeded {
			t.Fatalf("%s record = %s %+v", record.InputProtocol, record.Status, record.Error)
		}
	}
}

func codexFunctionCall(namespace, name, callID string, arguments builtintools.Object) builtintools.Object {
	item := builtintools.Object{"type": "function_call", "id": builtintools.ID("fc_"), "name": name, "call_id": callID, "arguments": builtintools.Text(arguments), "status": "completed"}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return item
}

func writeResponsesSSE(w http.ResponseWriter, model string, output ...builtintools.Object) {
	w.Header().Set("Content-Type", "text/event-stream")
	id := builtintools.ID("resp_")
	items := make([]any, 0, len(output))
	for _, item := range output {
		items = append(items, item)
	}
	write := func(event builtintools.Object) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], builtintools.Text(event))
	}
	write(builtintools.Object{"type": "response.created", "response": builtintools.Object{"id": id, "object": "response", "model": model, "status": "in_progress", "output": []any{}}})
	for index, item := range items {
		write(builtintools.Object{"type": "response.output_item.done", "output_index": index, "item": item})
	}
	write(builtintools.Object{"type": "response.completed", "response": builtintools.Object{"id": id, "object": "response", "model": model, "status": "completed", "output": items, "usage": builtintools.Object{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}}})
}
