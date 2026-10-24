package providerapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestVertexRejectsUnsupportedOperationsAndUnsafeModelIDs(t *testing.T) {
	vertex := &contract.VertexConnection{ProjectID: "vertex-test", Location: "global"}
	connection := &contract.HTTPConnection{BaseURL: vertex.BaseURL(), Vertex: vertex, Auth: contract.ServiceAuth{Scheme: contract.AuthSchemeGoogleServiceAccount}}
	for _, tc := range []struct{ path, body string }{
		{"/v1/embeddings", `{"model":"gemini-test"}`},
		{"/v1/responses", `{"model":"meta/model"}`},
		{"/v1beta/models/gemini-test:embedContent", `{}`},
		{"/v1beta/models/gemini-test%3Fkey=stolen:generateContent", `{}`},
		{"/v1/messages", `{"model":"claude-test/../../other"}`},
		{"/v1/messages", `{"model":"wrong-family"}`},
		{"/v1/messages", `{"model":42}`},
		{"/v1/messages", `{`},
		{"/v1/chat/completions", `{"model":"missing-publisher"}`},
		{"/v1/chat/completions", `{"model":"meta/a/b"}`},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			if err := VertexRequest(r, connection); err == nil {
				t.Fatal("accepted invalid Vertex request")
			}
		})
	}
}
