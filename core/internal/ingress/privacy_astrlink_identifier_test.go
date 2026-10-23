package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// TestIngressKeepsAstrLinkIdentifiersAndRecordsWhy follows an agent that
// quotes a local access token id it read from AstrLink's records beside a
// pasted local access token. Only the token may leave, replaced; the id goes
// upstream as written and the record says why it was left alone.
func TestIngressKeepsAstrLinkIdentifiersAndRecordsWhy(t *testing.T) {
	const (
		tokenID = "access_token_0f1e2d3c4b5a69788796a5b4c3d2e1f0"
		token   = "astr_Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7-Qx7"
	)
	body := `{"model":"gpt-5","messages":[{"role":"user","content":"token ` + token + ` has id ` + tokenID + `"}]}`
	model := privacy.DetectorFunc(func(_ context.Context, input privacy.DetectInput) ([]privacy.Finding, error) {
		var findings []privacy.Finding
		for index, segment := range input.Segments {
			for _, needle := range []string{token, tokenID} {
				if start := strings.Index(segment.Value, needle); start >= 0 {
					findings = append(findings, privacy.Finding{
						Segment: index, Start: start, End: start + len(needle),
						Kind: privacy.KindCommonSecret, Confidence: 0.97,
					})
				}
			}
		}
		return findings, nil
	})
	filter := testPrivacyEngine(t, privacy.Policy{
		Enabled: true, Mode: privacy.ModeLocalModel, Action: privacy.ActionRedact, MinConfidence: 0.8,
		LocalModelID: "model_00000000000000000000000000000001",
	}, model)
	var upstream string
	records := &memoryRequestRecordStore{}
	handler := NewWithDependencies(Dependencies{
		Resolver: resolverFunc(func(context.Context, endpoint.ResolveRequest) (endpoint.Resolved, error) {
			return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIChat, false)}, nil
		}),
		PrivacyFilter:  filter,
		RequestRecords: records,
		Forwarder: forwarderFunc(func(writer http.ResponseWriter, request *http.Request, _ transport.Target) error {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				return err
			}
			upstream = string(raw)
			writer.Header().Set("Content-Type", "application/json")
			_, err = writer.Write([]byte(`{"id":"chatcmpl_1","choices":[{"index":0,"finish_reason":"stop",` +
				`"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return err
		}),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(upstream, token) || !strings.Contains(upstream, tokenID) {
		t.Fatalf("upstream body = %s", upstream)
	}
	if len(records.records) != 1 {
		t.Fatalf("records=%#v", records.records)
	}
	record := records.records[0]
	if record.PrivacyDecision == nil || *record.PrivacyDecision != contract.PrivacyDecisionRedact {
		t.Fatalf("decision=%v", record.PrivacyDecision)
	}
	if len(record.PrivacyFindings) != 1 || record.PrivacyFindings[0].Kind != contract.CanonicalKindCommonSecret ||
		record.PrivacyFindings[0].Count != 1 {
		t.Fatalf("findings=%#v", record.PrivacyFindings)
	}
	if len(record.PrivacyIgnored) != 1 {
		t.Fatalf("ignored=%#v", record.PrivacyIgnored)
	}
	ignored := record.PrivacyIgnored[0]
	if ignored.Kind != contract.CanonicalKindCommonSecret || ignored.Reason != contract.PrivacyIgnoreAstrLinkIdentifier ||
		ignored.Count != 1 {
		t.Fatalf("ignored=%#v", ignored)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("record does not validate: %v", err)
	}
	encoded, _ := json.Marshal([]any{record.PrivacyFindings, record.PrivacyIgnored})
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), tokenID) {
		t.Fatalf("privacy entries carry a request value: %s", encoded)
	}
}
