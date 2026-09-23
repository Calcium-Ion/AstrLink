package privacyworker

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	defaultCacheEntries  = 16_384
	defaultCacheFindings = 131_072
	// A segment reporting more findings than the engine accepts per request
	// fails there anyway; keeping it would only crowd out useful entries.
	maxCachedSegmentFindings = 4096
)

type segmentDigest [sha256.Size]byte

type cacheEntry struct {
	digest segmentDigest
	// Segment is always zero; spans index the segment's Value.
	findings []privacy.Finding
}

// detectionCache remembers what one model installation found in each segment.
// Agents resend the whole conversation every turn, so without it every turn
// re-inspects all earlier turns and a long session eventually cannot finish
// inside the worker timeout. Entries hold keyed digests and spans, never text,
// and live only in memory.
type detectionCache struct {
	mu          sync.Mutex
	hmacKey     []byte
	bound       modelKey
	boundSet    bool
	entries     map[segmentDigest]*list.Element
	order       *list.List
	findings    int
	maxEntries  int
	maxFindings int
}

func newDetectionCache() *detectionCache {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("privacy worker cache key: " + err.Error())
	}
	return &detectionCache{
		hmacKey:     key,
		entries:     make(map[segmentDigest]*list.Element),
		order:       list.New(),
		maxEntries:  defaultCacheEntries,
		maxFindings: defaultCacheFindings,
	}
}

// digest keys a segment by exactly the text the model would receive. The
// prefix is always included: a model that ignores it only loses sharing.
func (cache *detectionCache) digest(segment privacy.Segment) segmentDigest {
	mac := hmac.New(sha256.New, cache.hmacKey)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(segment.ContextPrefix)))
	mac.Write(length[:])
	mac.Write([]byte(segment.ContextPrefix))
	mac.Write([]byte(segment.Value))
	var digest segmentDigest
	copy(digest[:], mac.Sum(nil))
	return digest
}

// lookup returns a copy of the findings cached for digest under key. Spans
// that no longer fit value mean a corrupt entry; it is dropped as a miss.
func (cache *detectionCache) lookup(key modelKey, digest segmentDigest, value string) ([]privacy.Finding, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.bindLocked(key)
	element, exists := cache.entries[digest]
	if !exists {
		return nil, false
	}
	entry := element.Value.(*cacheEntry)
	for _, finding := range entry.findings {
		if finding.Start < 0 || finding.End <= finding.Start || finding.End > len(value) {
			cache.removeLocked(element)
			return nil, false
		}
	}
	cache.order.MoveToFront(element)
	return append([]privacy.Finding(nil), entry.findings...), true
}

func (cache *detectionCache) store(key modelKey, digest segmentDigest, findings []privacy.Finding) {
	if len(findings) > maxCachedSegmentFindings || len(findings) > cache.maxFindings {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.bindLocked(key)
	if element, exists := cache.entries[digest]; exists {
		cache.removeLocked(element)
	}
	stored := make([]privacy.Finding, len(findings))
	for index, finding := range findings {
		finding.Segment = 0
		stored[index] = finding
	}
	cache.entries[digest] = cache.order.PushFront(&cacheEntry{digest: digest, findings: stored})
	cache.findings += len(stored)
	for len(cache.entries) > cache.maxEntries || cache.findings > cache.maxFindings {
		cache.removeLocked(cache.order.Back())
	}
}

func (cache *detectionCache) clear() {
	cache.mu.Lock()
	cache.clearLocked()
	cache.mu.Unlock()
}

// Results belong to the installation that produced them. A different key,
// including an in-place identity or manifest change, empties the cache.
func (cache *detectionCache) bindLocked(key modelKey) {
	if cache.boundSet && cache.bound == key {
		return
	}
	cache.clearLocked()
	cache.bound = key
	cache.boundSet = true
}

func (cache *detectionCache) clearLocked() {
	clear(cache.entries)
	cache.order.Init()
	cache.findings = 0
	cache.boundSet = false
	cache.bound = modelKey{}
}

func (cache *detectionCache) removeLocked(element *list.Element) {
	entry := cache.order.Remove(element).(*cacheEntry)
	delete(cache.entries, entry.digest)
	cache.findings -= len(entry.findings)
}
