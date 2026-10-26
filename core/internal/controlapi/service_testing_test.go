package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

type recordingServiceTester struct {
	calls   int
	service contract.Service
	input   contract.ServiceTestRequest
}

func (tester *recordingServiceTester) Test(_ context.Context, service contract.Service, input contract.ServiceTestRequest) contract.ServiceTestResult {
	tester.calls++
	tester.service = service
	tester.input = input
	return contract.ServiceTestResult{ServiceID: service.ID, Protocol: input.Protocol, Model: input.Model, Stream: input.Stream, OK: false, StatusCode: 429, DurationMS: 12, ErrorCode: "upstream_error", Message: "Quota exceeded"}
}

type blockingServiceTester struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (tester *blockingServiceTester) Test(ctx context.Context, service contract.Service, input contract.ServiceTestRequest) contract.ServiceTestResult {
	close(tester.started)
	select {
	case <-ctx.Done():
		close(tester.canceled)
	case <-tester.release:
	}
	return contract.ServiceTestResult{
		ServiceID: service.ID,
		Protocol:  input.Protocol,
		Model:     input.Model,
		Stream:    input.Stream,
		ErrorCode: "timeout",
	}
}

func TestServiceTestCancellationFromClientDisconnect(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateService(context.Background(), contract.Service{
		ID: "service_test", Name: "Test provider", Kind: contract.ServiceKindOpenAI, Enabled: true,
		Models: []string{"test-model"},
		HTTP:   &contract.HTTPConnection{BaseURL: "https://example.test", Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeNone}},
		Capabilities: []contract.Capability{{
			Protocol: contract.ProtocolOpenAIChat, Mode: contract.CapabilityModeNative, Streaming: true,
		}},
	}, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	tester := &blockingServiceTester{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc1234"), Dependencies{
		ServiceStore: store, ServiceTester: tester, ControlToken: testControlToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handler.ServeHTTP(writer, request)
		close(handlerDone)
	}))
	defer server.Close()
	defer close(tester.release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+ServicesPath+"/service_test/test", strings.NewReader(`{"protocol":"openai.chat","model":"test-model","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	request.Header.Set("Content-Type", "application/json")
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		response, _ := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-tester.started:
	case <-time.After(2 * time.Second):
		t.Fatal("service test did not reach the tester")
	}
	if ctx.Err() != nil {
		t.Fatal("client context was already canceled before the test began")
	}
	cancel()
	select {
	case <-tester.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("tester context was not canceled after the client disconnected")
	}
	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("control handler did not finish after cancellation")
	}
	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled client request did not return")
	}
}

func TestServiceTestAPI(t *testing.T) {
	store, err := openControlTestStore(t, context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tester := &recordingServiceTester{}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("test", "abc1234"), Dependencies{ServiceStore: store, ServiceTester: tester, ControlToken: testControlToken, NewServiceID: func() (contract.ServiceID, error) { return "service_test", nil }})
	if err != nil {
		t.Fatal(err)
	}
	created := controlRequest(t, handler, http.MethodPost, ServicesPath, "application/json", `{"name":"Test provider","kind":"openai","enabled":false,"http":{"base_url":"https://example.com/v1","auth":{"scheme":"none"}},"models":["configured-model"],"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body)
	}
	before, _ := store.GetService(context.Background(), "service_test")
	path := ServicesPath + "/service_test/test"
	for _, test := range []struct {
		name, method, path, media, body string
		status                          int
	}{
		{"success envelope for upstream failure", "POST", path, "application/json", `{"protocol":"openai.chat","model":"custom-model","stream":true,"prompt":"Hello"}`, 200},
		{"wrong method", "GET", path, "", "", 405},
		{"media", "POST", path, "text/plain", `{}`, 415},
		{"invalid JSON", "POST", path, "application/json", `{`, 400},
		{"unknown field", "POST", path, "application/json", `{"protocol":"openai.chat","model":"test","secret":"bad"}`, 400},
		{"unsupported protocol", "POST", path, "application/json", `{"protocol":"openai.responses","model":"test"}`, 422},
		{"empty model", "POST", path, "application/json", `{"protocol":"openai.chat","model":""}`, 422},
		{"unknown provider", "POST", ServicesPath + "/service_missing/test", "application/json", `{"protocol":"openai.chat","model":"test"}`, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := controlRequest(t, handler, test.method, test.path, test.media, test.body, "")
			if response.Code != test.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			if test.status == 200 {
				var result contract.ServiceTestResult
				decode(t, response, &result)
				if result.OK || result.StatusCode != 429 || result.ServiceID != "service_test" || result.Model != "custom-model" {
					t.Fatalf("result = %+v", result)
				}
			}
		})
	}
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest("POST", path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthorized.Code)
	}
	if tester.calls != 1 || tester.service.Enabled || tester.input.Prompt != "Hello" {
		t.Fatalf("tester = %+v", tester)
	}
	after, _ := store.GetService(context.Background(), "service_test")
	if before.ETag != after.ETag {
		t.Fatal("test mutated provider configuration")
	}
}
