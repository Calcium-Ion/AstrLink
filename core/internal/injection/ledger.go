// Package injection puts plugin blocks into requests: a resident block in
// the system prompt, each turn's block at the start of the turn's opening
// user message, and a ledger that puts earlier turns' blocks back where they
// were, so the prompt cache holds across turns.
package injection

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
)

// Ledger limits: 1024 sessions, 256 blocks a session and 64 MiB in all. A
// session past them drops its oldest blocks; the ledger drops the sessions
// used longest ago. A dropped block is not replayed, which costs one cache
// write, never a wrong request.
const (
	ledgerSessions         = 1024
	ledgerBlocksPerSession = 256
	ledgerBytes            = 64 << 20
)

// Ledger remembers, for each session, which block went at the start of which
// opening user message. Messages are known by an HMAC of their text under a
// key that lives only in this process, so the ledger holds no user text, and a
// restart empties it.
type Ledger struct {
	key      []byte
	mutex    sync.Mutex
	order    *list.List
	sessions map[string]*list.Element
	bytes    int
	// limits are the constants above; tests lower them.
	maxSessions, maxBlocks, maxBytes int
}

type ledgerSession struct {
	id     string
	blocks map[string]string
	// order lists the fingerprints oldest first.
	order []string
}

// NewLedger returns an empty ledger with a fresh key.
func NewLedger() *Ledger {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("injection: no randomness for the ledger key: " + err.Error())
	}
	return &Ledger{
		key: key, order: list.New(), sessions: map[string]*list.Element{},
		maxSessions: ledgerSessions, maxBlocks: ledgerBlocksPerSession, maxBytes: ledgerBytes,
	}
}

// Fingerprint names an opening user message by its text. Whitespace at the
// ends and line ending style do not change it.
func (ledger *Ledger) Fingerprint(text string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	mac := hmac.New(sha256.New, ledger.key)
	mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

// Remember records that block went at the start of the message with
// fingerprint in session.
func (ledger *Ledger) Remember(session, fingerprint, block string) {
	if session == "" || fingerprint == "" || block == "" {
		return
	}
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	entry := ledger.touch(session)
	if previous, found := entry.blocks[fingerprint]; found {
		ledger.bytes -= len(previous)
	} else {
		entry.order = append(entry.order, fingerprint)
	}
	entry.blocks[fingerprint] = block
	ledger.bytes += len(block)
	for len(entry.order) > ledger.maxBlocks {
		oldest := entry.order[0]
		entry.order = entry.order[1:]
		ledger.bytes -= len(entry.blocks[oldest])
		delete(entry.blocks, oldest)
	}
	ledger.evict(session)
}

// Forget drops the block of the message with fingerprint in session, so that
// a turn that settled on no block does not pick up an older one.
func (ledger *Ledger) Forget(session, fingerprint string) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	element, found := ledger.sessions[session]
	if !found {
		return
	}
	entry := element.Value.(*ledgerSession)
	block, found := entry.blocks[fingerprint]
	if !found {
		return
	}
	ledger.bytes -= len(block)
	delete(entry.blocks, fingerprint)
	entry.order = slices.DeleteFunc(entry.order, func(kept string) bool { return kept == fingerprint })
}

// Lookup returns the block that went at the start of the message with
// fingerprint in session.
func (ledger *Ledger) Lookup(session, fingerprint string) (string, bool) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	element, found := ledger.sessions[session]
	if !found {
		return "", false
	}
	ledger.order.MoveToFront(element)
	block, found := element.Value.(*ledgerSession).blocks[fingerprint]
	return block, found
}

func (ledger *Ledger) touch(session string) *ledgerSession {
	if element, found := ledger.sessions[session]; found {
		ledger.order.MoveToFront(element)
		return element.Value.(*ledgerSession)
	}
	entry := &ledgerSession{id: session, blocks: map[string]string{}}
	ledger.sessions[session] = ledger.order.PushFront(entry)
	return entry
}

// evict drops the sessions used longest ago until the ledger is within its
// limits, sparing keep, the session being written.
func (ledger *Ledger) evict(keep string) {
	for len(ledger.sessions) > ledger.maxSessions || ledger.bytes > ledger.maxBytes {
		element := ledger.order.Back()
		entry := element.Value.(*ledgerSession)
		if entry.id == keep {
			if len(ledger.sessions) == 1 {
				return
			}
			ledger.order.MoveToFront(element)
			continue
		}
		for _, block := range entry.blocks {
			ledger.bytes -= len(block)
		}
		ledger.order.Remove(element)
		delete(ledger.sessions, entry.id)
	}
}
