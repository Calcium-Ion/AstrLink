package controlapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const customKindPatch = `{"regex_source":"custom",` +
	`"kind_rules":[{"kind":"custom_org","enabled":true,"style":"token","notify":true,"label":"公司名称"}],` +
	`"custom_regex_rules":[{"kind":"custom_org","pattern":"ACME-[0-9]+"},{"kind":"email","pattern":"(?i)[a-z]+@corp\\.test"}]}`

func TestPolicyPatchDeclaresCustomKind(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	path := PoliciesPath + "/" + string(contract.DefaultPrivacyPolicyID)
	get := policyRequest(t, handler, http.MethodGet, path, "", "", "")

	// A rule naming a kind nobody declared is refused, as before.
	refused := policyRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		`{"regex_source":"custom","custom_regex_rules":[{"kind":"custom_org","pattern":"ACME-[0-9]+"}]}`,
		get.Header().Get("ETag"))
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("undeclared kind status=%d body=%s", refused.Code, refused.Body.String())
	}
	for _, kind := range []string{"organization", "company", "agency"} {
		response := policyRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
			`{"regex_source":"custom","custom_regex_rules":[{"kind":"`+kind+`","pattern":"ACME-[0-9]+"}]}`,
			get.Header().Get("ETag"))
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bare kind %q status=%d", kind, response.Code)
		}
	}

	response := policyRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		customKindPatch, get.Header().Get("ETag"))
	if response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	reread := policyRequest(t, handler, http.MethodGet, path, "", "", "")
	var policy contract.Policy
	decode(t, reread, &policy)
	last := policy.KindRules[len(policy.KindRules)-1]
	if len(policy.KindRules) != len(contract.PrivacyKinds())+1 || last.Kind != "custom_org" ||
		last.Label != "公司名称" || !last.Enabled {
		t.Fatalf("persisted kind rules = %#v", policy.KindRules)
	}

	// Renaming changes the label only; the id the rules use stays.
	renamed := policyRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		`{"kind_rules":[{"kind":"custom_org","enabled":false,"style":"token","notify":true,"label":"合作公司"}]}`,
		reread.Header().Get("ETag"))
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", renamed.Code, renamed.Body.String())
	}
	decode(t, renamed, &policy)
	if got := policy.KindRules[len(policy.KindRules)-1]; got.Label != "合作公司" || got.Enabled ||
		policy.CustomRegexRules[0].Kind != "custom_org" {
		t.Fatalf("renamed policy = %#v", policy)
	}

	// Dropping the declaration while a rule still names it is refused, so
	// protection is never removed behind the user's back.
	dropped := policyRequest(t, handler, http.MethodPatch, path, "application/merge-patch+json",
		`{"kind_rules":[]}`, renamed.Header().Get("ETag"))
	if dropped.Code != http.StatusUnprocessableEntity {
		t.Fatalf("drop status=%d body=%s", dropped.Code, dropped.Body.String())
	}

	// Observers see the kind but not the patterns.
	summary := SummarizePolicy(policy)
	if strings.Join(summary.CustomRegexRules.Kinds, ",") != "custom_org,email" {
		t.Fatalf("summary kinds = %v", summary.CustomRegexRules.Kinds)
	}
}

func TestPolicyDryRunRedactsCustomKindWithItsOwnMarker(t *testing.T) {
	_, handler, _ := newPolicyHandler(t)
	provider, err := privacy.NewStorePolicyProvider(handler.policyStore)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := privacy.New(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler.privacyFilter = filter

	// The override is the editor's unsaved draft: nothing is stored.
	response := policyRequest(t, handler, http.MethodPost, PolicyDryRunPath, "application/json",
		`{"protocol":"openai.chat","sample_text":"ACME-7731 owes ACME-7731","policy":`+customKindPatch+`}`, "")
	if response.Code != http.StatusOK {
		t.Fatalf("dry-run status=%d body=%s", response.Code, response.Body.String())
	}
	var result contract.PolicyDryRunResponse
	decode(t, response, &result)
	if result.Decision != "redact" || result.FindingsSummary != "custom_org=2" ||
		len(result.Redactions) != 1 || result.Redactions[0].Kind != "custom_org" ||
		!strings.HasPrefix(result.Redactions[0].Placeholder, "<PRIVATE_CUSTOM_ORG_") ||
		result.RedactedBody == nil || strings.Contains(*result.RedactedBody, "ACME-7731") {
		t.Fatalf("dry-run result = %#v", result)
	}
	if len(result.Findings) != 2 || result.Findings[0].Kind != "custom_org" {
		t.Fatalf("findings = %#v", result.Findings)
	}
	stored, err := handler.policyStore.GetPolicy(t.Context(), contract.DefaultPrivacyPolicyID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Policy.CustomRegexRules) != 0 || len(stored.Policy.KindRules) != len(contract.PrivacyKinds()) {
		t.Fatalf("dry-run changed the stored policy: %#v", stored.Policy)
	}
}
