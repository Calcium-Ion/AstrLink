package controlapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/claudecli"
)

const (
	ClaudeCLIPath        = "/control/v1/claude-cli"
	ClaudeCLIInstallPath = ClaudeCLIPath + "/install"
)

// ClaudeCLI is the claude CLI official Claude accounts run. Satisfied by
// claudecli.Tool.
type ClaudeCLI interface {
	Status(context.Context) contract.ClaudeCLIStatus
	Install() error
}

// newSubscriptionMode is the mode a new subscription account starts in: the
// requested one, else official for Claude and legacy for everything else.
func newSubscriptionMode(provider contract.SubscriptionProvider, requested *contract.SubscriptionMode) (contract.SubscriptionMode, bool) {
	if requested == nil {
		if contract.SubscriptionModeOfficial.SupportedBy(provider) {
			return contract.SubscriptionModeOfficial, true
		}
		return "", true
	}
	mode := *requested
	if mode == "" || !mode.Valid() || !mode.SupportedBy(provider) {
		return "", false
	}
	if mode == contract.SubscriptionModeLegacy && !contract.SubscriptionModeOfficial.SupportedBy(provider) {
		// Providers without an official mode keep documents without one.
		return "", true
	}
	return mode, true
}

func (handler *Handler) registerClaudeCLIRoutes() {
	if handler.claudeCLI == nil {
		return
	}
	handler.mux.HandleFunc(ClaudeCLIPath, handler.authenticated(handler.getOnly(handler.getClaudeCLI), RoleObserver))
	handler.mux.HandleFunc(ClaudeCLIInstallPath, handler.authenticated(handler.installClaudeCLI, RoleOperator))
}

func (handler *Handler) getClaudeCLI(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, handler.claudeCLI.Status(request.Context()))
}

func (handler *Handler) installClaudeCLI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is allowed")
		return
	}
	if err := handler.claudeCLI.Install(); err != nil {
		switch {
		case errors.Is(err, claudecli.ErrInstallUnavailable):
			writeError(writer, http.StatusConflict, "claude_cli_install_unavailable", "install Claude Code on this computer with the official instructions")
		case errors.Is(err, claudecli.ErrInstallRunning):
			writeError(writer, http.StatusConflict, "claude_cli_installing", "Claude Code is already being installed")
		default:
			writeError(writer, http.StatusInternalServerError, "claude_cli_install_failed", "Claude Code install could not start")
		}
		return
	}
	writeJSON(writer, http.StatusAccepted, handler.claudeCLI.Status(request.Context()))
}
