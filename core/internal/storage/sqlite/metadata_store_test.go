package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	storagecontract "github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestMetadataAttributesKeepTheirOwners(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	env := contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^(dev|staging|prod)$", Owner: contract.MetadataOwnerUser}
	if err := store.CreateMetadataAttribute(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMetadataAttribute(ctx, env); !errors.Is(err, storagecontract.ErrConflict) {
		t.Fatalf("second create = %v", err)
	}
	project := contract.MetadataAttribute{Key: "project", Label: "项目", Pattern: "^[a-z0-9][a-z0-9._-]{0,63}$", Marker: "memory-project"}
	if err := store.RegisterPluginAttributes(ctx, "powercontext", []contract.MetadataAttribute{project}); err != nil {
		t.Fatal(err)
	}
	// Registering again updates the plugin's own attribute.
	project.Label = "Project"
	if err := store.RegisterPluginAttributes(ctx, "powercontext", []contract.MetadataAttribute{project}); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []struct {
		name  string
		write func() error
	}{
		{"a plugin taking the user's key", func() error {
			return store.RegisterPluginAttributes(ctx, "other", []contract.MetadataAttribute{{Key: "env", Label: "Env", Pattern: "^.+$"}})
		}},
		{"a plugin taking another plugin's key", func() error {
			return store.RegisterPluginAttributes(ctx, "other", []contract.MetadataAttribute{project})
		}},
		{"a plugin taking a marker", func() error {
			return store.RegisterPluginAttributes(ctx, "other", []contract.MetadataAttribute{{Key: "scope", Label: "Scope", Pattern: "^.+$", Marker: "memory-project"}})
		}},
		{"the user taking a plugin's key", func() error {
			return store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "project", Label: "P", Pattern: "^.+$", Owner: contract.MetadataOwnerUser})
		}},
		{"the user editing a plugin's attribute", func() error {
			return store.UpdateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "project", Label: "P", Pattern: "^.+$", Owner: contract.MetadataOwnerUser})
		}},
		{"the user taking a marker", func() error {
			return store.UpdateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "env", Label: "环境", Pattern: "^.+$", Marker: "memory-project", Owner: contract.MetadataOwnerUser})
		}},
	} {
		if err := conflict.write(); !errors.Is(err, storagecontract.ErrConflict) {
			t.Fatalf("%s = %v, want a conflict", conflict.name, err)
		}
	}
	env.Label, env.Marker = "部署环境", "deploy-env"
	if err := store.UpdateMetadataAttribute(ctx, env); err != nil {
		t.Fatal(err)
	}
	attributes, err := store.ListMetadataAttributes(ctx)
	if err != nil || len(attributes) != 2 {
		t.Fatalf("ListMetadataAttributes = %+v, %v", attributes, err)
	}
	if got := attributes[0]; got.Key != "env" || got.Label != "部署环境" || got.Marker != "deploy-env" || got.Owner != contract.MetadataOwnerUser {
		t.Fatalf("env = %+v", got)
	}
	if got := attributes[1]; got.Key != "project" || got.Label != "Project" || got.Owner != "powercontext" || got.Marker != "memory-project" {
		t.Fatalf("project = %+v", got)
	}
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "Bad", Label: "x", Pattern: "^x$", Owner: contract.MetadataOwnerUser}); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("invalid attribute = %v", err)
	}
}

func TestMetadataValuesFollowTheirAttribute(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	for _, attribute := range []contract.MetadataAttribute{
		{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser},
		{Key: "team", Label: "团队", Pattern: "^[a-z]+$", Owner: contract.MetadataOwnerUser},
	} {
		if err := store.CreateMetadataAttribute(ctx, attribute); err != nil {
			t.Fatal(err)
		}
	}
	token := contract.AccessTokenID("token_laptop")
	if err := store.ReplaceTokenMetadata(ctx, token, map[string]string{"env": "dev", "team": "infra"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceTokenMetadata(ctx, token, map[string]string{"env": "prod"}); err != nil {
		t.Fatal(err)
	}
	values, err := store.MetadataValues(ctx, storagecontract.MetadataTargetToken, string(token))
	if err != nil || len(values) != 1 || values["env"].Value != "prod" || values["env"].SetBy != contract.MetadataSetByTokenDefault {
		t.Fatalf("token values = %+v, %v", values, err)
	}
	if err := store.ReplaceTokenMetadata(ctx, token, map[string]string{"missing": "x"}); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("a value for no attribute = %v", err)
	}
	if values, _ := store.MetadataValues(ctx, storagecontract.MetadataTargetToken, string(token)); values["env"].Value != "prod" {
		t.Fatalf("a refused replace changed the values: %+v", values)
	}
	binding := storagecontract.MetadataValueRecord{TargetKind: storagecontract.MetadataTargetSession, TargetID: "session_1", Key: "team", Value: "infra", SetBy: contract.MetadataSetByModelMarker, Turn: 2}
	if err := store.PutMetadataValue(ctx, binding); err != nil {
		t.Fatal(err)
	}
	session, err := store.MetadataValues(ctx, storagecontract.MetadataTargetSession, "session_1")
	if err != nil || session["team"].Turn != 2 || session["team"].SetBy != contract.MetadataSetByModelMarker {
		t.Fatalf("session values = %+v, %v", session, err)
	}
	if err := store.DeleteMetadataAttribute(ctx, "team"); err != nil {
		t.Fatal(err)
	}
	if session, _ := store.MetadataValues(ctx, storagecontract.MetadataTargetSession, "session_1"); len(session) != 0 {
		t.Fatalf("values outlived their attribute: %+v", session)
	}
	listed, err := store.ListMetadataValues(ctx, storagecontract.MetadataTargetToken)
	if err != nil || len(listed) != 1 || listed[0].TargetID != string(token) {
		t.Fatalf("ListMetadataValues = %+v, %v", listed, err)
	}
	if err := store.DeleteMetadataAttribute(ctx, "team"); !errors.Is(err, storagecontract.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestRegisteringAttributesWaitsForOtherWriters(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	project := contract.MetadataAttribute{Key: "project", Label: "项目", Pattern: "^[a-z0-9-]{1,32}$", Marker: "memory-project"}
	if err := store.RegisterPluginAttributes(ctx, "powercontext", []contract.MetadataAttribute{project}); err != nil {
		t.Fatal(err)
	}
	// Core registers a plugin's attributes at start while requests already
	// bind sessions: the registration reads before it writes.
	var writers sync.WaitGroup
	errs := make(chan error, 64)
	for worker := range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for index := range 20 {
				now := time.Now()
				errs <- store.PutMetadataValue(ctx, storagecontract.MetadataValueRecord{
					TargetKind: storagecontract.MetadataTargetSession, TargetID: fmt.Sprintf("session_%d_%d", worker, index),
					Key: "project", Value: "astrlink", SetBy: contract.MetadataSetByModelMarker, CreatedAt: now, UpdatedAt: now,
				})
			}
		}()
	}
	for range 20 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			errs <- store.RegisterPluginAttributes(ctx, "powercontext", []contract.MetadataAttribute{project})
		}()
	}
	go func() { writers.Wait(); close(errs) }()
	for err := range errs {
		if err != nil {
			t.Fatalf("a write failed beside another: %v", err)
		}
	}
}

func TestMetadataValueStatsCountSessionsAndTheirLastRequest(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "project", Label: "项目", Pattern: "^[a-z]+$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	for session, value := range map[string]string{"session_a": "alpha", "session_b": "alpha", "session_c": "beta", "session_d": "none"} {
		if err := store.PutMetadataValue(ctx, storagecontract.MetadataValueRecord{
			TargetKind: storagecontract.MetadataTargetSession, TargetID: session, Key: "project", Value: value, SetBy: contract.MetadataSetByManual,
		}); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	for index, session := range []string{"session_b", "session_b", "session_c"} {
		id := contract.SessionID(session)
		model := "claude"
		record := contract.RequestRecord{
			ID: contract.RequestID("request_" + strconv.Itoa(index)), StartedAt: started.Add(time.Duration(index) * time.Minute),
			Status: contract.RequestStatusPending, InputProtocol: contract.ProtocolAnthropicMessages, RequestedModel: &model,
			Audit: contract.NotCapturedAuditSummary(), SessionID: &id,
		}
		if err := store.UpsertRequestRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := store.MetadataValueStats(ctx, "project", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 3 || stats[0].Value != "beta" || stats[0].Sessions != 1 || !stats[0].LastUsedAt.Equal(started.Add(2*time.Minute)) ||
		stats[1].Value != "alpha" || stats[1].Sessions != 2 || !stats[1].LastUsedAt.Equal(started.Add(time.Minute)) ||
		stats[2].Value != "none" || stats[2].LastUsedAt != nil {
		t.Fatalf("stats = %+v", stats)
	}
	if limited, _ := store.MetadataValueStats(ctx, "project", 1); len(limited) != 1 {
		t.Fatalf("limit 1 = %+v", limited)
	}
}

func TestWorkspaceValuesKeepTheFirstBindingUntilReplaced(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	for _, attribute := range []contract.MetadataAttribute{
		{Key: "project", Label: "项目", Pattern: "^[a-z]+$", Marker: "memory-project", Owner: "powercontext"},
		{Key: "env", Label: "环境", Pattern: "^(dev|prod)$", Owner: contract.MetadataOwnerUser},
	} {
		if err := store.CreateMetadataAttribute(ctx, attribute); err != nil {
			t.Fatal(err)
		}
	}
	workspace := strings.Repeat("a", 64)
	first := storagecontract.MetadataWorkspaceRecord{Key: "project", Workspace: workspace, Name: "astrlink", Value: "astrlink"}
	if bound, err := store.PutWorkspaceValue(ctx, first, false); err != nil || !bound {
		t.Fatalf("first binding = %v, %v", bound, err)
	}
	// Another session in the same directory binding another value does not
	// move the workspace; the user's explicit choice does.
	other := first
	other.Value = "paseo"
	if bound, err := store.PutWorkspaceValue(ctx, other, false); err != nil || bound {
		t.Fatalf("second binding without replace = %v, %v", bound, err)
	}
	if values, err := store.WorkspaceValues(ctx, workspace); err != nil || values["project"].Value != "astrlink" || values["project"].Name != "astrlink" {
		t.Fatalf("WorkspaceValues = %+v, %v", values, err)
	}
	if bound, err := store.PutWorkspaceValue(ctx, other, true); err != nil || !bound {
		t.Fatalf("replace = %v, %v", bound, err)
	}
	if values, _ := store.WorkspaceValues(ctx, workspace); values["project"].Value != "paseo" {
		t.Fatalf("replaced value = %+v", values)
	}
	env := storagecontract.MetadataWorkspaceRecord{Key: "env", Workspace: workspace, Name: "astrlink", Value: "dev"}
	if _, err := store.PutWorkspaceValue(ctx, env, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutWorkspaceValue(ctx, storagecontract.MetadataWorkspaceRecord{Key: "project", Workspace: "short", Value: "x"}, false); !errors.Is(err, storagecontract.ErrInvalidArgument) {
		t.Fatalf("a workspace that is no fingerprint: %v", err)
	}
	if count, err := store.CountWorkspaceValues(ctx, []string{"project"}); err != nil || count != 1 {
		t.Fatalf("CountWorkspaceValues = %d, %v", count, err)
	}
	if removed, err := store.DeleteWorkspaceValues(ctx, []string{"project"}); err != nil || removed != 1 {
		t.Fatalf("DeleteWorkspaceValues = %d, %v", removed, err)
	}
	if values, _ := store.WorkspaceValues(ctx, workspace); len(values) != 1 || values["env"].Value != "dev" {
		t.Fatalf("clearing one attribute touched another: %+v", values)
	}
	if err := store.DeleteMetadataAttribute(ctx, "env"); err != nil {
		t.Fatal(err)
	}
	if values, _ := store.WorkspaceValues(ctx, workspace); len(values) != 0 {
		t.Fatalf("workspace values outlived their attribute: %+v", values)
	}
}

func TestSessionBindingsTakeTheNewSources(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata.db"))
	defer store.Close()
	ctx := context.Background()
	if err := store.CreateMetadataAttribute(ctx, contract.MetadataAttribute{Key: "project", Label: "项目", Pattern: "^[a-z]+$", Owner: contract.MetadataOwnerUser}); err != nil {
		t.Fatal(err)
	}
	for index, setBy := range []string{contract.MetadataSetByWorkspace, contract.MetadataSetByUserAnswer} {
		record := storagecontract.MetadataValueRecord{TargetKind: storagecontract.MetadataTargetSession, TargetID: "session_" + string(rune('a'+index)), Key: "project", Value: "astrlink", SetBy: setBy, Turn: 1}
		if err := store.PutMetadataValue(ctx, record); err != nil {
			t.Fatalf("set by %s: %v", setBy, err)
		}
	}
	if err := store.PutMetadataValue(ctx, storagecontract.MetadataValueRecord{TargetKind: storagecontract.MetadataTargetSession, TargetID: "session_x", Key: "project", Value: "astrlink", SetBy: "guess"}); err == nil {
		t.Fatal("an unknown source was stored")
	}
}
