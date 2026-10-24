package servicemodel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

const vertexCatalog = `{"publisherModels":[
 {"name":"publishers/google/models/gemini-test","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/google/models/gemini-test@default","versionId":"default"},
 {"name":"publishers/anthropic/models/claude-test","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/anthropic/models/claude-test@20250101","versionId":"20250101"},
 {"name":"publishers/meta/models/llama-test-maas","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/meta/models/llama-test-maas"},
 {"name":"publishers/google/models/gemini-embedding-test","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/google/models/gemini-embedding-test"},
 {"name":"publishers/google/models/gemini-test-tts","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/google/models/gemini-test-tts"},
 {"name":"publishers/google/models/gemini-test-transcribe-preview","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/google/models/gemini-test-transcribe-preview"},
 {"name":"publishers/google/models/gemma-test","supportedActions":{"deploy":{}}},
 {"name":"publishers/other/models/unknown-chat","publisherModelTemplate":"projects/{project}/locations/{location}/publishers/other/models/unknown-chat"},
 {"name":"publishers/anthropic/models/claude-unknown-version","versionId":"default"}
 ]}`

func TestVertexCatalogOnlyUsesIdentifiableServingResources(t *testing.T) {
	models, _, _, err := decodeVertexPage([]byte(vertexCatalog))
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := discoveryOf(models)
	if err != nil || strings.Join(discovery.Models, ",") != "claude-test@20250101,gemini-test,meta/llama-test-maas" {
		t.Fatalf("catalog=%v %v", discovery, err)
	}
	if len(discovery.Protocols) != 3 {
		t.Fatal("missing model protocols")
	}
	for _, body := range []string{`{}`, `{"publisherModels":null}`, `{"publisherModels":"bad"}`} {
		if _, _, _, err := decodeVertexPage([]byte(body)); !errors.Is(err, ErrUpstream) {
			t.Fatal("accepted malformed catalog")
		}
	}
}

func TestVertexProbeUsesSharedAuthAndBoundedPagination(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "vertex-test", "client_email": "test@vertex-test.iam.gserviceaccount.com", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))})
	for _, mode := range []string{"pages", "repeat", "denied", "cancel", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			calls, tokens := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "oauth2.googleapis.com" {
					tokens++
					return probeResponse(`{"access_token":"google-token","token_type":"Bearer","expires_in":3600}`), nil
				}
				calls++
				if r.URL.Host != "aiplatform.googleapis.com" || r.URL.Path != "/v1beta1/publishers/*/models" || r.Header.Get("Authorization") != "Bearer google-token" || r.Header.Get("X-Goog-User-Project") != "vertex-test" {
					t.Errorf("incorrect catalog request: %s", r.URL)
				}
				if r.URL.Query().Get("listAllVersions") != "true" || r.URL.Query().Get("pageSize") != "300" {
					t.Error("missing catalog pagination options")
				}
				if mode == "unknown" {
					return probeResponse(`{"publisherModels":[{"name":"publishers/other/models/unknown"}]}`), nil
				}
				if mode == "cancel" {
					cancel()
					return nil, ctx.Err()
				}
				if mode == "denied" {
					response := probeResponse(`sensitive-upstream-body`)
					response.StatusCode = 403
					return response, nil
				}
				if calls == 1 || mode == "repeat" {
					return probeResponse(strings.TrimSuffix(vertexCatalog, "}") + `,"nextPageToken":"next"}`), nil
				}
				if r.URL.Query().Get("pageToken") != "next" {
					t.Error("lost next page token")
				}
				return probeResponse(`{"publisherModels":[]}`), nil
			})}
			prober := New(nil, nil, client)
			vertex := &contract.VertexConnection{ProjectID: "vertex-test", Location: "global"}
			connection := contract.HTTPConnection{BaseURL: vertex.BaseURL(), Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleServiceAccount}, Vertex: vertex}
			got, err := prober.ProbeHTTP(ctx, "service_vertex", contract.ServiceKindVertex, connection, secret, contract.ProtocolOpenAIModels)
			switch mode {
			case "pages":
				if err != nil || len(got.Models) != 3 || calls != 2 {
					t.Fatalf("models=%v calls=%d error=%v", got, calls, err)
				}
			case "repeat":
				if err == nil || !strings.Contains(err.Error(), "repeated") || calls != 2 {
					t.Fatalf("repeated page: %d %v", calls, err)
				}
			case "denied":
				if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("denied: %v", err)
				}
			case "unknown":
				if err == nil || !strings.Contains(err.Error(), "no identifiable") {
					t.Fatalf("unknown catalog: %v %v", got, err)
				}
			case "cancel":
				if err == nil {
					t.Fatal("cancelled probe succeeded")
				}
			}
			if tokens != 1 {
				t.Fatalf("token exchanges=%d", tokens)
			}
		})
	}
}
