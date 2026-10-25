package privacy

import (
	"encoding/json"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

// TestNaturalPlaceholdersStayInsideReservedNamespaces is the load-bearing test
// for the natural shape. A stand-in that could resolve would send an agent to a
// live host, and one that a detector re-matches on a later turn would be
// redacted a second time, making the original unrecoverable.
func TestNaturalPlaceholdersStayInsideReservedNamespaces(t *testing.T) {
	allocator := newPlaceholderAllocator(testDerivationKey(4), naturalKindRule, nil)
	for _, test := range []struct {
		kind   Kind
		value  string
		verify func(*testing.T, string)
	}{
		{KindEmail, "alice@example.com", func(t *testing.T, got string) {
			if !strings.HasSuffix(got, "@private.invalid") {
				t.Fatalf("email stand-in %q is not under .invalid", got)
			}
		}},
		{KindURL, "https://internal.corp/orders/91", func(t *testing.T, got string) {
			if !strings.HasPrefix(got, "https://private.invalid/") {
				t.Fatalf("url stand-in %q is not under .invalid", got)
			}
		}},
		{KindIPAddress, "10.4.7.9", func(t *testing.T, got string) {
			if !isReservedTestNetAddress(got) {
				t.Fatalf("v4 stand-in %q is outside the test networks", got)
			}
			if address := net.ParseIP(got); address == nil || address.To4() == nil {
				t.Fatalf("v4 stand-in %q changed address family", got)
			}
		}},
		{KindIPAddress, "2606:4700::1111", func(t *testing.T, got string) {
			if !isReservedTestNetAddress(got) {
				t.Fatalf("v6 stand-in %q is outside the documentation prefix", got)
			}
			address := net.ParseIP(got)
			if address == nil || address.To4() != nil {
				t.Fatalf("v6 stand-in %q changed address family", got)
			}
			// A model or tool that normalizes the address must not change it.
			if address.String() != got {
				t.Fatalf("v6 stand-in %q is not canonical, want %q", got, address.String())
			}
		}},
	} {
		t.Run(string(test.kind)+"/"+test.value, func(t *testing.T) {
			placeholder, style, err := allocator.allocate(test.kind, test.value)
			if err != nil {
				t.Fatalf("allocate: %v", err)
			}
			if style != contract.PlaceholderStyleNatural {
				t.Fatalf("style = %q", style)
			}
			test.verify(t, placeholder)
			// A stand-in that needed JSON escaping would not survive being
			// substituted into a serialized tool-argument string.
			encoded, err := json.Marshal(placeholder)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `"`+placeholder+`"` {
				t.Fatalf("stand-in %q requires JSON escaping: %s", placeholder, encoded)
			}
			// The stand-in must not be recognizable as the kind it replaces on a
			// later turn, or restoration of the outer value becomes impossible.
			if !isNaturalPlaceholder(test.kind, placeholder) {
				t.Fatalf("stand-in %q is not recognized by the guard", placeholder)
			}
		})
	}
}

// TestIPv6StandInsAreCanonical guards against a second spelling: a model or
// tool rewriting a group with a leading zero in RFC 5952 form would leave a
// stand-in the restorer no longer recognizes.
func TestIPv6StandInsAreCanonical(t *testing.T) {
	allocator := newPlaceholderAllocator(testDerivationKey(8), naturalKindRule, nil)
	for index := range 256 {
		value := "2606:4700::" + strconv.FormatInt(int64(index), 16)
		placeholder, style, err := allocator.allocate(KindIPAddress, value)
		if err != nil || style != contract.PlaceholderStyleNatural {
			t.Fatalf("allocate %q: %q %q %v", value, placeholder, style, err)
		}
		if canonical := net.ParseIP(placeholder).String(); canonical != placeholder {
			t.Fatalf("stand-in %q for %q is not canonical, want %q", placeholder, value, canonical)
		}
	}
}

// TestSelfRedactionGuardLeavesOwnStandInsAlone covers the chain-pollution case:
// a stand-in that escaped restoration returns in the client's history next
// turn, and redacting it again would bury the original behind two mappings of
// which only the outer one is known.
func TestSelfRedactionGuardLeavesOwnStandInsAlone(t *testing.T) {
	engine := mustTestEngine(t)
	policy := naturalPolicy()
	first, err := engine.Inspect(
		t.Context(),
		policy,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"mail alice@example.com host 10.4.7.9 v6 2606:4700::1111 link https://internal.corp/x"}]}`),
	)
	if err != nil || first.Decision != DecisionRedact {
		t.Fatalf("first inspect: %#v %v", first, err)
	}
	if len(first.Redactions) == 0 {
		t.Fatal("first inspect produced no redactions")
	}
	second, err := engine.Inspect(
		t.Context(), policy, contract.ProtocolOpenAIChat, first.Body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.Decision != DecisionAllow || len(second.Redactions) != 0 {
		t.Fatalf("stand-ins were redacted again: %#v", second)
	}
	for _, finding := range second.SuppressedFindings {
		if finding.Suppression != SuppressionPlaceholder {
			t.Fatalf("suppression reason = %q", finding.Suppression)
		}
	}
}

func TestTokenPlaceholdersAreNotRedactedAgain(t *testing.T) {
	engine := mustTestEngine(t)
	policy := tokenPolicy()
	first, err := engine.Inspect(
		t.Context(),
		policy,
		contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"mail alice@example.com"}]}`),
	)
	if err != nil || first.Decision != DecisionRedact {
		t.Fatalf("first inspect: %#v %v", first, err)
	}
	second, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, first.Body)
	if err != nil {
		t.Fatal(err)
	}
	if second.Decision != DecisionAllow || len(second.Redactions) != 0 {
		t.Fatalf("token placeholder was redacted again: %#v", second)
	}
}

// TestAllocatorReusesItsOwnPlaceholderFromTheBody covers a model quoting a
// placeholder in reasoning, which is never restored, so the client resends it
// next turn beside the original value. Re-deriving gave the value a new name on
// each such turn: the model saw one secret under several names, an older name
// in a tool call was no longer restored, and the prefix cache broke at the
// first redacted span.
func TestAllocatorReusesItsOwnPlaceholderFromTheBody(t *testing.T) {
	key := testDerivationKey(5)
	for _, test := range []struct {
		name  string
		kind  Kind
		value string
		rule  func(Kind) KindRule
	}{
		{"secret token", KindCommonSecret, "sk-abcdefghijklmnop123456", tokenKindRule},
		{"email token", KindEmail, "alice@example.com", tokenKindRule},
		{"email stand-in", KindEmail, "alice@example.com", naturalKindRule},
		{"url stand-in", KindURL, "https://internal.corp/orders/91", naturalKindRule},
		{"ipv6 stand-in", KindIPAddress, "2606:4700::1111", naturalKindRule},
	} {
		t.Run(test.name, func(t *testing.T) {
			free := newPlaceholderAllocator(key, test.rule, nil)
			placeholder, _, err := free.allocate(test.kind, test.value)
			if err != nil {
				t.Fatal(err)
			}
			body := []byte(`{"input":"` + test.value + ` was ` + placeholder + `"}`)
			replay := newPlaceholderAllocator(key, test.rule, body)
			got, _, err := replay.allocate(test.kind, test.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != placeholder {
				t.Fatalf("placeholder %q became %q after appearing in the body", placeholder, got)
			}
		})
	}
}

// TestAllocatorRederivesIPv4StandInAlreadyInBody keeps the collision guard for
// the one pool small enough to be shared: an address from the IPv4
// documentation ranges may be another value's stand-in or documentation text,
// which restoring would rewrite.
func TestAllocatorRederivesIPv4StandInAlreadyInBody(t *testing.T) {
	key := testDerivationKey(5)
	free := newPlaceholderAllocator(key, naturalKindRule, nil)
	standIn, _, err := free.allocate(KindIPAddress, "10.4.7.9")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"input":"10.4.7.9, see also ` + standIn + `"}`)
	constrained := newPlaceholderAllocator(key, naturalKindRule, body)
	avoided, style, err := constrained.allocate(KindIPAddress, "10.4.7.9")
	if err != nil {
		t.Fatal(err)
	}
	if avoided == standIn {
		t.Fatalf("allocator reused an IPv4 stand-in already present in the body: %q", avoided)
	}
	if style != contract.PlaceholderStyleNatural || !isReservedTestNetAddress(avoided) {
		t.Fatalf("re-derived stand-in %q (%s) left the reserved namespace", avoided, style)
	}
}

// TestExhaustedNamespaceFallsBackToTokens pins the choice not to reuse a
// stand-in once a finite pool runs dry: two originals sharing one placeholder
// could not both be restored.
func TestExhaustedNamespaceFallsBackToTokens(t *testing.T) {
	allocator := newPlaceholderAllocator(testDerivationKey(6), naturalKindRule, nil)
	naturals := 0
	tokens := 0
	// The IPv4 test networks hold 768 addresses, so asking for more forces the
	// fallback.
	pool := len(testNetPrefixes) * testNetHostsPerPrefix
	for index := range pool + 20 {
		placeholder, style, err := allocator.allocate(
			KindIPAddress, "10.4."+itoa(index/256)+"."+itoa(index%256),
		)
		if err != nil {
			t.Fatalf("allocate %d: %v", index, err)
		}
		switch style {
		case contract.PlaceholderStyleNatural:
			naturals++
		case contract.PlaceholderStyleToken:
			tokens++
			if !strings.HasPrefix(placeholder, "<PRIVATE_IP_ADDRESS_") {
				t.Fatalf("fallback placeholder = %q", placeholder)
			}
		}
	}
	if naturals > pool {
		t.Fatalf("allocated %d stand-ins from a pool of %d", naturals, pool)
	}
	if tokens == 0 {
		t.Fatal("exhausting the IPv4 pool did not fall back to token placeholders")
	}
	if !allocator.exhaustedKinds()[KindIPAddress] {
		t.Fatal("exhaustion was not reported")
	}
}

func TestNaturalStandInsRestoreThroughExistingMapping(t *testing.T) {
	engine := mustTestEngine(t)
	result, err := engine.Inspect(
		t.Context(),
		naturalPolicy(),
		contract.ProtocolOpenAIResponses,
		[]byte(`{"input":"alice@example.com"}`),
	)
	if err != nil || len(result.Redactions) != 1 {
		t.Fatalf("inspect result=%#v error=%v", result, err)
	}
	stand := result.Redactions[0].Placeholder
	response := []byte(`{"echo":"mail ` + stand + `"}`)
	want := `{"echo":"mail alice@example.com"}`
	if got := string(RestorePlaceholders(response, result.Redactions)); got != want {
		t.Fatalf("restored response=%q want=%q", got, want)
	}
}

// TestTokenStyleReproducesPreRedesignShapes is the regression guard for
// operators who keep every kind on the token shape: the wire format they
// already depend on must not shift under them.
func TestTokenStyleReproducesPreRedesignShapes(t *testing.T) {
	engine := mustTestEngine(t)
	body := []byte(`{"messages":[{"role":"system","content":"api_key=abcdefghijklmnop123456"},` +
		`{"role":"user","content":"mail alice@example.com call +1-415-987-6543 ` +
		`card 4539 1488 0343 6467 host 10.4.7.9 link https://internal.corp/x"}]}`)
	policy := tokenPolicy()
	result, err := engine.Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, body)
	if err != nil || result.Decision != DecisionRedact {
		t.Fatalf("inspect: %#v %v", result, err)
	}
	wantPrefixes := map[Kind]string{
		KindCommonSecret: "<SECRET_",
		KindEmail:        "<PRIVATE_EMAIL_",
		KindPhone:        "<PRIVATE_PHONE_",
		KindPaymentCard:  "<PRIVATE_PAYMENT_CARD_",
		KindIPAddress:    "<PRIVATE_IP_ADDRESS_",
		KindURL:          "<PRIVATE_URL_",
	}
	seen := make(map[Kind]bool, len(wantPrefixes))
	for _, redaction := range result.Redactions {
		if redaction.Style != contract.PlaceholderStyleToken {
			t.Fatalf("style for %q = %q", redaction.Kind, redaction.Style)
		}
		prefix, known := wantPrefixes[redaction.Kind]
		if !known {
			t.Fatalf("unexpected kind %q", redaction.Kind)
		}
		if !strings.HasPrefix(redaction.Placeholder, prefix) {
			t.Fatalf("placeholder %q for %q", redaction.Placeholder, redaction.Kind)
		}
		seen[redaction.Kind] = true
	}
	for kind := range wantPrefixes {
		if !seen[kind] {
			t.Fatalf("kind %q was not redacted: %s", kind, result.Body)
		}
	}
	// A restore round-trip must recover every original value. The comparison is
	// on the decoded document because the rewrite re-serializes and therefore
	// reorders object keys.
	restored := RestorePlaceholders(result.Body, result.Redactions)
	var got, want any
	if err := json.Unmarshal(restored, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n%s\nwant:\n%s", restored, body)
	}
}

func naturalKindRule(kind Kind) KindRule {
	style := contract.PlaceholderStyleNatural
	if contract.PlaceholderStyleLocked(string(kind)) {
		style = contract.PlaceholderStyleToken
	}
	return KindRule{Enabled: true, Style: style}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
