package console

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const (
	// A session ends after sessionIdleTimeout without a request, and
	// sessionLifetime after sign-in regardless. Restarting Core ends all.
	sessionIdleTimeout = 24 * time.Hour
	sessionLifetime    = 7 * 24 * time.Hour
	// maxSessions bounds memory; signing in beyond it ends the least
	// recently used session.
	maxSessions = 16
)

type session struct {
	created  time.Time
	lastSeen time.Time
}

// Sessions are the signed-in browser sessions, in memory and keyed by the
// SHA-256 of their token, so the token itself is not kept. The control API
// shares them to end the other sessions after a password change.
type Sessions struct {
	mu       sync.Mutex
	sessions map[[sha256.Size]byte]*session
}

func NewSessions() *Sessions {
	return &Sessions{sessions: make(map[[sha256.Size]byte]*session)}
}

func (store *Sessions) create(now time.Time) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	store.mu.Lock()
	defer store.mu.Unlock()
	var oldest [sha256.Size]byte
	var oldestSeen time.Time
	for key, existing := range store.sessions {
		if existing.expired(now) {
			delete(store.sessions, key)
			continue
		}
		if oldestSeen.IsZero() || existing.lastSeen.Before(oldestSeen) {
			oldest, oldestSeen = key, existing.lastSeen
		}
	}
	if len(store.sessions) >= maxSessions {
		delete(store.sessions, oldest)
	}
	store.sessions[sha256.Sum256([]byte(token))] = &session{created: now, lastSeen: now}
	return token, nil
}

// touch reports whether token names a live session and marks it used.
func (store *Sessions) touch(token string, now time.Time) bool {
	if token == "" {
		return false
	}
	key := sha256.Sum256([]byte(token))
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.sessions[key]
	if !ok {
		return false
	}
	if current.expired(now) {
		delete(store.sessions, key)
		return false
	}
	current.lastSeen = now
	return true
}

func (store *Sessions) remove(token string) {
	key := sha256.Sum256([]byte(token))
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.sessions, key)
}

// EndOtherSessions implements controlapi.ConsoleSessions.
func (store *Sessions) EndOtherSessions(request *http.Request) {
	var keep [sha256.Size]byte
	cookie, err := request.Cookie(SessionCookie)
	if err == nil && cookie.Value != "" {
		keep = sha256.Sum256([]byte(cookie.Value))
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for key := range store.sessions {
		if key != keep {
			delete(store.sessions, key)
		}
	}
}

func (current *session) expired(now time.Time) bool {
	return !now.Before(current.lastSeen.Add(sessionIdleTimeout)) || !now.Before(current.created.Add(sessionLifetime))
}
