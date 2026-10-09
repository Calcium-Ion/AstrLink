//go:build webui

package console

import (
	"embed"
	"io/fs"
)

//go:embed webui
var embedded embed.FS

// assets is the web console, embedded only in builds tagged webui.
var assets = mustSub(embedded, "webui")

func mustSub(files fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
