package privacy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func reportFingerprints(feed *WarningFeed, fingerprints ...Fingerprint) WarningEvent {
	secret := []Finding{{Kind: KindCommonSecret}}
	event, _ := feed.Report(Warning{
		RequestID:    "request_warned",
		Findings:     secret,
		Notified:     secret,
		Fingerprints: fingerprints,
	})
	return event
}

func TestWarningFeedNotifiesOncePerValue(t *testing.T) {
	feed := NewWarningFeed()
	first, second := Fingerprint{1}, Fingerprint{2}
	for index, step := range []struct {
		fingerprints []Fingerprint
		notifiable   bool
	}{
		{[]Fingerprint{first}, true},
		// A later call resends the value with the conversation history.
		{[]Fingerprint{first}, false},
		{[]Fingerprint{first, second}, true},
		{[]Fingerprint{second, first}, false},
		{nil, false},
	} {
		event := reportFingerprints(feed, step.fingerprints...)
		if event.Notifiable != step.notifiable || event.Sequence != uint64(index+1) {
			t.Fatalf("step %d: event = %#v, want notifiable %t", index, event, step.notifiable)
		}
	}
	page := feed.Since(0)
	if page.Epoch == "" || len(page.Events) != 5 || page.Events[0].Sequence != 1 {
		t.Fatalf("page = %#v", page)
	}
	if later := feed.Since(3); later.Epoch != page.Epoch || len(later.Events) != 2 ||
		later.Events[0].Sequence != 4 || later.Events[1].Sequence != 5 {
		t.Fatalf("events after 3 = %#v", later)
	}
	if none := feed.Since(5); len(none.Events) != 0 || none.Events == nil {
		t.Fatalf("events after the last = %#v, want an empty list", none.Events)
	}
}

func TestWarningFeedForgetsTheLeastRecentlySeenValue(t *testing.T) {
	feed := NewWarningFeed()
	feed.maxFingerprints = 2
	first, second, third := Fingerprint{1}, Fingerprint{2}, Fingerprint{3}
	reportFingerprints(feed, first)
	reportFingerprints(feed, second)
	// Seeing first again keeps it; second becomes the one to forget.
	reportFingerprints(feed, first)
	reportFingerprints(feed, third)
	if reportFingerprints(feed, first).Notifiable {
		t.Fatal("a value seen again must stay known")
	}
	if !reportFingerprints(feed, second).Notifiable {
		t.Fatal("a forgotten value must notify again")
	}
}

func TestWarningFeedKeepsOnlyRecentEvents(t *testing.T) {
	feed := NewWarningFeed()
	feed.maxEvents = 3
	for index := range 5 {
		reportFingerprints(feed, Fingerprint{byte(index)})
	}
	page := feed.Since(0)
	if len(page.Events) != 3 || page.Events[0].Sequence != 3 || page.Events[2].Sequence != 5 {
		t.Fatalf("events = %#v", page.Events)
	}
	if NewWarningFeed().Since(0).Epoch == page.Epoch {
		t.Fatal("every feed must have its own epoch")
	}
}

func TestWarningFeedLeavesOutKindsThatDoNotNotify(t *testing.T) {
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	stored := contract.DefaultPrivacyPolicy()
	stored.Enabled = true
	stored.RequestAction = contract.PolicyActionWarn
	// Emails are detected but do not notify; the default leaves them off.
	for index := range stored.KindRules {
		if stored.KindRules[index].Kind == string(KindEmail) {
			stored.KindRules[index].Enabled = true
			stored.KindRules[index].Notify = false
		}
	}
	policy, err := FromContractPolicy(stored)
	if err != nil {
		t.Fatal(err)
	}
	engine := newTestEngine(t, nil)
	feed := NewWarningFeed()
	report := func(input string) (Result, WarningEvent, bool) {
		t.Helper()
		result, err := engine.Inspect(context.Background(), policy, contract.ProtocolOpenAIResponses,
			[]byte(`{"model":"safe","input":"`+input+`"}`))
		if err != nil || result.Decision != DecisionWarn {
			t.Fatalf("result = %#v, %v", result, err)
		}
		event, fed := feed.Report(Warning{
			Findings: result.Findings, Notified: result.Notified, Fingerprints: result.Fingerprints,
		})
		return result, event, fed
	}

	result, event, fed := report("alice@acme.io " + token)
	if len(result.Findings) != 2 || len(result.Notified) != 1 ||
		result.Notified[0].Kind != KindCommonSecret || len(result.Fingerprints) != 1 {
		t.Fatalf("an email must stay recorded but not notify: %#v", result)
	}
	if !fed || !event.Notifiable ||
		!reflect.DeepEqual(event.Kinds, []WarningKindCount{{Kind: KindCommonSecret, Count: 1}}) {
		t.Fatalf("event = %#v, fed %t", event, fed)
	}
	// A new email beside the secret already warned about is no reason to
	// notify again.
	if _, event, fed = report("bob@acme.io " + token); !fed || event.Notifiable {
		t.Fatalf("event = %#v, fed %t", event, fed)
	}
	if result, _, fed = report("carol@acme.io"); fed || len(result.Findings) != 1 {
		t.Fatalf("a warning of emails only was fed: %#v", result)
	}
	if events := feed.Since(0).Events; len(events) != 2 || events[1].Sequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestWarningFeedCarriesKindsAndCountsButNoValue(t *testing.T) {
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	result, err := newTestEngine(t, nil).Inspect(context.Background(), Policy{
		Enabled: true, Mode: ModeRegex, Action: ActionWarn,
	}, contract.ProtocolOpenAIResponses, []byte(`{"model":"safe","input":"alice@example.com `+token+` alice@example.com"}`))
	if err != nil || result.Decision != DecisionWarn {
		t.Fatalf("result = %#v, %v", result, err)
	}
	feed := NewWarningFeed()
	feed.Report(Warning{
		RequestID:    "request_warned",
		ClientType:   contract.ClientCodex,
		Protocol:     contract.ProtocolOpenAIResponses,
		Findings:     result.Findings,
		Notified:     result.Notified,
		Fingerprints: result.Fingerprints,
	})
	encoded, err := json.Marshal(feed.Since(0))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{token, "alice@example.com", "alice", "ghp_"}
	for _, fingerprint := range result.Fingerprints {
		forbidden = append(forbidden,
			hex.EncodeToString(fingerprint[:]), base64.StdEncoding.EncodeToString(fingerprint[:]))
	}
	for _, value := range forbidden {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("feed carries %q: %s", value, encoded)
		}
	}
	var page struct {
		Events []struct {
			RequestID  string             `json:"request_id"`
			ClientType string             `json:"client_type"`
			Kinds      []WarningKindCount `json:"kinds"`
			Notifiable bool               `json:"notifiable"`
		} `json:"events"`
	}
	if err := json.Unmarshal(encoded, &page); err != nil {
		t.Fatal(err)
	}
	want := []WarningKindCount{{Kind: KindCommonSecret, Count: 1}, {Kind: KindEmail, Count: 2}}
	if len(page.Events) != 1 || page.Events[0].RequestID != "request_warned" ||
		page.Events[0].ClientType != string(contract.ClientCodex) || !page.Events[0].Notifiable ||
		len(page.Events[0].Kinds) != len(want) ||
		page.Events[0].Kinds[0] != want[0] || page.Events[0].Kinds[1] != want[1] {
		t.Fatalf("feed = %s", encoded)
	}
}
