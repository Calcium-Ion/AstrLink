//go:build !darwin && !windows && !linux

package toys

import "github.com/egoist/mygo"

// Elsewhere there is no pet.
func platformAvailable() bool { return false }

const platformMinTop = 0.0

func screenOf(bounds, work rect) screen { return screen{frame: bounds, visible: work, scale: 1} }

func composited() bool { return false }

func dockRect() *rect { return nil }

func styleWindow(*mygo.Window) {}

func preventActivation(*mygo.Window) {}

func frontRegardless(*mygo.Window) {}

type osProbe struct{}

func openOSProbe() osProbe { return osProbe{} }

func (osProbe) buttonDown() bool { return false }

func (osProbe) idleSeconds() (float64, bool) { return 0, false }

func (osProbe) otherWindows() []otherWindow { return nil }

func (osProbe) close() {}
