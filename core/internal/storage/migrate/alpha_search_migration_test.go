package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAlphaSearchMigrationAppendsOnlyMissingNativeCapabilities(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "alpha-search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	migrations := DefaultMigrations()
	index := -1
	for i, migration := range migrations {
		if migration.Name == "native_alpha_search_capabilities" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("alpha_search migration is missing")
	}
	previous, err := New(SQLDatabase{DB: database}, migrations[:index])
	if err != nil {
		t.Fatal(err)
	}
	if err := previous.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	const existing = `{"protocol":"openai.responses","mode":"native","streaming":true},{"protocol":"openai.chat","mode":"native","streaming":true,"convert_to":"openai.responses"}`
	const alpha = `{"protocol":"openai.alpha_search","mode":"native","streaming":false}`
	fixtures := []struct {
		id, kind, capabilities string
		appendAlpha            bool
	}{
		{"codex_missing", "codex_subscription", "[" + existing + "]", true},
		{"newapi_missing", "newapi", "[" + existing + "]", false},
		{"codex_present", "codex_subscription", "[" + alpha + "," + existing + "]", false},
		{"newapi_present", "newapi", "[" + existing + "," + alpha + "]", false},
		{"newapi_implicit_false", "newapi", `[{"protocol":"openai.alpha_search","mode":"native","convert_to":""}]`, false},
		{"codex_empty", "codex_subscription", "[]", true},
		{"codex_null", "codex_subscription", "null", true},
		{"codex_absent", "codex_subscription", "", true},
		{"newapi_empty", "newapi", "[]", false},
		{"newapi_null", "newapi", "null", false},
		{"newapi_absent", "newapi", "", false},
		{"openai_unchanged", "openai", "[" + existing + "]", false},
		{"claude_unchanged", "claude_subscription", "[" + existing + "]", false},
		{"custom_unchanged", "custom", "[" + existing + "]", false},
	}
	originals := make(map[string]string)
	for _, fixture := range fixtures {
		document := fmt.Sprintf(`{"id":%q,"kind":%q,"enabled":false,"models":["configured-model"],"http":{"base_url":"https://gateway.example/custom","auth":{"scheme":"bearer"},"credential_ref":"local://service/example"},"subscription":{"provider":"openai_codex","status":"needs_reauth"},"custom_setting":{"keep":true},"created_at":"2026-08-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`, fixture.id, fixture.kind)
		if fixture.capabilities != "" {
			document = document[:len(document)-1] + `,"capabilities":` + fixture.capabilities + `}`
		}
		originals[fixture.id] = document
		if _, err := database.Exec(`INSERT INTO services (id, document_json, created_at, updated_at, sort_position)
VALUES (?, ?, '2026-08-01T00:00:00Z', '2026-09-01T00:00:00Z', 7)`, fixture.id, document); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := New(SQLDatabase{DB: database}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := latest.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	upgraded := make(map[string]string)
	for _, fixture := range fixtures {
		t.Run(fixture.id, func(t *testing.T) {
			var document, createdAt, updatedAt string
			var position int
			if err := database.QueryRow(`SELECT document_json, created_at, updated_at, sort_position FROM services WHERE id = ?`, fixture.id).
				Scan(&document, &createdAt, &updatedAt, &position); err != nil {
				t.Fatal(err)
			}
			if createdAt != "2026-08-01T00:00:00Z" || updatedAt != "2026-09-01T00:00:00Z" || position != 7 {
				t.Fatalf("row metadata changed: %s %s %d", createdAt, updatedAt, position)
			}
			upgraded[fixture.id] = document
			if !fixture.appendAlpha && document != originals[fixture.id] {
				t.Fatalf("unchanged service was rewritten: %s", document)
			}
			var want, got map[string]any
			if err := json.Unmarshal([]byte(originals[fixture.id]), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(document), &got); err != nil {
				t.Fatal(err)
			}
			if fixture.appendAlpha {
				capabilities, _ := want["capabilities"].([]any)
				want["capabilities"] = append(capabilities, map[string]any{
					"protocol": "openai.alpha_search", "mode": "native", "streaming": false,
				})
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("upgrade changed more than the missing native capability:\ngot  %#v\nwant %#v", got, want)
			}
		})
	}

	// Both the runner and the statement itself must be safe to replay.
	if err := latest.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations[index].Statements {
		result, err := database.Exec(statement)
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 0 {
			t.Fatalf("replay changed %d rows: %v", changed, err)
		}
	}
	for id, want := range upgraded {
		var got string
		if err := database.QueryRow(`SELECT document_json FROM services WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("replay changed %s: %s", id, got)
		}
	}
}
