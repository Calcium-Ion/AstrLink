package controlapi

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestProjectMemoryControlAPI(t *testing.T) {
	ctx := context.Background()
	store, err := openControlTestStore(t, ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, Dependencies{ServiceStore: store, RequestRecords: store, ControlToken: "control-token-123456"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Hour)
	completed := started.Add(time.Second)
	project, session := "~/proj", contract.SessionID("session_memory")
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: "request_memory", SessionID: &session, StartedAt: started, CompletedAt: &completed,
		Status: contract.RequestStatusSucceeded, InputProtocol: contract.ProtocolAnthropicMessages,
		Audit: contract.NotCapturedAuditSummary(), ClientType: contract.ClientClaudeCode, Project: &project,
		Context: &contract.RequestContext{
			Parts: &contract.ContextParts{History: 1_000, Memory: 700},
			Items: []contract.ContextItem{
				{Kind: contract.ContextItemRules, Name: "AGENTS.md", Path: "~/proj/AGENTS.md", Scope: contract.ContextScopeProject, Tokens: 400, Hash: "fp1_rules"},
				{Kind: contract.ContextItemMemory, Name: "MEMORY.md", Path: "~/.claude/projects/~-proj/memory/MEMORY.md", Tokens: 300, Hash: "fp1_index"},
			},
			MemoryIndex: []contract.MemoryIndexEntry{{File: "reply.md", Title: "Reply in Chinese", Hook: "every report"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	local := time.Now().Local()
	dayStart := url.QueryEscape(time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).Format(time.RFC3339Nano))
	response := requestRecordHTTP(t, handler, http.MethodGet,
		RequestMemoryPath+"?project="+url.QueryEscape(project)+"&client_type=claude_code&day_start="+dayStart, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("memory status=%d body=%s", response.Code, response.Body.String())
	}
	var memory contract.ProjectMemory
	decode(t, response, &memory)
	if len(memory.Projects) != 1 || len(memory.Entries) != 1 || memory.Entries[0].Title != "Reply in Chinese" ||
		len(memory.Entries[0].Reads) != contract.MemoryDays || len(memory.Rules) != 1 || !memory.HasMemory || memory.Stories == nil ||
		memory.Toolbox.Skills == nil || memory.Toolbox.MCP == nil {
		t.Fatalf("memory = %+v", memory)
	}
	response = requestRecordHTTP(t, handler, http.MethodGet,
		RequestMemoryEntryPath+"?project="+url.QueryEscape(project)+"&file=reply.md", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("entry status=%d body=%s", response.Code, response.Body.String())
	}
	var detail contract.MemoryEntryDetail
	decode(t, response, &detail)
	if detail.Entry.File != "reply.md" || detail.Entry.NotedAt != nil || detail.Events == nil {
		t.Fatalf("entry = %+v", detail)
	}
	if response := requestRecordHTTP(t, handler, http.MethodGet, RequestMemoryEntryPath+"?file=missing.md", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("missing entry status=%d body=%s", response.Code, response.Body.String())
	}
	// The calls that named no project read as an empty view of that group.
	response = requestRecordHTTP(t, handler, http.MethodGet, RequestMemoryPath+"?no_project=true", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("no project status=%d body=%s", response.Code, response.Body.String())
	}
	memory = contract.ProjectMemory{}
	decode(t, response, &memory)
	if len(memory.Projects) != 1 || len(memory.Entries) != 0 || memory.Project != nil || memory.ClientType != nil {
		t.Fatalf("no project = %+v", memory)
	}

	future := url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
	stale := url.QueryEscape(time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339Nano))
	for _, path := range []string{
		RequestMemoryPath + "?project=",
		RequestMemoryPath + "?project=~/a&no_project=true",
		RequestMemoryPath + "?no_project=1",
		RequestMemoryPath + "?client_type=notepad",
		RequestMemoryPath + "?day_start=yesterday",
		RequestMemoryPath + "?day_start=" + future,
		RequestMemoryPath + "?day_start=" + stale,
		RequestMemoryPath + "?project=~/a&project=~/b",
		RequestMemoryPath + "?file=reply.md",
		RequestMemoryPath + "?limit=5",
		RequestMemoryEntryPath,
		RequestMemoryEntryPath + "?file=",
	} {
		response := requestRecordHTTP(t, handler, http.MethodGet, path, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{RequestMemoryPath, RequestMemoryEntryPath} {
		if response := requestRecordHTTP(t, handler, http.MethodPost, path, "", ""); response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("post %s status=%d", path, response.Code)
		}
	}
}

func TestRequestContextTextsControlAPI(t *testing.T) {
	ctx := context.Background()
	store, err := openControlTestStore(t, ctx, filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var read struct {
		protocol contract.ProtocolID
		body     string
	}
	dependencies := Dependencies{
		ServiceStore: store, RequestRecords: store, AuditSettings: store, AuditKeys: store, AuditBlobs: store,
		ControlToken: "control-token-123456",
		ContextTexts: func(protocol contract.ProtocolID, body []byte) contract.RequestContextTexts {
			read.protocol, read.body = protocol, string(body)
			return contract.RequestContextTexts{
				Items:  []contract.ContextItemText{{Kind: contract.ContextItemRules, Name: "AGENTS.md", Text: "Run gofmt."}},
				Writes: []contract.MemoryWriteText{},
				Reads:  []contract.MemoryReadText{},
			}
		},
	}
	handler, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.GetOrCreateAuditKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const body = `{"model":"claude-opus","messages":[]}`
	for _, id := range []contract.RequestID{"request_texts", "request_bare"} {
		if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
			ID: id, StartedAt: time.Now().UTC(), Status: contract.RequestStatusSucceeded,
			InputProtocol: contract.ProtocolAnthropicMessages, Audit: contract.AuditRecordSummary{RequestBodyCaptured: id == "request_texts"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	nonce, ciphertext, err := storage.SealAuditBlob(key, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertAuditBlob(ctx, storage.AuditBlob{
		RequestID: "request_texts", Direction: storage.AuditDirectionRequest, MediaType: "application/json",
		Nonce: nonce, Ciphertext: ciphertext, CapturedBytes: len(body), Truncated: true,
		Exposure: storage.AuditExposureShareable,
	}); err != nil {
		t.Fatal(err)
	}

	response := requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_texts/audit/context", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("texts status=%d body=%s", response.Code, response.Body.String())
	}
	var texts contract.RequestContextTexts
	decode(t, response, &texts)
	if texts.RequestID != "request_texts" || !texts.Truncated || texts.Withheld || len(texts.Items) != 1 ||
		read.protocol != contract.ProtocolAnthropicMessages || read.body != body {
		t.Fatalf("texts = %+v, read %+v", texts, read)
	}
	// A call whose body was not kept says so instead of failing.
	response = requestRecordHTTP(t, handler, http.MethodGet, RequestsPath+"/request_bare/audit/context?view=shareable", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("bare status=%d body=%s", response.Code, response.Body.String())
	}
	decode(t, response, &texts)
	if !texts.Withheld || texts.Reason != "" || texts.Items == nil || len(texts.Items) != 0 || texts.Reads == nil {
		t.Fatalf("bare texts = %+v", texts)
	}
	for path, status := range map[string]int{
		RequestsPath + "/request_missing/audit/context":            http.StatusNotFound,
		RequestsPath + "/request_texts/audit/context?view=private": http.StatusBadRequest,
	} {
		if response := requestRecordHTTP(t, handler, http.MethodGet, path, "", ""); response.Code != status {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	dependencies.ContextTexts = nil
	without, err := NewWithDependencies(contract.VersionResponse{
		CoreVersion: "0.0.0-test", ControlAPIVersion: "v1", ProtocolContractVersion: "v1",
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if response := requestRecordHTTP(t, without, http.MethodGet, RequestsPath+"/request_texts/audit/context", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("without a reader status=%d", response.Code)
	}
}
