//go:build !darwin

package darwin

import "time"

// FrontmostOtherApp reports no app outside macOS.
func FrontmostOtherApp() int { return 0 }

// ReturnForeground hands nothing back outside macOS.
func ReturnForeground(pid int) bool { return false }

// OnSessionResignActive never calls fn outside macOS.
func OnSessionResignActive(fn func()) {}

// AnimateFrame animates nothing outside macOS.
func AnimateFrame(nsWindow uintptr, shiftX, width float64, duration time.Duration) bool { return false }
