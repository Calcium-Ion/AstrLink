package controlapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// SyncPath is the configuration-sync resource.
const SyncPath = "/control/v1/sync"

// SyncManager is the sync engine as the control API sees it. *sync.Engine
// satisfies it; keeping it an interface here means the API surface can be
// exercised without one.
type SyncManager interface {
	Config(context.Context) (contract.SyncConfig, error)
	Configure(context.Context, contract.SyncConfig) error
	SetPassphrase(context.Context, []byte) error
	ClearPassphrase(context.Context) error
	Status(context.Context) (contract.SyncStatus, error)
	Sync(context.Context) (contract.SyncStatus, error)
}

// maxSyncBodyBytes bounds a sync request body. A configuration holds no
// documents, so it is small.
const maxSyncBodyBytes = 256 << 10

// syncConfigResponse is the configuration plus whether each written-only
// secret is stored. A read never returns the secrets themselves, so the form
// cannot round-trip them, and the flags say which fields a save already filled.
type syncConfigResponse struct {
	contract.SyncConfig
	RemotePasswordSet  bool `json:"remote_password_set"`
	RemoteSecretKeySet bool `json:"remote_secret_key_set"`
}

func newSyncConfigResponse(config contract.SyncConfig) syncConfigResponse {
	response := syncConfigResponse{
		RemotePasswordSet:  config.Remote.Password != "",
		RemoteSecretKeySet: config.Remote.SecretKey != "",
	}
	config.Remote.Password = ""
	config.Remote.SecretKey = ""
	response.SyncConfig = config
	return response
}

func (h *Handler) syncResource(w http.ResponseWriter, r *http.Request) {
	if h.syncManager == nil {
		writeError(w, 503, "sync_unavailable", "configuration sync is unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, SyncPath+"/")
	allow := func(method string) bool {
		if r.Method == method {
			return true
		}
		w.Header().Set("Allow", method)
		writeError(w, 405, "method_not_allowed", "method not allowed")
		return false
	}
	switch path {
	case "status":
		if !allow("GET") {
			return
		}
		value, err := h.syncManager.Status(r.Context())
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, value)
	case "config":
		if r.Method != "GET" && r.Method != "PUT" {
			w.Header().Set("Allow", "GET, PUT")
			writeError(w, 405, "method_not_allowed", "only GET and PUT are allowed")
			return
		}
		// A read reports the stored configuration, without the remote
		// credentials a status surface has no use for.
		if r.Method == "GET" {
			value, err := h.syncManager.Config(r.Context())
			if err != nil {
				h.writeStoreError(w, err)
				return
			}
			writeJSON(w, 200, newSyncConfigResponse(value))
			return
		}
		var config contract.SyncConfig
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSyncBodyBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil || decoder.Decode(new(any)) != io.EOF {
			writeError(w, 400, "invalid_sync_config", "invalid sync configuration")
			return
		}
		// A save that leaves the write-only secrets out keeps the stored ones,
		// so showing the page does not wipe a password the read never returned.
		stored, err := h.syncManager.Config(r.Context())
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		if config.Remote.Password == "" {
			config.Remote.Password = stored.Remote.Password
		}
		if config.Remote.SecretKey == "" {
			config.Remote.SecretKey = stored.Remote.SecretKey
		}
		if err := h.syncManager.Configure(r.Context(), config); err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, newSyncConfigResponse(config))
	case "passphrase":
		if r.Method == "DELETE" {
			// Forgetting the passphrase leaves the remote configuration, so
			// entering the passphrase again resumes sync.
			if err := h.syncManager.ClearPassphrase(r.Context()); err != nil {
				h.writeStoreError(w, err)
				return
			}
			writeJSON(w, 200, map[string]bool{"passphrase_set": false})
			return
		}
		if !allow("PUT") {
			return
		}
		var body struct {
			Passphrase string `json:"passphrase"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSyncBodyBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF {
			writeError(w, 400, "invalid_sync_passphrase", "a passphrase is required")
			return
		}
		passphrase := []byte(body.Passphrase)
		err := h.syncManager.SetPassphrase(r.Context(), passphrase)
		clear(passphrase)
		if err != nil {
			h.writeStoreError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"passphrase_set": true})
	case "run":
		if !allow("POST") {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		value, err := h.syncManager.Sync(ctx)
		if err != nil {
			writeError(w, 502, "sync_failed", err.Error())
			return
		}
		writeJSON(w, 200, value)
	default:
		writeError(w, 404, "not_found", "sync path not found")
	}
}
