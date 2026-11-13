// Package darwin calls AppKit where MyGo has no API of its own: handing the
// foreground back to the app the tray popover took it from, hearing the user
// switch away, and animating an inspector window as it widens. It drives
// the Objective-C runtime with purego, without cgo. Elsewhere its functions
// do nothing.
package darwin
