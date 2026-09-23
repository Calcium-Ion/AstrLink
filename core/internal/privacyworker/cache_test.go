package privacyworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const testFrameLog = "frames.log"

func segmentsInput(values ...string) privacy.DetectInput {
	segments := make([]privacy.Segment, len(values))
	for index, value := range values {
		segments[index] = privacy.Segment{Value: value}
	}
	return privacy.DetectInput{ExpectedLocalModelID: testInstallationID, Segments: segments}
}

// testFrames lists the texts of every frame the helper worker received.
func testFrames(t *testing.T, client *Client) [][]string {
	t.Helper()
	installation, _ := client.model.ReadyInstallation(testInstallationID)
	data, err := os.ReadFile(filepath.Join(installation.Directory, testFrameLog))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var texts []string
		if err := json.Unmarshal([]byte(line), &texts); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, texts)
	}
	return frames
}

func findingSegments(findings []privacy.Finding) []int {
	segments := make([]int, len(findings))
	for index, finding := range findings {
		segments[index] = finding.Segment
	}
	sort.Ints(segments)
	return segments
}

func TestClientInspectsOnlyUncachedSegments(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	findings, err := client.Detect(context.Background(),
		segmentsInput("a@example.test", "plain text", "b@example.test"))
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0, 2}) {
		t.Fatalf("first Detect findings=%+v err=%v", findings, err)
	}

	findings, err = client.Detect(context.Background(), segmentsInput(
		"a@example.test", "plain text", "b@example.test", "reply text", "c@example.test"))
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0, 2, 4}) {
		t.Fatalf("second Detect findings=%+v err=%v", findings, err)
	}
	for _, finding := range findings {
		if finding.Start != 0 || finding.End != len("a@example.test") ||
			finding.Kind != privacy.KindEmail || finding.Confidence != 0.99 {
			t.Fatalf("cached finding = %+v", finding)
		}
	}
	want := [][]string{
		{"a@example.test", "plain text", "b@example.test"},
		{"reply text", "c@example.test"},
	}
	if frames := testFrames(t, client); !reflect.DeepEqual(frames, want) {
		t.Fatalf("frames = %q, want %q", frames, want)
	}
}

func TestClientBatchesPendingTextByBytes(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	client.batchBytes = 16
	var reports []privacy.InspectionProgress
	ctx := privacy.WithInspectionProgress(context.Background(), func(progress privacy.InspectionProgress) {
		reports = append(reports, progress)
	})
	findings, err := client.Detect(ctx, segmentsInput(
		"a@x.test", "b@x.test", "", "a long plain value of text", "c@x.test"))
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0, 1, 4}) {
		t.Fatalf("findings=%+v err=%v", findings, err)
	}
	want := [][]string{{"a@x.test", "b@x.test"}, {"a long plain value of text"}, {"c@x.test"}}
	if frames := testFrames(t, client); !reflect.DeepEqual(frames, want) {
		t.Fatalf("frames = %q, want %q", frames, want)
	}
	if len(reports) != 4 || reports[0].Batches != 3 || reports[0].CompletedBatches != 0 {
		t.Fatalf("reports = %+v", reports)
	}
	last := reports[len(reports)-1]
	if last.Segments != 4 || last.Bytes != 50 || last.InspectedBytes != 50 ||
		last.CachedBytes != 0 || last.CompletedBatches != 3 {
		t.Fatalf("final progress = %+v", last)
	}

	reports = nil
	if _, err := client.Detect(ctx, segmentsInput("a@x.test", "b@x.test")); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].CachedSegments != 2 || reports[0].CachedBytes != 16 ||
		reports[0].Batches != 0 {
		t.Fatalf("cached reports = %+v", reports)
	}
}

func TestClientKeepsFinishedBatchesWhenALaterBatchTimesOut(t *testing.T) {
	client := newTestClient(t, "hang_on_marker", time.Second)
	client.batchBytes = 16
	_, err := client.Detect(context.Background(), segmentsInput("a@x.test", "b@x.test", "HANG here"))
	if !errors.Is(err, privacy.ErrDetectorTimeout) {
		t.Fatalf("Detect error = %v", err)
	}

	// The retry of the same conversation continues where the timeout left off.
	findings, err := client.Detect(context.Background(), segmentsInput("a@x.test", "b@x.test"))
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0, 1}) {
		t.Fatalf("retry findings=%+v err=%v", findings, err)
	}
	if frames := testFrames(t, client); len(frames) != 2 {
		t.Fatalf("frames = %q, want the finished batch and the stuck one", frames)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.process != nil || client.starting != nil {
		t.Fatal("a fully cached request started the worker")
	}
}

func TestClientStopsStartingBatchesAfterInspectionBudget(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	client.batchBytes = 8
	clock := time.Unix(0, 0)
	client.now = func() time.Time {
		clock = clock.Add(time.Hour)
		return clock
	}
	input := segmentsInput("a@x.test", "b@x.test", "c@x.test")
	if _, err := client.Detect(context.Background(), input); !errors.Is(err, privacy.ErrDetectorTimeout) {
		t.Fatalf("Detect error = %v", err)
	}
	if frames := testFrames(t, client); !reflect.DeepEqual(frames, [][]string{{"a@x.test"}}) {
		t.Fatalf("frames = %q, want only the first batch", frames)
	}
	client.mu.Lock()
	process := client.process
	client.mu.Unlock()
	if process == nil || !process.running() {
		t.Fatal("the budget stopped the worker; only a stuck frame should")
	}

	client.now = time.Now
	findings, err := client.Detect(context.Background(), input)
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0, 1, 2}) {
		t.Fatalf("retry findings=%+v err=%v", findings, err)
	}
	want := [][]string{{"a@x.test"}, {"b@x.test"}, {"c@x.test"}}
	if frames := testFrames(t, client); !reflect.DeepEqual(frames, want) {
		t.Fatalf("frames = %q, want %q", frames, want)
	}
}

func TestClientServesCachedSegmentsWhileWorkerIsLatched(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	input := segmentsInput("a@x.test", "plain")
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	process := client.process
	key := process.key()
	client.failed = &key
	client.mu.Unlock()
	client.stopProcess(process)

	findings, err := client.Detect(context.Background(), input)
	if err != nil || !reflect.DeepEqual(findingSegments(findings), []int{0}) {
		t.Fatalf("cached findings=%+v err=%v", findings, err)
	}
	if _, err := client.Detect(context.Background(), segmentsInput("a@x.test", "new text")); !errors.Is(
		err, privacy.ErrDetectorUnavailable,
	) {
		t.Fatalf("uncached Detect error = %v", err)
	}
}

func TestClientCacheFollowsModelSelection(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	input := segmentsInput("a@x.test")
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	// Thresholds and kinds apply after detection; they keep the cache.
	tweaked := localModelPolicy(testInstallationID)
	tweaked.MinConfidence = 0.9
	client.ApplyPolicy(tweaked)
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if frames := testFrames(t, client); len(frames) != 1 {
		t.Fatalf("frames after a policy tweak = %q", frames)
	}

	client.ApplyPolicy(contract.DefaultPrivacyPolicy())
	client.ApplyPolicy(localModelPolicy(testInstallationID))
	if _, err := client.Detect(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if frames := testFrames(t, client); len(frames) != 2 {
		t.Fatalf("frames after leaving model mode = %q", frames)
	}
}

func TestClientCachedInspectionRewritesIdentically(t *testing.T) {
	client := newTestClient(t, "per_text", 5*time.Second)
	engine, err := privacy.New(privacy.PolicyProviderFunc(func(context.Context, privacy.Scope) (privacy.Policy, error) {
		return privacy.Policy{}, nil
	}), client)
	if err != nil {
		t.Fatal(err)
	}
	policy := privacy.Policy{
		Enabled: true, Mode: privacy.ModeLocalModel, LocalModelID: testInstallationID, Action: privacy.ActionRedact,
	}
	body := []byte(`{"model":"m","input":[{"role":"user","content":"a@x.test"},{"role":"user","content":"plain"}]}`)
	first, err := engine.Inspect(context.Background(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || len(first.Redactions) != 1 {
		t.Fatalf("first Inspect redactions=%+v err=%v", first.Redactions, err)
	}
	second, err := engine.Inspect(context.Background(), policy, contract.ProtocolOpenAIResponses, body)
	if err != nil || !bytes.Equal(first.Body, second.Body) ||
		!reflect.DeepEqual(first.Redactions, second.Redactions) {
		t.Fatalf("cached Inspect differs: %s vs %s err=%v", first.Body, second.Body, err)
	}
	if frames := testFrames(t, client); len(frames) != 1 {
		t.Fatalf("frames = %q", frames)
	}
}

func TestDetectionCacheKeysBindingAndBounds(t *testing.T) {
	cache := newDetectionCache()
	if cache.digest(privacy.Segment{ContextPrefix: "ab", Value: "c"}) ==
		cache.digest(privacy.Segment{ContextPrefix: "a", Value: "bc"}) {
		t.Fatal("prefix and value boundary is ambiguous")
	}
	key := modelKey{modelID: testInstallationID, identity: "first"}
	a := cache.digest(privacy.Segment{Value: "a@x"})
	b := cache.digest(privacy.Segment{Value: "b@x"})
	c := cache.digest(privacy.Segment{Value: "c@x"})

	cache.store(key, a, []privacy.Finding{{Segment: 7, Start: 0, End: 3, Kind: privacy.KindEmail}})
	found, hit := cache.lookup(key, a, "a@x")
	if !hit || len(found) != 1 || found[0].Segment != 0 {
		t.Fatalf("lookup = %+v, %t", found, hit)
	}
	found[0].End = 1
	if again, _ := cache.lookup(key, a, "a@x"); again[0].End != 3 {
		t.Fatal("lookup returned the cached slice itself")
	}
	if _, hit := cache.lookup(key, a, "a@"); hit {
		t.Fatal("spans that do not fit the value were served")
	}
	if _, hit := cache.lookup(key, a, "a@x"); hit {
		t.Fatal("a corrupt entry was kept")
	}

	cache.maxEntries = 2
	cache.store(key, a, nil)
	cache.store(key, b, nil)
	cache.lookup(key, a, "a@x")
	cache.store(key, c, nil)
	if _, hit := cache.lookup(key, b, "b@x"); hit {
		t.Fatal("the least recently used entry was not evicted")
	}
	if _, hit := cache.lookup(key, a, "a@x"); !hit {
		t.Fatal("a recently used entry was evicted")
	}

	cache.maxFindings = 2
	two := []privacy.Finding{{Start: 0, End: 1}, {Start: 1, End: 2}}
	cache.store(key, b, two)
	if cache.findings > cache.maxFindings || len(cache.entries) > cache.maxEntries {
		t.Fatalf("bounds exceeded: findings=%d entries=%d", cache.findings, len(cache.entries))
	}
	if _, hit := cache.lookup(key, b, "b@x"); !hit {
		t.Fatal("the newest entry was evicted")
	}

	other := modelKey{modelID: testInstallationID, identity: "second"}
	if _, hit := cache.lookup(other, b, "b@x"); hit {
		t.Fatal("another installation read this installation's results")
	}
	if _, hit := cache.lookup(key, b, "b@x"); hit {
		t.Fatal("rebinding kept the previous installation's results")
	}
}
