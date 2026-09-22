package controlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const ServiceProxyProbesPath = "/control/v1/service-proxy-probes"

// probeServiceProxy uses only the draft proxy credentials. It neither saves the
// draft nor sends the service's API credentials or follows upstream redirects.
func (handler *Handler) probeServiceProxy(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeMethodNotAllowed(writer, http.MethodPost)
		return
	}
	var input struct {
		ServiceID contract.ServiceID `json:"service_id,omitempty"`
		Proxy     json.RawMessage    `json:"proxy"`
		TargetURL string             `json:"target_url"`
	}
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	target, err := url.Parse(input.TargetURL)
	if err != nil || len(input.TargetURL) > 2048 || target == nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_proxy_target", "proxy test target must be an HTTP or HTTPS URL without authentication, query or fragment")
		return
	}
	service := contract.Service{ID: "service_proxy_probe"}
	if input.ServiceID != "" {
		if input.ServiceID.Validate() != nil {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_service_id", "service_id is invalid")
			return
		}
		record, err := handler.serviceStore.GetService(request.Context(), input.ServiceID)
		if err != nil {
			handler.writeStoreError(writer, err)
			return
		}
		service = record.Service
	}
	var proxy struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(input.Proxy, &proxy) != nil || proxy.Mode != "custom" {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_proxy", "proxy test requires a custom proxy")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	ctx, err = handler.bindDraftProxy(ctx, service, input.Proxy)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_proxy", err.Error())
		return
	}
	probe, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_proxy_target", "invalid proxy test target")
		return
	}
	probe.Header.Set("User-Agent", "")
	client := networkproxy.WrapClient(&http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	})
	defer client.CloseIdleConnections()
	started := time.Now()
	response, err := client.Do(probe)
	if err != nil {
		// Transport errors can contain proxy userinfo; never return or log them.
		writeError(writer, http.StatusBadGateway, "proxy_connection_failed", "proxy connection failed; check the address, authentication and target")
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusProxyAuthRequired {
		writeError(writer, http.StatusBadGateway, "proxy_authentication_failed", "proxy authentication failed")
		return
	}
	writeJSON(writer, http.StatusOK, struct {
		LatencyMS  int64 `json:"latency_ms"`
		StatusCode int   `json:"status_code"`
	}{time.Since(started).Milliseconds(), response.StatusCode})
}

func applyServiceProxyInput(service *contract.Service, mutation *storage.CredentialMutation, raw json.RawMessage) error {
	if raw == nil {
		return nil
	}
	if isJSONNull(raw) {
		service.Proxy = nil
		mutation.ProxyPresent = true
		return nil
	}
	var input struct {
		Mode       string          `json:"mode"`
		URL        string          `json:"url,omitempty"`
		Credential json.RawMessage `json:"credential,omitempty"`
	}
	if strictUnmarshal(raw, &input) != nil {
		return fmt.Errorf("invalid proxy configuration")
	}
	proxy := &contract.ServiceProxy{Mode: input.Mode, URL: input.URL}
	if service.Proxy != nil && proxy.Mode == "custom" {
		proxy.CredentialRef = service.Proxy.CredentialRef
	}
	if input.Credential != nil {
		mutation.ProxyPresent = true
		proxy.CredentialRef = ""
		if !isJSONNull(input.Credential) {
			credential := &contract.ProxyCredential{}
			if strictUnmarshal(input.Credential, credential) != nil || credential.Validate() != nil || proxy.Mode != "custom" {
				return fmt.Errorf("invalid proxy authentication")
			}
			mutation.Proxy = credential
		}
	}
	if err := proxy.Validate(service.ID); err != nil {
		return err
	}
	service.Proxy = proxy
	return nil
}

func (handler *Handler) bindDraftProxy(ctx context.Context, service contract.Service, raw json.RawMessage) (context.Context, error) {
	mutation := storage.CredentialMutation{}
	if err := applyServiceProxyInput(&service, &mutation, raw); err != nil {
		return ctx, err
	}
	if mutation.ProxyPresent {
		return networkproxy.BindConfig(ctx, service.ID, service.Proxy, mutation.Proxy)
	}
	secrets, _ := handler.serviceStore.(secretstore.SecretStore)
	return networkproxy.Bind(ctx, service, secrets)
}
