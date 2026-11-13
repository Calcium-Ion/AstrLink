//go:build !windows

package agents

import (
	"os"

	"github.com/egoist/mygo"
)

// opensLinks reports whether an app is registered for `scheme:` links.
func opensLinks(scheme string) bool {
	app, err := mygo.Shell.DefaultApp(scheme + "://open")
	if err != nil || app.Path == "" {
		return false
	}
	// Launch Services can keep a record briefly after the app is deleted.
	_, err = os.Stat(app.Path)
	return err == nil
}
