package controlapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/metadata"
)

type attributeList struct {
	Attributes []contract.MetadataAttributeView `json:"attributes"`
}

func TestMetadataAttributesOfTheUserAndOfPlugins(t *testing.T) {
	_, handler, _, directory := newPluginHandler(t)
	if list := decodeBody[attributeList](t, controlRequest(t, handler, http.MethodGet, MetadataAttributesPath, "", "", "")); len(list.Attributes) != 0 {
		t.Fatalf("attributes before any = %+v", list)
	}
	response := controlRequest(t, handler, http.MethodPost, MetadataAttributesPath, "application/json", `{"key":"env","label":"环境","pattern":"^(dev|staging|prod)$"}`, "")
	created := decodeBody[contract.MetadataAttributeView](t, response)
	if response.Code != http.StatusCreated || created.Owner != contract.MetadataOwnerUser || created.Header != "X-Astrlink-Meta-Env" {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	for body, code := range map[string]int{
		`{"key":"env","label":"again","pattern":"^.+$"}`:                             http.StatusConflict,
		`{"key":"Env","label":"bad key","pattern":"^.+$"}`:                           http.StatusUnprocessableEntity,
		`{"key":"team","label":"unanchored","pattern":"[a-z]+"}`:                     http.StatusUnprocessableEntity,
		`{"key":"team","label":"gateway","pattern":"^.+$","marker":"astrlink-team"}`: http.StatusUnprocessableEntity,
	} {
		if response := controlRequest(t, handler, http.MethodPost, MetadataAttributesPath, "application/json", body, ""); response.Code != code {
			t.Fatalf("POST %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	response = approvedRequest(t, handler, http.MethodPatch, MetadataAttributesPath+"/env", "application/json", `{"label":"部署环境","pattern":"^(dev|prod)$","marker":"deploy-env"}`, "")
	if updated := decodeBody[contract.MetadataAttributeView](t, response); response.Code != http.StatusOK || updated.Label != "部署环境" || updated.Marker != "deploy-env" {
		t.Fatalf("PATCH = %d %s", response.Code, response.Body.String())
	}

	// Enabling the demo plugin registers its project attribute.
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/demo", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	list := decodeBody[attributeList](t, controlRequest(t, handler, http.MethodGet, MetadataAttributesPath, "", "", ""))
	if len(list.Attributes) != 2 || list.Attributes[1].Key != "project" || list.Attributes[1].Owner != "demo" || list.Attributes[1].Marker != "memory-project" || list.Attributes[1].OwnerMissing {
		t.Fatalf("attributes after enabling = %+v", list)
	}
	for _, request := range [][3]string{
		{http.MethodPatch, MetadataAttributesPath + "/project", `{"label":"P","pattern":"^.+$"}`},
		{http.MethodDelete, MetadataAttributesPath + "/project", ""},
		{http.MethodPost, MetadataAttributesPath, `{"key":"project","label":"P","pattern":"^.+$"}`},
	} {
		if response := controlRequest(t, handler, request[0], request[1], "application/json", request[2], ""); response.Code != http.StatusConflict {
			t.Fatalf("%s %s = %d %s", request[0], request[1], response.Code, response.Body.String())
		}
	}
	// Once the plugin is gone, its attribute can go too.
	if err := os.RemoveAll(filepath.Join(directory, "demo")); err != nil {
		t.Fatal(err)
	}
	if response := controlRequest(t, handler, http.MethodPost, PluginsPath+"/reload", "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("reload = %d", response.Code)
	}
	list = decodeBody[attributeList](t, controlRequest(t, handler, http.MethodGet, MetadataAttributesPath, "", "", ""))
	if !list.Attributes[1].OwnerMissing {
		t.Fatalf("attribute of a removed plugin = %+v", list.Attributes[1])
	}
	for _, key := range []string{"project", "env"} {
		if response := controlRequest(t, handler, http.MethodDelete, MetadataAttributesPath+"/"+key, "", "", ""); response.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s = %d %s", key, response.Code, response.Body.String())
		}
	}
}

func TestEnablingAPluginWithATakenAttributeFails(t *testing.T) {
	_, handler, _, _ := newPluginHandler(t)
	if response := controlRequest(t, handler, http.MethodPost, MetadataAttributesPath, "application/json", `{"key":"project","label":"项目","pattern":"^.+$"}`, ""); response.Code != http.StatusCreated {
		t.Fatalf("create = %d", response.Code)
	}
	response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/demo", "application/json", `{"enabled":true}`, "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "metadata_attribute_conflict") {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	if detail := decodeBody[contract.PluginDetail](t, controlRequest(t, handler, http.MethodGet, PluginsPath+"/demo", "", "", "")); detail.Enabled {
		t.Fatal("the plugin was enabled despite the conflict")
	}
}

func TestAccessTokenDefaultMetadata(t *testing.T) {
	_, handler, _, _ := newPluginHandler(t)
	if response := controlRequest(t, handler, http.MethodPost, MetadataAttributesPath, "application/json", `{"key":"env","label":"环境","pattern":"^(dev|prod)$"}`, ""); response.Code != http.StatusCreated {
		t.Fatalf("create = %d", response.Code)
	}
	created := controlRequest(t, handler, http.MethodPost, AccessTokensPath, "application/json", `{"name":"laptop"}`, "")
	token := decodeBody[accessTokenSecretResponse](t, created).Token.ID
	if created.Code != http.StatusCreated || token == "" {
		t.Fatalf("token = %d %s", created.Code, created.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPut, AccessTokensPath+"/token_missing/metadata", "application/json", `{"values":{}}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("PUT for a missing token = %d", response.Code)
	}
	path := AccessTokensPath + "/" + string(token) + "/metadata"
	if response := controlRequest(t, handler, http.MethodGet, path, "", "", ""); response.Code != http.StatusOK || response.Body.String() != `{"values":{}}`+"\n" {
		t.Fatalf("GET = %d %q", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"values":{"env":"staging"}}`, `{"values":{"missing":"x"}}`} {
		if response := controlRequest(t, handler, http.MethodPut, path, "application/json", body, ""); response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("PUT %s = %d %s", body, response.Code, response.Body.String())
		}
	}
	response := controlRequest(t, handler, http.MethodPut, path, "application/json", `{"values":{"env":"prod"}}`, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"env":"prod"`) {
		t.Fatalf("PUT = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodGet, path, "", "", ""); !strings.Contains(response.Body.String(), `"env":"prod"`) {
		t.Fatalf("GET after PUT = %s", response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPut, path, "application/json", `{"values":{}}`, ""); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "prod") {
		t.Fatalf("clearing = %d %s", response.Code, response.Body.String())
	}
	if response := controlRequest(t, handler, http.MethodPut, path, "application/json", `{"values":{"env":"dev"}}`, ""); response.Code != http.StatusOK {
		t.Fatalf("PUT = %d", response.Code)
	}
	if response := controlRequest(t, handler, http.MethodDelete, AccessTokensPath+"/"+string(token), "", "", ""); response.Code != http.StatusNoContent {
		t.Fatalf("DELETE token = %d", response.Code)
	}
	if defaults := handler.metadata.TokenDefaults(token); len(defaults) != 0 {
		t.Fatalf("defaults outlived their token: %v", defaults)
	}
}

func TestSessionMetadataIsSetByHandAndUnboundToNone(t *testing.T) {
	store, _, manager, _ := newPluginHandler(t)
	registry := metadata.NewRegistry(store)
	tokens, err := accesstoken.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, RequestRecords: store, ControlToken: testControlToken, ObserverToken: testObserverToken,
		AccessTokenManager: tokens, Plugins: manager, PluginStore: store, Metadata: registry, MetadataStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := approvedRequest(t, handler, http.MethodPatch, PluginsPath+"/demo", "application/json", `{"enabled":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("enable = %d %s", response.Code, response.Body.String())
	}
	started := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	session, turn := contract.SessionID("session_bound"), 3
	if err := store.InsertRequestRecord(context.Background(), contract.RequestRecord{
		ID: "request_bound", StartedAt: started, Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolAnthropicMessages, SessionID: &session, TurnIndex: &turn, Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}
	path := RequestSessionsPath + "/session_bound/metadata"
	if got := decodeBody[contract.SessionMetadata](t, controlRequest(t, handler, http.MethodGet, path, "", "", "")); len(got.Values) != 0 {
		t.Fatalf("metadata before any = %+v", got)
	}
	response := controlRequest(t, handler, http.MethodPut, path+"/project", "application/json", `{"value":"astrlink"}`, "")
	if got := decodeBody[contract.SessionMetadata](t, response); response.Code != http.StatusOK ||
		got.Values["project"].Value != "astrlink" || got.Values["project"].SetBy != contract.MetadataSetByManual || got.Values["project"].Turn != 3 {
		t.Fatalf("PUT = %d %s", response.Code, response.Body.String())
	}
	response = controlRequest(t, handler, http.MethodDelete, path+"/project", "", "", "")
	if got := decodeBody[contract.SessionMetadata](t, response); response.Code != http.StatusOK || got.Values["project"].Value != contract.MetadataNone {
		t.Fatalf("DELETE = %d %s", response.Code, response.Body.String())
	}
	for _, request := range [][3]string{
		{http.MethodPut, path + "/project", `{"value":"Not Valid"}`},
		{http.MethodPut, path + "/unknown", `{"value":"x"}`},
		{http.MethodPut, RequestSessionsPath + "/session_missing/metadata/project", `{"value":"x"}`},
		{http.MethodPost, path, `{}`},
	} {
		if response := controlRequest(t, handler, request[0], request[1], "application/json", request[2], ""); response.Code < 400 {
			t.Fatalf("%s %s = %d %s", request[0], request[1], response.Code, response.Body.String())
		}
	}
	observer := httptest.NewRequest(http.MethodPut, path+"/project", strings.NewReader(`{"value":"astrlink"}`))
	observer.Header.Set("Authorization", "Bearer "+testObserverToken)
	observer.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, observer)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("an observer changed a binding: %d", recorder.Code)
	}
}
