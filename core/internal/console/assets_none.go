//go:build !webui

package console

import "io/fs"

// assets is nil without the webui build tag, so desktop builds embed and
// serve no web console.
var assets fs.FS
