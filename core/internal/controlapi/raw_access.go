package controlapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const (
	// RawAccessPath lists agent requests for raw audit content awaiting a
	// desktop decision and the timed grants still running;
	// RawAccessPath/{grant_id}/decision decides one, and DELETE
	// RawAccessPath/{grant_id} revokes a running grant.
	RawAccessPath = "/control/v1/audit/raw-access"

	// RawGrantPath is the agent's view of its own grant: GET reads its state
	// and DELETE revokes it. Both identify the grant by RawGrantHeader.
	RawGrantPath = "/control/v1/audit/raw-grant"

	// RawGrantHeader carries a grant token on a full audit read and on
	// RawGrantPath. Control-plane only: the X-AstrLink-* namespace never
	// leaves the gateway.
	RawGrantHeader = "X-AstrLink-Raw-Grant"

	rawGrantPendingTTL = 10 * time.Minute
	// rawGrantOnceTTL is how long an approved once grant waits for its read.
	rawGrantOnceTTL = 15 * time.Minute
	// rawGrantFinishedTTL keeps a finished grant so a late read learns why
	// it failed.
	rawGrantFinishedTTL  = 15 * time.Minute
	maxPendingRawGrants  = 16
	maxRawReasonRunes    = 500
	maxRawClientRunes    = 64
	defaultRawClientName = "agent"
)

var (
	// ErrRawProofRequired means a decision or unlock carried no usable proof.
	ErrRawProofRequired = errors.New("raw access proof is required")
	// ErrRawPasswordInvalid means the raw password did not open the key.
	ErrRawPasswordInvalid = errors.New("raw password is invalid")
	// ErrRawNotConfigured means no raw password protects a raw sealing key
	// yet, so nothing raw can be opened.
	ErrRawNotConfigured = errors.New("raw sealing is not configured")
)

// RawBackoffError is returned while repeated wrong passwords are throttled.
type RawBackoffError struct{ Remaining time.Duration }

func (err *RawBackoffError) Error() string {
	return fmt.Sprintf("raw password attempts are throttled for %s", err.Remaining)
}

// RawProof is what a caller offers to open the raw sealing key: the raw
// password. Password bytes belong to the caller, which clears them.
type RawProof struct {
	Password []byte
}

// Empty reports whether no proof was offered at all.
func (proof RawProof) Empty() bool { return len(proof.Password) == 0 }

// RawVaultStatus describes raw sealing without exposing key material.
type RawVaultStatus struct {
	// Configured is true once a raw sealing key exists.
	Configured bool
	// PasswordSet is true when a password envelope exists. Until then raw
	// captures are not kept and nothing raw is readable.
	PasswordSet bool
	// KeyVerified is false while the stored public key fails its MAC; raw
	// captures are then not kept until a proof confirms it.
	KeyVerified bool
	// Unlocked is true while the operator's unlock session lasts.
	Unlocked bool
	// UnlockExpiresAt is when the idle unlock session ends.
	UnlockExpiresAt *time.Time
	// RetryAfter is how long wrong-password backoff still refuses proofs.
	RetryAfter time.Duration
	// KeyFingerprint is the hex SHA-256 of the stored public key, or empty
	// before a key exists. A change keeps it; a reset replaces it.
	KeyFingerprint string
}

// RawKeyOpener unwraps the per-part keys of raw-sealed audit parts. The
// returned key belongs to the caller, which clears it after use.
type RawKeyOpener interface {
	OpenBlobKey(storage.AuditBlob) ([]byte, error)
}

// RawKeyHolder keeps an opened private key until Close zeroes it.
type RawKeyHolder interface {
	RawKeyOpener
	Close()
}

// RawVault guards the raw sealing private key. A nil vault means raw
// sealing is unavailable: raw parts are withheld from every reader, and
// agents cannot ask for raw content.
type RawVault interface {
	Status(context.Context) (RawVaultStatus, error)
	// UnlockedOpener returns the operator's unlock session, extending its
	// idle deadline, or false when locked.
	UnlockedOpener() (RawKeyOpener, bool)
	// WithProof opens the private key with a fresh proof for one callback.
	// It never uses or extends the unlock session.
	WithProof(context.Context, RawProof, func(RawKeyOpener) error) error
	// HoldKey opens the private key for a caller that keeps it past this
	// call: with proof when one is given, otherwise from the unlock session,
	// which it does not extend. It returns ErrRawProofRequired when no proof
	// is given and the vault is locked.
	HoldKey(context.Context, RawProof) (RawKeyHolder, error)
}

type rawGrantStatus string

const (
	rawGrantPending  rawGrantStatus = "pending"
	rawGrantApproved rawGrantStatus = "approved"
	rawGrantDenied   rawGrantStatus = "denied"
	rawGrantExpired  rawGrantStatus = "expired"
	rawGrantConsumed rawGrantStatus = "consumed"
	rawGrantRevoked  rawGrantStatus = "revoked"
)

type rawGrantMode string

const (
	rawGrantOnce      rawGrantMode = "once"
	rawGrantWindow5m  rawGrantMode = "window_5m"
	rawGrantWindow1h  rawGrantMode = "window_1h"
	rawGrantDenyMode  rawGrantMode = "deny"
	rawGrantModeUsage              = "must be once, window_5m, window_1h, or deny"
)

// window is how long a timed grant lasts, or zero for once and deny.
func (mode rawGrantMode) window() time.Duration {
	switch mode {
	case rawGrantWindow5m:
		return 5 * time.Minute
	case rawGrantWindow1h:
		return time.Hour
	default:
		return 0
	}
}

// Grant scopes on the wire: a once grant reads the request it was asked
// for; a timed grant reads every request until it ends.
const (
	rawGrantScopeRequest = "request"
	rawGrantScopeAll     = "all_requests"
)

// rawGrant is one agent request for raw parts. It lives only in Core
// memory; a restart invalidates every grant.
type rawGrant struct {
	id         string
	tokenHash  [sha256.Size]byte
	requestID  contract.RequestID
	status     rawGrantStatus
	mode       rawGrantMode
	reason     string
	clientName string
	createdAt  time.Time
	expiresAt  time.Time
	// keys holds a once grant's unwrapped per-part keys for its request.
	// Parts still sealed by the audit key have no entry.
	keys map[storage.AuditDirection][]byte
	// holder keeps a timed grant's private key until the grant ends; timer
	// zeroes it on expiry.
	holder RawKeyHolder
	timer  *time.Timer
}

// end moves a live grant to status and zeroes what it holds.
func (grant *rawGrant) end(status rawGrantStatus, now time.Time) {
	grant.status = status
	if now.Before(grant.expiresAt) {
		grant.expiresAt = now
	}
	clearRawKeys(grant.keys)
	if grant.holder != nil {
		grant.holder.Close()
		grant.holder = nil
	}
	if grant.timer != nil {
		grant.timer.Stop()
		grant.timer = nil
	}
}

func (grant *rawGrant) live() bool {
	return grant.status == rawGrantPending || grant.status == rawGrantApproved
}

func (grant *rawGrant) timed() bool { return grant.mode.window() > 0 }

// RawAccessGrant is the wire shape of one grant, without its token.
type RawAccessGrant struct {
	GrantID    string             `json:"grant_id"`
	RequestID  contract.RequestID `json:"request_id"`
	Status     string             `json:"status"`
	Decision   string             `json:"decision,omitempty"`
	Scope      string             `json:"scope,omitempty"`
	Reason     string             `json:"reason"`
	ClientName string             `json:"client_name"`
	CreatedAt  time.Time          `json:"created_at"`
	ExpiresAt  time.Time          `json:"expires_at"`
}

func (grant *rawGrant) view() RawAccessGrant {
	view := RawAccessGrant{
		GrantID: grant.id, RequestID: grant.requestID, Status: string(grant.status),
		Reason: grant.reason, ClientName: grant.clientName,
		CreatedAt: grant.createdAt, ExpiresAt: grant.expiresAt,
	}
	if grant.mode != "" {
		view.Decision = string(grant.mode)
	}
	switch {
	case grant.timed():
		view.Scope = rawGrantScopeAll
	case grant.mode == rawGrantOnce:
		view.Scope = rawGrantScopeRequest
	}
	return view
}

var (
	errRawGrantInvalid    = errors.New("raw grant is unknown, expired, revoked, or for another request")
	errRawGrantPending    = errors.New("raw grant is awaiting a decision")
	errRawGrantDenied     = errors.New("raw grant was denied")
	errRawGrantNotFound   = errors.New("raw grant not found")
	errRawGrantNotPending = errors.New("raw grant is no longer pending")
	errRawGrantNotActive  = errors.New("raw grant is not running")
	errRawGrantsLimited   = errors.New("too many raw grants await a decision")
)

type rawGrantManager struct {
	mu     sync.Mutex
	grants map[string]*rawGrant
	now    func() time.Time
}

func newRawGrantManager() *rawGrantManager {
	return &rawGrantManager{grants: map[string]*rawGrant{}, now: time.Now}
}

func hashRawGrantToken(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

// expireLocked ends grants whose time ran out and forgets finished ones.
func (manager *rawGrantManager) expireLocked(now time.Time) {
	for id, grant := range manager.grants {
		if grant.live() && !now.Before(grant.expiresAt) {
			grant.end(rawGrantExpired, now)
		}
		if !grant.live() && now.Sub(grant.expiresAt) > rawGrantFinishedTTL {
			delete(manager.grants, id)
		}
	}
}

// expireTimed zeroes a timed grant's key as soon as its window closes,
// rather than at the next request that looks.
func (manager *rawGrantManager) expireTimed() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked(manager.now().UTC())
}

func (manager *rawGrantManager) create(
	requestID contract.RequestID,
	reason, clientName string,
) (RawAccessGrant, string, error) {
	var idBytes [8]byte
	var tokenBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return RawAccessGrant{}, "", err
	}
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return RawAccessGrant{}, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	clear(tokenBytes[:])
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	manager.expireLocked(now)
	// A client asking again for the same request replaces its earlier
	// request, so a retrying agent holds one place in the queue. Approved
	// grants do not count: each already took an operator decision.
	pending := 0
	for _, grant := range manager.grants {
		if grant.status != rawGrantPending {
			continue
		}
		if grant.requestID == requestID && grant.clientName == clientName {
			grant.end(rawGrantExpired, now)
			continue
		}
		pending++
	}
	if pending >= maxPendingRawGrants {
		return RawAccessGrant{}, "", errRawGrantsLimited
	}
	grant := &rawGrant{
		id:         "rawgrant_" + hex.EncodeToString(idBytes[:]),
		tokenHash:  hashRawGrantToken(token),
		requestID:  requestID,
		status:     rawGrantPending,
		reason:     reason,
		clientName: clientName,
		createdAt:  now,
		expiresAt:  now.Add(rawGrantPendingTTL),
	}
	manager.grants[grant.id] = grant
	return grant.view(), token, nil
}

// list returns the grants matching keep, oldest first.
func (manager *rawGrantManager) list(keep func(*rawGrant) bool) []RawAccessGrant {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked(manager.now().UTC())
	items := []RawAccessGrant{}
	for _, grant := range manager.grants {
		if keep(grant) {
			items = append(items, grant.view())
		}
	}
	sortRawGrants(items)
	return items
}

func (manager *rawGrantManager) pending() []RawAccessGrant {
	return manager.list(func(grant *rawGrant) bool { return grant.status == rawGrantPending })
}

// active returns the timed grants still running.
func (manager *rawGrantManager) active() []RawAccessGrant {
	return manager.list(isActiveRawGrant)
}

func isActiveRawGrant(grant *rawGrant) bool {
	return grant.status == rawGrantApproved && grant.timed()
}

func (manager *rawGrantManager) count(keep func(*rawGrant) bool) int {
	if manager == nil {
		return 0
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked(manager.now().UTC())
	count := 0
	for _, grant := range manager.grants {
		if keep(grant) {
			count++
		}
	}
	return count
}

func (manager *rawGrantManager) pendingCount() int {
	return manager.count(func(grant *rawGrant) bool { return grant.status == rawGrantPending })
}

func (manager *rawGrantManager) activeCount() int {
	return manager.count(isActiveRawGrant)
}

func sortRawGrants(items []RawAccessGrant) {
	for index := 1; index < len(items); index++ {
		for cursor := index; cursor > 0 && items[cursor].CreatedAt.Before(items[cursor-1].CreatedAt); cursor-- {
			items[cursor], items[cursor-1] = items[cursor-1], items[cursor]
		}
	}
}

// pendingGrant returns a copy of a grant still awaiting a decision.
func (manager *rawGrantManager) pendingGrant(id string) (RawAccessGrant, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked(manager.now().UTC())
	grant, ok := manager.grants[id]
	if !ok {
		return RawAccessGrant{}, errRawGrantNotFound
	}
	if grant.status != rawGrantPending {
		return grant.view(), errRawGrantNotPending
	}
	return grant.view(), nil
}

// decide settles a pending grant. A once grant takes over keys; a timed
// grant takes over holder. Whatever is not taken over is zeroed.
func (manager *rawGrantManager) decide(
	id string,
	mode rawGrantMode,
	keys map[storage.AuditDirection][]byte,
	holder RawKeyHolder,
) (RawAccessGrant, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	manager.expireLocked(now)
	grant, ok := manager.grants[id]
	if !ok || grant.status != rawGrantPending {
		clearRawKeys(keys)
		if holder != nil {
			holder.Close()
		}
		if !ok {
			return RawAccessGrant{}, errRawGrantNotFound
		}
		return grant.view(), errRawGrantNotPending
	}
	grant.mode = mode
	switch {
	case mode == rawGrantDenyMode:
		grant.status = rawGrantDenied
		clearRawKeys(keys)
		if holder != nil {
			holder.Close()
		}
	case mode.window() > 0:
		clearRawKeys(keys)
		grant.status = rawGrantApproved
		grant.expiresAt = now.Add(mode.window())
		grant.holder = holder
		grant.timer = time.AfterFunc(mode.window(), manager.expireTimed)
	default:
		if holder != nil {
			holder.Close()
		}
		grant.status = rawGrantApproved
		grant.expiresAt = now.Add(rawGrantOnceTTL)
		grant.keys = keys
	}
	return grant.view(), nil
}

// revokeAll ends every grant still pending or approved and zeroes what it
// holds: on agent access turned off, a manual lock, a password change or
// reset, and shutdown.
func (manager *rawGrantManager) revokeAll() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	for _, grant := range manager.grants {
		if grant.live() {
			grant.end(rawGrantRevoked, now)
		}
	}
	manager.expireLocked(now)
}

// revoke ends one running timed grant by id, for the operator.
func (manager *rawGrantManager) revoke(id string) (RawAccessGrant, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	manager.expireLocked(now)
	grant, ok := manager.grants[id]
	if !ok {
		return RawAccessGrant{}, errRawGrantNotFound
	}
	if !isActiveRawGrant(grant) {
		return grant.view(), errRawGrantNotActive
	}
	grant.end(rawGrantRevoked, now)
	return grant.view(), nil
}

// revokeToken ends the grant a token names, for whoever holds it. A grant
// that already ended is returned as it is; revoked reports whether this
// call ended it.
func (manager *rawGrantManager) revokeToken(token string) (RawAccessGrant, bool, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	now := manager.now().UTC()
	manager.expireLocked(now)
	grant := manager.byTokenLocked(token)
	if grant == nil {
		return RawAccessGrant{}, false, errRawGrantInvalid
	}
	if !grant.live() {
		return grant.view(), false, nil
	}
	grant.end(rawGrantRevoked, now)
	return grant.view(), true, nil
}

// status returns the grant a token names.
func (manager *rawGrantManager) status(token string) (RawAccessGrant, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.expireLocked(manager.now().UTC())
	grant := manager.byTokenLocked(token)
	if grant == nil {
		return RawAccessGrant{}, errRawGrantInvalid
	}
	return grant.view(), nil
}

func (manager *rawGrantManager) byTokenLocked(token string) *rawGrant {
	hash := hashRawGrantToken(token)
	for _, grant := range manager.grants {
		if grant.tokenHash == hash {
			return grant
		}
	}
	return nil
}

// rawGrantLease is one claimed read. A once lease carries copies of its
// part keys, which the reader clears; a timed lease borrows its grant's
// opener, which stops opening once the grant ends.
type rawGrantLease struct {
	grant  RawAccessGrant
	keys   map[storage.AuditDirection][]byte
	opener RawKeyOpener
}

func (lease *rawGrantLease) release() {
	if lease != nil {
		clearRawKeys(lease.keys)
	}
}

func (manager *rawGrantManager) lookupLocked(token string, requestID contract.RequestID) (*rawGrant, error) {
	manager.expireLocked(manager.now().UTC())
	grant := manager.byTokenLocked(token)
	if grant == nil {
		return nil, errRawGrantInvalid
	}
	// Only a running timed grant reaches past the request it was asked for.
	if grant.requestID != requestID && !isActiveRawGrant(grant) {
		return nil, errRawGrantInvalid
	}
	switch grant.status {
	case rawGrantPending:
		return grant, errRawGrantPending
	case rawGrantDenied:
		return grant, errRawGrantDenied
	case rawGrantApproved:
		return grant, nil
	default:
		return grant, errRawGrantInvalid
	}
}

// check validates a grant token for a request without spending a read.
func (manager *rawGrantManager) check(token string, requestID contract.RequestID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	_, err := manager.lookupLocked(token, requestID)
	return err
}

// claim takes one read. A once grant is consumed and its keys zeroed here,
// before any content is opened, so two concurrent reads cannot both win. A
// timed grant reads any request, as often as asked, until it ends.
func (manager *rawGrantManager) claim(token string, requestID contract.RequestID) (*rawGrantLease, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	grant, err := manager.lookupLocked(token, requestID)
	if err != nil {
		return nil, err
	}
	lease := &rawGrantLease{grant: grant.view()}
	lease.grant.RequestID = requestID
	if grant.timed() {
		lease.opener = grant.holder
		return lease, nil
	}
	lease.keys = make(map[storage.AuditDirection][]byte, len(grant.keys))
	for direction, key := range grant.keys {
		lease.keys[direction] = append([]byte(nil), key...)
	}
	grant.end(rawGrantConsumed, manager.now().UTC())
	return lease, nil
}

func clearRawKeys(keys map[storage.AuditDirection][]byte) {
	for direction, key := range keys {
		clear(key)
		delete(keys, direction)
	}
}

func (handler *Handler) registerRawAccessRoutes() {
	handler.mux.HandleFunc(RawAccessPath, handler.authenticated(handler.listRawAccess, RoleOperator))
	handler.mux.HandleFunc(RawAccessPath+"/", handler.authenticated(handler.rawAccessItem, RoleOperator))
	// The token holder reads its grant's state and may give the grant up;
	// the resource answers nothing else.
	handler.mux.HandleFunc(RawGrantPath, handler.authenticatedBy(handler.rawGrantResource, func(*http.Request) Role { return RoleObserver }))
}

// RawAccessList is the wire shape of GET RawAccessPath.
type RawAccessList struct {
	// Items are the requests awaiting a decision, oldest first.
	Items []RawAccessGrant `json:"items"`
	// Active are the timed grants still running, oldest first.
	Active []RawAccessGrant `json:"active"`
	// Unlocked is true while the operator's unlock session lasts, so an
	// approval needs no password.
	Unlocked bool `json:"unlocked"`
}

func (handler *Handler) listRawAccess(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	status, err := handler.rawVaultStatus(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing state is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, RawAccessList{
		Items: handler.rawGrants.pending(), Active: handler.rawGrants.active(), Unlocked: status.Unlocked,
	})
}

func (handler *Handler) rawAccessItem(writer http.ResponseWriter, request *http.Request) {
	rest := strings.TrimPrefix(request.URL.Path, RawAccessPath+"/")
	id, action, found := strings.Cut(rest, "/")
	switch {
	case id == "" || strings.Contains(action, "/"):
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
	case !found:
		if request.Method != http.MethodDelete {
			writer.Header().Set("Allow", http.MethodDelete)
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only DELETE is allowed")
			return
		}
		handler.revokeRawAccess(writer, id)
	case action != "decision":
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
	case request.Method != http.MethodPost:
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
	default:
		handler.decideRawAccess(writer, request, id)
	}
}

// revokeRawAccess ends a running timed grant from the desktop.
func (handler *Handler) revokeRawAccess(writer http.ResponseWriter, grantID string) {
	revoked, err := handler.rawGrants.revoke(grantID)
	switch {
	case errors.Is(err, errRawGrantNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "raw access grant not found")
		return
	case errors.Is(err, errRawGrantNotActive):
		writeError(writer, http.StatusConflict, "raw_access_not_active", "raw access grant already ended")
		return
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "raw_access_failed", "raw access grant could not be revoked")
		return
	}
	handler.observers.noteRawEvent(RawAccessEventRevoked, revoked)
	writeJSON(writer, http.StatusOK, revoked)
}

// rawGrantResource lets the holder of a grant token read its state or
// revoke it. Revoking takes no password: it only gives access up.
func (handler *Handler) rawGrantResource(writer http.ResponseWriter, request *http.Request) {
	token := strings.TrimSpace(request.Header.Get(RawGrantHeader))
	switch request.Method {
	case http.MethodGet:
		if token == "" {
			writeRawGrantError(writer, errRawGrantInvalid)
			return
		}
		enabled, err := handler.agentRawAccessEnabled(request.Context())
		if err != nil {
			handler.writeAuditSettingsStoreError(writer, err)
			return
		}
		if !enabled {
			writeError(writer, http.StatusConflict, "raw_access_disabled", "agent raw access requests are turned off")
			return
		}
		grant, err := handler.rawGrants.status(token)
		if !writeRawGrantError(writer, err) {
			return
		}
		writeJSON(writer, http.StatusOK, grant)
	case http.MethodDelete:
		if token == "" {
			writeRawGrantError(writer, errRawGrantInvalid)
			return
		}
		grant, revoked, err := handler.rawGrants.revokeToken(token)
		if !writeRawGrantError(writer, err) {
			return
		}
		if revoked {
			handler.observers.noteRawEvent(RawAccessEventRevoked, grant)
		}
		writeJSON(writer, http.StatusOK, grant)
	default:
		writer.Header().Set("Allow", "GET, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and DELETE are allowed")
	}
}

// RevokeRawGrants ends every agent grant and zeroes the keys they hold. Core
// calls it on shutdown.
func (handler *Handler) RevokeRawGrants() {
	if handler != nil {
		handler.rawGrants.revokeAll()
	}
}

type rawAccessRequestBody struct {
	Reason     string `json:"reason"`
	ClientName string `json:"client_name"`
}

type rawAccessCreated struct {
	RawAccessGrant
	GrantToken string `json:"grant_token"`
}

// requestRawAccess files an agent's request for one request's raw parts.
// The returned token is useless until the desktop approves.
func (handler *Handler) requestRawAccess(writer http.ResponseWriter, request *http.Request, rawID string) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	id, ok := parseRequestIDSegment(writer, rawID)
	if !ok {
		return
	}
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var body rawAccessRequestBody
	if !decodeControlJSON(writer, request, &body) {
		return
	}
	reason := strings.TrimSpace(stripControlRunes(body.Reason))
	if reason == "" || utf8.RuneCountInString(reason) > maxRawReasonRunes {
		writeValidationFailed(writer, "reason is required", []errorDetail{{
			Field: "reason", Reason: fmt.Sprintf("must contain 1 to %d characters", maxRawReasonRunes),
		}})
		return
	}
	clientName := strings.TrimSpace(stripControlRunes(body.ClientName))
	if clientName == "" {
		clientName = defaultRawClientName
	}
	if utf8.RuneCountInString(clientName) > maxRawClientRunes {
		writeValidationFailed(writer, "client_name is too long", []errorDetail{{
			Field: "client_name", Reason: fmt.Sprintf("must contain at most %d characters", maxRawClientRunes),
		}})
		return
	}
	if _, err := handler.requestRecords.GetRequestRecord(request.Context(), id); err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if !handler.writeRawAccessAvailability(writer, request) {
		return
	}
	grant, token, err := handler.rawGrants.create(id, reason, clientName)
	if errors.Is(err, errRawGrantsLimited) {
		writeError(writer, http.StatusTooManyRequests, "raw_access_limited", "too many raw access requests are awaiting a decision")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_access_failed", "raw access request could not be created")
		return
	}
	handler.observers.noteRawEvent(RawAccessEventRequested, grant)
	writeJSON(writer, http.StatusCreated, rawAccessCreated{RawAccessGrant: grant, GrantToken: token})
}

// writeRawAccessAvailability answers 409 when agents cannot get raw content
// at all. An unset raw password outranks the settings switch, since turning
// the switch on would not help.
func (handler *Handler) writeRawAccessAvailability(writer http.ResponseWriter, request *http.Request) bool {
	status, err := handler.rawVaultStatus(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing state is unavailable")
		return false
	}
	if !status.PasswordSet {
		writeRawPasswordNotSet(writer)
		return false
	}
	enabled, err := handler.agentRawAccessEnabled(request.Context())
	if err != nil {
		handler.writeAuditSettingsStoreError(writer, err)
		return false
	}
	if !enabled {
		writeError(writer, http.StatusConflict, "raw_access_disabled", "agent raw access requests are turned off")
		return false
	}
	return true
}

func (handler *Handler) rawVaultStatus(ctx context.Context) (RawVaultStatus, error) {
	if handler.rawVault == nil {
		return RawVaultStatus{}, nil
	}
	return handler.rawVault.Status(ctx)
}

// rawPasswordRequired is true while body capture is on and no raw password
// protects the raw key, so the raw parts it captures are dropped. Without
// capture nothing raw is kept, and a missing password costs nothing. A read
// error reads as false; the capture switch still asks.
func (handler *Handler) rawPasswordRequired(ctx context.Context) bool {
	if handler.rawVault == nil || handler.auditSettings == nil {
		return false
	}
	settings, err := handler.auditSettings.GetAuditSettings(ctx)
	if err != nil || !(settings.RequestBodyEnabled || settings.ResponseContentEnabled) {
		return false
	}
	status, err := handler.rawVault.Status(ctx)
	return err == nil && !status.PasswordSet
}

func (handler *Handler) agentRawAccessEnabled(ctx context.Context) (bool, error) {
	if handler.auditSettings == nil {
		return contract.DefaultAuditSettings().AgentRawAccessEnabled, nil
	}
	settings, err := handler.auditSettings.GetAuditSettings(ctx)
	if err != nil {
		return false, err
	}
	return settings.AgentRawAccessEnabled, nil
}

// decideRawAccess settles a pending request from the desktop. While the
// operator's unlock session lasts an approval needs no password; otherwise
// it answers raw_proof_required until the password comes with it.
func (handler *Handler) decideRawAccess(writer http.ResponseWriter, request *http.Request, grantID string) {
	members, release, ok := decodeSecretJSON(writer, request, "decision", "proof")
	if !ok {
		return
	}
	defer release()
	var decision string
	if raw, present := members["decision"]; !present || strictUnmarshal(raw, &decision) != nil {
		writeValidationFailed(writer, "decision is required", []errorDetail{{
			Field: "decision", Reason: rawGrantModeUsage,
		}})
		return
	}
	proof, err := parseRawProof(members["proof"])
	if err != nil {
		writeValidationFailed(writer, "proof is invalid", []errorDetail{{
			Field: "proof", Reason: "must be {password}",
		}})
		return
	}
	defer proof.clear()
	mode := rawGrantMode(decision)
	switch mode {
	case rawGrantOnce, rawGrantWindow5m, rawGrantWindow1h, rawGrantDenyMode:
	default:
		writeValidationFailed(writer, "decision is invalid", []errorDetail{{
			Field: "decision", Reason: rawGrantModeUsage,
		}})
		return
	}
	pending, err := handler.rawGrants.pendingGrant(grantID)
	if !handler.writeRawGrantDecisionError(writer, err) {
		return
	}
	if mode == rawGrantDenyMode {
		decided, err := handler.rawGrants.decide(grantID, mode, nil, nil)
		if !handler.writeRawGrantDecisionError(writer, err) {
			return
		}
		handler.observers.noteRawEvent(RawAccessEventDenied, decided)
		writeJSON(writer, http.StatusOK, decided)
		return
	}
	if !handler.writeRawAccessAvailability(writer, request) {
		return
	}
	if handler.rawVault == nil {
		writeRawPasswordNotSet(writer)
		return
	}
	holder, err := handler.rawVault.HoldKey(request.Context(), proof.RawProof)
	if errors.Is(err, ErrRawProofRequired) {
		writeError(writer, http.StatusUnprocessableEntity, "raw_proof_required", "raw content is locked; approving needs the raw password")
		return
	}
	if err != nil {
		if errors.Is(err, ErrRawPasswordInvalid) {
			handler.observers.noteRawEvent(RawAccessEventPasswordInvalid, pending)
		}
		handler.writeRawProofError(writer, err)
		return
	}
	var keys map[storage.AuditDirection][]byte
	if mode == rawGrantOnce {
		keys, err = handler.openGrantKeys(request.Context(), pending.RequestID, holder)
		holder.Close()
		holder = nil
		if err != nil {
			handler.writeRawProofError(writer, err)
			return
		}
	}
	decided, err := handler.rawGrants.decide(grantID, mode, keys, holder)
	if !handler.writeRawGrantDecisionError(writer, err) {
		return
	}
	handler.observers.noteRawEvent(RawAccessEventApproved, decided)
	writeJSON(writer, http.StatusOK, decided)
}

// openGrantKeys unwraps only this request's raw-sealed part keys for a
// once grant.
func (handler *Handler) openGrantKeys(
	ctx context.Context,
	requestID contract.RequestID,
	opener RawKeyOpener,
) (map[storage.AuditDirection][]byte, error) {
	blobs := []storage.AuditBlob{}
	if handler.auditBlobs != nil {
		loaded, err := handler.auditBlobs.GetAuditBlobsByRequest(ctx, requestID)
		if err != nil {
			return nil, err
		}
		blobs = loaded
	}
	keys := map[storage.AuditDirection][]byte{}
	for _, blob := range blobs {
		if !rawSealed(blob) {
			continue
		}
		key, err := opener.OpenBlobKey(blob)
		if err != nil {
			clearRawKeys(keys)
			return nil, err
		}
		keys[blob.Direction] = key
	}
	return keys, nil
}

func (handler *Handler) writeRawGrantDecisionError(writer http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, errRawGrantNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "raw access request not found")
	case errors.Is(err, errRawGrantNotPending):
		writeError(writer, http.StatusConflict, "raw_access_not_pending", "raw access request was already decided or expired")
	default:
		writeError(writer, http.StatusInternalServerError, "raw_access_failed", "raw access decision failed")
	}
	return false
}

// writeRawProofError maps vault proof failures shared by approval and
// unlock onto their wire codes.
func (handler *Handler) writeRawProofError(writer http.ResponseWriter, err error) {
	var backoff *RawBackoffError
	switch {
	case errors.As(err, &backoff):
		seconds := int((backoff.Remaining + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeErrorDetails(writer, http.StatusTooManyRequests, "raw_password_backoff",
			"too many wrong raw passwords; wait before retrying",
			[]errorDetail{{Reason: "retry_after", RetryAfterSeconds: seconds}})
	case errors.Is(err, ErrRawPasswordInvalid):
		writeError(writer, http.StatusForbidden, "raw_password_invalid", "raw password is incorrect")
	case errors.Is(err, ErrRawProofRequired):
		writeError(writer, http.StatusUnprocessableEntity, "raw_proof_required", "this proof is not accepted here")
	case errors.Is(err, ErrRawNotConfigured):
		writeRawPasswordNotSet(writer)
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "request record not found")
	default:
		writeError(writer, http.StatusInternalServerError, "raw_vault_unavailable", "raw sealing key could not be opened")
	}
}

func writeRawPasswordNotSet(writer http.ResponseWriter) {
	writeErrorDetails(writer, http.StatusConflict, "raw_access_unavailable",
		"no raw password is set", []errorDetail{{Reason: "raw_password_not_set"}})
}

const maxSecretBodyBytes = 16 << 10

// decodeSecretJSON reads a small JSON object that carries a secret into one
// fixed buffer and returns its members. release clears the buffer and every
// member, so no copy made here outlives the request. Unknown members are
// rejected like decodeControlJSON does.
func decodeSecretJSON(
	writer http.ResponseWriter,
	request *http.Request,
	allowed ...string,
) (map[string]json.RawMessage, func(), bool) {
	if !requireMediaType(writer, request, "application/json") {
		return nil, nil, false
	}
	buffer := make([]byte, maxSecretBodyBytes+1)
	read, err := io.ReadFull(http.MaxBytesReader(writer, request.Body, maxSecretBodyBytes), buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		clear(buffer)
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return nil, nil, false
	}
	body := buffer[:read]
	members := map[string]json.RawMessage{}
	release := func() {
		for name, member := range members {
			clear(member)
			delete(members, name)
		}
		clear(buffer)
	}
	if err := json.Unmarshal(body, &members); err != nil || members == nil {
		release()
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return nil, nil, false
	}
	for name := range members {
		known := false
		for _, candidate := range allowed {
			known = known || name == candidate
		}
		if !known {
			release()
			writeError(writer, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
			return nil, nil, false
		}
	}
	return members, release, true
}

type ownedRawProof struct{ RawProof }

func (proof ownedRawProof) clear() { clear(proof.Password) }

// parseRawProof reads {password}. An absent or null proof is empty, which
// deny accepts and approval rejects.
func parseRawProof(raw json.RawMessage) (ownedRawProof, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return ownedRawProof{}, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return ownedRawProof{}, errors.New("proof must be an object")
	}
	defer func() {
		for _, member := range members {
			clear(member)
		}
	}()
	var kind string
	var proof ownedRawProof
	for name, member := range members {
		switch name {
		case "kind":
			if err := strictUnmarshal(member, &kind); err != nil {
				proof.clear()
				return ownedRawProof{}, err
			}
		case "password":
			password, err := decodeJSONStringBytes(member)
			if err != nil {
				proof.clear()
				return ownedRawProof{}, err
			}
			proof.Password = password
		default:
			proof.clear()
			return ownedRawProof{}, errors.New("unknown proof field")
		}
	}
	switch kind {
	case "":
	case "password":
		if len(proof.Password) == 0 {
			return ownedRawProof{}, errors.New("password proof needs a password")
		}
	default:
		proof.clear()
		return ownedRawProof{}, errors.New("unknown proof kind")
	}
	return proof, nil
}

func decodeJSONStringBytes(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return nil, errors.New("secret must be a JSON string")
	}
	body := data[1 : len(data)-1]
	out := make([]byte, 0, len(body))
	for index := 0; index < len(body); index++ {
		char := body[index]
		if char != '\\' {
			out = append(out, char)
			continue
		}
		index++
		if index >= len(body) {
			clear(out)
			return nil, errors.New("secret has a truncated escape")
		}
		switch body[index] {
		case '"', '\\', '/':
			out = append(out, body[index])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			code, next, ok := readJSONUnicodeEscape(body, index+1)
			if !ok {
				clear(out)
				return nil, errors.New("secret has an invalid unicode escape")
			}
			out = utf8.AppendRune(out, code)
			index = next - 1
		default:
			clear(out)
			return nil, errors.New("secret has an invalid escape")
		}
	}
	if !utf8.Valid(out) {
		clear(out)
		return nil, errors.New("secret must be valid UTF-8")
	}
	return out, nil
}

// readJSONUnicodeEscape reads the hex digits after "\u" at start, joining a
// surrogate pair when one follows, and returns the index after the escape.
func readJSONUnicodeEscape(body []byte, start int) (rune, int, bool) {
	first, ok := parseHex4(body, start)
	if !ok {
		return 0, 0, false
	}
	next := start + 4
	if first >= 0xD800 && first < 0xDC00 {
		if next+6 <= len(body) && body[next] == '\\' && body[next+1] == 'u' {
			second, ok := parseHex4(body, next+2)
			if ok && second >= 0xDC00 && second < 0xE000 {
				return (first-0xD800)<<10 | (second - 0xDC00) + 0x10000, next + 6, true
			}
		}
		return utf8.RuneError, next, true
	}
	if first >= 0xDC00 && first < 0xE000 {
		return utf8.RuneError, next, true
	}
	return first, next, true
}

func parseHex4(body []byte, start int) (rune, bool) {
	if start+4 > len(body) {
		return 0, false
	}
	var value rune
	for _, char := range body[start : start+4] {
		value <<= 4
		switch {
		case char >= '0' && char <= '9':
			value |= rune(char - '0')
		case char >= 'a' && char <= 'f':
			value |= rune(char-'a') + 10
		case char >= 'A' && char <= 'F':
			value |= rune(char-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func stripControlRunes(value string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value)
}

// parseRequestIDSegment validates one canonical request ID path segment.
func parseRequestIDSegment(writer http.ResponseWriter, rawID string) (contract.RequestID, bool) {
	decodedID, err := url.PathUnescape(rawID)
	if err != nil || decodedID != rawID {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id must use its canonical form")
		return "", false
	}
	id := contract.RequestID(decodedID)
	if err := id.Validate(); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_id", "request_id is invalid")
		return "", false
	}
	return id, true
}
