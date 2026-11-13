package ingress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

type redirectServices []contract.Service

func (services redirectServices) ListServices(context.Context, storage.ServiceListOptions) (storage.ServicePage, error) {
	page := storage.ServicePage{}
	for _, service := range services {
		page.Items = append(page.Items, storage.ServiceRecord{Service: service})
	}
	return page, nil
}

// A Claude Code request keeps its model while routing, so it can fail over
// from a Claude subscription to Copilot, whose built-in rule sends its dotted
// name.
func TestClaudeCodeModelFailsOverToCopilotUnderItsDottedName(t *testing.T) {
	subscription := func(id contract.ServiceID, kind contract.ServiceKind, models ...string) contract.Service {
		return contract.Service{
			ID: id, Name: string(id), Kind: kind, Enabled: true, Models: models,
			Capabilities: kind.SubscriptionProvider().Capabilities(),
			Subscription: &contract.SubscriptionConnection{
				Provider: kind.SubscriptionProvider(), Status: contract.SubscriptionStatusConnected,
				CredentialRef: "local://subscription/" + string(id),
			},
		}
	}
	copilot := subscription("service_b_copilot", contract.ServiceKindCopilotSubscription, "claude-sonnet-4.6")
	resolver, err := endpoint.NewStoreResolver(redirectServices{
		subscription("service_a_claude", contract.ServiceKindClaudeSubscription, "claude-sonnet-4-6"),
		copilot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hosts, models []string
	handler := NewWithDependencies(Dependencies{
		Resolver:   resolver,
		Authorizer: endpoint.NewServiceAuthorizer(codingPlanCredentials{}, codingPlanCredentials{}),
		Forwarder: transport.New(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(request.Body)
			hosts = append(hosts, request.URL.Host)
			models = append(models, ojson.GetBytes(body, "model").String())
			if request.URL.Host == "api.anthropic.com" {
				return jsonResponse(http.StatusServiceUnavailable, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`), nil
			}
			return jsonResponse(http.StatusOK, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4.6","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`), nil
		})),
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if strings.Join(hosts, ",") != "api.anthropic.com,api.githubcopilot.com" ||
		strings.Join(models, ",") != "claude-sonnet-4-6,claude-sonnet-4.6" {
		t.Fatalf("attempts = %v, models = %v", hosts, models)
	}
}
