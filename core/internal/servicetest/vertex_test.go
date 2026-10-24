package servicetest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
	"github.com/QuantumNous/astrlink/core/internal/vertexauth"
)

func TestSavedVertexConnectionTestUsesServiceAccountAndNativeProtocol(t *testing.T) {
	store := gatewayStore(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "vertex-test", "client_email": "test@vertex-test.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	vertex := &contract.VertexConnection{ProjectID: "vertex-test", Location: "global"}
	service := contract.Service{ID: "service_test", Name: "Vertex", Kind: contract.ServiceKindVertex, Enabled: true, Models: []string{"gemini-test"}, HTTP: &contract.HTTPConnection{BaseURL: vertex.BaseURL(), Vertex: vertex, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleServiceAccount}}, Capabilities: []contract.Capability{{Protocol: contract.ProtocolOpenAIResponses, Mode: contract.CapabilityModeNative, Streaming: true, ConvertTo: contract.ProtocolAnthropicMessages}}}
	saved, err := store.CreateService(context.Background(), service, storage.CredentialMutation{Present: true, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	deps := gatewayDependencies(t, store)
	tokens := vertexauth.New(&http.Client{Transport: timingTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "oauth2.googleapis.com" {
			t.Errorf("token host=%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"google-token","expires_in":3600,"token_type":"Bearer"}`))}, nil
	})})
	deps.Authorizer = endpoint.NewServiceAuthorizer(store, nil).WithVertexTokens(tokens)
	calls := 0
	deps.Forwarder = transport.New(timingTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/projects/vertex-test/locations/global/publishers/google/models/gemini-test:generateContent" || r.Header.Get("Authorization") != "Bearer google-token" {
			t.Errorf("upstream=%s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))}, nil
	}))
	result := NewWithDependencies(deps, nil).Test(context.Background(), saved.Service, contract.ServiceTestRequest{Protocol: contract.ProtocolGoogleGenerateContent, Model: "gemini-test", Prompt: "hello"})
	if !result.OK || result.Output != "ok" || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	if record := onlyGatewayRecord(t, store); record.Status != contract.RequestStatusSucceeded {
		t.Fatalf("test record=%+v", record)
	}
}
