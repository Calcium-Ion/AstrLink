package controlapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/vertexauth"
)

func TestVertexCredentialLifecycleAndDraftDiscovery(t *testing.T) {
	store, handler := newServiceHandler(t, "service_vertex")
	handler.capabilities.ConversionEngine = relaykitbridge.Descriptor(relaykitbridge.NewEngine())
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	account, _ := json.MarshalIndent(map[string]string{"type": "service_account", "project_id": "vertex-test", "client_email": "test@vertex-test.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}, "", "  ")
	secretJSON, _ := json.Marshal(string(account))
	const config = `"base_url":"https://aiplatform.googleapis.com","auth":{"scheme":"google_service_account"},"vertex":{"project_id":"vertex-test","location":"global"}`
	created := serviceRequestForTest(t, handler, http.MethodPost, ServicesPath, "application/json", `{"name":"Vertex","kind":"vertex","http":{`+config+`,"credential":{"secret":`+string(secretJSON)+`}},"models":["gemini-test"],"capabilities":[{"protocol":"google.generate_content","mode":"native","streaming":true},{"protocol":"openai.models","mode":"native","streaming":false}]}`, "")
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	path := ServicesPath + "/service_vertex"
	stored, err := store.Get(context.Background(), secretstore.Ref("local://service/service_vertex"))
	if err != nil || !json.Valid(stored) || strings.ContainsAny(string(stored), "\r\n") {
		t.Fatalf("credential was not compacted: %v", err)
	}
	defer clear(stored)
	detail := serviceRequestForTest(t, handler, http.MethodGet, path, "", "", "")
	for _, leak := range []string{"credential_hint", "PRIVATE KEY", "client_email"} {
		if strings.Contains(detail.Body.String(), leak) {
			t.Errorf("detail exposed %s", leak)
		}
	}
	for _, target := range []string{"google.generate_content", "anthropic.messages", "openai.chat"} {
		patch := fmt.Sprintf(`{"capabilities":[{"protocol":"openai.responses","mode":"native","streaming":true,"convert_to":%q},{"protocol":"openai.chat","mode":"native","streaming":true}],"model_protocols":[{"model":"gemini-test","protocol":%q}]}`, target, target)
		updated := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", patch, detail.Header().Get("ETag"))
		if updated.Code != 200 {
			t.Fatalf("save formats: %d %s", updated.Code, updated.Body)
		}
		detail = serviceRequestForTest(t, handler, http.MethodGet, path, "", "", "")
		record, err := store.GetService(context.Background(), "service_vertex")
		if err != nil || string(record.Service.ModelProtocol("gemini-test")) != target || string(record.Service.Capabilities[0].ConvertTo) != target || record.Service.Capabilities[1].ConvertTo != "" {
			t.Fatalf("saved configuration=%+v error=%v", record.Service, err)
		}
	}
	for _, patch := range []string{
		`{"capabilities":[{"protocol":"openai.responses","mode":"native","streaming":true}]}`,
		`{"capabilities":[{"protocol":"openai.chat","mode":"native","streaming":true,"convert_to":"openai.responses"}]}`,
		`{"model_protocols":[{"model":"gemini-test","protocol":"openai.responses"}]}`,
	} {
		invalid := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", patch, detail.Header().Get("ETag"))
		if invalid.Code != 422 {
			t.Fatalf("accepted unsupported format: %d %s", invalid.Code, invalid.Body)
		}
	}
	cleared := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"model_protocols":[]}`, detail.Header().Get("ETag"))
	if cleared.Code != 200 {
		t.Fatalf("clear formats: %d %s", cleared.Code, cleared.Body)
	}
	detail = serviceRequestForTest(t, handler, http.MethodGet, path, "", "", "")

	tokens, catalogs := 0, 0
	client := &http.Client{Transport: controlRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"publisherModels":[{"name":"publishers/google/models/gemini-test","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/google/models/gemini-test@default"}]}`
		if r.URL.Host == "oauth2.googleapis.com" {
			tokens++
			body = fmt.Sprintf(`{"access_token":"token-%d","token_type":"Bearer","expires_in":3600}`, tokens)
		} else {
			catalogs++
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer token-%d", tokens) {
				t.Error("discovery did not use OAuth token")
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	manager := vertexauth.New(client)
	handler.serviceModels = servicemodel.New(store, nil, client).WithVertexTokens(manager)
	probe := func(secret string) int {
		t.Helper()
		credential := ""
		if secret != "" {
			encoded, _ := json.Marshal(secret)
			credential = `,"credential":{"secret":` + string(encoded) + `}`
		}
		response := controlRequest(t, handler, http.MethodPost, ServiceModelProbesPath, "application/json", `{"service_id":"service_vertex","kind":"vertex","http":{`+config+credential+`},"protocol":"openai.models"}`, "")
		return response.Code
	}
	if status := probe(""); status != 200 {
		t.Fatalf("saved draft probe status=%d", status)
	}
	record, err := store.GetService(context.Background(), "service_vertex")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Service.ModelProtocols) != 0 || record.Service.ModelProtocol("gemini-test") != "" {
		t.Fatal("catalog probe restored a cleared model format")
	}
	ctx, err := networkproxy.Bind(context.Background(), record.Service, store)
	if err != nil {
		t.Fatal(err)
	}
	ep, err := record.Service.EndpointView()
	if err != nil {
		t.Fatal(err)
	}
	authorizer := endpoint.NewServiceAuthorizer(store, nil).WithVertexTokens(manager)
	headers, err := authorizer.Headers(ctx, ep, nil)
	if err != nil || headers.Get("Authorization") != "Bearer token-1" || tokens != 1 {
		t.Fatalf("shared auth: exchanges=%d error=%v", tokens, err)
	}

	invalid := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"http":{"vertex":{"project_id":"vertex-test","location":"us-east5"}}}`, detail.Header().Get("ETag"))
	if invalid.Code != 422 {
		t.Fatalf("accepted inconsistent base URL: %d", invalid.Code)
	}
	record, _ = store.GetService(context.Background(), "service_vertex")
	if record.Service.HTTP.Vertex.Location != "global" {
		t.Fatal("failed patch mutated cached configuration")
	}
	invalid = serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"http":{"credential":{"secret":"invalid-private-key"}}}`, record.ETag)
	if invalid.Code != 422 {
		t.Fatalf("accepted invalid credential: %d", invalid.Code)
	}
	if status := probe(""); status != 200 || tokens != 1 {
		t.Fatal("failed patch changed credentials")
	}

	replacement := strings.Replace(string(account), "test@vertex-test", "replacement@vertex-test", 1)
	if status := probe(replacement); status != 200 || tokens != 2 {
		t.Fatal("draft override did not exchange a new token")
	}
	unchanged, _ := store.Get(context.Background(), secretstore.Ref(ep.CredentialRef))
	defer clear(unchanged)
	if string(unchanged) != string(stored) {
		t.Fatal("draft probe persisted credentials")
	}
	encoded, _ := json.Marshal(replacement)
	updated := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"http":{"credential":{"secret":`+string(encoded)+`}}}`, record.ETag)
	if updated.Code != 200 {
		t.Fatalf("replace: %d %s", updated.Code, updated.Body.String())
	}
	if status := probe(""); status != 200 {
		t.Fatalf("replacement probe: %d", status)
	}
	removed := serviceRequestForTest(t, handler, http.MethodPatch, path, "application/merge-patch+json", `{"http":{"credential":null}}`, updated.Header().Get("ETag"))
	if removed.Code != 200 {
		t.Fatalf("remove: %d", removed.Code)
	}
	before := catalogs
	if status := probe(""); status == 200 || catalogs != before {
		t.Fatal("removed credential reused cached token")
	}
	if _, err := authorizer.Headers(ctx, ep, nil); err == nil {
		t.Fatal("inference reused deleted credentials")
	}
}
