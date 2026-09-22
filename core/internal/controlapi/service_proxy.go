package controlapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

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
