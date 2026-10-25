package migrate

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// migrateBeforeFreshInstallDefaults opens a database at the published schema
// just before fresh_install_defaults and returns it with the full list.
func migrateBeforeFreshInstallDefaults(t *testing.T) (*sql.DB, []Migration) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh-install-defaults.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "fresh_install_defaults" {
			index = i
		}
	}
	if index < 1 || migrations[index-1].Name != "request_output_activity" {
		t.Fatal("fresh_install_defaults must follow the published output activity migration")
	}
	before, err := New(SQLDatabase{DB: db}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, migrations
}

func migrateFully(t *testing.T, db *sql.DB, migrations []Migration) {
	t.Helper()
	after, err := New(SQLDatabase{DB: db}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := after.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapTokenSourceMarksOnlyTheTokenTheBootstrapIssued(t *testing.T) {
	db, migrations := migrateBeforeFreshInstallDefaults(t)
	// What a store before this fix wrote: the bootstrap token and its state
	// share one timestamp, and every token is stored as a user's.
	const issued = "2026-10-01T08:00:00.123456789Z"
	if _, err := db.Exec(`INSERT INTO local_access_token_bootstrap_state (singleton, completed_at) VALUES (1, ?)`, issued); err != nil {
		t.Fatal(err)
	}
	for _, token := range []struct{ id, name, createdAt string }{
		{"access_token_bootstrap", "默认令牌", issued},
		{"access_token_created", "laptop", "2026-10-02T09:00:00Z"},
	} {
		if _, err := db.Exec(`INSERT INTO local_access_tokens
(id, name, name_key, token_hash, token_hint, source, created_at)
VALUES (?, ?, ?, randomblob(32), 'astr_…AAAAAA', 'user', ?)`, token.id, token.name, token.name, token.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	migrateFully(t, db, migrations)
	for id, want := range map[string]string{
		"access_token_bootstrap": "system_default",
		"access_token_created":   "user",
	} {
		var source string
		if err := db.QueryRow(`SELECT source FROM local_access_tokens WHERE id = ?`, id).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != want {
			t.Errorf("%s source = %q, want %q", id, source, want)
		}
	}
}

// The per-kind rules every earlier build read for a policy without
// kind_rules, before credentials became the only default.
const previousDefaultKindRules = `[
{"kind":"common_secret","enabled":true,"style":"token","notify":true},
{"kind":"payment_card","enabled":true,"style":"token","notify":true},
{"kind":"account","enabled":true,"style":"token","notify":true},
{"kind":"email","enabled":true,"style":"natural","notify":true},
{"kind":"phone","enabled":true,"style":"token","notify":true},
{"kind":"url","enabled":false,"style":"natural","notify":true},
{"kind":"ip_address","enabled":false,"style":"natural","notify":true},
{"kind":"private_person","enabled":true,"style":"token","notify":true},
{"kind":"private_address","enabled":true,"style":"token","notify":true},
{"kind":"private_date","enabled":false,"style":"token","notify":true}]`

const secretsOnlyKindRules = `[
{"kind":"common_secret","enabled":true,"style":"token","notify":true},
{"kind":"payment_card","enabled":false,"style":"token","notify":true},
{"kind":"account","enabled":false,"style":"token","notify":true},
{"kind":"email","enabled":false,"style":"natural","notify":true},
{"kind":"phone","enabled":false,"style":"token","notify":true},
{"kind":"url","enabled":false,"style":"natural","notify":true},
{"kind":"ip_address","enabled":false,"style":"natural","notify":true},
{"kind":"private_person","enabled":false,"style":"token","notify":true},
{"kind":"private_address","enabled":false,"style":"token","notify":true},
{"kind":"private_date","enabled":false,"style":"token","notify":true}]`

func readPolicyDocument(t *testing.T, db *sql.DB) (map[string]any, string) {
	t.Helper()
	var document, updatedAt string
	if err := db.QueryRow(`SELECT document_json, updated_at FROM policies WHERE id = 'policy_privacy_default'`).Scan(&document, &updatedAt); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(document), &fields); err != nil {
		t.Fatal(err)
	}
	return fields, updatedAt
}

func decodeJSON(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestFreshInstallDefaultsMoveOnlyTheUntouchedPrivacyPolicy(t *testing.T) {
	db, migrations := migrateBeforeFreshInstallDefaults(t)
	// Exactly what every release through v0.2.18 stored for a policy nobody
	// saved: the v4 seed as the later migrations completed it.
	before, beforeUpdated := readPolicyDocument(t, db)
	if before["enabled"] != false {
		t.Fatalf("published default = %#v", before)
	}
	if _, present := before["kind_rules"]; present {
		t.Fatalf("published default already lists kind_rules: %#v", before)
	}
	migrateFully(t, db, migrations)

	after, afterUpdated := readPolicyDocument(t, db)
	want := map[string]any{}
	for key, value := range before {
		want[key] = value
	}
	want["enabled"] = true
	want["kind_rules"] = decodeJSON(t, secretsOnlyKindRules)
	want["allowlist_rules"] = decodeJSON(t, `[
{"type":"domain_suffix","value":"localhost"},
{"type":"domain_suffix","value":"github.com"},
{"type":"domain_suffix","value":"githubusercontent.com"},
{"type":"domain_suffix","value":"example.com"},
{"type":"domain_suffix","value":"example.net"},
{"type":"domain_suffix","value":"example.org"},
{"type":"domain_suffix","value":"example"},
{"type":"domain_suffix","value":"test"},
{"type":"domain_suffix","value":"invalid"},
{"type":"cidr","value":"127.0.0.0/8"},
{"type":"cidr","value":"::1/128"},
{"type":"cidr","value":"10.0.0.0/8"},
{"type":"cidr","value":"172.16.0.0/12"},
{"type":"cidr","value":"192.168.0.0/16"},
{"type":"cidr","value":"169.254.0.0/16"}]`)
	if !reflect.DeepEqual(after, want) {
		t.Fatalf("migrated default =\n%#v\nwant\n%#v", after, want)
	}
	// Not a change the user made: sync must not see one.
	if afterUpdated != beforeUpdated {
		t.Fatalf("updated_at moved from %q to %q", beforeUpdated, afterUpdated)
	}
	var logged int
	if err := db.QueryRow(`SELECT COUNT(*) FROM config_change_log WHERE entity_kind = 'policy'`).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != 0 {
		t.Fatalf("the migration logged %d policy changes", logged)
	}
	// The trigger it held off is back for the user's own edits.
	if _, err := db.Exec(`UPDATE policies SET document_json = json_set(document_json, '$.enabled', json('false'))`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM config_change_log WHERE entity_kind = 'policy' AND entity_id = 'policy_privacy_default'`).Scan(&logged); err != nil {
		t.Fatal(err)
	}
	if logged != 1 {
		t.Fatalf("an edit after the migration logged %d changes, want 1", logged)
	}
}

func TestFreshInstallDefaultsLeaveEveryChangedPrivacyPolicyAsItWas(t *testing.T) {
	tests := []struct {
		name string
		// edit turns the published default into what the user left.
		edit string
		// kindRules is the list expected afterwards; empty means the stored
		// document must not change at all.
		kindRules string
	}{
		{
			// Every save writes the full kind list.
			name: "privacy turned on",
			edit: `json_set(document_json, '$.enabled', json('true'), '$.kind_rules', json('` + previousDefaultKindRules + `'))`,
		},
		{
			name: "turned on and off again",
			edit: `json_set(document_json, '$.kind_rules', json('` + previousDefaultKindRules + `'))`,
		},
		{
			name: "local model chosen",
			edit: `json_set(document_json, '$.enabled', json('true'), '$.detector', 'local_model',
    '$.local_model_id', 'model_de5ac42e03b4af887b31a7645d3ce111', '$.kind_rules', json('` + previousDefaultKindRules + `'))`,
		},
		{
			// A test build saved this before kind_rules existed.
			name:      "turned on before kind rules existed",
			edit:      `json_set(document_json, '$.enabled', json('true'))`,
			kindRules: previousDefaultKindRules,
		},
		{
			name:      "allowlist cleared before kind rules existed",
			edit:      `json_set(document_json, '$.allowlist_rules', json('[]'))`,
			kindRules: previousDefaultKindRules,
		},
		{
			name:      "blocking before kind rules existed",
			edit:      `json_set(document_json, '$.request_action', 'block')`,
			kindRules: previousDefaultKindRules,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, migrations := migrateBeforeFreshInstallDefaults(t)
			if _, err := db.Exec(`UPDATE policies SET document_json = ` + test.edit + ` WHERE id = 'policy_privacy_default'`); err != nil {
				t.Fatal(err)
			}
			before, _ := readPolicyDocument(t, db)
			migrateFully(t, db, migrations)
			after, _ := readPolicyDocument(t, db)
			want := before
			if test.kindRules != "" {
				want = map[string]any{}
				for key, value := range before {
					want[key] = value
				}
				want["kind_rules"] = decodeJSON(t, test.kindRules)
			}
			if !reflect.DeepEqual(after, want) {
				t.Fatalf("policy =\n%#v\nwant\n%#v", after, want)
			}
		})
	}
}
