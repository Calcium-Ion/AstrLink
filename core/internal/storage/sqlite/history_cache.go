package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
)

type historyReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Both counters belong to a pinned connection. data_version detects commits
// from other connections/processes; total_changes detects that connection's
// own writes. Comparing data_version across pooled connections is invalid.
type historySnapshot struct {
	connection       any
	version, changes int64
}

func historyVersion(ctx context.Context, conn *sql.Conn) (historySnapshot, error) {
	var snapshot historySnapshot
	if err := conn.Raw(func(driverConn any) error {
		snapshot.connection = driverConn
		return nil
	}); err != nil {
		return snapshot, err
	}
	err := conn.QueryRowContext(ctx, `SELECT data_version, total_changes() FROM pragma_data_version`).Scan(&snapshot.version, &snapshot.changes)
	return snapshot, err
}

type historyCacheEntry struct {
	snapshot historySnapshot
	key      string
	data     []byte
}

// Serialized values keep callers from mutating cached slices or pointers.
// Bounds apply even when history contains many distinct models or filters.
type historyCache struct {
	mu      sync.Mutex
	entries []historyCacheEntry
}

func (cache *historyCache) get(snapshot historySnapshot, key string) []byte {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for i := range cache.entries {
		entry := &cache.entries[i]
		if entry.snapshot == snapshot && entry.key == key {
			return entry.data
		}
	}
	return nil
}

func (cache *historyCache) put(snapshot historySnapshot, key string, data []byte) {
	if len(data) > 1<<20 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	kept := cache.entries[:0]
	for _, entry := range cache.entries {
		if entry.snapshot.connection == snapshot.connection && (entry.snapshot != snapshot || entry.key == key) {
			continue
		}
		kept = append(kept, entry)
	}
	clear(cache.entries[len(kept):])
	cache.entries = kept
	entry := historyCacheEntry{snapshot: snapshot, key: key, data: data}
	if len(cache.entries) == 8 {
		copy(cache.entries, cache.entries[1:])
		cache.entries[7] = entry
	} else {
		cache.entries = append(cache.entries, entry)
	}
}

func cachedHistoryRead[T any](ctx context.Context, db *sql.DB, cache *historyCache, options any, load func(*sql.Conn) (T, error)) (T, error) {
	var zero T
	key, err := json.Marshal(options)
	if err != nil {
		return zero, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return zero, err
	}
	defer conn.Close()
	before, err := historyVersion(ctx, conn)
	if err != nil {
		return zero, err
	}
	if data := cache.get(before, string(key)); data != nil {
		var result T
		if err := json.Unmarshal(data, &result); err != nil {
			return zero, err
		}
		return result, nil
	}
	result, err := load(conn)
	if err != nil {
		return result, err
	}
	// A commit during a read must not publish a mixed or obsolete snapshot.
	// The result still has the same consistency as the uncached read.
	if after, err := historyVersion(ctx, conn); err == nil && before == after {
		if data, err := json.Marshal(result); err == nil {
			cache.put(before, string(key), data)
		}
	}
	return result, nil
}
