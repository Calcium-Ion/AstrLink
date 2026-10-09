package ingress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/builtintools"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

func TestMiniMaxServiceImagesUseImageGeneration(t *testing.T) {
	jpeg := base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff\xe0minimax"))
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nreference"))
	answer := `{"id":"task","data":{"image_base64":["` + jpeg + `"]},"metadata":{"success_count":"1","failed_count":"0"},"base_resp":{"status_code":0,"status_msg":"success"}}`
	service := contract.ServiceFromEndpoint(validEndpoint(contract.ProtocolOpenAIResponses, false))
	service.Kind = contract.ServiceKindMiniMaxCoding
	// A provider saved with MiniMax's Anthropic root still reaches /v1.
	service.HTTP.BaseURL = "https://api.minimaxi.com/anthropic"
	var bodies []builtintools.Object
	forwarder := transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://api.minimaxi.com/v1/image_generation" {
			t.Errorf("upstream request = %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer plan-key" || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("User-Agent") != "" {
			t.Errorf("upstream headers = %v", request.Header)
		}
		var body builtintools.Object
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("upstream body: %v", err)
		}
		bodies = append(bodies, body)
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(answer)),
		}, nil
	}))
	handler := NewWithDependencies(Dependencies{
		Resolver: serviceImagesResolver{service: endpoint.Resolved{Service: service, BaseURL: service.HTTP.BaseURL}},
		Authorizer: authorizerFunc(func(context.Context, contract.Endpoint) (http.Header, error) {
			return http.Header{"Authorization": {"Bearer plan-key"}}, nil
		}),
		Forwarder: forwarder,
	})
	executor := handler.builtinExecutor(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Request{Protocol: contract.ProtocolOpenAIResponses})
	executor.Inspect = nil
	config := contract.BuiltinTool{Enabled: true, Backend: "service_images", ServiceID: service.ID, Model: "image-01"}
	call := builtintools.Invocation{
		Kind: "image_generation", Arguments: builtintools.Object{"prompt": "a red fox"},
		Options: builtintools.Object{"type": "image_generation", "size": "1536x1024", "quality": "high"},
	}

	result, err := executor.Execute(context.Background(), config, call)
	if err != nil || len(result.Items) != 1 || result.Items[0]["output_format"] != "jpeg" || !strings.HasPrefix(result.Images[0], "data:image/jpeg;base64,") {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if got := fmt.Sprint(bodies[0]); got != "map[height:1024 model:image-01 n:1 prompt:a red fox response_format:base64 width:1536]" {
		t.Fatalf("generation body = %s", got)
	}

	config.Model = "image-01-live"
	call.Images = []string{"data:image/png;base64," + png}
	if _, err := executor.Execute(context.Background(), config, call); err != nil {
		t.Fatal(err)
	}
	edit := bodies[1]
	references := builtintools.Array(edit["subject_reference"])
	if edit["model"] != "image-01-live" || edit["aspect_ratio"] != "3:2" || edit["width"] != nil || len(references) != 1 ||
		builtintools.Map(references[0])["type"] != "character" || builtintools.Map(references[0])["image_file"] != "data:image/png;base64,"+png {
		t.Fatalf("edit body = %v", edit)
	}

	call.Options = builtintools.Object{"type": "image_generation", "input_image_mask": builtintools.Object{"image_url": "data:image/png;base64," + png}}
	if _, err := executor.Execute(context.Background(), config, call); err == nil || !strings.Contains(err.Error(), "masks") || len(bodies) != 2 {
		t.Fatalf("mask err = %v, upstream calls = %d", err, len(bodies))
	}
}

func TestMiniMaxImageSize(t *testing.T) {
	for _, test := range []struct{ model, size, want string }{
		{"image-01", "auto", ""},
		{"image-01", "1024x1536", "1024x1536"},
		{"image-01", "4096x4096", "1:1"},
		{"image-01", "2688x1152", "21:9"},
		{"image-01-live", "1024x1024", "1:1"},
		{"image-01-live", "1536x1024", "3:2"},
		{"image-01-live", "1024x1792", "9:16"},
		{"image-01-live", "1792x1024", "16:9"},
		{"image-01-live", "2688x1152", "16:9"},
	} {
		request := builtintools.Object{}
		if err := minimaxImageSize(request, test.model, test.size); err != nil {
			t.Fatalf("%s %s: %v", test.model, test.size, err)
		}
		got := builtintools.String(request["aspect_ratio"])
		if request["width"] != nil {
			got = fmt.Sprintf("%dx%d", request["width"], request["height"])
		}
		if got != test.want {
			t.Errorf("%s %s = %q, want %q", test.model, test.size, got, test.want)
		}
	}
	if err := minimaxImageSize(builtintools.Object{}, "image-01", "large"); err == nil {
		t.Fatal("invalid size accepted")
	}
}

func TestMiniMaxImageFailures(t *testing.T) {
	_, err := minimaxImageRequest("/images/generations", "application/json", strings.NewReader(`{"model":"image-01","prompt":"logo","background":"transparent"}`))
	if err == nil || !strings.Contains(err.Error(), "transparent") {
		t.Fatalf("transparent background err = %v", err)
	}
	for _, test := range []struct{ body, want string }{
		{`{"base_resp":{"status_code":1026,"status_msg":"echoed prompt"}}`, "status 1026: the prompt was flagged as sensitive"},
		{`{"base_resp":{"status_code":2056,"status_msg":"echoed prompt"}}`, "status 2056: the plan's usage limit"},
		{`{"base_resp":{"status_code":1999,"status_msg":"echoed prompt"}}`, "status 1999"},
		{`{"data":{"image_base64":[]},"metadata":{"failed_count":1},"base_resp":{"status_code":0}}`, "content check withheld"},
		{`{"data":{"image_base64":["x"]}}`, "invalid image provider response"},
	} {
		_, err := minimaxImageResponse([]byte(test.body))
		if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "echoed prompt") {
			t.Errorf("%s: err = %v", test.body, err)
		}
	}
}

func TestCodexImageFallbackDrawsWithMiniMaxImageModel(t *testing.T) {
	jpegImage := encodedTestImage(t, "jpeg")
	for _, test := range []struct {
		name      string
		redirects []contract.ModelRedirect
		want      string
	}{
		{name: "default", want: "image-01"},
		{name: "redirect", redirects: []contract.ModelRedirect{{From: "gpt-image-2", To: "image-01-live", Enabled: true}}, want: "image-01-live"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var models []any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body builtintools.Object
				_ = json.NewDecoder(r.Body).Decode(&body)
				switch r.URL.Path {
				case "/v1/responses":
					writeResponsesSSE(w, builtintools.String(body["model"]), builtinMessage("ok"))
				case "/v1/image_generation":
					mu.Lock()
					models = append(models, body["model"])
					mu.Unlock()
					_, _ = io.WriteString(w, `{"data":{"image_base64":["`+jpegImage+`"]},"base_resp":{"status_code":0}}`)
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			minimax := wsCandidate(server.URL)
			minimax.Service.ID = "service_minimax"
			minimax.Service.Kind = contract.ServiceKindMiniMaxCoding
			minimax.Service.ResponsesWebSocketEnabled = nil
			minimax.Service.Models = []string{"gpt-5.6-sol"}
			minimax.Service.HTTP.Auth = contract.ServiceAuth{Scheme: contract.AuthSchemeBearer}
			minimax.Service.HTTP.CredentialRef = "local://service/service_minimax"
			store := newRedirectSettingsStore(test.redirects...)
			store.settings.BuiltinTools = &contract.BuiltinTools{}
			handler := NewWithDependencies(Dependencies{
				Resolver:       codexToolResolver{candidateResolver{candidates: []endpoint.Resolved{minimax}}},
				Authorizer:     endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
				RequestRecords: store,
			})
			codexTurn(t, handler, true)
			image := codexClientRequest("/v1/images/generations", `{"prompt":"a red fox","model":"gpt-image-2"}`, true)
			image.Header.Set(codexImageTurnHeader, codexTestTurn)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, image)
			mu.Lock()
			defer mu.Unlock()
			if response.Code != http.StatusOK || fmt.Sprint(models) != "["+test.want+"]" {
				t.Fatalf("image = %d %s, MiniMax models = %v", response.Code, response.Body.String(), models)
			}
		})
	}
}
