package controlapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const customServiceWithModelListPath = `{"name":"Custom","kind":"custom","http":{"base_url":"https://gateway.example","auth":{"scheme":"none"},"model_list_path":"/api/models"},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`

func TestCustomServiceModelListPathPersistsAndPatches(t *testing.T) {
	store, handler := newServiceHandler(t, "service_custom_path")
	service := createServiceForTest(t, handler, customServiceWithModelListPath)
	if service.HTTP == nil || service.HTTP.ModelListPath != "/api/models" {
		t.Fatalf("create lost model_list_path: %#v", service.HTTP)
	}
	path := ServicesPath + "/" + string(service.ID)

	record, err := store.GetService(context.Background(), service.ID)
	if err != nil || record.Service.HTTP.ModelListPath != "/api/models" {
		t.Fatalf("persisted %#v %v", record.Service.HTTP, err)
	}
	response := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		`{"http":{"model_list_path":"/v2/models"}}`, record.ETag)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	record, err = store.GetService(context.Background(), service.ID)
	if err != nil || record.Service.HTTP.ModelListPath != "/v2/models" {
		t.Fatalf("patched %#v %v", record.Service.HTTP, err)
	}

	response = serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		`{"http":{"model_list_path":null}}`, record.ETag)
	if response.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", response.Code, response.Body.String())
	}
	record, err = store.GetService(context.Background(), service.ID)
	if err != nil || record.Service.HTTP.ModelListPath != "" {
		t.Fatalf("cleared %#v %v", record.Service.HTTP, err)
	}
	if strings.Contains(serviceRequestForTest(t, handler, http.MethodGet, path, "", "", "").Body.String(), "model_list_path") {
		t.Fatal("cleared model_list_path is still serialized")
	}
}

func TestModelListPathRejectedForNonCustomServices(t *testing.T) {
	store, handler := newServiceHandler(t, "service_openai_path", "service_openai_patch")
	response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json",
		`{"name":"OpenAI","kind":"openai","http":{"base_url":"https://api.example/v1","auth":{"scheme":"none"},"model_list_path":"/v1/models"},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`,
		"")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}

	service := createServiceForTest(t, handler,
		`{"name":"OpenAI","kind":"openai","http":{"base_url":"https://api.example/v1","auth":{"scheme":"none"}},"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true}]}`)
	record, err := store.GetService(context.Background(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	response = serviceRequestForTest(t, handler, http.MethodPatch, ServicesPath+"/"+string(service.ID), "application/merge-patch+json",
		`{"http":{"model_list_path":"/v1/models"}}`, record.ETag)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestModelListPathRejectsInvalidFormat(t *testing.T) {
	for _, value := range []string{"v1/models", "/v1/models?limit=1", "/v1/models#x", "/v1/ models"} {
		t.Run(value, func(t *testing.T) {
			_, handler := newServiceHandler(t, "service_bad_path")
			body := strings.Replace(customServiceWithModelListPath, `"/api/models"`, `"`+value+`"`, 1)
			response := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json", body, "")
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDraftModelProbeUsesCustomModelListPath(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/astrlink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var urls []string
	client := &http.Client{Transport: controlRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		urls = append(urls, request.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"model-b"},{"id":"model-a"}]}`)),
		}, nil
	})}
	handler, err := NewWithDependencies(
		contract.DefaultVersionResponse("0.1.0-test", "abc1234"),
		Dependencies{
			ServiceStore:  store,
			ServiceModels: servicemodel.New(store, nil, client),
			ControlToken:  testControlToken,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	response := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json",
		`{"kind":"custom","http":{"base_url":"https://gateway.example","auth":{"scheme":"none"},"model_list_path":"/api/models"},"protocol":"openai.models"}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(urls) != 1 || urls[0] != "https://gateway.example/api/models" {
		t.Fatalf("urls=%v", urls)
	}

	response = controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json",
		`{"kind":"openai","http":{"base_url":"https://api.example","auth":{"scheme":"none"},"model_list_path":"/api/models"},"protocol":"openai.models"}`, "")
	if response.Code != http.StatusUnprocessableEntity || len(urls) != 1 {
		t.Fatalf("non-custom probe status=%d urls=%v", response.Code, urls)
	}
}
