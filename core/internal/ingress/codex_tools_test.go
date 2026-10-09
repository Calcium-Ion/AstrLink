package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type builtinToolSecrets struct{}

func (builtinToolSecrets) Get(context.Context, secretstore.Ref) ([]byte, error) {
	return []byte("tool-key"), nil
}
func (builtinToolSecrets) Put(context.Context, secretstore.Ref, []byte) error { return nil }
func (builtinToolSecrets) Delete(context.Context, secretstore.Ref) error      { return nil }

// serveCodexTool sends what Codex sends for its own image and search tools,
// including the header that switches those tools on.
func serveCodexTool(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Openai-Actor-Authorization", "codex-imagegen")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func encodedTestImage(t *testing.T, format string) string {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buffer bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&buffer, canvas, nil)
	} else {
		err = png.Encode(&buffer, canvas)
	}
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func imageToolSettings(baseURL string) *contract.BuiltinTools {
	return &contract.BuiltinTools{ImageGeneration: contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: baseURL + "/v1", Model: "image-model"}}
}

func TestCodexImageRequestsUseBuiltinImageGeneration(t *testing.T) {
	jpegImage := encodedTestImage(t, "jpeg")
	pngImage := encodedTestImage(t, "png")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tool-key" || r.Header.Get(transport.OpenAIActorAuthorizationHeader) != "" {
			t.Errorf("image backend headers = %v", r.Header)
		}
		switch r.URL.Path {
		case "/v1/images/generations":
			var body builtintools.Object
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "image-model" || body["prompt"] != "a red fox" || body["background"] != "transparent" || body["size"] != nil || body["quality"] != nil {
				t.Errorf("generation body = %v", body)
			}
			// A JPEG answer must still reach Codex as PNG.
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": jpegImage, "revised_prompt": "a red fox in studio light"}}, "usage": builtintools.Object{"total_tokens": 9}}))
		case "/v1/images/edits":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("model") != "image-model" || r.FormValue("prompt") != "make it blue" || r.FormValue("size") != "1024x1024" || len(r.MultipartForm.File["image[]"]) != 1 {
				t.Errorf("edit form = %v files=%v", r.MultipartForm.Value, r.MultipartForm.File)
			}
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": pngImage}}}))
		default:
			t.Errorf("image backend path = %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = imageToolSettings(backend.URL)
	handler := NewWithDependencies(Dependencies{RequestRecords: store, ProxyCredentials: builtinToolSecrets{}})

	for _, test := range []struct{ path, body, revised string }{
		{"/v1/images/generations", `{"prompt":"a red fox","background":"transparent","model":"gpt-image-2","quality":"auto","size":"auto"}`, "a red fox in studio light"},
		{"/v1/images/edits", `{"images":[{"image_url":"data:image/png;base64,` + pngImage + `"}],"prompt":"make it blue","background":"opaque","model":"gpt-image-2","size":"1024x1024"}`, ""},
	} {
		response := serveCodexTool(handler, test.path, test.body)
		if response.Code != http.StatusOK {
			t.Fatal(test.path, response.Code, response.Body.String())
		}
		var answer struct {
			Created int64 `json:"created"`
			Data    []struct {
				B64JSON       string `json:"b64_json"`
				RevisedPrompt string `json:"revised_prompt"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.Created <= 0 || len(answer.Data) != 1 {
			t.Fatal(test.path, err, response.Body.String())
		}
		data, err := base64.StdEncoding.DecodeString(answer.Data[0].B64JSON)
		if err != nil || !bytes.HasPrefix(data, pngSignature) || answer.Data[0].RevisedPrompt != test.revised {
			t.Fatalf("%s answer is not the PNG Codex saves: %v %q", test.path, err, answer.Data[0].RevisedPrompt)
		}
		if config, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width != 4 || config.Height != 3 {
			t.Fatalf("%s PNG = %+v %v", test.path, config, err)
		}
	}
	records := store.snapshot()
	if len(records) != 2 {
		t.Fatalf("records = %d", len(records))
	}
	for _, record := range records {
		if record.InputProtocol != contract.ProtocolOpenAIImages || record.Status != contract.RequestStatusSucceeded {
			t.Fatalf("record = %s %s", record.InputProtocol, record.Status)
		}
		if record.ModelRedirect == nil || record.ModelRedirect.From != "gpt-image-2" || record.ModelRedirect.To != "image-model" {
			t.Fatalf("record does not show the configured image model: %+v", record.ModelRedirect)
		}
		var summaries []string
		for _, event := range record.Events {
			summaries = append(summaries, event.Summary)
		}
		if !strings.Contains(strings.Join(summaries, "\n"), "image_generation · external") {
			t.Fatalf("record events = %q", summaries)
		}
	}
	if records[0].InputPreview == nil || *records[0].InputPreview != "a red fox" {
		t.Fatalf("record preview = %v", records[0].InputPreview)
	}
}

func TestCodexImageRequestFailuresAreNotRetryable(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	for _, test := range []struct {
		name, path, body, code string
		settings               *contract.BuiltinTools
		status                 int
		calls                  int32
	}{
		{name: "turned off", path: "/v1/images/generations", body: `{"prompt":"fox"}`, settings: &contract.BuiltinTools{}, status: http.StatusUnprocessableEntity, code: "image_generation_disabled"},
		{name: "backend failure", path: "/v1/images/generations", body: `{"prompt":"fox"}`, settings: imageToolSettings(backend.URL), status: http.StatusFailedDependency, code: "image_generation_failed", calls: 1},
		{name: "several images", path: "/v1/images/generations", body: `{"prompt":"fox","n":2}`, settings: imageToolSettings(backend.URL), status: http.StatusBadRequest, code: "invalid_request"},
		{name: "uploaded file", path: "/v1/images/edits", body: `{"prompt":"fox","images":[{"file_id":"file_1"}]}`, settings: imageToolSettings(backend.URL), status: http.StatusBadRequest, code: "invalid_request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls.Store(0)
			store := newRedirectSettingsStore()
			store.settings.BuiltinTools = test.settings
			handler := NewWithDependencies(Dependencies{RequestRecords: store, ProxyCredentials: builtinToolSecrets{}})
			response := serveCodexTool(handler, test.path, test.body)
			// Codex retries every 5xx, and each retry can bill another image.
			assertInferenceError(t, response, test.status, test.code)
			if calls.Load() != test.calls {
				t.Fatalf("backend calls = %d", calls.Load())
			}
			records := store.snapshot()
			if len(records) != 1 || records[0].Status != contract.RequestStatusFailed || records[0].Error == nil || records[0].Error.Code != test.code {
				t.Fatalf("records = %+v", records)
			}
		})
	}
}

func TestCodexImagePromptPassesPrivacyPolicy(t *testing.T) {
	for _, action := range []privacy.Action{privacy.ActionRedact, privacy.ActionBlock} {
		t.Run(string(action), func(t *testing.T) {
			var calls atomic.Int32
			pngImage := encodedTestImage(t, "png")
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body builtintools.Object
				_ = json.NewDecoder(r.Body).Decode(&body)
				prompt := builtintools.String(body["prompt"])
				if strings.Contains(prompt, "alice@example.com") || !emailPlaceholderPattern.MatchString(prompt) {
					t.Errorf("image prompt = %q", prompt)
				}
				_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"data": []any{builtintools.Object{"b64_json": pngImage}}}))
			}))
			defer backend.Close()
			store := newRedirectSettingsStore()
			store.settings.BuiltinTools = imageToolSettings(backend.URL)
			handler := NewWithDependencies(Dependencies{
				RequestRecords: store, ProxyCredentials: builtinToolSecrets{},
				PrivacyFilter: testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: action, ResponseRestore: true}, nil),
			})
			response := serveCodexTool(handler, "/v1/images/generations", `{"prompt":"a badge that reads alice@example.com","model":"gpt-image-2"}`)
			if action == privacy.ActionBlock {
				assertInferenceError(t, response, http.StatusForbidden, "policy_blocked")
				if calls.Load() != 0 {
					t.Fatal("blocked prompt reached the image backend")
				}
				return
			}
			if response.Code != http.StatusOK || calls.Load() != 1 {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}

func searchToolSettings(baseURL string) *contract.BuiltinTools {
	return &contract.BuiltinTools{WebSearch: contract.BuiltinTool{Enabled: true, Backend: "external", BaseURL: baseURL}}
}

func searchOutput(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var answer map[string]json.RawMessage
	var output string
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || string(answer["encrypted_output"]) != "null" || json.Unmarshal(answer["output"], &output) != nil {
		t.Fatalf("search answer = %s (%v)", response.Body.String(), err)
	}
	return output
}

func TestCodexSearchUsesBuiltinWebSearch(t *testing.T) {
	var searches, extracts atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body builtintools.Object
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/search":
			searches.Add(1)
			if body["query"] != "go release notes" || fmt.Sprint(body["include_domains"]) != "[go.dev]" {
				t.Errorf("search body = %v", body)
			}
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"results": []any{
				builtintools.Object{"url": "https://go.dev/doc/devel/release", "title": "Release History", "content": "Go 1.26 was released."},
			}}))
		case "/extract":
			extracts.Add(1)
			if fmt.Sprint(body["urls"]) != "[https://go.dev/doc/devel/release]" {
				t.Errorf("extract body = %v", body)
			}
			_, _ = io.WriteString(w, builtintools.Text(builtintools.Object{"results": []any{
				builtintools.Object{"url": "https://go.dev/doc/devel/release", "title": "Release History", "raw_content": "Release History\nGo 1.26 adds the new feature.\nOlder releases"},
			}}))
		default:
			t.Errorf("search backend path = %s", r.URL.Path)
		}
	}))
	defer backend.Close()
	store := newRedirectSettingsStore()
	store.settings.BuiltinTools = searchToolSettings(backend.URL)
	handler := NewWithDependencies(Dependencies{RequestRecords: store, ProxyCredentials: builtinToolSecrets{}})

	// Codex's default cached mode still searches through a search API.
	output := searchOutput(t, serveCodexTool(handler, "/v1/alpha/search", `{"id":"session-1","model":"gpt-5.6-sol","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"never forwarded"}]}],"commands":{"search_query":[{"q":"go release notes","domains":["go.dev"]}]},"settings":{"external_web_access":false},"max_output_tokens":10000}`))
	if !strings.Contains(output, "[turn0search0] Release History\nhttps://go.dev/doc/devel/release\nGo 1.26 was released.") {
		t.Fatalf("search output = %q", output)
	}
	output = searchOutput(t, serveCodexTool(handler, "/v1/alpha/search", `{"id":"session-1","model":"gpt-5.6-sol","commands":{"open":[{"ref_id":"turn0search0"}],"find":[{"ref_id":"turn0search0","pattern":"new feature"}],"weather":[{"location":"Paris"}]}}`))
	for _, want := range []string{"[turn1view0] Release History", "Go 1.26 adds the new feature.", `Matches for "new feature" in https://go.dev/doc/devel/release`, "not available here: weather"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q: %q", want, output)
		}
	}
	// Reference IDs belong to the Codex session that received them.
	output = searchOutput(t, serveCodexTool(handler, "/v1/alpha/search", `{"id":"session-2","model":"gpt-5.6-sol","commands":{"open":[{"ref_id":"turn0search0"}]}}`))
	if !strings.Contains(output, "unknown reference ID") || searches.Load() != 1 || extracts.Load() != 2 {
		t.Fatalf("output = %q searches=%d extracts=%d", output, searches.Load(), extracts.Load())
	}
	records := store.snapshot()
	if len(records) != 3 || records[0].InputProtocol != contract.ProtocolOpenAISearch || records[0].Status != contract.RequestStatusSucceeded {
		t.Fatalf("records = %+v", records)
	}
	if records[0].InputPreview == nil || *records[0].InputPreview != "go release notes" {
		t.Fatalf("record preview = %v", records[0].InputPreview)
	}
}

func TestCodexSearchAnswersEveryOutcomeWithText(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()
	query := `{"id":"session","model":"gpt-5.6-sol","commands":{"search_query":[{"q":"alice@example.com"}]}}`
	for _, test := range []struct {
		name     string
		settings *contract.BuiltinTools
		action   privacy.Action
		want     string
		status   contract.RequestStatus
		calls    int32
	}{
		{name: "turned off", settings: &contract.BuiltinTools{}, want: "Web search is turned off", status: contract.RequestStatusFailed},
		{name: "backend failure", settings: searchToolSettings(backend.URL), want: "failed: tool API returned HTTP 500", status: contract.RequestStatusFailed, calls: 1},
		{name: "privacy block", settings: searchToolSettings(backend.URL), action: privacy.ActionBlock, want: "privacy policy blocked this search", status: contract.RequestStatusBlocked},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls.Store(0)
			store := newRedirectSettingsStore()
			store.settings.BuiltinTools = test.settings
			dependencies := Dependencies{RequestRecords: store, ProxyCredentials: builtinToolSecrets{}}
			if test.action != "" {
				dependencies.PrivacyFilter = testPrivacyEngine(t, privacy.Policy{Enabled: true, Mode: privacy.ModeRegex, Action: test.action}, nil)
			}
			// Codex ends the whole turn when this request fails.
			output := searchOutput(t, serveCodexTool(NewWithDependencies(dependencies), "/v1/alpha/search", query))
			if !strings.Contains(output, test.want) || calls.Load() != test.calls {
				t.Fatalf("output = %q calls=%d", output, calls.Load())
			}
			records := store.snapshot()
			if len(records) != 1 || records[0].Status != test.status {
				t.Fatalf("records = %+v", records)
			}
		})
	}
}
