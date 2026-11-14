package clients

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/agents"
	cc "github.com/QuantumNous/astrlink/desktop/internal/clientconfig"
	"github.com/QuantumNous/astrlink/desktop/internal/clientconfig/format"
)

// DimAgent 0.5.24's tables and the Rust store's v34 provider tables.
const dimAgentStable = `
CREATE TABLE providers (
 providerId TEXT PRIMARY KEY, source TEXT NOT NULL, displayName TEXT NOT NULL,
 driverKind TEXT NOT NULL, defaultBaseUrl TEXT, baseUrl TEXT, auth TEXT NOT NULL,
 credential TEXT, accountUsageSettings TEXT, headers TEXT, proxy TEXT,
 timeoutMs INTEGER, retryConfig TEXT, models TEXT NOT NULL, modelConfigOverrides TEXT,
 capabilityDefaults TEXT, activeModelId TEXT, modelsSource TEXT NOT NULL,
 catalogVersion TEXT, modelsUpdatedAt TEXT, metadata TEXT, enabled INTEGER NOT NULL,
 createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL, version INTEGER NOT NULL
);
CREATE TABLE provider_selections (
 selectionId TEXT PRIMARY KEY, scope TEXT NOT NULL, cwd TEXT, providerId TEXT NOT NULL,
 modelId TEXT, createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL, version INTEGER NOT NULL
);
CREATE UNIQUE INDEX global_selection ON provider_selections(scope) WHERE scope='global';
`

const dimAgentNative = `
CREATE TABLE agent_store_format(version INTEGER NOT NULL);
INSERT INTO agent_store_format VALUES(34);
CREATE TABLE providers(provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, record TEXT NOT NULL);
CREATE TABLE provider_selections(scope TEXT PRIMARY KEY, version INTEGER NOT NULL, provider_id TEXT NOT NULL, model_id TEXT NOT NULL);
`

// dimAgentFixture is a home with DimAgent's database for target, holding a
// session AstrLink must never touch, and a connection of its own to it.
func dimAgentFixture(t *testing.T, target string) (agents.Home, string, *sql.DB) {
	t.Helper()
	home := scratchHome(t)
	path := DimAgent.ConfigPaths(home, target, nil)[0]
	mkdir(t, filepath.Dir(path))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	schema := dimAgentNative
	if strings.HasSuffix(target, "standard") {
		schema = dimAgentStable
	}
	for _, statement := range []string{"PRAGMA journal_mode=WAL", schema,
		"CREATE TABLE sessions(id TEXT, body TEXT); INSERT INTO sessions VALUES('session','keep conversation')"} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return home, path, db
}

func dimAgentSaved(t *testing.T, path string) map[string]any {
	t.Helper()
	text, err := dimAgentRead(path)
	if err != nil || text == nil {
		t.Fatalf("read: %v %v", text, err)
	}
	return decode(t, *text).(map[string]any)
}

func dimAgentDesired(t *testing.T, target string) map[string]any {
	t.Helper()
	c := connection(codexModels())
	c.Target = target
	plan := planWrite(t, DimAgent, texts("{}"), c, nil)
	return decode(t, plan.Contents[0]).(map[string]any)
}

func dimAgentPlace(t *testing.T, target string) cc.Place { return targeted(t, DimAgent, target) }

func dimAgentQuery[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var value T
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

func dimAgentStatus(t *testing.T, home agents.Home, target string) cc.ClientStatus {
	t.Helper()
	statuses, err := cc.StatusOf(home, []cc.Client{DimAgent}, origin, tokens())
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range statuses {
		if status.Target != nil && *status.Target == target {
			return status
		}
	}
	t.Fatalf("no status for %s", target)
	return cc.ClientStatus{}
}

func TestDimAgentWritesAllProtocolsInBothStoresAndRestoresEmptySettings(t *testing.T) {
	for _, target := range DimAgent.Targets() {
		home, path, db := dimAgentFixture(t, target)
		place := dimAgentPlace(t, target)
		list := []string{"gpt-route", "responses-model", "claude-model", "gemini-model"}
		c := listed(codexModels(), list...)
		c.Target = target
		for i, model := range list {
			c.Protocols[model] = dimAgentRoutes[i].protocol
		}
		if asked := writePlace(t, home, place, c, cc.Catalog{}, false); asked != nil {
			t.Fatalf("%s: asked %+v", target, asked)
		}
		stable := strings.HasSuffix(target, "standard")
		if stable {
			if _, err := db.Exec(`INSERT INTO providers(providerId,source,displayName,driverKind,auth,models,modelsSource,enabled,createdAt,updatedAt,version) VALUES('other','custom','Other','openai-compatible','{}','[]','user',1,'created','updated',1)`); err != nil {
				t.Fatal(err)
			}
		} else if _, err := db.Exec(`INSERT INTO providers VALUES('other',77,'other private settings')`); err != nil {
			t.Fatal(err)
		}
		value := dimAgentSaved(t, path)
		if want := map[string]any{"providerId": "local-chat", "modelId": "gpt-route"}; !reflect.DeepEqual(value["selection"], want) {
			t.Fatalf("%s: selection %v", target, value["selection"])
		}
		for i, route := range dimAgentRoutes {
			provider := value["providers"].(map[string]any)[route.id].(map[string]any)
			if provider["driverKind"] != route.driver || provider["baseUrl"] != dimAgentAddress(route.protocol, origin) {
				t.Fatalf("%s %s: %v", target, route.id, provider)
			}
			if !reflect.DeepEqual(provider["headers"], map[string]any{"User-Agent": "DimAgent"}) ||
				!reflect.DeepEqual(provider["auth"], map[string]any{"type": "api_key", "headerName": "Authorization", "prefix": "Bearer "}) {
				t.Fatalf("%s %s: %v", target, route.id, provider)
			}
			model := provider["models"].([]any)[0].(map[string]any)
			if model["modelId"] != list[i] || model["capabilities"].(map[string]any)["toolCalling"] != true {
				t.Fatalf("%s %s: %v", target, route.id, model)
			}
			if stable {
				if provider["credential"].(map[string]any)["apiKey"] != token || provider["activeModelId"] != list[i] {
					t.Fatalf("%s %s: %v", target, route.id, provider)
				}
				continue
			}
			if provider["credential"] != token {
				t.Fatalf("%s %s: %v", target, route.id, provider["credential"])
			}
			var version int64
			var raw string
			if err := db.QueryRow("SELECT version,record FROM providers WHERE provider_id=?", route.id).Scan(&version, &raw); err != nil {
				t.Fatal(err)
			}
			if decode(t, raw).(map[string]any)["version"] != format.Number(fmt.Sprint(version)) {
				t.Fatalf("%s %s: version %d in %s", target, route.id, version, raw)
			}
		}
		status := dimAgentStatus(t, home, target)
		if status.State != cc.Configured || status.Models.Model == nil || *status.Models.Model != "gpt-route" || !reflect.DeepEqual(status.Models.Models, list) {
			t.Fatalf("%s: status %+v", target, status)
		}
		syncAt(t, home, "http://127.0.0.1:19999")
		if got := dimAgentSaved(t, path)["providers"].(map[string]any)["local-gemini"].(map[string]any)["baseUrl"]; got != "http://127.0.0.1:19999/v1beta" {
			t.Fatalf("%s: synced %v", target, got)
		}
		removePlace(t, home, place)
		if saved := dimAgentSaved(t, path); len(saved) != 0 {
			t.Fatalf("%s: left %v", target, saved)
		}
		wantVersion := int64(77)
		if stable {
			wantVersion = 1
		}
		if got := dimAgentQuery[int64](t, db, "SELECT version FROM providers"); got != wantVersion {
			t.Fatalf("%s: other provider version %d", target, got)
		}
		if got := dimAgentQuery[string](t, db, "SELECT body FROM sessions"); got != "keep conversation" {
			t.Fatalf("%s: session %q", target, got)
		}
		if runtime.GOOS != "windows" {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if mode := fileMode(t, path+suffix); mode != 0o600 {
					t.Fatalf("%s%s: mode %o", target, suffix, mode)
				}
			}
		}
	}
}

func TestDimAgentConfirmsReplacementMasksSecretsAndRestoresTheProviderAndSelection(t *testing.T) {
	for _, target := range []string{"standard", "rust"} {
		home, path, _ := dimAgentFixture(t, target)
		original := dimAgentDesired(t, target)
		provider := original["providers"].(map[string]any)["local-chat"].(map[string]any)
		provider["baseUrl"] = "https://example.test/v1"
		if target == "standard" {
			provider["credential"] = map[string]any{"type": "apiKey", "apiKey": "previous-private-token"}
			provider["timeoutMs"] = format.Number("1250.5")
		} else {
			provider["credential"] = "previous-private-token"
		}
		provider["headers"] = map[string]any{"Authorization": "Bearer another-private-secret", "X-Custom": "custom-private-value"}
		provider["proxy"] = "http://user:proxy-password@example.test"
		original["selection"] = map[string]any{"providerId": "other", "modelId": "other-model"}
		empty, encoded := "{}", format.Encode(original)
		if err := dimAgentWrite(path, &empty, &encoded); err != nil {
			t.Fatal(err)
		}
		original = dimAgentSaved(t, path)
		place := dimAgentPlace(t, target)
		c := connection(codexModels())
		c.Target = target
		if asked := writePlace(t, home, place, c, cc.Catalog{}, false); asked == nil {
			t.Fatalf("%s: wrote over the user's provider", target)
		}
		if saved := dimAgentSaved(t, path); !reflect.DeepEqual(saved, original) {
			t.Fatalf("%s: changed %v", target, saved)
		}
		model := "gpt-route"
		selection, err := cc.Select(&cc.Models{Model: &model}, DimAgent)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := cc.PreviewPlace(home, place, origin, &cc.PreviewWrite{
			Selection: selection, Secret: cc.Secret{ID: "token_01", Token: token}, Hint: "astr_…AAA0", Details: cc.Catalog{},
		})
		if err != nil {
			t.Fatal(err)
		}
		shown := fmt.Sprintf("%+v", preview)
		for _, secret := range []string{token, "previous-private-token", "another-private-secret", "custom-private-value", "proxy-password"} {
			if strings.Contains(shown, secret) {
				t.Fatalf("%s: preview shows %q", target, secret)
			}
		}
		if !strings.Contains(shown, "astr_…AAA0") {
			t.Fatalf("%s: preview %s", target, shown)
		}
		writePlace(t, home, place, c, cc.Catalog{}, true)
		// Moving to another protocol gives the replaced provider back, and
		// a port sync follows the provider AstrLink still manages.
		moved := connection(codexModels())
		moved.Target = target
		moved.Protocols["gpt-route"] = "openai.responses"
		writePlace(t, home, place, moved, cc.Catalog{}, true)
		syncAt(t, home, "http://127.0.0.1:19999")
		saved := dimAgentSaved(t, path)["providers"].(map[string]any)
		if !reflect.DeepEqual(saved["local-chat"], original["providers"].(map[string]any)["local-chat"]) {
			t.Fatalf("%s: local-chat %v", target, saved["local-chat"])
		}
		if got := saved["local-responses"].(map[string]any)["baseUrl"]; got != "http://127.0.0.1:19999/v1" {
			t.Fatalf("%s: synced %v", target, got)
		}
		removePlace(t, home, place)
		if saved := dimAgentSaved(t, path); !reflect.DeepEqual(saved, original) {
			t.Fatalf("%s: restored %v\nwant %v", target, saved, original)
		}
	}
}

func TestDimAgentKeepsUserChangesAndOtherRows(t *testing.T) {
	for _, target := range []string{"standard", "rust"} {
		home, path, _ := dimAgentFixture(t, target)
		place := dimAgentPlace(t, target)
		c := connection(codexModels())
		c.Target = target
		writePlace(t, home, place, c, cc.Catalog{}, false)
		written, err := dimAgentRead(path)
		if err != nil {
			t.Fatal(err)
		}
		changed := dimAgentSaved(t, path)
		changed["providers"].(map[string]any)["local-chat"].(map[string]any)["baseUrl"] = "https://changed.test/v1"
		changed["selection"] = map[string]any{"providerId": "other", "modelId": "chosen-by-user"}
		encoded := format.Encode(changed)
		if err := dimAgentWrite(path, written, &encoded); err != nil {
			t.Fatal(err)
		}
		changed = dimAgentSaved(t, path)
		if state := dimAgentStatus(t, home, target).State; state != cc.Modified {
			t.Fatalf("%s: %s", target, state)
		}
		syncAt(t, home, "http://127.0.0.1:19999")
		removePlace(t, home, place)
		if saved := dimAgentSaved(t, path); !reflect.DeepEqual(saved, changed) {
			t.Fatalf("%s: %v\nwant %v", target, saved, changed)
		}
	}
}

func TestDimAgentConcurrentChangesAndSQLErrorsRollBackTheWholeWrite(t *testing.T) {
	for _, target := range []string{"standard", "rust"} {
		_, path, db := dimAgentFixture(t, target)
		next := format.Encode(dimAgentDesired(t, target))
		empty := "{}"
		if _, err := db.Exec(`CREATE TRIGGER reject_selection BEFORE INSERT ON provider_selections BEGIN SELECT RAISE(ABORT, 'private database detail'); END`); err != nil {
			t.Fatal(err)
		}
		err := dimAgentWrite(path, &empty, &next)
		if err == nil || strings.Contains(err.Error(), "private database detail") || strings.Contains(err.Error(), token) {
			t.Fatalf("%s: %v", target, err)
		}
		if saved := dimAgentSaved(t, path); len(saved) != 0 {
			t.Fatalf("%s: kept %v", target, saved)
		}
		if _, err := db.Exec("DROP TRIGGER reject_selection"); err != nil {
			t.Fatal(err)
		}
		if err := dimAgentWrite(path, &empty, &next); err != nil {
			t.Fatal(err)
		}
		if err := dimAgentWrite(path, &empty, &next); err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatalf("%s: %v", target, err)
		}
		if saved := dimAgentSaved(t, path); !reflect.DeepEqual(saved, decode(t, next)) {
			t.Fatalf("%s: %v", target, saved)
		}
	}
}

func TestDimAgentRemovalKeepsWorkspaceSelectionsAndRollsBack(t *testing.T) {
	_, path, db := dimAgentFixture(t, "rust")
	next := format.Encode(dimAgentDesired(t, "rust"))
	empty := "{}"
	if err := dimAgentWrite(path, &empty, &next); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO provider_selections VALUES('workspace:test',1,'local-chat','gpt-route')`); err != nil {
		t.Fatal(err)
	}
	if err := dimAgentWrite(path, &next, nil); err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatal(err)
	}
	if saved := dimAgentSaved(t, path); !reflect.DeepEqual(saved, decode(t, next)) {
		t.Fatal(saved)
	}
	if got := dimAgentQuery[int64](t, db, "SELECT COUNT(*) FROM provider_selections"); got != 2 {
		t.Fatal(got)
	}
}

func TestDimAgentUnsupportedAndOversizedStoresStayUntouchedAndDoNotHideOtherClients(t *testing.T) {
	home, path, db := dimAgentFixture(t, "rust")
	if _, err := db.Exec("UPDATE agent_store_format SET version=999"); err != nil {
		t.Fatal(err)
	}
	empty, next := "{}", format.Encode(dimAgentDesired(t, "rust"))
	if err := dimAgentWrite(path, &empty, &next); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	statuses, err := cc.Status(home, origin, tokens())
	if err != nil {
		t.Fatal(err)
	}
	var invalid, codex bool
	for _, status := range statuses {
		if status.Client == DimAgent && status.Target != nil && *status.Target == "rust" {
			invalid = status.State == cc.Invalid
		}
		codex = codex || status.Client == Codex
	}
	if !invalid || !codex {
		t.Fatalf("invalid %v, codex listed %v", invalid, codex)
	}
	if _, err := db.Exec("UPDATE agent_store_format SET version=34"); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat(" ", dimAgentLongest+1)
	if _, err := db.Exec("INSERT INTO providers VALUES('local-chat',1,?)", large); err != nil {
		t.Fatal(err)
	}
	if _, err := dimAgentRead(path); err == nil {
		t.Fatal("read an oversized record")
	}
	if err := dimAgentWrite(path, &empty, &next); err == nil {
		t.Fatal("wrote over an oversized record")
	}
	if got := dimAgentQuery[int64](t, db, "SELECT length(record) FROM providers"); got != int64(len(large)) {
		t.Fatal(got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	if text, err := dimAgentRead(path); err != nil || text != nil {
		t.Fatal(text, err)
	}
	if err := dimAgentWrite(path, nil, &empty); err == nil {
		t.Fatal("wrote a missing database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created the database", err)
	}
}

func TestDimAgentIsDetectedByItsDatabase(t *testing.T) {
	home := scratchHome(t)
	for _, target := range DimAgent.Targets() {
		if DimAgent.Detected(home, target) {
			t.Fatalf("%s detected without its database", target)
		}
		writeFile(t, DimAgent.ConfigPaths(home, target, nil)[0], "")
		if !DimAgent.Detected(home, target) {
			t.Fatalf("%s not detected", target)
		}
	}
}
