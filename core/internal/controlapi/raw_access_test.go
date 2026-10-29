package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/rawseal"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

const (
	rawTestRequestID  = "request_raw_access"
	rawTestOtherID    = "request_raw_other"
	rawTestLaterID    = "request_raw_later"
	rawTestPassword   = `correct "horse" é`
	rawTestPasswordJS = `"correct \"horse\" \u00e9"`
	rawTestSecret     = "alice@example.com"
)

// fakeRawVault stands in for the Phase 1b vault: it checks a fixed
// password and holds the fixture's raw private key in the clear.
type fakeRawVault struct {
	mu        sync.Mutex
	status    RawVaultStatus
	statusErr error
	unlocked  bool
	backoff   time.Duration
	proofs    int
	opened    int
	private   []byte
	keyID     int64
}

type fakeRawOpener struct{ vault *fakeRawVault }

func (opener fakeRawOpener) OpenBlobKey(blob storage.AuditBlob) ([]byte, error) {
	opener.vault.opened++
	if opener.vault.private == nil {
		return nil, errors.New("fake vault holds no raw key")
	}
	return openRawBlobKey(opener.vault.private, opener.vault.keyID, blob)
}

// createKey stores a raw key only this vault holds, so the store seals raw
// parts to it as it does in Core.
func (vault *fakeRawVault) createKey(t *testing.T, store *sqlite.Store) {
	t.Helper()
	const keyID = 7
	private, public, err := rawseal.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := rawseal.WrapPassword(private, []byte(rawTestPassword), rawVaultTestKDF, keyID, public)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := storedPasswordEnvelope(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRawSealingKey(context.Background(), storage.NewRawSealingKey{
		KeyID: keyID, PublicKey: public, Envelopes: []storage.RawKeyEnvelope{envelope},
	}); err != nil {
		t.Fatal(err)
	}
	vault.private, vault.keyID = private, keyID
}

func (vault *fakeRawVault) Status(context.Context) (RawVaultStatus, error) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	status := vault.status
	status.Unlocked = vault.unlocked
	return status, vault.statusErr
}

func (vault *fakeRawVault) UnlockedOpener() (RawKeyOpener, bool) {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if !vault.unlocked {
		return nil, false
	}
	return fakeRawOpener{vault}, true
}

func (vault *fakeRawVault) WithProof(_ context.Context, proof RawProof, use func(RawKeyOpener) error) error {
	vault.mu.Lock()
	vault.proofs++
	backoff := vault.backoff
	vault.mu.Unlock()
	switch {
	case backoff > 0:
		return &RawBackoffError{Remaining: backoff}
	case string(proof.Password) != rawTestPassword:
		return ErrRawPasswordInvalid
	}
	return use(fakeRawOpener{vault})
}

// HoldKey checks proof like WithProof; an open unlock session is no proof.
func (vault *fakeRawVault) HoldKey(_ context.Context, proof RawProof) (RawKeyHolder, error) {
	vault.mu.Lock()
	backoff := vault.backoff
	if !proof.Empty() {
		vault.proofs++
	}
	vault.mu.Unlock()
	switch {
	case proof.Empty():
		return nil, ErrRawProofRequired
	case backoff > 0:
		return nil, &RawBackoffError{Remaining: backoff}
	case string(proof.Password) != rawTestPassword:
		return nil, ErrRawPasswordInvalid
	}
	return &proofOpener{private: append([]byte(nil), vault.private...), keyID: vault.keyID}, nil
}

func configuredRawVault() *fakeRawVault {
	return &fakeRawVault{status: RawVaultStatus{Configured: true, PasswordSet: true}}
}

type rawAccessFixture struct {
	store   *sqlite.Store
	handler *Handler
}

// newRawAccessFixture stores one redacted request: raw request and
// response bodies, a shareable upstream body, and shareable meta. The raw
// bodies are kept only when vault is a fake vault with a raw password.
func newRawAccessFixture(t *testing.T, vault RawVault) rawAccessFixture {
	t.Helper()
	return newRawAccessFixtureAt(t, filepath.Join(t.TempDir(), "astrlink.db"), vault)
}

func newRawAccessFixtureAt(t *testing.T, path string, vault RawVault) rawAccessFixture {
	t.Helper()
	store, err := openControlTestStore(t, context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []contract.RequestID{rawTestRequestID, rawTestOtherID} {
		insertRawTestRecord(t, store, id)
	}
	meta, err := json.Marshal(contract.AuditHTTPMeta{Method: "POST", URL: "/v1/chat/completions", HTTPVersion: "HTTP/1.1"})
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	insertRawTestParts(t, store, storage.AuditExposureShareable, map[storage.AuditDirection]string{
		storage.AuditDirectionUpstreamRequest: `{"content":"mail <EMAIL_1>"}`,
		storage.AuditDirectionHTTPMeta:        string(meta),
	})
	if fake, ok := vault.(*fakeRawVault); ok && fake.status.PasswordSet {
		fake.createKey(t, store)
	}
	captureRawTestParts(t, store)
	return rawAccessFixture{store: store, handler: newRawAccessHandler(t, store, vault)}
}

func insertRawTestRecord(t *testing.T, store *sqlite.Store, id contract.RequestID) {
	t.Helper()
	decision := contract.PrivacyDecisionRedact
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: id, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
		InputProtocol:   contract.ProtocolOpenAIChat,
		Audit:           contract.AuditRecordSummary{RequestBodyCaptured: true, ResponseContentCaptured: true},
		PrivacyDecision: &decision,
		PrivacyFindings: []contract.PrivacyFinding{{Kind: contract.CanonicalKindEmail, JSONPath: "/messages/0/content", Count: 1}},
	}); err != nil {
		t.Fatal(err)
	}
}

// captureRawTestParts captures the fixture request's raw bodies the way Core
// does: the store seals them to the raw key, or keeps nothing without one.
func captureRawTestParts(t *testing.T, store *sqlite.Store) {
	t.Helper()
	captureRawTestPartsFor(t, store, rawTestRequestID)
}

func captureRawTestPartsFor(t *testing.T, store *sqlite.Store, id contract.RequestID) {
	t.Helper()
	insertRawTestPartsFor(t, store, id, storage.AuditExposureRaw, map[storage.AuditDirection]string{
		storage.AuditDirectionRequest:  `{"content":"mail ` + rawTestSecret + `"}`,
		storage.AuditDirectionResponse: `{"reply":"sent to ` + rawTestSecret + `"}`,
	})
}

func insertRawTestParts(t *testing.T, store *sqlite.Store, exposure storage.AuditExposure, parts map[storage.AuditDirection]string) {
	t.Helper()
	insertRawTestPartsFor(t, store, rawTestRequestID, exposure, parts)
}

func insertRawTestPartsFor(
	t *testing.T,
	store *sqlite.Store,
	id contract.RequestID,
	exposure storage.AuditExposure,
	parts map[storage.AuditDirection]string,
) {
	t.Helper()
	ctx := context.Background()
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	for direction, plain := range parts {
		nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
			RequestID: id, Direction: direction, MediaType: "application/json",
			Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: len(plain), Exposure: exposure,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func newRawAccessHandler(t *testing.T, store *sqlite.Store, vault RawVault) *Handler {
	t.Helper()
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore:   store,
		RequestRecords: store,
		AuditSettings:  store,
		AuditKeys:      store,
		AuditBlobs:     store,
		RawVault:       vault,
		ControlToken:   testControlToken,
		ObserverToken:  testObserverToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type rawCaller int

const (
	rawAsAgent rawCaller = iota
	rawAsOperator
)

func rawHTTP(
	t *testing.T,
	handler *Handler,
	caller rawCaller,
	method, path, body, grant string,
) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	switch caller {
	case rawAsAgent:
		request.Header.Set("Authorization", "Bearer "+testObserverToken)
		request.Header.Set("User-Agent", observerUserAgentPrefix+"/test")
	case rawAsOperator:
		request.Header.Set("Authorization", "Bearer "+testControlToken)
	}
	if grant != "" {
		request.Header.Set(RawGrantHeader, grant)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func wantRawStatus(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want %d body=%s", response.Code, status, response.Body.String())
	}
	if code == "" {
		return
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != code {
		t.Fatalf("code=%q want %q body=%s", envelope.Error.Code, code, response.Body.String())
	}
}

func setAgentRawAccess(t *testing.T, store *sqlite.Store, enabled bool) {
	t.Helper()
	settings, err := store.GetAuditSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.AgentRawAccessEnabled = enabled
	if err := store.UpdateAuditSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

func setBodyCapture(t *testing.T, store *sqlite.Store, enabled bool) {
	t.Helper()
	settings, err := store.GetAuditSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.RequestBodyEnabled = enabled
	settings.ResponseContentEnabled = enabled
	if err := store.UpdateAuditSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
}

func rawAuditPath(id string) string { return RequestsPath + "/" + id + "/audit" }

func requestRawGrant(t *testing.T, handler *Handler, id string) rawAccessCreated {
	t.Helper()
	response := rawHTTP(t, handler, rawAsAgent, http.MethodPost, rawAuditPath(id)+"/raw-access",
		`{"reason":"debug a failed tool call","client_name":"Claude Code"}`, "")
	wantRawStatus(t, response, http.StatusCreated, "")
	var created rawAccessCreated
	decode(t, response, &created)
	if created.GrantToken == "" || created.Status != "pending" || created.Decision != "" ||
		created.ClientName != "Claude Code" || created.RequestID != contract.RequestID(id) {
		t.Fatalf("created=%#v", created)
	}
	return created
}

func decideRawGrant(t *testing.T, handler *Handler, grantID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return rawHTTP(t, handler, rawAsOperator, http.MethodPost, RawAccessPath+"/"+grantID+"/decision", body, "")
}

func readRawAudit(t *testing.T, handler *Handler, caller rawCaller, path, grant string) contract.AuditContent {
	t.Helper()
	response := rawHTTP(t, handler, caller, http.MethodGet, path, "", grant)
	wantRawStatus(t, response, http.StatusOK, "")
	var content contract.AuditContent
	decode(t, response, &content)
	if err := content.Validate(); err != nil {
		t.Fatalf("content invalid: %v body=%s", err, response.Body.String())
	}
	return content
}

func TestShareableAuditViewWithholdsRawParts(t *testing.T) {
	fixture := newRawAccessFixture(t, configuredRawVault())
	path := rawAuditPath(rawTestRequestID) + "?view=shareable"
	response := rawHTTP(t, fixture.handler, rawAsAgent, http.MethodGet, path, "", "")
	wantRawStatus(t, response, http.StatusOK, "")
	wire := response.Body.String()
	if strings.Contains(wire, rawTestSecret) {
		t.Fatalf("shareable view leaked raw content: %s", wire)
	}
	var content contract.AuditContent
	decode(t, response, &content)
	if content.View != contract.AuditContentViewShareable || len(content.PrivacyFindings) != 1 {
		t.Fatalf("content=%#v", content)
	}
	request, reply := content.RequestBody, content.ResponseContent
	if request == nil || !request.Withheld || request.Reason != contract.AuditWithheldPrivacyRedacted ||
		request.RawAvailable == nil || !*request.RawAvailable || request.CapturedBytes == 0 {
		t.Fatalf("request part=%#v", request)
	}
	if reply == nil || !reply.Withheld || reply.Reason != contract.AuditWithheldPrivacyRestored {
		t.Fatalf("response part=%#v", reply)
	}
	upstream := content.UpstreamRequestBody
	if upstream == nil || upstream.Withheld || upstream.Exposure != contract.AuditPartExposureShareable ||
		!strings.Contains(upstream.Content, "<EMAIL_1>") {
		t.Fatalf("upstream part=%#v", upstream)
	}
	if content.HTTPMeta == nil || content.HTTPMeta.Method != "POST" {
		t.Fatalf("http_meta=%#v", content.HTTPMeta)
	}
	if !strings.Contains(wire, `"withheld":true,"reason":"privacy_redacted","raw_available":true`) ||
		strings.Contains(wire, `"content":""`) {
		t.Fatalf("withheld wire shape: %s", wire)
	}
	if snapshot := fixture.handler.observers.snapshot(context.Background()); snapshot.ReadLevel != ReadLevelShareable {
		t.Fatalf("read level=%q", snapshot.ReadLevel)
	}

	// The full view without a grant is operator-only.
	wantRawStatus(t, rawHTTP(t, fixture.handler, rawAsAgent, http.MethodGet, rawAuditPath(rawTestRequestID), "", ""),
		http.StatusForbidden, "forbidden")
	for _, query := range []string{"?view=raw", "?view=shareable&view=full", "?view="} {
		wantRawStatus(t, rawHTTP(t, fixture.handler, rawAsAgent, http.MethodGet, rawAuditPath(rawTestRequestID)+query, "", ""),
			http.StatusBadRequest, "validation_failed")
	}

	// raw_available is false when asking cannot succeed.
	setAgentRawAccess(t, fixture.store, false)
	content = readRawAudit(t, fixture.handler, rawAsAgent, path, "")
	if content.RequestBody.RawAvailable == nil || *content.RequestBody.RawAvailable {
		t.Fatalf("disabled raw_available=%v", content.RequestBody.RawAvailable)
	}
	unsealed := newRawAccessHandler(t, fixture.store, nil)
	content = readRawAudit(t, unsealed, rawAsAgent, path, "")
	if content.RequestBody.RawAvailable == nil || *content.RequestBody.RawAvailable {
		t.Fatalf("unconfigured raw_available=%v", content.RequestBody.RawAvailable)
	}
}

func TestPrivacyWithheldReasonCoversEveryDecision(t *testing.T) {
	record := func(decision *contract.PrivacyDecision) contract.RequestRecord {
		return contract.RequestRecord{PrivacyDecision: decision}
	}
	decision := func(value contract.PrivacyDecision) *contract.PrivacyDecision { return &value }
	request := storage.AuditBlob{Direction: storage.AuditDirectionRequest, Exposure: storage.AuditExposureRaw}
	cases := []struct {
		record contract.RequestRecord
		blob   storage.AuditBlob
		want   contract.AuditWithheldReason
	}{
		{record(decision(contract.PrivacyDecisionRedact)), request, contract.AuditWithheldPrivacyRedacted},
		{record(decision(contract.PrivacyDecisionBlock)), request, contract.AuditWithheldPrivacyBlocked},
		{record(decision(contract.PrivacyDecisionFailOpen)), request, contract.AuditWithheldPrivacyFailOpen},
		{record(decision(contract.PrivacyDecisionTrusted)), request, contract.AuditWithheldPrivacyTrusted},
		{record(decision(contract.PrivacyDecisionTrusted)),
			storage.AuditBlob{Direction: storage.AuditDirectionUpstreamRequest, Exposure: storage.AuditExposureRaw},
			contract.AuditWithheldPrivacyTrusted},
		{record(nil), request, contract.AuditWithheldPrivacyUnknown},
		{record(decision(contract.PrivacyDecisionRedact)),
			storage.AuditBlob{Direction: storage.AuditDirectionRequest, Exposure: storage.AuditExposurePending},
			contract.AuditWithheldPrivacyPending},
		{record(decision(contract.PrivacyDecisionRedact)),
			storage.AuditBlob{Direction: storage.AuditDirectionResponse, Exposure: storage.AuditExposureRaw},
			contract.AuditWithheldPrivacyRestored},
		{record(nil),
			storage.AuditBlob{Direction: storage.AuditDirectionUpstreamResponse, Exposure: storage.AuditExposureRaw},
			contract.AuditWithheldPrivacyUnknown},
	}
	for _, testCase := range cases {
		if got := privacyWithheldReason(testCase.record, testCase.blob); got != testCase.want {
			t.Fatalf("%s/%s reason=%q want %q", testCase.blob.Direction, testCase.blob.Exposure, got, testCase.want)
		}
	}
}

func TestOperatorFullViewFollowsRawUnlock(t *testing.T) {
	// Raw parts captured before a raw password is set are not kept.
	unprotected := newRawAccessFixture(t, nil)
	content := readRawAudit(t, unprotected.handler, rawAsOperator, rawAuditPath(rawTestRequestID), "")
	if content.View != contract.AuditContentViewFull {
		t.Fatalf("view=%q", content.View)
	}
	for direction, part := range map[string]*contract.AuditContentPart{
		"request": content.RequestBody, "response": content.ResponseContent,
	} {
		if part == nil || !part.Withheld || part.Reason != contract.AuditWithheldRawNotKept ||
			part.RawAvailable == nil || *part.RawAvailable || strings.Contains(part.Content, rawTestSecret) {
			t.Fatalf("unprotected %s part=%#v", direction, part)
		}
	}
	if content.UpstreamRequestBody == nil || content.UpstreamRequestBody.Withheld {
		t.Fatalf("unprotected read withheld a shareable part: %#v", content.UpstreamRequestBody)
	}

	vault := configuredRawVault()
	locked := newRawAccessFixture(t, vault).handler
	content = readRawAudit(t, locked, rawAsOperator, rawAuditPath(rawTestRequestID), "")
	for name, part := range map[string]*contract.AuditContentPart{
		"request": content.RequestBody, "response": content.ResponseContent,
	} {
		if part == nil || !part.Withheld || part.Reason != contract.AuditWithheldRawLocked {
			t.Fatalf("locked %s part=%#v", name, part)
		}
	}
	if content.UpstreamRequestBody == nil || content.UpstreamRequestBody.Withheld {
		t.Fatalf("locked read withheld a shareable part: %#v", content.UpstreamRequestBody)
	}

	vault.unlocked = true
	content = readRawAudit(t, locked, rawAsOperator, rawAuditPath(rawTestRequestID), "")
	if content.RequestBody == nil || content.RequestBody.Withheld || !strings.Contains(content.RequestBody.Content, rawTestSecret) {
		t.Fatalf("unlocked read=%#v", content.RequestBody)
	}
	// An operator read is not an agent read.
	if snapshot := locked.observers.snapshot(context.Background()); snapshot.ReadLevel != "" || snapshot.LastRawReadAt != nil {
		t.Fatalf("operator read noted as agent read: %#v", snapshot)
	}

	vault.statusErr = errors.New("vault state unreadable")
	wantRawStatus(t, rawHTTP(t, locked, rawAsOperator, http.MethodGet, rawAuditPath(rawTestRequestID), "", ""),
		http.StatusInternalServerError, "raw_vault_unavailable")
}

func TestRawAccessGrantOnceLifecycle(t *testing.T) {
	vault := configuredRawVault()
	fixture := newRawAccessFixture(t, vault)
	handler := fixture.handler
	created := requestRawGrant(t, handler, rawTestRequestID)
	full := rawAuditPath(rawTestRequestID)

	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", created.GrantToken),
		http.StatusConflict, "raw_access_pending")

	list := rawHTTP(t, handler, rawAsOperator, http.MethodGet, RawAccessPath, "", "")
	wantRawStatus(t, list, http.StatusOK, "")
	if strings.Contains(list.Body.String(), created.GrantToken) {
		t.Fatal("pending list leaked the grant token")
	}
	var page struct{ Items []RawAccessGrant }
	decode(t, list, &page)
	if len(page.Items) != 1 || page.Items[0].GrantID != created.GrantID || page.Items[0].Reason != "debug a failed tool call" {
		t.Fatalf("pending=%#v", page.Items)
	}
	if snapshot := handler.observers.snapshot(context.Background()); snapshot.PendingRawAccess != 1 {
		t.Fatalf("pending_raw_access=%d", snapshot.PendingRawAccess)
	}

	wantRawStatus(t, decideRawGrant(t, handler, created.GrantID, `{"decision":"once"}`),
		http.StatusUnprocessableEntity, "raw_proof_required")
	wantRawStatus(t, decideRawGrant(t, handler, created.GrantID, `{"decision":"once","proof":{"password":"wrong password"}}`),
		http.StatusForbidden, "raw_password_invalid")

	approve := decideRawGrant(t, handler, created.GrantID, `{"decision":"once","proof":{"kind":"password","password":`+rawTestPasswordJS+`}}`)
	wantRawStatus(t, approve, http.StatusOK, "")
	var decided RawAccessGrant
	decode(t, approve, &decided)
	if decided.Status != "approved" || decided.Decision != "once" || !decided.ExpiresAt.After(decided.CreatedAt) {
		t.Fatalf("decided=%#v", decided)
	}
	wantRawStatus(t, decideRawGrant(t, handler, created.GrantID, `{"decision":"deny"}`),
		http.StatusConflict, "raw_access_not_pending")

	// The token opens only its own request.
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, rawAuditPath(rawTestOtherID), "", created.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")

	content := readRawAudit(t, handler, rawAsAgent, full, created.GrantToken)
	if content.RequestBody == nil || content.RequestBody.Withheld || !strings.Contains(content.RequestBody.Content, rawTestSecret) ||
		content.ResponseContent == nil || content.ResponseContent.Withheld {
		t.Fatalf("granted read=%#v", content)
	}
	snapshot := handler.observers.snapshot(context.Background())
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", created.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")

	if snapshot.ReadLevel != ReadLevelRaw || snapshot.LastRawReadAt == nil || snapshot.PendingRawAccess != 0 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	kinds := []RawAccessEventKind{}
	for _, event := range snapshot.RawAccessEvents {
		if event.GrantID != created.GrantID || event.ClientName != "Claude Code" {
			t.Fatalf("event=%#v", event)
		}
		kinds = append(kinds, event.Kind)
	}
	want := []RawAccessEventKind{RawAccessEventRequested, RawAccessEventPasswordInvalid, RawAccessEventApproved, RawAccessEventRawRead}
	if len(kinds) != len(want) {
		t.Fatalf("events=%v want %v", kinds, want)
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("events=%v want %v", kinds, want)
		}
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), created.GrantToken) || strings.Contains(string(encoded), "horse") {
		t.Fatalf("observer log leaked a secret: %s", encoded)
	}

	// Grants live only in memory: a restarted Core forgets them.
	second := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, second.GrantID, `{"decision":"until_lock","proof":{"password":`+rawTestPasswordJS+`}}`),
		http.StatusOK, "")
	restarted := newRawAccessHandler(t, fixture.store, vault)
	wantRawStatus(t, rawHTTP(t, restarted, rawAsAgent, http.MethodGet, full, "", second.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
}

func TestRawAccessGrantDenyAndExpiry(t *testing.T) {
	vault := configuredRawVault()
	handler := newRawAccessFixture(t, vault).handler
	full := rawAuditPath(rawTestRequestID)
	approve := `{"decision":"once","proof":{"password":` + rawTestPasswordJS + `}}`

	denied := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, denied.GrantID, `{"decision":"deny"}`), http.StatusOK, "")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", denied.GrantToken),
		http.StatusForbidden, "raw_access_denied")
	if vault.proofs != 0 {
		t.Fatalf("deny asked for proof %d times", vault.proofs)
	}

	now := time.Now()
	handler.rawGrants.now = func() time.Time { return now }
	expiring := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, expiring.GrantID, approve), http.StatusOK, "")
	now = now.Add(rawGrantOnceTTL)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", expiring.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")

	stale := requestRawGrant(t, handler, rawTestRequestID)
	now = now.Add(rawGrantPendingTTL)
	wantRawStatus(t, decideRawGrant(t, handler, stale.GrantID, approve), http.StatusConflict, "raw_access_not_pending")
	// Finished grants are forgotten once their late-read window passes.
	now = now.Add(rawGrantFinishedTTL + time.Second)
	wantRawStatus(t, decideRawGrant(t, handler, stale.GrantID, approve), http.StatusNotFound, "not_found")
}

func rawGrantState(t *testing.T, handler *Handler, token string) RawAccessGrant {
	t.Helper()
	response := rawHTTP(t, handler, rawAsAgent, http.MethodGet, RawGrantPath, "", token)
	wantRawStatus(t, response, http.StatusOK, "")
	var grant RawAccessGrant
	decode(t, response, &grant)
	return grant
}

func rawAccessPage(t *testing.T, handler *Handler) RawAccessList {
	t.Helper()
	response := rawHTTP(t, handler, rawAsOperator, http.MethodGet, RawAccessPath, "", "")
	wantRawStatus(t, response, http.StatusOK, "")
	var page RawAccessList
	decode(t, response, &page)
	return page
}

func TestRawAccessLastingGrantReadsEveryRequestUntilItEnds(t *testing.T) {
	vault := configuredRawVault()
	fixture := newRawAccessFixture(t, vault)
	handler := fixture.handler
	full := rawAuditPath(rawTestRequestID)
	now := time.Now().UTC()
	handler.rawGrants.now = func() time.Time { return now }

	grant := requestRawGrant(t, handler, rawTestRequestID)
	if state := rawGrantState(t, handler, grant.GrantToken); state.Status != "pending" || state.Scope != "" {
		t.Fatalf("pending state=%#v", state)
	}
	approve := decideRawGrant(t, handler, grant.GrantID, `{"decision":"until_lock","proof":{"password":`+rawTestPasswordJS+`}}`)
	wantRawStatus(t, approve, http.StatusOK, "")
	approved := approve.Body.String()
	var decided RawAccessGrant
	decode(t, approve, &decided)
	// Only a lock ends it, so it names no time.
	if decided.Decision != "until_lock" || decided.Scope != rawGrantScopeAll || decided.ExpiresAt != nil ||
		!strings.Contains(approved, `"expires_at":null`) {
		t.Fatalf("decided=%#v body=%s", decided, approved)
	}
	if state := rawGrantState(t, handler, grant.GrantToken); state.Status != "approved" || state.Scope != rawGrantScopeAll {
		t.Fatalf("approved state=%#v", state)
	}
	page := rawAccessPage(t, handler)
	if len(page.Items) != 0 || len(page.Active) != 1 || page.Active[0].GrantID != grant.GrantID {
		t.Fatalf("page=%#v", page)
	}
	if strings.Contains(fmt.Sprint(page), grant.GrantToken) {
		t.Fatal("the active list leaked the grant token")
	}
	if snapshot := handler.observers.snapshot(context.Background()); snapshot.ActiveRawGrants != 1 {
		t.Fatalf("active_raw_grants=%d", snapshot.ActiveRawGrants)
	}

	// No read limit, and every request is covered, including one recorded
	// after the approval.
	for read := 0; read < 60; read++ {
		readRawAudit(t, handler, rawAsAgent, full, grant.GrantToken)
	}
	insertRawTestRecord(t, fixture.store, rawTestLaterID)
	captureRawTestPartsFor(t, fixture.store, rawTestLaterID)
	later := readRawAudit(t, handler, rawAsAgent, rawAuditPath(rawTestLaterID), grant.GrantToken)
	if later.RequestBody == nil || later.RequestBody.Withheld || !strings.Contains(later.RequestBody.Content, rawTestSecret) {
		t.Fatalf("later read=%#v", later.RequestBody)
	}
	readRawAudit(t, handler, rawAsAgent, rawAuditPath(rawTestOtherID), grant.GrantToken)
	events := handler.observers.snapshot(context.Background()).RawAccessEvents
	if last := events[len(events)-1]; last.Kind != RawAccessEventRawRead || last.RequestID != rawTestOtherID {
		t.Fatalf("last event=%#v", last)
	}

	// Time alone does not end it; a lock does, and zeroes the held key.
	// TestRawGrantsFollowLocksAndPasswordChanges sends the lock itself.
	now = now.Add(24 * time.Hour)
	readRawAudit(t, handler, rawAsAgent, full, grant.GrantToken)
	handler.rawGrants.revokeAll()
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", grant.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
	if state := rawGrantState(t, handler, grant.GrantToken); state.Status != "revoked" || state.ExpiresAt == nil {
		t.Fatalf("state after the lock=%#v", state)
	}
	if page := rawAccessPage(t, handler); len(page.Active) != 0 {
		t.Fatalf("active after the lock=%#v", page.Active)
	}
	handler.rawGrants.mu.Lock()
	held := handler.rawGrants.grants[grant.GrantID].holder
	handler.rawGrants.mu.Unlock()
	if held != nil {
		t.Fatal("a locked grant kept its key")
	}

	// An hour grant ends with its hour, or earlier at a lock.
	hour := requestRawGrant(t, handler, rawTestRequestID)
	approve = decideRawGrant(t, handler, hour.GrantID, `{"decision":"window_1h","proof":{"password":`+rawTestPasswordJS+`}}`)
	wantRawStatus(t, approve, http.StatusOK, "")
	decode(t, approve, &decided)
	if decided.Decision != "window_1h" || decided.ExpiresAt == nil || !decided.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("decided=%#v", decided)
	}
	// The five-minute window is gone.
	short := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, short.GrantID, `{"decision":"window_5m","proof":{"password":`+rawTestPasswordJS+`}}`),
		http.StatusBadRequest, "validation_failed")
	// A once grant still reads only its own request.
	once := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, once.GrantID, `{"decision":"once","proof":{"password":`+rawTestPasswordJS+`}}`), http.StatusOK, "")
	if state := rawGrantState(t, handler, once.GrantToken); state.Scope != rawGrantScopeRequest {
		t.Fatalf("once state=%#v", state)
	}
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, rawAuditPath(rawTestOtherID), "", once.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
	if page := rawAccessPage(t, handler); len(page.Active) != 1 || page.Active[0].GrantID != hour.GrantID {
		t.Fatalf("active=%#v", page.Active)
	}
	now = now.Add(time.Hour)
	if page := rawAccessPage(t, handler); len(page.Active) != 0 {
		t.Fatalf("active after the hour=%#v", page.Active)
	}
}

func TestRawAccessApprovalNeedsAFreshProofEvenWhileUnlocked(t *testing.T) {
	vault := configuredRawVault()
	handler := newRawAccessFixture(t, vault).handler
	full := rawAuditPath(rawTestRequestID)

	// The operator's own unlock is no proof: a lasting grant outlives the
	// click, which UI automation could make.
	vault.unlocked = true
	for _, decision := range []string{"once", "until_lock", "window_1h"} {
		grant := requestRawGrant(t, handler, rawTestRequestID)
		wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `{"decision":"`+decision+`"}`),
			http.StatusUnprocessableEntity, "raw_proof_required")
		wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID,
			`{"decision":"`+decision+`","proof":{"password":`+rawTestPasswordJS+`}}`), http.StatusOK, "")
		content := readRawAudit(t, handler, rawAsAgent, full, grant.GrantToken)
		if content.RequestBody.Withheld || !strings.Contains(content.RequestBody.Content, rawTestSecret) {
			t.Fatalf("%s read=%#v", decision, content.RequestBody)
		}
	}
	if vault.proofs != 3 {
		t.Fatalf("approvals checked %d proofs, want 3", vault.proofs)
	}
	// Denying needs none.
	denied := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, denied.GrantID, `{"decision":"deny"}`), http.StatusOK, "")
	// The observer role cannot decide, unlocked or not.
	grant := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, RawAccessPath+"/"+grant.GrantID+"/decision", `{"decision":"once"}`, ""),
		http.StatusForbidden, "forbidden")
}

func TestRawAccessGrantRevocation(t *testing.T) {
	fixture := newRawAccessFixture(t, configuredRawVault())
	handler := fixture.handler
	full := rawAuditPath(rawTestRequestID)
	approve := `{"decision":"window_1h","proof":{"password":` + rawTestPasswordJS + `}}`

	// Whoever holds the token revokes it with observer credentials.
	held := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, held.GrantID, approve), http.StatusOK, "")
	readRawAudit(t, handler, rawAsAgent, full, held.GrantToken)
	revoke := rawHTTP(t, handler, rawAsAgent, http.MethodDelete, RawGrantPath, "", held.GrantToken)
	wantRawStatus(t, revoke, http.StatusOK, "")
	var revoked RawAccessGrant
	decode(t, revoke, &revoked)
	if revoked.Status != "revoked" || revoked.GrantID != held.GrantID {
		t.Fatalf("revoked=%#v", revoked)
	}
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", held.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
	if state := rawGrantState(t, handler, held.GrantToken); state.Status != "revoked" {
		t.Fatalf("state=%#v", state)
	}
	if page := rawAccessPage(t, handler); len(page.Active) != 0 {
		t.Fatalf("active after revoke=%#v", page.Active)
	}
	events := handler.observers.snapshot(context.Background()).RawAccessEvents
	if last := events[len(events)-1]; last.Kind != RawAccessEventRevoked || last.GrantID != held.GrantID {
		t.Fatalf("last event=%#v", last)
	}
	// Revoking again answers what the grant already is.
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodDelete, RawGrantPath, "", held.GrantToken), http.StatusOK, "")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodDelete, RawGrantPath, "", "unknown-token"),
		http.StatusForbidden, "raw_grant_invalid")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodDelete, RawGrantPath, "", ""),
		http.StatusForbidden, "raw_grant_invalid")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, RawGrantPath, "", held.GrantToken),
		http.StatusMethodNotAllowed, "method_not_allowed")

	// The desktop revokes a running grant by id.
	desktop := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, desktop.GrantID, approve), http.StatusOK, "")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodDelete, RawAccessPath+"/"+desktop.GrantID, "", ""),
		http.StatusForbidden, "forbidden")
	wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodDelete, RawAccessPath+"/"+desktop.GrantID, "", ""),
		http.StatusOK, "")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", desktop.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
	wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodDelete, RawAccessPath+"/"+desktop.GrantID, "", ""),
		http.StatusConflict, "raw_access_not_active")
	wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodDelete, RawAccessPath+"/rawgrant_0000000000000000", "", ""),
		http.StatusNotFound, "not_found")
	wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodGet, RawAccessPath+"/"+desktop.GrantID, "", ""),
		http.StatusMethodNotAllowed, "method_not_allowed")

	// Shutdown revokes whatever still runs.
	last := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, last.GrantID, approve), http.StatusOK, "")
	handler.RevokeRawGrants()
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", last.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")

	// With agent access off the state read says so.
	setAgentRawAccess(t, fixture.store, false)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, RawGrantPath, "", last.GrantToken),
		http.StatusConflict, "raw_access_disabled")
}

func TestRawAccessProofs(t *testing.T) {
	vault := configuredRawVault()
	handler := newRawAccessFixture(t, vault).handler

	grant := requestRawGrant(t, handler, rawTestRequestID)
	for _, body := range []string{
		`{"decision":"forever","proof":{"password":"x"}}`,
		`{"decision":"once","proof":{"kind":"local_presence"}}`,
		`{"decision":"once","proof":{"kind":"passkey","password":"x"}}`,
		`{"decision":"once","proof":{"password":"x","passkey":{"credential_id":"AQ","prf":"AQ"}}}`,
		`{"decision":"once","proof":{"passkey":{"credential_id":"AQ","prf":"AQ"}}}`,
		`{"decision":"once","proof":{"password":"x","local_presence":true}}`,
		`{"decision":"once","proof":{"kind":"password"}}`,
		`{"decision":"once","proof":{"kind":"retina"}}`,
		`{"decision":"once","proof":{"password":"x","pin":"1"}}`,
		`{"decision":"once","proof":{"password":42}}`,
		`{"decision":"once","proof":"password"}`,
		`{"proof":{"password":"x"}}`,
	} {
		wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, body), http.StatusBadRequest, "validation_failed")
	}
	wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `{"decision":"once","extra":true}`),
		http.StatusBadRequest, "invalid_json")
	wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `not json`), http.StatusBadRequest, "invalid_json")
	wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `{"decision":"once","proof":{"password":"`+strings.Repeat("a", maxSecretBodyBytes)+`"}}`),
		http.StatusBadRequest, "invalid_json")
	wantRawStatus(t, decideRawGrant(t, handler, "rawgrant_0000000000000000", `{"decision":"deny"}`),
		http.StatusNotFound, "not_found")

	// Only the raw password approves.
	wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `{"decision":"once","proof":{"password":"wrong password"}}`),
		http.StatusForbidden, "raw_password_invalid")
	wantRawStatus(t, decideRawGrant(t, handler, grant.GrantID, `{"decision":"once","proof":{"password":`+rawTestPasswordJS+`}}`),
		http.StatusOK, "")

	throttled := requestRawGrant(t, handler, rawTestRequestID)
	vault.backoff = 2500 * time.Millisecond
	response := decideRawGrant(t, handler, throttled.GrantID, `{"decision":"once","proof":{"password":`+rawTestPasswordJS+`}}`)
	wantRawStatus(t, response, http.StatusTooManyRequests, "raw_password_backoff")
	if response.Header().Get("Retry-After") != "3" {
		t.Fatalf("Retry-After=%q", response.Header().Get("Retry-After"))
	}
	var envelope errorEnvelope
	decode(t, response, &envelope)
	if len(envelope.Error.Details) != 1 || envelope.Error.Details[0].RetryAfterSeconds != 3 {
		t.Fatalf("details=%#v", envelope.Error.Details)
	}
}

func TestRawAccessRequestAvailabilityAndValidation(t *testing.T) {
	fixture := newRawAccessFixture(t, nil)
	path := rawAuditPath(rawTestRequestID) + "/raw-access"
	body := `{"reason":"debug"}`
	response := rawHTTP(t, fixture.handler, rawAsAgent, http.MethodPost, path, body, "")
	wantRawStatus(t, response, http.StatusConflict, "raw_access_unavailable")
	if !strings.Contains(response.Body.String(), `"reason":"raw_password_not_set"`) {
		t.Fatalf("body=%s", response.Body.String())
	}
	// An unset raw password outranks the settings switch.
	setAgentRawAccess(t, fixture.store, false)
	wantRawStatus(t, rawHTTP(t, fixture.handler, rawAsAgent, http.MethodPost, path, body, ""),
		http.StatusConflict, "raw_access_unavailable")
	vault := configuredRawVault()
	handler := newRawAccessHandler(t, fixture.store, vault)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, body, ""),
		http.StatusConflict, "raw_access_disabled")

	setAgentRawAccess(t, fixture.store, true)
	for _, invalid := range []string{
		`{}`,
		`{"reason":"   "}`,
		`{"reason":"\u0000\u0007"}`,
		`{"reason":"` + strings.Repeat("é", maxRawReasonRunes+1) + `"}`,
		`{"reason":"debug","client_name":"` + strings.Repeat("c", maxRawClientRunes+1) + `"}`,
	} {
		wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, invalid, ""),
			http.StatusBadRequest, "validation_failed")
	}
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, `{"reason":"debug","grant":"x"}`, ""),
		http.StatusBadRequest, "")
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, rawAuditPath("request_missing")+"/raw-access", body, ""),
		http.StatusNotFound, "not_found")

	response = rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, `{"reason":"line one\nline two"}`, "")
	wantRawStatus(t, response, http.StatusCreated, "")
	var created rawAccessCreated
	decode(t, response, &created)
	if created.ClientName != defaultRawClientName || created.Reason != "line one line two" {
		t.Fatalf("created=%#v", created)
	}

	for live := 1; live < maxPendingRawGrants; live++ {
		wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path,
			fmt.Sprintf(`{"reason":"debug","client_name":"agent %d"}`, live), ""), http.StatusCreated, "")
	}
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, `{"reason":"debug","client_name":"one more"}`, ""),
		http.StatusTooManyRequests, "raw_access_limited")
	// Asking again replaces the client's own request instead of queueing.
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodPost, path, body, ""), http.StatusCreated, "")
}

func TestRawAccessRequestReplacesTheClientsPendingOne(t *testing.T) {
	handler := newRawAccessFixture(t, configuredRawVault()).handler
	full := rawAuditPath(rawTestRequestID)
	approve := `{"decision":"until_lock","proof":{"password":` + rawTestPasswordJS + `}}`

	first := requestRawGrant(t, handler, rawTestRequestID)
	other := requestRawGrant(t, handler, rawTestOtherID)
	second := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", first.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")
	wantRawStatus(t, decideRawGrant(t, handler, first.GrantID, approve), http.StatusConflict, "raw_access_not_pending")
	pending := map[string]bool{}
	for _, grant := range handler.rawGrants.pending() {
		pending[grant.GrantID] = true
	}
	if len(pending) != 2 || !pending[other.GrantID] || !pending[second.GrantID] {
		t.Fatalf("pending=%v", pending)
	}

	// An approved grant is not replaced by a new request.
	wantRawStatus(t, decideRawGrant(t, handler, second.GrantID, approve), http.StatusOK, "")
	requestRawGrant(t, handler, rawTestRequestID)
	readRawAudit(t, handler, rawAsAgent, full, second.GrantToken)
	if got := handler.rawGrants.pendingCount(); got != 2 {
		t.Fatalf("pending count=%d", got)
	}
}

func patchAgentRawAccess(t *testing.T, handler *Handler, enabled bool) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPatch, AuditSettingsPath, strings.NewReader(fmt.Sprintf(`{"agent_raw_access_enabled":%t}`, enabled)))
	request.Header.Set("Content-Type", "application/merge-patch+json")
	request.Header.Set("Authorization", "Bearer "+testControlToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	wantRawStatus(t, response, http.StatusOK, "")
}

func TestTurningAgentRawAccessOffRevokesGrants(t *testing.T) {
	fixture := newRawAccessFixture(t, configuredRawVault())
	handler := fixture.handler
	full := rawAuditPath(rawTestRequestID)
	approve := `{"decision":"until_lock","proof":{"password":` + rawTestPasswordJS + `}}`

	approved := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, approved.GrantID, approve), http.StatusOK, "")
	readRawAudit(t, handler, rawAsAgent, full, approved.GrantToken)
	pending := requestRawGrant(t, handler, rawTestOtherID)

	patchAgentRawAccess(t, handler, false)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", approved.GrantToken),
		http.StatusConflict, "raw_access_disabled")
	if items := handler.rawGrants.pending(); len(items) != 0 {
		t.Fatalf("pending after turning off=%#v", items)
	}
	wantRawStatus(t, decideRawGrant(t, handler, pending.GrantID, approve), http.StatusConflict, "raw_access_not_pending")
	handler.rawGrants.mu.Lock()
	held := handler.rawGrants.grants[approved.GrantID].holder
	handler.rawGrants.mu.Unlock()
	if held != nil {
		t.Fatal("a revoked grant kept its key")
	}

	// Turning it back on does not bring the grants back.
	patchAgentRawAccess(t, handler, true)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", approved.GrantToken),
		http.StatusForbidden, "raw_grant_invalid")

	// A grant that outlived the switch, such as one approved while it
	// changed, is still refused while the switch is off.
	late := requestRawGrant(t, handler, rawTestRequestID)
	wantRawStatus(t, decideRawGrant(t, handler, late.GrantID, approve), http.StatusOK, "")
	setAgentRawAccess(t, fixture.store, false)
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, full, "", late.GrantToken),
		http.StatusConflict, "raw_access_disabled")
	// The operator's own full view does not depend on the agent switch.
	readRawAudit(t, handler, rawAsOperator, full, "")
}

func TestDecodeJSONStringBytes(t *testing.T) {
	cases := map[string]string{
		`""`:                   "",
		`"plain"`:              "plain",
		`"a\"b\\c\/d"`:         `a"b\c/d`,
		`"\b\f\n\r\t"`:         "\b\f\n\r\t",
		`"lone \ud83d here"`:   "lone \uFFFD here",
		`"é raw utf-8"`:        "é raw utf-8",
		`"\u0041\u0042\u0043"`: "ABC",
		`"\u00e9\u4e2d"`:       "\u00e9\u4e2d",
		`"\ud83d\ude00"`:       "\U0001F600",
	}
	for input, want := range cases {
		got, err := decodeJSONStringBytes([]byte(input))
		if err != nil || string(got) != want {
			t.Fatalf("decode(%s)=%q err=%v want %q", input, got, err, want)
		}
		var standard string
		if err := json.Unmarshal([]byte(input), &standard); err != nil || standard != want {
			t.Fatalf("encoding/json disagrees on %s: %q", input, standard)
		}
	}
	for _, input := range []string{`plain`, `"unterminated`, `"bad \x escape"`, `"\u12"`, `"trailing \"`, `42`, `null`} {
		if _, err := decodeJSONStringBytes([]byte(input)); err == nil {
			t.Fatalf("decode(%s) succeeded", input)
		}
	}
}

func TestAuditReadReportsAMissingKeyOnlyAfterTheLocalKeyChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "astrlink.db")
	fixture := newRawAccessFixtureAt(t, path, nil)
	// A body that does not open under the current audit key is corrupt.
	nonce := make([]byte, storage.AuditNonceBytes)
	if err := fixture.store.InsertAuditBlob(context.Background(), storage.AuditBlob{
		RequestID: rawTestOtherID, Direction: storage.AuditDirectionRequest, MediaType: "application/json",
		Nonce: nonce, Ciphertext: []byte(strings.Repeat("x", 32)), CapturedBytes: 16,
		Exposure: storage.AuditExposureShareable,
	}); err != nil {
		t.Fatal(err)
	}
	wantRawStatus(t, rawHTTP(t, fixture.handler, rawAsOperator, http.MethodGet, rawAuditPath(rawTestOtherID), "", ""),
		http.StatusConflict, "audit_decrypt_failed")
	readRawAudit(t, fixture.handler, rawAsOperator, rawAuditPath(rawTestRequestID), "")
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}

	// The local key file is lost: every body sealed before now reports the
	// key as missing, and the metadata stays readable.
	store, err := openControlTestStore(t, context.Background(), path,
		sqlite.WithLocalKey([]byte(strings.Repeat("\x5c", 32))), sqlite.WithLogger(func(string, ...any) {}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler := newRawAccessHandler(t, store, nil)
	for _, id := range []string{rawTestRequestID, rawTestOtherID} {
		wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodGet, rawAuditPath(id), "", ""),
			http.StatusConflict, "audit_key_missing")
	}
	wantRawStatus(t, rawHTTP(t, handler, rawAsAgent, http.MethodGet, rawAuditPath(rawTestRequestID)+"?view=shareable", "", ""),
		http.StatusConflict, "audit_key_missing")
	wantRawStatus(t, rawHTTP(t, handler, rawAsOperator, http.MethodGet, RequestsPath+"/"+rawTestRequestID, "", ""),
		http.StatusOK, "")
}
