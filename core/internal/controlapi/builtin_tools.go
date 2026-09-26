package controlapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

const BuiltinToolsPath = "/control/v1/builtin-tools/"

type BuiltinToolTester interface {
	TestBuiltinTool(context.Context, string, contract.BuiltinTool) (map[string]any, error)
}

func (handler *Handler) builtinToolResource(writer http.ResponseWriter, request *http.Request) {
	kind, action, ok := strings.Cut(strings.TrimPrefix(request.URL.Path, BuiltinToolsPath), "/")
	if !ok || !contract.BuiltinToolKind(kind) {
		writeError(writer, 404, "not_found", "builtin tool endpoint not found")
		return
	}
	if action == "test" {
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", "POST")
			writeError(writer, 405, "method_not_allowed", "use POST")
			return
		}
		if handler.builtinToolTester == nil {
			writeError(writer, 503, "unavailable", "tool testing is unavailable")
			return
		}
		var config contract.BuiltinTool
		if !decodeControlJSON(writer, request, &config) {
			return
		}
		if err := config.Validate(kind); err != nil {
			writeError(writer, 422, "invalid_builtin_tool", err.Error())
			return
		}
		result, err := handler.builtinToolTester.TestBuiltinTool(request.Context(), kind, config)
		if err != nil {
			writeError(writer, 502, "builtin_tool_test_failed", err.Error())
			return
		}
		writeJSON(writer, 200, result)
		return
	}
	if action != "credential" {
		writeError(writer, 404, "not_found", "builtin tool endpoint not found")
		return
	}
	secrets, ok := handler.serviceStore.(secretstore.SecretStore)
	if !ok {
		writeError(writer, 503, "unavailable", "credential store is unavailable")
		return
	}
	ref := secretstore.Ref("local://builtin-tool/" + kind)
	switch request.Method {
	case http.MethodGet:
		value, err := secrets.Get(request.Context(), ref)
		defer clear(value)
		if err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, 500, "credential_unavailable", "could not read tool credential status")
			return
		}
		writeJSON(writer, 200, map[string]bool{"configured": err == nil})
	case http.MethodPut:
		var input struct {
			Secret string `json:"secret"`
		}
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if len(input.Secret) == 0 || len(input.Secret) > 16384 || strings.ContainsAny(input.Secret, "\r\n") {
			writeError(writer, 422, "invalid_credential", "API key must contain 1 to 16384 bytes without newlines")
			return
		}
		value := []byte(input.Secret)
		defer clear(value)
		if err := secrets.Put(request.Context(), ref, value); err != nil {
			writeError(writer, 500, "credential_unavailable", "could not save tool credential")
			return
		}
		writeJSON(writer, 200, map[string]bool{"configured": true})
	case http.MethodDelete:
		if err := secrets.Delete(request.Context(), ref); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
			writeError(writer, 500, "credential_unavailable", "could not delete tool credential")
			return
		}
		writeJSON(writer, 200, map[string]bool{"configured": false})
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		writeError(writer, 405, "method_not_allowed", "use GET, PUT or DELETE")
	}
}
