package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

const notesPlugin = `export const meta = { apiVersion: 2, key: "notes", name: "Notes", version: "2.0.0", capabilities: ["storage"] };
export async function describeStatus(page) {
  console.info("language", page.language);
  return [{ type: "text", text: "ok" }];
}
export async function onAction(input) {
  await null;
  throw new RangeError("no action " + input.action);
}
`

// observerRequest sends a request as an observer, as agent tools on the
// local socket are.
func observerRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testObserverToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestPluginJournalAndDryRun(t *testing.T) {
	_, handler, manager, directory := newPluginHandler(t)
	writePlugin(t, directory, "notes", "plugin.js", notesPlugin)
	if err := os.WriteFile(filepath.Join(directory, "notes", plugin.DevelopmentMarker), []byte(`{"project":"/home/me/notes","built_at":"2026-10-23T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := observerRequest(handler, http.MethodGet, PluginsPath, "")
	list := decodeBody[struct {
		Directory string                   `json:"directory"`
		Plugins   []contract.PluginSummary `json:"plugins"`
	}](t, response)
	var notes *contract.PluginSummary
	for index := range list.Plugins {
		if list.Plugins[index].Key == "notes" {
			notes = &list.Plugins[index]
		}
	}
	if list.Directory != directory || notes == nil || notes.APIVersion != 2 || notes.Development == nil || notes.Development.Project != "/home/me/notes" {
		t.Fatalf("list = %s", response.Body.String())
	}

	path := PluginsPath + "/notes/dry-run"
	if response := observerRequest(handler, http.MethodPost, path, `{"hook":"describeStatus","args":[{"language":"en"}]}`); response.Code != http.StatusForbidden {
		t.Fatalf("an observer's dry run = %d", response.Code)
	}
	response = controlRequest(t, handler, http.MethodPost, path, "application/json", `{"hook":"describeStatus","args":[{"language":"en"}]}`, "")
	result := decodeBody[contract.PluginDryRunResult](t, response)
	if response.Code != http.StatusOK || string(result.Result) != `[{"type":"text","text":"ok"}]` || result.Error != nil || len(result.Logs) != 1 || result.Logs[0].Message != "language en" {
		t.Fatalf("dry run = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodPost, path, "application/json", `{"hook":"onAction","args":[{"action":"wipe"}]}`, "")
	result = decodeBody[contract.PluginDryRunResult](t, response)
	if result.Error == nil || result.Error.Reason != plugin.ReasonPluginError || !strings.Contains(result.Error.Message, "RangeError: no action wipe") || !strings.Contains(result.Error.Stack, "plugin.js:") {
		t.Fatalf("failed dry run = %s", response.Body.String())
	}
	for _, body := range []string{`{"hook":"prepareTurn"}`, `{"hook":"describeStatus","extra":1}`} {
		if response := controlRequest(t, handler, http.MethodPost, path, "application/json", body, ""); response.Code < 400 {
			t.Fatalf("dry run %s = %d", body, response.Code)
		}
	}

	response = observerRequest(handler, http.MethodGet, PluginsPath+"/notes/journal", "")
	journal := decodeBody[contract.PluginJournal](t, response)
	if response.Code != http.StatusOK || len(journal.Entries) != 2 || !journal.Entries[0].DryRun || journal.Entries[1].Level != plugin.JournalError || journal.Latest != journal.Entries[1].Seq {
		t.Fatalf("journal = %s", response.Body.String())
	}
	response = observerRequest(handler, http.MethodGet, PluginsPath+"/notes/journal?since="+strconv.FormatUint(journal.Latest, 10), "")
	if later := decodeBody[contract.PluginJournal](t, response); len(later.Entries) != 0 || later.Latest != journal.Latest {
		t.Fatalf("journal after the latest = %s", response.Body.String())
	}
	if response := observerRequest(handler, http.MethodGet, PluginsPath+"/notes/journal?since=x", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("journal with a bad since = %d", response.Code)
	}
}
