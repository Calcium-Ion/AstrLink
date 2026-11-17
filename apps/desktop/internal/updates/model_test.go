package updates

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

const (
	pinnedRevision  = "49e8b7b83d34fd75a86cf07698bd80972d444ff3"
	releaseRevision = "11697fb46757a275ada41873c5e0e8f59bfd421d"
	earlierPin      = "1111111111111111111111111111111111111111"
)

func guardModel(revision, version string) map[string]any {
	return map[string]any{
		"id":       "astrlink-guard",
		"name":     "AstrLink Guard",
		"repo_id":  "QuantumNous/astrlink-guard",
		"revision": revision,
		"version":  version,
		"variants": []map[string]any{
			{"id": "cpu-int8", "supported": true},
			{"id": "gpu-fp16", "supported": false},
		},
	}
}

func decodeCatalog(t *testing.T, items ...map[string]any) modelCatalog {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"items": items})
	var catalog modelCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func guardReleases(t *testing.T, release map[string]any) []catalogModel {
	t.Helper()
	return latestReleases(decodeCatalog(t, guardModel(pinnedRevision, "0.1.0")), decodeCatalog(t, release))
}

func guardInstallation(id, revision, variant string) installation {
	catalog := "astrlink-guard"
	return installation{
		ID: id, Source: "catalog", CatalogID: &catalog, RepoID: "QuantumNous/astrlink-guard",
		Revision: revision, VariantID: variant, Status: "ready",
	}
}

func guardUpdate(phase ModelPhase) *PrivacyModelUpdate {
	return &PrivacyModelUpdate{CatalogID: "astrlink-guard", Name: "AstrLink Guard", Version: "0.1.1", Phase: phase}
}

func localPolicy(id *string) privacyPolicy {
	return privacyPolicy{Enabled: true, Detector: "local_model", LocalModelID: id}
}

func ptr(s string) *string { return &s }

func TestModelReminderFollowsANewerReleaseOfTheModelInUse(t *testing.T) {
	got := pendingUpdate(guardReleases(t, guardModel(releaseRevision, "0.1.1")),
		[]installation{guardInstallation("model_a", pinnedRevision, "cpu-int8")}, localPolicy(ptr("model_a")))
	if !reflect.DeepEqual(got, guardUpdate(ModelAvailable)) {
		t.Fatalf("got %+v", got)
	}
}

func TestModelReminderFallsBackToThePinnedEntryWithoutANewerRelease(t *testing.T) {
	for _, release := range []map[string]any{
		guardModel(pinnedRevision, "0.1.0"),
		guardModel(releaseRevision, "0.0.9"),
		guardModel(releaseRevision, "not-semver"),
	} {
		latest := guardReleases(t, release)
		if len(latest) != 1 || latest[0].Revision != pinnedRevision {
			t.Fatalf("%v: got %+v", release["version"], latest)
		}
		if got := pendingUpdate(latest, []installation{guardInstallation("model_a", pinnedRevision, "cpu-int8")}, localPolicy(ptr("model_a"))); got != nil {
			t.Fatalf("%v: got %+v", release["version"], got)
		}
	}
}

func TestModelReminderMovesInstallsOfAnEarlierPinToThePinnedRelease(t *testing.T) {
	got := pendingUpdate(guardReleases(t, guardModel(pinnedRevision, "0.1.0")),
		[]installation{guardInstallation("model_a", earlierPin, "cpu-int8")}, localPolicy(ptr("model_a")))
	want := guardUpdate(ModelAvailable)
	want.Version = "0.1.0"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestModelReminderStaysQuietWhenThePolicyDoesNotRunThatModel(t *testing.T) {
	releases := guardReleases(t, guardModel(releaseRevision, "0.1.1"))
	installations := []installation{guardInstallation("model_a", pinnedRevision, "cpu-int8")}
	regex := localPolicy(ptr("model_a"))
	regex.Detector = "regex"
	disabled := localPolicy(ptr("model_a"))
	disabled.Enabled = false
	for _, policy := range []privacyPolicy{regex, disabled, localPolicy(nil), localPolicy(ptr("model_other"))} {
		if got := pendingUpdate(releases, installations, policy); got != nil {
			t.Fatalf("%+v: got %+v", policy, got)
		}
	}
	local := guardInstallation("model_a", pinnedRevision, "cpu-int8")
	local.Source = "local"
	if got := pendingUpdate(releases, []installation{local}, localPolicy(ptr("model_a"))); got != nil {
		t.Fatalf("local install: got %+v", got)
	}
}

func TestModelReminderWaitsForTheSwitchOnceTheNewRevisionIsReady(t *testing.T) {
	releases := guardReleases(t, guardModel(releaseRevision, "0.1.1"))
	downloading := guardInstallation("model_b", releaseRevision, "cpu-int8")
	downloading.Status = "downloading"
	both := []installation{guardInstallation("model_a", pinnedRevision, "cpu-int8"), downloading}
	if got := pendingUpdate(releases, both, localPolicy(ptr("model_a"))); !reflect.DeepEqual(got, guardUpdate(ModelAvailable)) {
		t.Fatalf("downloading: got %+v", got)
	}
	both[1].Status = "ready"
	if got := pendingUpdate(releases, both, localPolicy(ptr("model_a"))); !reflect.DeepEqual(got, guardUpdate(ModelReady)) {
		t.Fatalf("ready: got %+v", got)
	}
	// Another variant of the new revision does not replace this one.
	otherVariant := []installation{
		guardInstallation("model_a", pinnedRevision, "cpu-int8"),
		guardInstallation("model_c", releaseRevision, "gpu-fp16"),
	}
	if got := pendingUpdate(releases, otherVariant, localPolicy(ptr("model_a"))); !reflect.DeepEqual(got, guardUpdate(ModelAvailable)) {
		t.Fatalf("other variant: got %+v", got)
	}
	if got := pendingUpdate(releases, both, localPolicy(ptr("model_b"))); got != nil {
		t.Fatalf("switched: got %+v", got)
	}
}

func TestModelReminderSerializesThePhaseForTheShell(t *testing.T) {
	data, _ := json.Marshal(guardUpdate(ModelReady))
	want := `{"catalog_id":"astrlink-guard","name":"AstrLink Guard","version":"0.1.1","phase":"ready"}`
	if string(data) != want {
		t.Fatalf("got %s", data)
	}
}

func TestModelReminderSkipsVariantsTheReleaseCannotRunHere(t *testing.T) {
	got := pendingUpdate(guardReleases(t, guardModel(releaseRevision, "0.1.1")),
		[]installation{guardInstallation("model_a", pinnedRevision, "gpu-fp16")}, localPolicy(ptr("model_a")))
	if got != nil {
		t.Fatalf("got %+v", got)
	}
}

// fakeModels answers like Core, counting the release reads.
type fakeModels struct {
	catalog, releases, policy, installations string
	releaseReads                             int
	failReleases                             bool
}

func (f *fakeModels) Catalog(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.catalog), nil
}

func (f *fakeModels) Releases(context.Context) (json.RawMessage, error) {
	f.releaseReads++
	if f.failReleases {
		return nil, errors.New("Core is restarting")
	}
	return json.RawMessage(f.releases), nil
}

func (f *fakeModels) Policy(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.policy), nil
}

func (f *fakeModels) Installations(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.installations), nil
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestModelReminderPublishesChangesAndAnnouncesEachReminderOnce(t *testing.T) {
	source := &fakeModels{
		catalog:  marshal(t, map[string]any{"items": []any{guardModel(pinnedRevision, "0.1.0")}}),
		releases: marshal(t, map[string]any{"items": []any{guardModel(releaseRevision, "0.1.1")}}),
		policy:   `{"policy":{"enabled":true,"detector":"local_model","local_model_id":"model_a"},"etag":"\"1\""}`,
		installations: marshal(t, map[string]any{"items": []any{map[string]any{
			"id": "model_a", "source": "catalog", "catalog_id": "astrlink-guard", "repo_id": "QuantumNous/astrlink-guard",
			"revision": pinnedRevision, "variant_id": "cpu-int8", "status": "ready",
		}}}),
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	var published []*PrivacyModelUpdate
	var announced []PrivacyModelUpdate
	m := &ModelUpdates{
		Source:   source,
		Ready:    func() bool { return true },
		Publish:  func(u *PrivacyModelUpdate) { published = append(published, u) },
		Announce: func(u PrivacyModelUpdate) { announced = append(announced, u) },
		now:      func() time.Time { return now },
	}
	ctx := context.Background()
	m.tick(ctx)
	if len(published) != 1 || !reflect.DeepEqual(published[0], guardUpdate(ModelAvailable)) || len(announced) != 1 {
		t.Fatalf("first tick: published %v, announced %v", published, announced)
	}
	if !reflect.DeepEqual(m.Status(), guardUpdate(ModelAvailable)) {
		t.Fatalf("status %+v", m.Status())
	}
	// The same reminder is not published again; the releases wait for the
	// next check.
	now = now.Add(time.Minute)
	m.tick(ctx)
	if len(published) != 1 || source.releaseReads != 1 {
		t.Fatalf("second tick: published %d, release reads %d", len(published), source.releaseReads)
	}
	// The policy switches: the reminder settles.
	source.policy = `{"policy":{"enabled":false,"detector":"local_model","local_model_id":"model_a"}}`
	m.tick(ctx)
	if len(published) != 2 || published[1] != nil || m.Status() != nil {
		t.Fatalf("settled: published %v", published)
	}
	// Back on: the main window hears of it again, the notification does
	// not repeat.
	source.policy = `{"policy":{"enabled":true,"detector":"local_model","local_model_id":"model_a"}}`
	m.tick(ctx)
	if len(published) != 3 || len(announced) != 1 {
		t.Fatalf("again: published %d, announced %d", len(published), len(announced))
	}
	// Recheck reads the releases at once.
	m.Recheck()
	m.tick(ctx)
	if source.releaseReads != 2 {
		t.Fatalf("recheck: release reads %d", source.releaseReads)
	}
	// After the interval the releases are read again.
	now = now.Add(modelCheckInterval)
	m.tick(ctx)
	if source.releaseReads != 3 {
		t.Fatalf("interval: release reads %d", source.releaseReads)
	}
}

func TestModelReminderRetriesAFailedCheckSoonerAndNeedsReleasesFirst(t *testing.T) {
	source := &fakeModels{failReleases: true, catalog: `{"items":[]}`}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	published := 0
	m := &ModelUpdates{Source: source, Ready: func() bool { return true },
		Publish: func(*PrivacyModelUpdate) { published++ }, now: func() time.Time { return now }}
	m.tick(context.Background())
	if published != 0 || m.nextCheck != now.Add(modelRetryInterval) {
		t.Fatalf("published %d, next check %v", published, m.nextCheck)
	}
}

func TestModelReminderRunsUntilCancelledAndWakesOnDemand(t *testing.T) {
	ticked := make(chan struct{}, 4)
	m := &ModelUpdates{Source: &fakeModels{}, Ready: func() bool {
		ticked <- struct{}{}
		return false
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	m.Wake()
	select {
	case <-ticked:
	case <-done:
		t.Fatal("Run returned before it was cancelled")
	case <-time.After(10 * time.Second):
		t.Fatal("Wake did not run a check")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return once cancelled")
	}
}
