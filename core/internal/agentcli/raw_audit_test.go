package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	storage "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const (
	cliRawOperatorToken = "cli-raw-operator-token"
	cliRawObserverToken = "cli-raw-observer-token"
	cliRawPassword      = "correct horse battery"
	cliRawRequestID     = "request_cli_raw"
	cliRawMarker        = "privacy-MARKER@example.com"
)

// cliRawVault accepts one fixed password and opens raw parts with the
// private half of the store's raw sealing key.
type cliRawVault struct {
	mu      sync.Mutex
	status  controlapi.RawVaultStatus
	private []byte
}

type cliRawOpener struct{ private []byte }

func (opener cliRawOpener) OpenBlobKey(blob storage.AuditBlob) ([]byte, error) {
	return rawseal.OpenBlobKey(opener.private, rawseal.BlobKeyInfo(string(blob.RequestID), string(blob.Direction)), blob.WrappedKey)
}

func (vault *cliRawVault) Status(context.Context) (controlapi.RawVaultStatus, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	return vault.status, nil
}

func (vault *cliRawVault) UnlockedOpener() (controlapi.RawKeyOpener, bool) { return nil, false }

func (opener cliRawOpener) Close() {}

func (vault *cliRawVault) HoldKey(_ context.Context, proof controlapi.RawProof) (controlapi.RawKeyHolder, error) {
	if proof.Empty() {
		return nil, controlapi.ErrRawProofRequired
	}
	if string(proof.Password) != cliRawPassword {
		return nil, controlapi.ErrRawPasswordInvalid
	}
	return cliRawOpener{private: vault.private}, nil
}

func (vault *cliRawVault) WithProof(_ context.Context, proof controlapi.RawProof, use func(controlapi.RawKeyOpener) error) error {
	if string(proof.Password) != cliRawPassword {
		return controlapi.ErrRawPasswordInvalid
	}
	return use(cliRawOpener{private: vault.private})
}

type cliRawFixture struct {
	store  *sqlite.Store
	vault  *cliRawVault
	server *httptest.Server
	client *Client
}

// newCLIRawFixture serves the real Control API over one redacted request:
// the client's request body is raw, the upstream body carries a placeholder.
func newCLIRawFixture(t *testing.T) cliRawFixture {
	t.Helper()
	return newCLIRawFixtureWith(t, true)
}

// newCLIRawFixtureWith captures the request with or without a raw password.
func newCLIRawFixtureWith(t *testing.T, rawPassword bool) cliRawFixture {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Raw captures are kept only once a raw password protects the raw key.
	var private []byte
	status := controlapi.RawVaultStatus{}
	if rawPassword {
		private = newCLIRawKey(t, store)
		status = controlapi.RawVaultStatus{Configured: true, PasswordSet: true, KeyVerified: true}
	}
	decision := contract.PrivacyDecisionRedact
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: cliRawRequestID, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol:   contract.ProtocolOpenAIChat,
		Audit:           contract.AuditRecordSummary{RequestBodyCaptured: true, UpstreamRequestBodyCaptured: true},
		PrivacyDecision: &decision,
		PrivacyFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "/messages/0/content", Count: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, part := range []struct {
		direction storage.AuditDirection
		exposure  storage.AuditExposure
		plain     string
	}{
		{storage.AuditDirectionRequest, storage.AuditExposureRaw, `{"content":"mail ` + cliRawMarker + `"}`},
		{storage.AuditDirectionUpstreamRequest, storage.AuditExposureShareable, `{"content":"mail <EMAIL_1>"}`},
	} {
		nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(part.plain))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
			RequestID: cliRawRequestID, Direction: part.direction, MediaType: "application/json",
			Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: len(part.plain), Exposure: part.exposure,
		}); err != nil {
			t.Fatal(err)
		}
	}
	vault := &cliRawVault{status: status, private: private}
	handler, err := controlapi.NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), controlapi.Dependencies{
		ServiceStore:   store,
		RequestRecords: store,
		AuditSettings:  store,
		AuditKeys:      store,
		AuditBlobs:     store,
		RawVault:       vault,
		ControlToken:   cliRawOperatorToken,
		ObserverToken:  cliRawObserverToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	interval := rawPollInterval
	rawPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { rawPollInterval = interval })
	client, err := Dial(DialOptions{ControlURL: server.URL, ControlToken: cliRawObserverToken})
	if err != nil {
		t.Fatal(err)
	}
	return cliRawFixture{store: store, vault: vault, server: server, client: client}
}

// newCLIRawKey stores a raw sealing key under a password envelope and
// returns its private half.
func newCLIRawKey(t *testing.T, store *sqlite.Store) []byte {
	t.Helper()
	private, public, err := rawseal.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	const keyID = 11
	kdf := rawseal.KDFParams{Algorithm: "argon2id", Version: 19, Time: 1, MemoryKiB: 64, Threads: 1}
	wrapped, err := rawseal.WrapPassword(private, []byte(cliRawPassword), kdf, keyID, public)
	if err != nil {
		t.Fatal(err)
	}
	kdfJSON, err := wrapped.KDFJSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRawSealingKey(context.Background(), storage.NewRawSealingKey{
		KeyID: keyID, PublicKey: public,
		Envelopes: []storage.RawKeyEnvelope{{
			Kind: rawseal.KindPassword, KDFJSON: kdfJSON, Salt: wrapped.Salt, Nonce: wrapped.Nonce, Wrapped: wrapped.Wrapped,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return private
}

// operator calls the Control API as the desktop.
func (fixture cliRawFixture) operator(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, fixture.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+cliRawOperatorToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func (fixture cliRawFixture) decide(t *testing.T, grantID, body string) {
	t.Helper()
	if status, decoded := fixture.operator(t, http.MethodPost, controlapi.RawAccessPath+"/"+grantID+"/decision", body); status != http.StatusOK {
		t.Fatalf("decision status=%d body=%v", status, decoded)
	}
}

func (fixture cliRawFixture) pendingGrants(t *testing.T) []map[string]any {
	t.Helper()
	status, list := fixture.operator(t, http.MethodGet, controlapi.RawAccessPath, "")
	if status != http.StatusOK {
		t.Fatalf("pending list status=%d body=%v", status, list)
	}
	items, _ := list["items"].([]any)
	grants := make([]map[string]any, 0, len(items))
	for _, item := range items {
		grants = append(grants, item.(map[string]any))
	}
	return grants
}

type rawAuditOutcome struct {
	decoded map[string]any
	text    string
	err     error
}

func runRawAudit(client *Client, arguments map[string]any) rawAuditOutcome {
	raw, err := callCommand(context.Background(), client, "raw-audit", arguments)
	outcome := rawAuditOutcome{text: string(raw), err: err}
	if err == nil {
		_ = json.Unmarshal(raw, &outcome.decoded)
	}
	return outcome
}

// startRawAudit runs raw-audit in the background and returns the grant it
// filed once the desktop can see it, or nil when the command ended first.
func startRawAudit(t *testing.T, fixture cliRawFixture, arguments map[string]any) (map[string]any, <-chan rawAuditOutcome) {
	t.Helper()
	known := map[string]bool{}
	for _, grant := range fixture.pendingGrants(t) {
		known[grant["grant_id"].(string)] = true
	}
	done := make(chan rawAuditOutcome, 1)
	go func() { done <- runRawAudit(fixture.client, arguments) }()
	for {
		for _, grant := range fixture.pendingGrants(t) {
			if !known[grant["grant_id"].(string)] {
				return grant, done
			}
		}
		select {
		case outcome := <-done:
			ended := make(chan rawAuditOutcome, 1)
			ended <- outcome
			return nil, ended
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func auditPart(t *testing.T, wrapped map[string]any, name string) map[string]any {
	t.Helper()
	part, ok := wrapped["audit"].(map[string]any)[name].(map[string]any)
	if !ok {
		t.Fatalf("%s missing from %v", name, wrapped)
	}
	return part
}

func TestGetRequestAuditReturnsShareableParts(t *testing.T) {
	fixture := newCLIRawFixture(t)
	raw, err := callCommand(context.Background(), fixture.client, "audit", map[string]any{"id": cliRawRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(cliRawMarker)) {
		t.Fatal("shareable audit leaked the marked value")
	}
	var wrapped map[string]any
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	if wrapped["content_view"] != "shareable" || wrapped["bodies_captured"] != true {
		t.Fatalf("wrapper=%v", wrapped)
	}
	request := auditPart(t, wrapped, "request_body")
	if request["content_view"] != "withheld" || request["reason"] != "privacy_redacted" ||
		request["raw_available"] != true || request["reason_detail"] == nil {
		t.Fatalf("request_body=%v", request)
	}
	if _, hasContent := request["content"]; hasContent {
		t.Fatalf("withheld part carries content: %v", request)
	}
	upstream := auditPart(t, wrapped, "upstream_request_body")
	if upstream["content_view"] != "shareable" || !strings.Contains(upstream["content"].(string), "<EMAIL_1>") {
		t.Fatalf("upstream_request_body=%v", upstream)
	}
	if !strings.Contains(wrapped["hint"].(string), "astrlink raw-audit") {
		t.Fatalf("hint=%v", wrapped["hint"])
	}
	if findings := wrapped["audit"].(map[string]any)["privacy_findings"].([]any); len(findings) != 1 {
		t.Fatalf("privacy_findings=%v", findings)
	}

	// Without raw sealing the agent is told not to ask.
	fixture.vault.mu.Lock()
	fixture.vault.status = controlapi.RawVaultStatus{}
	fixture.vault.mu.Unlock()
	raw, err = callCommand(context.Background(), fixture.client, "audit", map[string]any{"id": cliRawRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	if auditPart(t, wrapped, "request_body")["raw_available"] != false ||
		!strings.Contains(wrapped["hint"].(string), "Do not run raw-audit") {
		t.Fatalf("unavailable wrapper=%v", wrapped)
	}
	if outcome := runRawAudit(fixture.client, map[string]any{"id": cliRawRequestID, "reason": "debug"}); outcome.err == nil ||
		!strings.HasPrefix(outcome.err.Error(), "raw_access_unavailable") {
		t.Fatalf("unavailable err=%v", outcome.err)
	}
}

func TestGetRequestAuditExplainsRawPartsThatWereNotKept(t *testing.T) {
	fixture := newCLIRawFixtureWith(t, false)
	raw, err := callCommand(context.Background(), fixture.client, "audit", map[string]any{"id": cliRawRequestID})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(cliRawMarker)) {
		t.Fatal("audit leaked the marked value")
	}
	var wrapped map[string]any
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		t.Fatal(err)
	}
	request := auditPart(t, wrapped, "request_body")
	if request["reason"] != "raw_not_kept" || request["raw_available"] != false ||
		!strings.Contains(fmt.Sprint(request["reason_detail"]), "set a raw password") {
		t.Fatalf("request_body=%v", request)
	}
	if upstream := auditPart(t, wrapped, "upstream_request_body"); upstream["content_view"] != "shareable" {
		t.Fatalf("upstream_request_body=%v", upstream)
	}
	if outcome := runRawAudit(fixture.client, map[string]any{"id": cliRawRequestID, "reason": "debug"}); outcome.err == nil ||
		!strings.Contains(outcome.err.Error(), "has not set a raw password") {
		t.Fatalf("raw request err=%v", outcome.err)
	}
}

func TestRawAuditWaitsForTheUsersDecision(t *testing.T) {
	fixture := newCLIRawFixture(t)
	arguments := map[string]any{"id": cliRawRequestID, "reason": "the upstream rejected the email field", "agent": "claude-code"}

	grant, done := startRawAudit(t, fixture, arguments)
	if grant == nil || grant["client_name"] != "claude-code" || grant["reason"] != arguments["reason"] {
		t.Fatalf("grant=%v", grant)
	}
	select {
	case outcome := <-done:
		t.Fatalf("returned before the user decided: %+v", outcome)
	case <-time.After(50 * time.Millisecond):
	}
	fixture.decide(t, grant["grant_id"].(string), `{"decision":"once","proof":{"password":"`+cliRawPassword+`"}}`)
	approved := <-done
	if approved.err != nil {
		t.Fatal(approved.err)
	}
	if approved.decoded["status"] != "approved" || approved.decoded["content_view"] != "raw" ||
		!strings.Contains(approved.text, cliRawMarker) || strings.Contains(approved.text, "grant_token") {
		t.Fatalf("approved=%s", approved.text)
	}
	if part := auditPart(t, approved.decoded, "request_body"); part["content_view"] != "raw" {
		t.Fatalf("raw request_body=%v", part)
	}
	if part := auditPart(t, approved.decoded, "upstream_request_body"); part["content_view"] != "shareable" {
		t.Fatalf("raw upstream_request_body=%v", part)
	}

	grant, done = startRawAudit(t, fixture, arguments)
	fixture.decide(t, grant["grant_id"].(string), `{"decision":"deny"}`)
	if outcome := <-done; outcome.err == nil || !strings.HasPrefix(outcome.err.Error(), "raw_access_denied") {
		t.Fatalf("denied err=%v", outcome.err)
	}

	// Turning agent raw access off ends a wait and refuses new requests.
	_, done = startRawAudit(t, fixture, arguments)
	settings, err := fixture.store.GetAuditSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.AgentRawAccessEnabled = false
	if err := fixture.store.UpdateAuditSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if outcome := <-done; outcome.err == nil || !strings.HasPrefix(outcome.err.Error(), "raw_access_disabled") {
		t.Fatalf("disabled while waiting err=%v", outcome.err)
	}
	if outcome := runRawAudit(fixture.client, arguments); outcome.err == nil || !strings.HasPrefix(outcome.err.Error(), "raw_access_disabled") {
		t.Fatalf("disabled err=%v", outcome.err)
	}

	if outcome := runRawAudit(fixture.client, map[string]any{"id": cliRawRequestID}); outcome.err == nil {
		t.Fatal("missing reason accepted")
	}
	if outcome := runRawAudit(fixture.client, map[string]any{
		"id": cliRawRequestID, "reason": strings.Repeat("x", maxRawReasonRunes+1),
	}); outcome.err == nil {
		t.Fatal("oversized reason accepted")
	}
}

func TestRawAuditReusesALastingGrantUntilRevoked(t *testing.T) {
	fixture := newCLIRawFixture(t)
	grant, done := startRawAudit(t, fixture, map[string]any{"id": cliRawRequestID, "reason": "debug the email field"})
	fixture.decide(t, grant["grant_id"].(string), `{"decision":"until_lock","proof":{"password":"`+cliRawPassword+`"}}`)
	approved := <-done
	if approved.err != nil {
		t.Fatal(approved.err)
	}
	printed, _ := approved.decoded["raw_grant"].(map[string]any)
	token, _ := printed["grant_token"].(string)
	// It names no time; the agent is told what ends it instead.
	ends, _ := printed["ends"].(string)
	if token == "" || printed["decision"] != "until_lock" || printed["expires_at"] != nil ||
		!strings.Contains(ends, "screen locks") || !strings.Contains(printed["usage"].(string), "raw-revoke --grant") ||
		!strings.Contains(approved.text, cliRawMarker) {
		t.Fatalf("approved=%s", approved.text)
	}

	// The printed token reads again without a new request or reason.
	reused := runRawAudit(fixture.client, map[string]any{"id": cliRawRequestID, "grant": token})
	if reused.err != nil || !strings.Contains(reused.text, cliRawMarker) || strings.Contains(reused.text, "grant_token") {
		t.Fatalf("reused=%+v", reused)
	}
	if pending := fixture.pendingGrants(t); len(pending) != 0 {
		t.Fatalf("reuse filed a request: %v", pending)
	}

	revoked, err := callCommand(context.Background(), fixture.client, "raw-revoke", map[string]any{"grant": token})
	if err != nil || !strings.Contains(string(revoked), `"status":"revoked"`) {
		t.Fatalf("revoke=%s err=%v", revoked, err)
	}
	if outcome := runRawAudit(fixture.client, map[string]any{"id": cliRawRequestID, "grant": token}); outcome.err == nil ||
		!strings.HasPrefix(outcome.err.Error(), "raw_grant_invalid") || !strings.Contains(outcome.err.Error(), "request raw access again") {
		t.Fatalf("revoked read err=%v", outcome.err)
	}
	// Revoking twice reports the grant's final state.
	if again, err := callCommand(context.Background(), fixture.client, "raw-revoke", map[string]any{"grant": token}); err != nil ||
		!strings.Contains(string(again), `"status":"revoked"`) {
		t.Fatalf("second revoke=%s err=%v", again, err)
	}
	if _, err := callCommand(context.Background(), fixture.client, "raw-revoke", map[string]any{"grant": "unknown"}); err == nil ||
		!strings.HasPrefix(err.Error(), "raw_grant_invalid") {
		t.Fatalf("unknown revoke err=%v", err)
	}
	if _, err := callCommand(context.Background(), fixture.client, "raw-revoke", map[string]any{}); err == nil {
		t.Fatal("missing grant accepted")
	}
}

func TestRawAuditStopsWaitingAtTheDeadline(t *testing.T) {
	fixture := newCLIRawFixture(t)
	fixture.client.Agent = "Codex"
	outcome := runRawAudit(fixture.client, map[string]any{
		"id": cliRawRequestID, "reason": "debug", "wait": 30 * time.Millisecond,
	})
	if outcome.err == nil || !strings.HasPrefix(outcome.err.Error(), "raw_access_pending") {
		t.Fatalf("deadline err=%v", outcome.err)
	}
	// The detected host names the request when the agent passed no name.
	if grants := fixture.pendingGrants(t); len(grants) != 1 || grants[0]["client_name"] != "Codex" {
		t.Fatalf("pending=%v", grants)
	}
}
