package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/migrate"
)

func TestLearnedIdentityStoreUpgradesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	ctx := context.Background()
	database, err := sql.Open(driverName, sqliteFileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var previous []migrate.Migration
	for _, migration := range migrate.DefaultMigrations() {
		if migration.Name == "learned_client_identity" {
			break
		}
		previous = append(previous, migration)
	}
	runner, err := migrate.New(migrate.SQLDatabase{DB: database}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// An upgraded database starts without learned identities.
	store := openTestStore(t, path)
	documents, err := store.ListLearnedIdentities(ctx)
	if err != nil || len(documents) != 0 {
		t.Fatalf("upgraded list = %v, %v", documents, err)
	}
	if _, err := store.GetLearnedIdentity(ctx, contract.SubscriptionProviderClaudeCode); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("missing identity error = %v", err)
	}

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for _, put := range []struct {
		provider contract.SubscriptionProvider
		document string
	}{
		{contract.SubscriptionProviderClaudeCode, `{"user_agent":"claude-cli/2.1.300","version":"2.1.300"}`},
		{contract.SubscriptionProviderOpenAICodex, `{"user_agent":"codex-tui/0.160.0","version":"0.160.0"}`},
		{contract.SubscriptionProviderClaudeCode, `{"user_agent":"claude-cli/2.1.301","version":"2.1.301"}`},
	} {
		if err := store.PutLearnedIdentity(ctx, put.provider, []byte(put.document)); err != nil {
			t.Fatalf("put %s: %v", put.provider, err)
		}
		now = now.Add(time.Minute)
	}
	for _, invalid := range []struct {
		provider contract.SubscriptionProvider
		document string
	}{
		{contract.SubscriptionProviderXAIGrok, `{}`},
		{"unknown", `{}`},
		{contract.SubscriptionProviderClaudeCode, `{`},
	} {
		if err := store.PutLearnedIdentity(ctx, invalid.provider, []byte(invalid.document)); !errors.Is(err, storagecontract.ErrInvalidArgument) {
			t.Fatalf("put %s %s error = %v", invalid.provider, invalid.document, err)
		}
	}
	if _, err := store.GetLearnedIdentity(ctx, contract.SubscriptionProviderXAIGrok); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("get unsupported provider error = %v", err)
	}
	var updatedAt string
	if err := store.db.QueryRow(`SELECT updated_at FROM learned_client_identity WHERE provider = ?`, contract.SubscriptionProviderClaudeCode).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if updatedAt != "2026-09-25T12:02:00Z" {
		t.Fatalf("updated_at = %q", updatedAt)
	}
	// A row written by a newer build for a provider this build does not learn
	// is left alone.
	if _, err := store.db.Exec(`INSERT INTO learned_client_identity (provider, document_json, updated_at) VALUES ('xai_grok', '{}', '2026-09-25T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, path)
	defer reopened.Close()
	documents, err = reopened.ListLearnedIdentities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[contract.SubscriptionProvider]string{
		contract.SubscriptionProviderClaudeCode:  `{"user_agent":"claude-cli/2.1.301","version":"2.1.301"}`,
		contract.SubscriptionProviderOpenAICodex: `{"user_agent":"codex-tui/0.160.0","version":"0.160.0"}`,
	}
	if len(documents) != len(want) {
		t.Fatalf("documents = %q", documents)
	}
	for provider, document := range want {
		if string(documents[provider]) != document {
			t.Fatalf("%s document = %s, want %s", provider, documents[provider], document)
		}
	}
	got, err := reopened.GetLearnedIdentity(ctx, contract.SubscriptionProviderOpenAICodex)
	if err != nil || string(got) != want[contract.SubscriptionProviderOpenAICodex] {
		t.Fatalf("get Codex = %s, %v", got, err)
	}
}

func TestLearnedIdentitySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	ctx := context.Background()
	store := openTestStore(t, path)
	registry := accountauth.NewIdentityRegistry(store, store)
	claude := http.Header{
		"User-Agent":                  {"claude-cli/2.1.300 (external, cli)"},
		"X-App":                       {"cli"},
		"X-Stainless-Lang":            {"js"},
		"X-Stainless-Package-Version": {"0.95.1"},
		"X-Stainless-Runtime-Version": {"v24.9.0"},
	}
	codex := http.Header{
		"User-Agent": {"codex_cli_rs/0.160.0 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1"},
		"Originator": {"codex_cli_rs"},
	}
	if changed, err := registry.LearnClaude(ctx, claude); !changed || err != nil {
		t.Fatalf("LearnClaude = %t, %v", changed, err)
	}
	if changed, err := registry.LearnCodex(ctx, codex); !changed || err != nil {
		t.Fatalf("LearnCodex = %t, %v", changed, err)
	}
	wantClaude := registry.ClaudeIdentityFor(ctx)
	wantCodex := registry.CodexIdentityFor(ctx, "")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, path)
	defer reopened.Close()
	restarted := accountauth.NewIdentityRegistry(reopened, reopened)
	if got := restarted.ClaudeIdentityFor(ctx); got.Version != accountauth.DefaultClaudeIdentity().Version {
		t.Fatalf("identity before hydration = %+v", got)
	}
	if err := restarted.Hydrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := restarted.ClaudeIdentityFor(ctx); got.UserAgent != wantClaude.UserAgent || got.Headers["X-Stainless-Runtime-Version"] != "v24.9.0" {
		t.Fatalf("hydrated Claude = %+v, want %+v", got, wantClaude)
	}
	if got := restarted.CodexIdentityFor(ctx, ""); got.UserAgent != wantCodex.UserAgent || got.Version != "0.160.0" {
		t.Fatalf("hydrated Codex = %+v, want %+v", got, wantCodex)
	}

	// The persisted settings still decide whether the learned identity is used.
	settings, err := reopened.GetRoutingSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.ClaudeIdentityAutoLearn = false
	if err := reopened.UpdateRoutingSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if got := restarted.ClaudeIdentityFor(ctx); got.UserAgent != accountauth.DefaultClaudeUserAgent {
		t.Fatalf("Claude with learning off = %+v", got)
	}
	if got := restarted.CodexIdentityFor(ctx, ""); got.Version != "0.160.0" {
		t.Fatalf("Codex followed the Claude setting: %+v", got)
	}
}
