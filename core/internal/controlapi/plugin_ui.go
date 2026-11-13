package controlapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// maxPluginUICallBytes bounds a call of a plugin's interface code.
const maxPluginUICallBytes = 1 << 20

// pluginClient serves a plugin's interface code, client.js, and its source
// map, while the code loads: the plugin enabled with ui.code granted, and
// its interface not turned off. The code comes wrapped in a call of
// __astrlinkPlugins.define, which hands each plugin its own client: the
// desktop and the web console define it before loading any.
func (handler *Handler) pluginClient(writer http.ResponseWriter, target *plugin.Plugin, file string) {
	if !target.UILoads() {
		writeError(writer, http.StatusNotFound, "plugin_ui_unavailable", "the plugin's interface does not load")
		return
	}
	path := filepath.Join(handler.plugins.Dir(), target.Key, file)
	opened, err := os.Open(path)
	if err != nil {
		writeError(writer, http.StatusNotFound, "plugin_ui_unavailable", "the plugin's interface does not load")
		return
	}
	defer opened.Close()
	data, err := io.ReadAll(io.LimitReader(opened, pluginbuild.MaxClientBytes*4+1))
	if err != nil || len(data) > pluginbuild.MaxClientBytes*4 {
		writeError(writer, http.StatusInternalServerError, "plugin_ui_unavailable", "could not read the plugin's interface")
		return
	}
	header := writer.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	if file == "client.js.map" {
		header.Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(data)
		return
	}
	header.Set("Content-Type", "text/javascript; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(wrapClient(target.Key, data))
}

// wrapClient wraps a plugin's interface code so that it runs with its own
// client. The prefix stays on the code's first line, which keeps the source
// map's lines; the bundler's map comment moves past the wrapper.
func wrapClient(key string, code []byte) []byte {
	code = bytes.TrimRight(code, "\n")
	if at := bytes.LastIndex(code, []byte("\n//# sourceMappingURL=")); at >= 0 {
		code = code[:at]
	}
	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, "globalThis.__astrlinkPlugins&&globalThis.__astrlinkPlugins.define(%q,function(__astrlinkClient){", key)
	buffer.Write(code)
	buffer.WriteString("\n});\n//# sourceMappingURL=client.js.map\n")
	return buffer.Bytes()
}

// pluginUICall runs the plugin's onUiCall hook for its interface code.
func (handler *Handler) pluginUICall(writer http.ResponseWriter, request *http.Request, target *plugin.Plugin) {
	if handler.pluginHost == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "plugins are unavailable")
		return
	}
	if !target.UILoads() || !target.Has(plugin.HookOnUICall) {
		writeError(writer, http.StatusNotFound, "plugin_ui_unavailable", "the plugin's interface does not load or has no backend calls")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxPluginUICallBytes)
	var input contract.PluginUICallRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 64 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_plugin_ui_call", "name must be 1 to 64 characters")
		return
	}
	result, err := handler.pluginHost.UICall(request.Context(), target, input.Name, input.Input)
	if err != nil {
		var failure *plugin.CallError
		reason, message := plugin.ReasonUnavailable, "the plugin could not answer"
		if errors.As(err, &failure) {
			reason, message = failure.Reason, failure.Message
		}
		writeErrorDetails(writer, http.StatusBadGateway, "plugin_call_failed", message, []errorDetail{{Reason: reason}})
		return
	}
	if len(bytes.TrimSpace(result)) == 0 {
		result = []byte("null")
	}
	writeJSON(writer, http.StatusOK, contract.PluginUICallResult{Result: result})
}
