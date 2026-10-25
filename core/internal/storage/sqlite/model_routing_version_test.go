package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func versionedRoutingGroup() contract.ModelRoutingGroup {
	return contract.ModelRoutingGroup{
		ID: "program", Enabled: true,
		Members: []contract.ModelRoutingMember{
			{ID: "fast", ServiceID: "service_a", Model: "model_a"},
			{ID: "strong", ServiceID: "service_a", Model: "model_b"},
		},
		DefaultMemberID: "fast",
		Rules: []contract.ModelRoutingRule{{
			ID: "streamed", Priority: 1,
			When:           contract.ModelRoutingCondition{Field: "streaming", EqualsBool: boolPointerForVersions(true)},
			TargetMemberID: "strong",
		}},
	}
}

func boolPointerForVersions(value bool) *bool { return &value }

func saveRoutingGroups(t *testing.T, store *Store, groups ...contract.ModelRoutingGroup) {
	t.Helper()
	settings := contract.DefaultRoutingSettings()
	settings.ModelRoutingGroups = groups
	if err := store.UpdateRoutingSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

func latestGroupVersion(t *testing.T, store *Store, group contract.ModelRoutingGroup) int {
	t.Helper()
	version, err := store.EnsureModelRoutingGroupVersion(context.Background(), group)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// Saving routing settings numbers each group's configuration. Only a change
// to how the group routes is a new version: saving the same content again or
// switching the group off is not.
func TestRoutingSettingsSaveNumbersGroupVersions(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "versions.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	group := versionedRoutingGroup()
	saveRoutingGroups(t, store, group)
	if version := latestGroupVersion(t, store, group); version != 1 {
		t.Fatalf("first save version = %d, want 1", version)
	}
	saveRoutingGroups(t, store, group)
	disabled := group
	disabled.Enabled = false
	saveRoutingGroups(t, store, disabled)
	if version := latestGroupVersion(t, store, disabled); version != 1 {
		t.Fatalf("version after resave and disable = %d, want 1", version)
	}

	changed := versionedRoutingGroup()
	changed.Rules[0].TargetMemberID = "fast"
	saveRoutingGroups(t, store, changed)
	if version := latestGroupVersion(t, store, changed); version != 2 {
		t.Fatalf("version after a rule change = %d, want 2", version)
	}
	first, err := store.GetModelRoutingGroupVersion(ctx, "program", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.LatestVersion != 2 || first.Group.Rules[0].TargetMemberID != "strong" {
		t.Fatalf("version 1 = %+v, want the original rule and latest 2", first)
	}
	if _, err := time.Parse(time.RFC3339Nano, first.CreatedAt); err != nil {
		t.Fatalf("created_at %q: %v", first.CreatedAt, err)
	}

	// Going back to the first configuration is a change someone made.
	saveRoutingGroups(t, store, group)
	if version := latestGroupVersion(t, store, group); version != 3 {
		t.Fatalf("version after reverting = %d, want 3", version)
	}
	if _, err := store.GetModelRoutingGroupVersion(ctx, "program", 4); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing version err = %v, want ErrNotFound", err)
	}
}

// A configuration that reached the store without a settings save, such as
// one written before versions existed, is numbered by its first request.
func TestEnsureModelRoutingGroupVersionNumbersUnsavedConfiguration(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "versions-lazy.db"))
	defer store.Close()
	group := versionedRoutingGroup()
	if version := latestGroupVersion(t, store, group); version != 1 {
		t.Fatalf("first lookup version = %d, want 1", version)
	}
	if version := latestGroupVersion(t, store, group); version != 1 {
		t.Fatalf("second lookup version = %d, want 1", version)
	}
}

// A request that read its settings just before a save still labels itself
// with the configuration it used, and never adds a version of its own.
func TestRequestUnderAnOlderConfigurationKeepsItsVersion(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "versions-stale.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	original := versionedRoutingGroup()
	saveRoutingGroups(t, store, original)
	changed := versionedRoutingGroup()
	changed.Rules[0].TargetMemberID = "fast"
	saveRoutingGroups(t, store, changed)
	if version := latestGroupVersion(t, store, original); version != 1 {
		t.Fatalf("in-flight request under the first configuration = version %d, want 1", version)
	}
	first, err := store.GetModelRoutingGroupVersion(ctx, "program", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.LatestVersion != 2 {
		t.Fatalf("latest version = %d, want 2: the stale request must not mint one", first.LatestVersion)
	}
}

func sessionRoutingBinding(memberID, model string, now time.Time) contract.ModelRoutingBinding {
	return contract.ModelRoutingBinding{
		Principal: "principal_01", SessionID: "session_01", Protocol: contract.ProtocolOpenAIResponses,
		VirtualModelID: "astrlink/program", GroupID: "program", MemberID: memberID, ServiceID: "service_a", Model: model,
		ConfigSnapshotVersion: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", RequestID: "request_origin",
		CreatedAt: bindingTime(now), UpdatedAt: bindingTime(now), ExpiresAt: bindingTime(now.Add(time.Hour)),
	}
}

// "Re-select" in the session details clears routing bindings with the API
// provider ones, and a request already in flight cannot bring one back.
func TestReleaseClearsRoutingBindingsAndFencesInFlightRequests(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "routing-release.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	saveRoutingGroups(t, store, versionedRoutingGroup())
	started := time.Now().UTC()
	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("strong", "model_b", started), started); err != nil {
		t.Fatal(err)
	}
	audit, err := store.GetChannelBindingAudit(ctx, "session_01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.ModelRoutingBindings) != 1 {
		t.Fatalf("routing bindings = %+v, want the live one", audit.ModelRoutingBindings)
	}
	listed := audit.ModelRoutingBindings[0]
	if listed.VirtualModelID != "astrlink/program" || listed.Model != "model_b" || listed.RequestID != "request_origin" || listed.Principal != "principal_01" {
		t.Fatalf("listed binding = %+v", listed)
	}

	if err := store.ReleaseChannelBindings(ctx, "session_01"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetModelRoutingBinding(ctx, "principal_01", "session_01", contract.ProtocolOpenAIResponses, "astrlink/program"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("binding after release err = %v, want ErrNotFound", err)
	}
	// The request that started before the release finishes afterwards.
	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("strong", "model_b", time.Now().UTC()), started); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetModelRoutingBinding(ctx, "principal_01", "session_01", contract.ProtocolOpenAIResponses, "astrlink/program"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("in-flight write after release err = %v, want ErrNotFound", err)
	}
	later := time.Now().UTC().Add(time.Second)
	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("fast", "model_a", later), later); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetModelRoutingBinding(ctx, "principal_01", "session_01", contract.ProtocolOpenAIResponses, "astrlink/program"); err != nil || got.MemberID != "fast" {
		t.Fatalf("binding from a later request = %+v, %v", got, err)
	}
}

// A request that started earlier but finished later cannot replace the
// binding a newer request already wrote.
func TestLateRoutingBindingDoesNotReplaceANewerOne(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "routing-late.db"))
	defer store.Close()
	earlier := time.Now().UTC()
	later := earlier.Add(time.Second)
	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("fast", "model_a", later), later); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("strong", "model_b", earlier), earlier); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetModelRoutingBinding(ctx, "principal_01", "session_01", contract.ProtocolOpenAIResponses, "astrlink/program")
	if err != nil || got.MemberID != "fast" {
		t.Fatalf("binding = %+v, %v; want the newer request's member", got, err)
	}
}

// The session details offer only bindings a request would still reuse.
func TestSessionAuditHidesRoutingBindingsTheGroupNoLongerHonors(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "routing-audit.db"))
	defer store.Close()
	if _, err := store.CreateService(ctx, pathTestService("service_a"), storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	saveRoutingGroups(t, store, versionedRoutingGroup())
	now := time.Now().UTC()
	removed := sessionRoutingBinding("retired", "model_a", now)
	if err := store.UpsertModelRoutingBinding(ctx, removed, now); err != nil {
		t.Fatal(err)
	}
	audit, err := store.GetChannelBindingAudit(ctx, "session_01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.ModelRoutingBindings) != 0 {
		t.Fatalf("binding to a removed member listed: %+v", audit.ModelRoutingBindings)
	}

	if err := store.UpsertModelRoutingBinding(ctx, sessionRoutingBinding("strong", "model_b", now), now); err != nil {
		t.Fatal(err)
	}
	disabled := versionedRoutingGroup()
	disabled.Enabled = false
	saveRoutingGroups(t, store, disabled)
	audit, err = store.GetChannelBindingAudit(ctx, "session_01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.ModelRoutingBindings) != 0 {
		t.Fatalf("binding to a disabled group listed: %+v", audit.ModelRoutingBindings)
	}
}
