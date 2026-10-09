package console

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/controlapi"
)

// assetExists reports whether path names a file embedded with the console.
func (handler *Handler) assetExists(urlPath string) bool {
	if handler.assets == nil {
		return false
	}
	info, err := fs.Stat(handler.assets, assetName(urlPath))
	return err == nil && info.Mode().IsRegular()
}

func assetName(urlPath string) string {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" {
		return "index.html"
	}
	return name
}

// serveAsset serves the page and the files embedded with it. There is no
// catch-all route: the listener is shared with the inference plane.
func (handler *Handler) serveAsset(writer http.ResponseWriter, request *http.Request) {
	if handler.assets == nil {
		controlapi.WriteError(writer, http.StatusNotFound, "web_console_unavailable", "this build does not include the web console")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		controlapi.WriteError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are allowed")
		return
	}
	name := assetName(request.URL.Path)
	content, err := fs.ReadFile(handler.assets, name)
	// A clean checkout has a tracked placeholder so tagged Go tests/builds work
	// without generating frontend artifacts. Staging supplies index.html.
	if name == "index.html" && errors.Is(err, fs.ErrNotExist) {
		content, err = fs.ReadFile(handler.assets, "placeholder.html")
	}
	if err != nil {
		// Missing files and directories alike.
		controlapi.WriteError(writer, http.StatusNotFound, "not_found", "file not found")
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(writer, request, name, time.Time{}, bytes.NewReader(content))
}
