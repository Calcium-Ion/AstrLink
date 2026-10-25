package ingress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// A rule naming a custom classifier dimension must route live requests, and
// must not break the groups that never name one: the snapshot version covers
// every group, so it has to be computed against the declared dimensions too.
// The version check and the classification share one settings read.
func TestModelRoutingCustomDimensionRuleRoutesEveryGroup(t *testing.T) {
	cloudServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"answers":{"task_type":{"choice":"general","confidence":0.95},"topic":{"choice":"legal","confidence":0.9}}}`))
	}))
	defer cloudServer.Close()
	settings := enabledClassifierSettings(cloudServer.URL, contract.ClassifierProtocolJev)
	settings.Dimensions = []contract.ClassifierDimension{{Name: "topic", Options: []contract.ClassifierDimensionOption{{Name: "legal"}, {Name: "other"}}}}
	topical := cloudTestGroup(nil)
	topical.ID = "topical"
	topical.Rules = []contract.ModelRoutingRule{{
		ID: "legal", Priority: 1,
		When:           contract.ModelRoutingCondition{Field: "topic", In: []string{"legal"}},
		TargetMemberID: "strong",
	}}
	groups := []contract.ModelRoutingGroup{cloudTestGroup(nil), topical}

	versions := map[string]string{}
	for _, test := range []struct{ model, rule, member string }{
		{model: "astrlink/topical", rule: "legal", member: "strong-model"},
		// No rule of this group names the dimension. It used to answer 422
		// because the version was computed against built-in fields only.
		{model: "astrlink/superclaude", rule: "", member: "fast-model"},
	} {
		store := newRedirectSettingsStore()
		store.settings.ModelRoutingGroups = groups
		reads := 0
		handler := NewWithDependencies(Dependencies{
			Resolver: resolverFunc(func(_ context.Context, request endpoint.ResolveRequest) (endpoint.Resolved, error) {
				return endpoint.Resolved{Endpoint: validEndpoint(contract.ProtocolOpenAIResponses, false), UpstreamModel: request.Model}, nil
			}),
			RequestRecords: store,
			ClassifierSettings: ClassifierSettingsProviderFunc(func(context.Context) (contract.ClassifierSettings, error) {
				reads++
				return settings, nil
			}),
			ProxyCredentials: memorySecretStore{ref: contract.ClassifierCredentialRef, secret: []byte("cloud-key")},
			Forwarder: forwarderFunc(func(writer http.ResponseWriter, _ *http.Request, _ transport.Target) error {
				_, _ = writer.Write([]byte(`{"model":"fast-model"}`))
				return nil
			}),
		})
		response := serveCloudRouting(t, handler, `{"model":"`+test.model+`","input":"review this contract clause"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", test.model, response.Code, response.Body.String())
		}
		decision := store.root(t).ModelRouting
		if decision == nil || decision.RuleID != test.rule || decision.MemberModel != test.member {
			t.Fatalf("%s: decision = %+v", test.model, decision)
		}
		if reads != 1 {
			t.Fatalf("%s: classifier settings read %d times, want once", test.model, reads)
		}
		versions[test.model] = decision.ConfigSnapshotVersion
	}
	if versions["astrlink/topical"] != versions["astrlink/superclaude"] {
		t.Fatalf("one configuration produced two versions: %v", versions)
	}
}

// /v1/models lists a routing group whose rules read a custom dimension: the
// group is valid against the declared dimensions, as it was when it was saved,
// and the dimensions are read only for the group that needs them.
func TestModelRoutingDiscoveryListsGroupsOnCustomDimensions(t *testing.T) {
	dimensions := []contract.ClassifierDimension{{Name: "lang", Options: []contract.ClassifierDimensionOption{{Name: "zh"}, {Name: "en"}}}}
	plain := cloudTestGroup(nil)
	plain.ID = "plain"
	dimensional := cloudTestGroup(nil)
	dimensional.ID = "dimensional"
	dimensional.Rules = []contract.ModelRoutingRule{{ID: "zh", Priority: 1, When: contract.ModelRoutingCondition{Field: "lang", Equals: "zh"}, TargetMemberID: "strong"}}
	var entries []discoveryEntry
	for _, id := range []string{"fast-model", "strong-model"} {
		raw, _ := json.Marshal(openAISynthesizedModel{ID: id, Object: "model", OwnedBy: "system"})
		entries = append(entries, discoveryEntry{id: id, raw: raw})
	}
	reads := 0
	merged, err := appendModelRoutingDiscoveryEntries(contract.ProtocolOpenAIModels, entries, []contract.ModelRoutingGroup{plain, dimensional}, func() []contract.ClassifierDimension {
		reads++
		return dimensions
	})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range merged {
		ids = append(ids, entry.id)
	}
	if strings.Join(ids, ",") != "astrlink/dimensional,astrlink/plain,fast-model,strong-model" {
		t.Fatalf("/v1/models ids = %v", ids)
	}
	if reads != 1 {
		t.Fatalf("dimensions read %d times, want once, for the dimensional group", reads)
	}
	// Without the dimension declared the group is not valid and stays out.
	merged, err = appendModelRoutingDiscoveryEntries(contract.ProtocolOpenAIModels, entries, []contract.ModelRoutingGroup{dimensional}, func() []contract.ClassifierDimension { return nil })
	if err != nil || len(merged) != 2 {
		t.Fatalf("undeclared dimension: entries = %d, err = %v", len(merged), err)
	}
}
