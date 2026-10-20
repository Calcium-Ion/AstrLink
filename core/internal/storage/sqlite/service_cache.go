package sqlite

import (
	"maps"
	"slices"
	"sync"

	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	maxServiceCacheEntries = 256
	maxServiceCacheBytes   = 16 << 20
)

type serviceDocumentEntry struct {
	document string
	record   storagecontract.ServiceRecord
	used     uint64
}

// Cache only parsing, never the database read. Comparing the complete freshly
// read document makes updates from sync and other SQLite connections visible
// immediately, without coupling invalidation to frequent audit writes.
type serviceDocumentCache struct {
	mu      sync.Mutex
	entries map[string]serviceDocumentEntry
	bytes   int
	clock   uint64
}

func (cache *serviceDocumentCache) decode(id, document string) (storagecontract.ServiceRecord, error) {
	cache.mu.Lock()
	if entry, ok := cache.entries[id]; ok && entry.document == document {
		cache.clock++
		entry.used = cache.clock
		cache.entries[id] = entry
		cache.mu.Unlock()
		return cloneCachedServiceRecord(entry.record), nil
	}
	cache.mu.Unlock()

	record, err := decodeServiceRecord(id, []byte(document))
	if err != nil || len(document) > maxServiceCacheBytes {
		return record, err
	}
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]serviceDocumentEntry)
	}
	if old, ok := cache.entries[id]; ok {
		cache.bytes -= len(old.document)
		delete(cache.entries, id)
	}
	for len(cache.entries) >= maxServiceCacheEntries || cache.bytes+len(document) > maxServiceCacheBytes {
		var oldestID string
		var oldest uint64
		for key, entry := range cache.entries {
			if oldestID == "" || entry.used < oldest {
				oldestID, oldest = key, entry.used
			}
		}
		cache.bytes -= len(cache.entries[oldestID].document)
		delete(cache.entries, oldestID)
	}
	cache.clock++
	cache.entries[id] = serviceDocumentEntry{document: document, record: record, used: cache.clock}
	cache.bytes += len(document)
	cache.mu.Unlock()
	return cloneCachedServiceRecord(record), nil
}

// Cached documents are immutable. Copy all reference-bearing fields before
// returning a record so callers can edit configuration without changing the
// cache or another in-flight request. Strings and time.Location are immutable.
func cloneCachedServiceRecord(record storagecontract.ServiceRecord) storagecontract.ServiceRecord {
	service := &record.Service
	service.Models = slices.Clone(service.Models)
	service.Capabilities = slices.Clone(service.Capabilities)
	service.ModelRedirects = slices.Clone(service.ModelRedirects)
	service.ModelProtocols = slices.Clone(service.ModelProtocols)
	service.Proxy = cloneServicePointer(service.Proxy)
	service.ResponsesWebSocketEnabled = cloneServicePointer(service.ResponsesWebSocketEnabled)
	service.HTTP = cloneServicePointer(service.HTTP)
	service.FailurePolicy = cloneServicePointer(service.FailurePolicy)
	if policy := service.FailurePolicy; policy != nil {
		policy.HTTPStatus = maps.Clone(policy.HTTPStatus)
		policy.ResponseStartTimeoutSeconds = cloneServicePointer(policy.ResponseStartTimeoutSeconds)
		policy.ThinkingSignatureRecovery = cloneServicePointer(policy.ThinkingSignatureRecovery)
		policy.OpenAIReasoningRecovery = cloneServicePointer(policy.OpenAIReasoningRecovery)
		policy.OpenAIFunctionOutputRecovery = cloneServicePointer(policy.OpenAIFunctionOutputRecovery)
	}
	service.Subscription = cloneServicePointer(service.Subscription)
	if subscription := service.Subscription; subscription != nil {
		subscription.TokenExpiresAt = cloneServicePointer(subscription.TokenExpiresAt)
		subscription.LastRefreshAt = cloneServicePointer(subscription.LastRefreshAt)
		subscription.LastError = cloneServicePointer(subscription.LastError)
		subscription.Risk = cloneServicePointer(subscription.Risk)
		if subscription.Risk != nil {
			subscription.Risk.PausedUntil = cloneServicePointer(subscription.Risk.PausedUntil)
		}
	}
	return record
}

func cloneServicePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
