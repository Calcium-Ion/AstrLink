//go:build !darwin

package widget

// systemFeed is nil: only macOS has desktop widgets.
func systemFeed() *Feed { return nil }
