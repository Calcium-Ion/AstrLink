package privacy

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	placeholderTokenHexLength   = 16
	placeholderNaturalHexLength = 12
	placeholderDerivationTrials = 64
)

// derivationKey is the process-lifetime HMAC key behind every placeholder
// suffix. Deriving suffixes from the plaintext instead of drawing fresh entropy
// makes a given value map to the same placeholder on every turn, which keeps a
// multi-turn conversation internally coherent and keeps the upstream prefix
// cache alive past the first redacted span.
//
// The value is HMAC'd rather than hashed bare because a bare digest of an email
// address or an IP is trivially reversed by enumeration. The key stays in
// memory for the life of the process: it is never persisted, never logged, and
// never leaves the machine. A restart rotates it, costing one cache miss.
var derivationKey = sync.OnceValue(func() []byte {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		panic("privacy: placeholder derivation key is unavailable: " + err.Error())
	}
	return key
})

var errNamespaceExhausted = errors.New("privacy: reserved placeholder namespace is exhausted")

// placeholderAllocator mints one placeholder per distinct (kind, value) pair.
//
// It guards three distinct collisions:
//
//   - Two different plaintexts deriving the same suffix. Resolved by re-deriving
//     with an incrementing counter folded into the HMAC input.
//   - A candidate that already occurs literally in the request. Restoring would
//     then rewrite genuine content, so the candidate is rejected and re-derived.
//   - A kind whose reserved namespace is finite (the test IP ranges, the
//     fictional phone block) running dry. The affected value falls back to a
//     token placeholder rather than reusing a stand-in, because two originals
//     sharing one placeholder cannot both be restored.
type placeholderAllocator struct {
	key       []byte
	resolve   func(Kind) KindRule
	body      []byte
	used      map[string]struct{}
	exhausted map[Kind]bool
}

func newPlaceholderAllocator(
	key []byte,
	resolve func(Kind) KindRule,
	body []byte,
) *placeholderAllocator {
	if len(key) == 0 {
		key = derivationKey()
	}
	if resolve == nil {
		resolve = func(Kind) KindRule {
			return KindRule{Enabled: true, Style: contract.PlaceholderStyleToken}
		}
	}
	return &placeholderAllocator{
		key:       key,
		resolve:   resolve,
		body:      body,
		used:      make(map[string]struct{}),
		exhausted: make(map[Kind]bool),
	}
}

// allocate returns the placeholder for one distinct (kind, value) pair together
// with the style actually used, which may differ from the configured style when
// a finite namespace is exhausted.
func (allocator *placeholderAllocator) allocate(
	kind Kind,
	value string,
) (string, contract.PlaceholderStyle, error) {
	if allocator == nil || len(allocator.key) == 0 {
		return "", "", ErrUnsafeRewrite
	}
	natural := allocator.resolve(kind).Style == contract.PlaceholderStyleNatural
	if natural && !allocator.exhausted[kind] {
		placeholder, err := allocator.allocateNatural(kind, value)
		switch {
		case err == nil:
			return placeholder, contract.PlaceholderStyleNatural, nil
		case errors.Is(err, errNamespaceExhausted):
			allocator.exhausted[kind] = true
		default:
			return "", "", err
		}
	}
	placeholder, err := allocator.allocateToken(kind, value)
	if err != nil {
		return "", "", err
	}
	return placeholder, contract.PlaceholderStyleToken, nil
}

// exhaustedKinds reports the kinds that had to fall back to token placeholders.
func (allocator *placeholderAllocator) exhaustedKinds() map[Kind]bool {
	if allocator == nil {
		return nil
	}
	return allocator.exhausted
}

func (allocator *placeholderAllocator) allocateToken(kind Kind, value string) (string, error) {
	for trial := range placeholderDerivationTrials {
		suffix := allocator.derive(kind, value, trial, placeholderTokenHexLength)
		candidate := withPlaceholderSuffix(replacementFor(kind), suffix)
		if allocator.reserve(candidate) {
			return candidate, nil
		}
	}
	return "", ErrUnsafeRewrite
}

func (allocator *placeholderAllocator) allocateNatural(kind Kind, value string) (string, error) {
	builder, supported := naturalPlaceholderBuilders[kind]
	if !supported {
		return "", errNamespaceExhausted
	}
	for trial := range placeholderDerivationTrials {
		suffix := allocator.derive(kind, value, trial, placeholderNaturalHexLength)
		candidate, ok := builder(value, suffix, trial)
		if !ok {
			return "", errNamespaceExhausted
		}
		if allocator.reserve(candidate) {
			return candidate, nil
		}
	}
	return "", errNamespaceExhausted
}

// reserve accepts a candidate only if no earlier value in this request took it
// and it does not already appear in the request body.
func (allocator *placeholderAllocator) reserve(candidate string) bool {
	if _, exists := allocator.used[candidate]; exists {
		return false
	}
	if bytes.Contains(allocator.body, []byte(candidate)) {
		return false
	}
	allocator.used[candidate] = struct{}{}
	return true
}

func (allocator *placeholderAllocator) derive(
	kind Kind,
	value string,
	trial int,
	hexLength int,
) string {
	mac := hmac.New(sha256.New, allocator.key)
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	mac.Write([]byte{0})
	mac.Write([]byte{byte(trial >> 8), byte(trial)})
	return hex.EncodeToString(mac.Sum(nil))[:hexLength]
}

func withPlaceholderSuffix(base, suffix string) string {
	if strings.HasPrefix(base, "<") && strings.HasSuffix(base, ">") {
		return base[:len(base)-1] + "_" + suffix + ">"
	}
	return base + "_" + suffix
}

// naturalPlaceholderDomain sits under the .invalid TLD that RFC 2606 reserves
// permanently: it can never be registered and is guaranteed not to resolve.
// Documentation domains such as example.com are deliberately avoided because
// IANA operates them and they answer real DNS queries, so an agent acting on
// one would reach a live host.
const naturalPlaceholderDomain = "private.invalid"

// naturalPlaceholderBuilders map a derived suffix to a syntactically valid
// stand-in drawn from a permanently reserved namespace. The second return value
// is false once a finite namespace cannot serve the request.
var naturalPlaceholderBuilders = map[Kind]func(value, suffix string, trial int) (string, bool){
	KindEmail: func(_, suffix string, _ int) (string, bool) {
		return "redacted-" + suffix + "@" + naturalPlaceholderDomain, true
	},
	KindURL: func(_, suffix string, _ int) (string, bool) {
		return "https://" + naturalPlaceholderDomain + "/r/" + suffix, true
	},
	KindIPAddress: naturalIPAddress,
	KindPhone:     naturalPhone,
	// A payment card stand-in is built to fail the Luhn checksum so it can never
	// coincide with an issuable card, which also keeps the detector from
	// matching it again on a later turn.
	KindPaymentCard: naturalPaymentCard,
	// An account stand-in carries the unassigned XX country code so it can never
	// be a real IBAN, for the same reason.
	KindAccount: naturalAccount,
}

// testNetPrefixes are the documentation ranges reserved by RFC 5737. They are
// guaranteed never to appear on the public internet.
var testNetPrefixes = []string{"203.0.113.", "192.0.2.", "198.51.100."}

const testNetHostsPerPrefix = 256

// naturalIPAddress keeps the address family of the original, because a v4
// literal replaced by a v6 one would break any surrounding syntax that assumed
// dotted quads.
func naturalIPAddress(value, suffix string, trial int) (string, bool) {
	if address := net.ParseIP(value); address != nil && address.To4() == nil {
		// RFC 3849 reserves 2001:db8::/32 for documentation. The space is large
		// enough that the derived suffix alone keeps collisions negligible. The
		// suffix is split into four-digit groups because a single group holds at
		// most 16 bits, and a longer run would not parse as an address at all.
		return "2001:db8::" + strings.Join(hextetGroups(suffix), ":"), true
	}
	index, ok := suffixIndex(suffix)
	if !ok {
		return "", false
	}
	total := len(testNetPrefixes) * testNetHostsPerPrefix
	if trial >= total {
		return "", false
	}
	slot := (index + trial) % total
	return testNetPrefixes[slot/testNetHostsPerPrefix] +
		strconv.Itoa(slot%testNetHostsPerPrefix), true
}

// fictionalPhoneCount covers the North American 555-0100 through 555-0199 range
// set aside for fictional use.
const fictionalPhoneCount = 100

// naturalPhone emits +1-555-555-01NN. The line range is the fictional NANP
// block, and 555 is additionally unassigned as an area code, so the number is
// unreachable twice over. The full ten digits matter: a shorter stand-in would
// not be a well-formed North American number, which is the whole reason a model
// copies it without being told to.
func naturalPhone(_, suffix string, trial int) (string, bool) {
	index, ok := suffixIndex(suffix)
	if !ok {
		return "", false
	}
	if trial >= fictionalPhoneCount {
		return "", false
	}
	return fmt.Sprintf("+1-555-555-01%02d", (index+trial)%fictionalPhoneCount), true
}

func naturalPaymentCard(_, suffix string, trial int) (string, bool) {
	index, ok := suffixIndex(suffix)
	if !ok {
		return "", false
	}
	return breakLuhn(fmt.Sprintf("4000000000000%03d", (index+trial)%1000)), true
}

func naturalAccount(_, suffix string, trial int) (string, bool) {
	index, ok := suffixIndex(suffix)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("XX00REDACTED%010d", (index+trial)%10_000_000_000), true
}

// breakLuhn perturbs the final digit until the checksum fails, so the result
// cannot be a valid card number.
func breakLuhn(digits string) string {
	body := []byte(digits)
	last := int(digits[len(digits)-1] - '0')
	for offset := range 10 {
		body[len(body)-1] = byte('0' + (last+offset)%10)
		if !validPaymentCard(string(body)) {
			return string(body)
		}
	}
	return string(body)
}

// hextetGroups splits a hex string into IPv6 groups of at most four digits.
func hextetGroups(suffix string) []string {
	groups := make([]string, 0, (len(suffix)+3)/4)
	for start := 0; start < len(suffix); start += 4 {
		end := min(start+4, len(suffix))
		groups = append(groups, suffix[start:end])
	}
	return groups
}

func suffixIndex(suffix string) (int, bool) {
	raw, err := hex.DecodeString(suffix)
	if err != nil || len(raw) < 4 {
		return 0, false
	}
	// Mask the top bit so the result stays positive on 32-bit platforms.
	return int(raw[0]&0x7f)<<24 | int(raw[1])<<16 | int(raw[2])<<8 | int(raw[3]), true
}

// isNaturalPlaceholder reports whether a value already belongs to one of the
// reserved stand-in namespaces. Without this check a stand-in that escaped
// restoration and returned in the client's history would be redacted again, and
// the original behind the outer placeholder could never be recovered.
func isNaturalPlaceholder(kind Kind, value string) bool {
	switch kind {
	case KindEmail:
		at := strings.LastIndex(value, "@")
		return at >= 0 && isReservedInvalidHost(value[at+1:])
	case KindURL:
		return isReservedInvalidHost(placeholderURLHost(value))
	case KindIPAddress:
		return isReservedTestNetAddress(value)
	case KindPhone:
		digits := string(decimalDigits(value))
		return len(digits) == 11 && strings.HasPrefix(digits, "155555501")
	case KindPaymentCard:
		return strings.HasPrefix(string(decimalDigits(value)), "4000000000000")
	case KindAccount:
		compact := strings.ToUpper(strings.ReplaceAll(value, " ", ""))
		return strings.HasPrefix(compact, "XX00REDACTED")
	default:
		return false
	}
}

// isTokenPlaceholder reports whether a value is already an opaque token
// placeholder, for any kind.
func isTokenPlaceholder(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "<") || !strings.HasSuffix(trimmed, ">") {
		return false
	}
	inner := trimmed[1 : len(trimmed)-1]
	return strings.HasPrefix(inner, "PRIVATE") || strings.HasPrefix(inner, "SECRET")
}

func isReservedInvalidHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == naturalPlaceholderDomain ||
		host == "invalid" || strings.HasSuffix(host, ".invalid")
}

func placeholderURLHost(value string) string {
	trimmed := value
	if index := strings.Index(trimmed, "://"); index >= 0 {
		trimmed = trimmed[index+3:]
	}
	if index := strings.IndexAny(trimmed, "/?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	if index := strings.LastIndex(trimmed, "@"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		return host
	}
	return trimmed
}

func isReservedTestNetAddress(value string) bool {
	address := net.ParseIP(value)
	if address == nil {
		return false
	}
	for _, block := range reservedTestNetBlocks() {
		if block.Contains(address) {
			return true
		}
	}
	return false
}

var reservedTestNetBlocks = sync.OnceValue(func() []*net.IPNet {
	blocks := make([]*net.IPNet, 0, 4)
	for _, notation := range []string{
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32",
	} {
		if _, block, err := net.ParseCIDR(notation); err == nil {
			blocks = append(blocks, block)
		}
	}
	return blocks
})
