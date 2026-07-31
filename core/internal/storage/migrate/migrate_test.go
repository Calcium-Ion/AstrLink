package migrate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type execCall struct {
	query string
	args  []any
}

type fakeDatabase struct {
	transaction *fakeTransaction
	beginErr    error
}

func (database *fakeDatabase) Begin(context.Context) (Transaction, error) {
	if database.beginErr != nil {
		return nil, database.beginErr
	}
	return database.transaction, nil
}

type fakeTransaction struct {
	currentVersion int64
	rowErr         error
	failContains   string
	execCalls      []execCall
	committed      bool
	rolledBack     bool
}

func (transaction *fakeTransaction) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	transaction.execCalls = append(transaction.execCalls, execCall{query: query, args: append([]any(nil), args...)})
	if transaction.failContains != "" && strings.Contains(query, transaction.failContains) {
		return nil, errors.New("injected execution failure")
	}
	return nil, nil
}

func (transaction *fakeTransaction) QueryRowContext(context.Context, string, ...any) Row {
	return fakeRow{value: transaction.currentVersion, err: transaction.rowErr}
}

func (transaction *fakeTransaction) Commit() error {
	transaction.committed = true
	return nil
}

func (transaction *fakeTransaction) Rollback() error {
	transaction.rolledBack = true
	return nil
}

type fakeRow struct {
	value int64
	err   error
}

func (row fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected destination count")
	}
	destination, ok := destinations[0].(*int64)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*destination = row.value
	return nil
}

func TestRunnerAppliesMigrationsInOrderAndRecordsThem(t *testing.T) {
	transaction := &fakeTransaction{}
	runner, err := New(&fakeDatabase{transaction: transaction}, []Migration{
		{Version: 1, Name: "one", Statements: []string{"CREATE TABLE one (id INTEGER)"}},
		{Version: 3, Name: "three", Statements: []string{"CREATE TABLE three (id INTEGER)"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.now = func() time.Time {
		return time.Date(2026, 7, 22, 1, 2, 3, 0, time.FixedZone("test", 8*60*60))
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if !transaction.committed || transaction.rolledBack {
		t.Fatalf("committed=%t rolledBack=%t", transaction.committed, transaction.rolledBack)
	}
	if len(transaction.execCalls) != 5 {
		t.Fatalf("exec call count = %d, want 5", len(transaction.execCalls))
	}
	if !strings.Contains(transaction.execCalls[0].query, "schema_migrations") ||
		!strings.Contains(transaction.execCalls[1].query, "TABLE one") ||
		!strings.Contains(transaction.execCalls[3].query, "TABLE three") {
		t.Fatalf("unexpected execution order: %#v", transaction.execCalls)
	}
	if got := transaction.execCalls[2].args; len(got) != 3 || got[0] != int64(1) || got[1] != "one" || got[2] != "2026-07-21T17:02:03Z" {
		t.Fatalf("first migration record args = %#v", got)
	}
	if got := transaction.execCalls[4].args; len(got) != 3 || got[0] != int64(3) || got[1] != "three" {
		t.Fatalf("second migration record args = %#v", got)
	}
}

func TestRunnerSkipsAlreadyAppliedMigrations(t *testing.T) {
	transaction := &fakeTransaction{currentVersion: 1}
	runner, err := New(&fakeDatabase{transaction: transaction}, []Migration{
		{Version: 1, Name: "one", Statements: []string{"CREATE TABLE one (id INTEGER)"}},
		{Version: 2, Name: "two", Statements: []string{"CREATE TABLE two (id INTEGER)"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range transaction.execCalls {
		if strings.Contains(call.query, "TABLE one") {
			t.Fatalf("already applied migration was executed: %#v", call)
		}
	}
	if !transaction.committed {
		t.Fatal("transaction was not committed")
	}
}

func TestRunnerRollsBackOnMigrationFailure(t *testing.T) {
	transaction := &fakeTransaction{failContains: "TABLE broken"}
	runner, err := New(&fakeDatabase{transaction: transaction}, []Migration{
		{Version: 1, Name: "broken", Statements: []string{"CREATE TABLE broken ("}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); err == nil || !strings.Contains(err.Error(), "apply migration 1") {
		t.Fatalf("Up error = %v", err)
	}
	if transaction.committed || !transaction.rolledBack {
		t.Fatalf("committed=%t rolledBack=%t", transaction.committed, transaction.rolledBack)
	}
}

func TestRunnerRejectsNewerDatabase(t *testing.T) {
	transaction := &fakeTransaction{currentVersion: 2}
	runner, err := New(&fakeDatabase{transaction: transaction}, []Migration{
		{Version: 1, Name: "one", Statements: []string{"SELECT 1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(context.Background()); !errors.Is(err, ErrDatabaseNewer) {
		t.Fatalf("Up error = %v, want ErrDatabaseNewer", err)
	}
	if !transaction.rolledBack {
		t.Fatal("newer database transaction was not rolled back")
	}
}

func TestNewRejectsUnorderedMigrations(t *testing.T) {
	_, err := New(&fakeDatabase{transaction: &fakeTransaction{}}, []Migration{
		{Version: 2, Name: "two", Statements: []string{"SELECT 2"}},
		{Version: 1, Name: "one", Statements: []string{"SELECT 1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "strictly increasing") {
		t.Fatalf("New error = %v", err)
	}
}

func TestDefaultMigrationsIsolateCredentialsFromGenericDocuments(t *testing.T) {
	migrations := DefaultMigrations()
	if len(migrations) == 0 {
		t.Fatal("default migrations are empty")
	}
	foundCredentialTable := false
	foundAccessTokenTable := false
	foundAccessTokenSecretTable := false
	foundAccessTokenBootstrapState := false
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			lower := strings.ToLower(statement)
			if strings.Contains(lower, "create table service_credentials") {
				foundCredentialTable = true
				if !strings.Contains(lower, "credential_value blob not null") || !strings.Contains(lower, "references services(id) on delete cascade") {
					t.Fatalf("credential table lacks required isolation/cascade contract: %s", statement)
				}
				continue
			}
			if strings.Contains(lower, "create table local_access_tokens") {
				foundAccessTokenTable = true
				for _, required := range []string{
					"token_hash blob not null unique",
					"name_key text not null unique",
					"source text not null",
				} {
					if !strings.Contains(lower, required) {
						t.Fatalf("access token table lacks %q: %s", required, statement)
					}
				}
				if strings.Contains(lower, "token_value") {
					t.Fatalf("access token metadata table contains recoverable secret: %s", statement)
				}
				continue
			}
			if strings.Contains(lower, "create table local_access_token_secrets") {
				foundAccessTokenSecretTable = true
				if !strings.Contains(lower, "references local_access_tokens(id) on delete cascade") ||
					!strings.Contains(lower, "token_value text not null") {
					t.Fatalf("access token secret table lacks required isolation/cascade: %s", statement)
				}
				continue
			}
			if strings.Contains(lower, "create table local_access_token_bootstrap_state") {
				foundAccessTokenBootstrapState = true
				if !strings.Contains(lower, "singleton integer primary key") {
					t.Fatalf("access token bootstrap state lacks singleton marker: %s", statement)
				}
				continue
			}
			if strings.Contains(lower, "document_json") {
				for _, forbidden := range []string{"api_key", "access_token", "secret", "credential_value"} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("generic document migration %d contains credential column %q: %s", migration.Version, forbidden, statement)
					}
				}
			}
		}
	}
	if !foundCredentialTable {
		t.Fatal("default migrations do not create service_credentials")
	}
	if !foundAccessTokenTable || !foundAccessTokenSecretTable || !foundAccessTokenBootstrapState {
		t.Fatalf(
			"access token migrations incomplete: metadata=%t secret=%t bootstrap=%t",
			foundAccessTokenTable,
			foundAccessTokenSecretTable,
			foundAccessTokenBootstrapState,
		)
	}
}

func TestDefaultMigrationsUpgradeVersionTwoWithoutLosingExistingData(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	migrations := DefaultMigrations()
	versionTwoRunner, err := New(SQLDatabase{DB: database}, migrations[:2])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionTwoRunner.Up(context.Background()); err != nil {
		t.Fatalf("migrate to version 2: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO endpoints (id, document_json, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"endpoint_existing",
		`{"id":"endpoint_existing"}`,
		"2026-07-24T00:00:00Z",
		"2026-07-24T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO endpoint_credentials (endpoint_id, credential_value, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"endpoint_existing",
		[]byte("existing-secret"),
		"2026-07-24T00:00:00Z",
		"2026-07-24T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	fullRunner, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := fullRunner.Up(context.Background()); err != nil {
		t.Fatalf("upgrade to latest version: %v", err)
	}
	var version int
	if err := database.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 12 {
		t.Fatalf("schema version = %d, want 12", version)
	}
	var requestRecordsTable int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='request_records'`,
	).Scan(&requestRecordsTable); err != nil || requestRecordsTable != 1 {
		t.Fatalf("request_records missing after upgrade: count=%d err=%v", requestRecordsTable, err)
	}
	var servicesTable int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='services'`,
	).Scan(&servicesTable); err != nil || servicesTable != 1 {
		t.Fatalf("services missing after upgrade: count=%d err=%v", servicesTable, err)
	}
	var auditTables int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master
WHERE type='table' AND name IN ('audit_settings', 'audit_keys', 'audit_blobs')`).Scan(&auditTables); err != nil || auditTables != 3 {
		t.Fatalf("audit tables missing after upgrade: count=%d err=%v", auditTables, err)
	}
	var credential []byte
	if err := database.QueryRow(
		`SELECT credential_value FROM service_credentials WHERE service_id = 'endpoint_existing'`,
	).Scan(&credential); err != nil {
		t.Fatal(err)
	}
	if string(credential) != "existing-secret" {
		t.Fatalf("credential after upgrade = %q", credential)
	}
	var tableCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table'
  AND name IN ('local_access_tokens', 'local_access_token_secrets', 'local_access_token_bootstrap_state')`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 3 {
		t.Fatalf("access token table count = %d, want 3", tableCount)
	}
	var tokenCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM local_access_tokens`).Scan(&tokenCount); err != nil {
		t.Fatal(err)
	}
	if tokenCount != 0 {
		t.Fatalf("schema migration unexpectedly synthesized token rows: %d", tokenCount)
	}
	var privacyPolicyDocument string
	if err := database.QueryRow(
		`SELECT document_json FROM policies WHERE id = 'policy_privacy_default'`,
	).Scan(&privacyPolicyDocument); err != nil {
		t.Fatalf("read default privacy policy: %v", err)
	}
	for _, expected := range []string{
		`"enabled":false`,
		`"detector":"regex"`,
		`"local_model_id":null`,
		`"min_confidence":0.6`,
		`"match":{}`,
		`"request_action":"redact"`,
		`"response_action":"allow"`,
		`"response_restore":true`,
	} {
		if !strings.Contains(privacyPolicyDocument, expected) {
			t.Fatalf("default privacy policy missing %s: %s", expected, privacyPolicyDocument)
		}
	}
}

func TestSelectableLocalModelMigrationPreservesLegacyOpenAIFilterSelection(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrations := DefaultMigrations()
	versionFour, err := New(SQLDatabase{DB: database}, migrations[:4])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionFour.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE policies
SET document_json = json_set(
    json_remove(document_json, '$.local_model_id'),
    '$.detector',
    'openai_privacy_filter'
)
WHERE id = 'policy_privacy_default'`); err != nil {
		t.Fatal(err)
	}
	full, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var document string
	if err := database.QueryRow(`SELECT document_json
FROM policies
WHERE id = 'policy_privacy_default'`).Scan(&document); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"detector":"local_model"`,
		`"local_model_id":"model_de5ac42e03b4af887b31a7645d3ce111"`,
	} {
		if !strings.Contains(document, expected) {
			t.Fatalf("migrated document=%s missing %s", document, expected)
		}
	}
	var columns int
	if err := database.QueryRow(`SELECT COUNT(*)
FROM pragma_table_info('privacy_model_installations')
WHERE name IN ('manifest_identity', 'manifest_sha256', 'manifest_json')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 3 {
		t.Fatalf("manifest binding column count=%d", columns)
	}
}

func TestPrivacyModelDisplayMetadataMigrationBackfillsOldRecords(t *testing.T) {
	database, err := sql.Open(
		"sqlite",
		filepath.Join(t.TempDir(), "privacy-model-metadata.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrations := DefaultMigrations()
	versionFive, err := New(SQLDatabase{DB: database}, migrations[:5])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionFive.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		id       string
		document string
	}{
		{
			id: "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			document: `{"id":"model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
				`"source":"catalog","catalog_id":"catalog_openai_privacy_filter"}`,
		},
		{
			id: "model_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			document: `{"id":"model_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",` +
				`"source":"custom","catalog_id":null}`,
		},
	} {
		if _, err := database.Exec(
			`INSERT INTO privacy_model_installations
			    (id, document_json, created_at, updated_at)
			VALUES (?, ?, ?, ?)`,
			record.id,
			record.document,
			"2026-07-24T00:00:00Z",
			"2026-07-24T00:00:00Z",
		); err != nil {
			t.Fatal(err)
		}
	}
	full, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	var catalogSource, license, languages string
	if err := database.QueryRow(`SELECT
    json_extract(document_json, '$.catalog_source'),
    json_extract(document_json, '$.license'),
    json_extract(document_json, '$.languages')
FROM privacy_model_installations
WHERE id = 'model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`).Scan(
		&catalogSource,
		&license,
		&languages,
	); err != nil {
		t.Fatal(err)
	}
	if catalogSource != "official" ||
		license != "Apache-2.0" ||
		languages != `["en"]` {
		t.Fatalf(
			"catalog metadata source=%q license=%q languages=%q",
			catalogSource,
			license,
			languages,
		)
	}
	var customSource, customLicense sql.NullString
	if err := database.QueryRow(`SELECT
    json_extract(document_json, '$.catalog_source'),
    json_extract(document_json, '$.license'),
    json_extract(document_json, '$.languages')
FROM privacy_model_installations
WHERE id = 'model_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`).Scan(
		&customSource,
		&customLicense,
		&languages,
	); err != nil {
		t.Fatal(err)
	}
	if customSource.Valid || customLicense.Valid || languages != `[]` {
		t.Fatalf(
			"custom metadata source=%#v license=%#v languages=%q",
			customSource,
			customLicense,
			languages,
		)
	}
}

func TestHTTPMetaMigrationPreservesAuditBlobsAndWidensDirection(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	migrations := DefaultMigrations()
	versionNine, err := New(SQLDatabase{DB: database}, migrations[:9])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionNine.Up(context.Background()); err != nil {
		t.Fatalf("migrate to version 9: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO request_records (
    id, started_at, status, input_protocol, streaming, audit_json, created_at
) VALUES ('request_v9', '2026-07-20T00:00:00Z', 'succeeded', 'openai.responses', 0, '{}', '2026-07-20T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	ciphertext := []byte("ciphertext-v9")
	if _, err := database.Exec(
		`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
) VALUES ('request_v9', 'request', 'application/json', ?, ?, 0, 13, '2026-07-20T00:00:00Z')`,
		[]byte("nonce-000000"), ciphertext,
	); err != nil {
		t.Fatal(err)
	}

	full, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Up(context.Background()); err != nil {
		t.Fatalf("upgrade to version 10: %v", err)
	}

	var count int
	var preserved []byte
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM audit_blobs WHERE request_id = 'request_v9'`,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("blob count=%d err=%v", count, err)
	}
	if err := database.QueryRow(
		`SELECT ciphertext FROM audit_blobs WHERE request_id = 'request_v9'`,
	).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if string(preserved) != string(ciphertext) {
		t.Fatalf("ciphertext altered: %q", preserved)
	}

	// The rebuilt CHECK must accept the new direction and the FK must survive.
	if _, err := database.Exec(
		`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
) VALUES ('request_v9', 'http_meta', 'application/json', ?, ?, 0, 2, '2026-07-20T00:00:00Z')`,
		[]byte("nonce-000001"), []byte("{}"),
	); err != nil {
		t.Fatalf("http_meta direction rejected after migration: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO audit_blobs (
    request_id, direction, media_type, nonce, ciphertext, truncated, captured_bytes, created_at
) VALUES ('request_v9', 'bogus', 'application/json', ?, ?, 0, 2, '2026-07-20T00:00:00Z')`,
		[]byte("nonce-000002"), []byte("{}"),
	); err == nil {
		t.Fatal("bogus direction accepted after migration")
	}
	if _, err := database.Exec(`DELETE FROM request_records WHERE id = 'request_v9'`); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_blobs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade delete broken after rebuild: count=%d err=%v", count, err)
	}

	var httpMetaEnabled int
	if err := database.QueryRow(
		`SELECT http_meta_enabled FROM audit_settings WHERE id = 1`,
	).Scan(&httpMetaEnabled); err != nil || httpMetaEnabled != 1 {
		t.Fatalf("http_meta_enabled=%d err=%v, want backfilled 1", httpMetaEnabled, err)
	}
}
