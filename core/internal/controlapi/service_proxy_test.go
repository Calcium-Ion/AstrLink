package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
)

func TestServiceProxyCRUDCredentialsAndDraftProbe(t *testing.T) {
	store, handler := newServiceHandler(t, "service_proxy_one")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "provider.invalid" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			t.Error("draft probe lost saved instance authentication")
		}
		io.WriteString(w, `{"data":[{"id":"model-through-proxy"}]}`)
	}))
	defer proxy.Close()
	input := `{"mode":"custom","url":"` + proxy.URL + `","credential":{"username":"user","password":"secret"}}`
	create := serviceRequestForTest(t, handler, "POST", ServicesPath, "application/json", `{"name":"proxy test","kind":"openai","http":{"base_url":"http://provider.invalid","auth":{"scheme":"none"}},"capabilities":[],"proxy":`+input+`}`, "")
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	var service contract.Service
	json.Unmarshal(create.Body.Bytes(), &service)
	ref := secretstore.Ref(service.Proxy.CredentialRef)
	if strings.Contains(create.Body.String(), "secret") || strings.Contains(create.Body.String(), `"username"`) {
		t.Fatal("proxy secret exposed")
	}
	secret, err := store.Get(context.Background(), ref)
	if err != nil || !strings.Contains(string(secret), "secret") {
		t.Fatal("proxy authentication missing")
	}
	path := ServicesPath + "/" + string(service.ID)
	patch := func(body, etag string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := serviceRequestForTest(t, handler, "PATCH", path, "application/merge-patch+json", body, etag)
		if r.Code != status {
			t.Fatalf("patch=%s status=%d body=%s", body, r.Code, r.Body.String())
		}
		return r
	}
	patch(`{"proxy":{"mode":"direct"}}`, `"stale"`, 412)
	retained := patch(`{"name":"renamed"}`, create.Header().Get("ETag"), 200)
	retained = patch(`{"proxy":{"mode":"custom","url":"`+proxy.URL+`"}}`, retained.Header().Get("ETag"), 200)
	if _, err := store.Get(context.Background(), ref); err != nil {
		t.Fatal("omitted authentication was removed")
	}
	handler.serviceModels = servicemodel.New(store, nil, nil)
	probe := serviceRequestForTest(t, handler, "POST", ServiceModelProbesPath, "application/json", `{"service_id":"service_proxy_one","kind":"openai","http":{"base_url":"http://provider.invalid","auth":{"scheme":"none"}},"protocol":"openai.models"}`, "")
	if probe.Code != 200 || !strings.Contains(probe.Body.String(), "model-through-proxy") {
		t.Fatal(probe.Body.String())
	}
	patch(`{"proxy":{"mode":"custom","url":"http://user:password@proxy"}}`, retained.Header().Get("ETag"), 422)
	removed := patch(`{"proxy":{"mode":"custom","url":"`+proxy.URL+`","credential":null}}`, retained.Header().Get("ETag"), 200)
	if _, err := store.Get(context.Background(), ref); err == nil {
		t.Fatal("explicit clearing retained secret")
	}
	restored := patch(`{"proxy":`+input+`}`, removed.Header().Get("ETag"), 200)
	inherited := patch(`{"proxy":null}`, restored.Header().Get("ETag"), 200)
	if strings.Contains(inherited.Body.String(), `"proxy"`) {
		t.Fatal("null did not restore inheritance")
	}
	if _, err := store.Get(context.Background(), ref); err == nil {
		t.Fatal("inherit retained proxy credentials")
	}
	restored = patch(`{"proxy":`+input+`}`, inherited.Header().Get("ETag"), 200)
	deleted := serviceRequestForTest(t, handler, "DELETE", path, "", "", restored.Header().Get("ETag"))
	if deleted.Code != 204 {
		t.Fatal(deleted.Body.String())
	}
	if _, err := store.Get(context.Background(), ref); err == nil {
		t.Fatal("delete retained proxy credentials")
	}
}
