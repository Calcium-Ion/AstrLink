package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
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

func TestServiceTestAPI(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
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
