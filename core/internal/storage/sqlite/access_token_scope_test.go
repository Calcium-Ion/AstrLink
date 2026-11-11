package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestAccessTokenScopeRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "astrlink.db"))
	defer store.Close()
	manager, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	selected := contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_zhang"},
	}
	created, err := manager.Create(ctx, "张三", selected)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	authenticated, err := manager.AuthenticateToken(ctx, created.Value)
	if err != nil || !authenticated.Services.Restricted() || !slices.Equal(authenticated.Services.ServiceIDs, selected.ServiceIDs) {
		t.Fatalf("authenticate = %+v, %v", authenticated.Services, err)
	}

	plain, err := manager.Create(ctx, "默认")
	if err != nil {
		t.Fatal(err)
	}
	if token, err := manager.AuthenticateToken(ctx, plain.Value); err != nil || token.Services.Restricted() {
		t.Fatalf("unscoped token = %+v, %v", token.Services, err)
	}

	updated, err := manager.Update(ctx, created.Token.ID, "张三 新", contract.AllAccessTokenServices())
	if err != nil || updated.Name != "张三 新" || updated.Services.Restricted() {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	if token, err := manager.AuthenticateToken(ctx, created.Value); err != nil || token.Services.Restricted() {
		t.Fatalf("token after update = %+v, %v", token.Services, err)
	}
	if _, err := manager.Update(ctx, created.Token.ID, "默认", contract.AllAccessTokenServices()); err == nil {
		t.Fatal("renaming onto another token's name succeeded")
	}
	if _, err := manager.Update(ctx, created.Token.ID, "张三", contract.AccessTokenServices{Scope: contract.AccessTokenServicesSelected}); err == nil {
		t.Fatal("an empty selection was accepted")
	}
}

func TestAccessTokenScopeSyncs(t *testing.T) {
	ctx := context.Background()
	source := openTestStore(t, filepath.Join(t.TempDir(), "source.db"))
	defer source.Close()
	manager, err := accesstoken.NewManager(source)
	if err != nil {
		t.Fatal(err)
	}
	selected := contract.AccessTokenServices{
		Scope: contract.AccessTokenServicesSelected, ServiceIDs: []contract.ServiceID{"service_zhang", "service_team"},
	}
	created, err := manager.Create(ctx, "研发组", selected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(ctx, "默认"); err != nil {
		t.Fatal(err)
	}
	entities, err := source.SyncEntities(ctx, syncKindAccessToken)
	if err != nil {
		t.Fatalf("read entities: %v", err)
	}
	var scoped, plain []byte
	var scopedValue []byte
	for _, entity := range entities {
		var document contract.AccessTokenDocument
		if err := json.Unmarshal(entity.Document, &document); err != nil {
			t.Fatal(err)
		}
		if entity.LocalID == string(created.Token.ID) {
			scoped, scopedValue = entity.Document, entity.Secret
		} else {
			plain = entity.Document
		}
	}
	if !json.Valid(scoped) || !json.Valid(plain) {
		t.Fatalf("documents missing: %s / %s", scoped, plain)
	}
	var plainFields map[string]any
	_ = json.Unmarshal(plain, &plainFields)
	if _, ok := plainFields["service_scope"]; ok {
		t.Fatalf("unscoped token document carries a scope: %s", plain)
	}

	target := openTestStore(t, filepath.Join(t.TempDir(), "target.db"))
	defer target.Close()
	if err := target.SyncWriteAccessToken(ctx, string(created.Token.ID), scoped, scopedValue); err != nil {
		t.Fatalf("write: %v", err)
	}
	tokens, err := target.ListAccessTokens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	index := slices.IndexFunc(tokens, func(token storagecontract.AccessTokenMetadata) bool { return token.ID == created.Token.ID })
	if index < 0 {
		t.Fatalf("synced token missing from %v", tokens)
	}
	if got := tokens[index].Services; !got.Restricted() || !slices.Equal(got.ServiceIDs, selected.ServiceIDs) {
		t.Fatalf("synced scope = %+v", got)
	}

	// A scope change alone is a change worth syncing.
	if err := source.ClearChangesThrough(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Update(ctx, created.Token.ID, "研发组", contract.AllAccessTokenServices()); err != nil {
		t.Fatal(err)
	}
	if count, err := source.PendingChanges(ctx); err != nil || count == 0 {
		t.Fatalf("pending after scope change = %d, %v", count, err)
	}
}
