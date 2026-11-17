package ui

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// A change that grants a plugin more is confirmed only in a small window
// of its own (control.PluginApprovalWindow): it opens by itself, without
// taking the keyboard, when the main window asks for such a change, so
// plugin interface code in the main window can click nothing there. It
// floats above other windows; closing it decides nothing.

const (
	pluginApprovalWidth     = 460
	pluginApprovalHeight    = 600
	pluginApprovalMinWidth  = 380
	pluginApprovalMinHeight = 420
)

// pluginApprovalOpening keeps two changes from building two windows.
var pluginApprovalOpening = make(chan struct{}, 1)

// PluginApprovalWindow is the plugin confirmation window, for the plugin
// commands.
func (a *App) PluginApprovalWindow() control.PluginApprovalWindow { return pluginApproval{a} }

type pluginApproval struct{ a *App }

// Open shows the window, or tells an open one that the change waiting was
// replaced. One opened for a new change does not take the keyboard; the
// user asking for it does.
func (p pluginApproval) Open(focus bool) error {
	pluginApprovalOpening <- struct{}{}
	defer func() { <-pluginApprovalOpening }()
	if w := windowByLabel(control.PluginApprovalLabel); w != nil {
		if err := host.PluginApprovalChanged.Emit(w, struct{}{}); err != nil {
			log.Printf("astrlink: unable to tell the plugin confirmation window of a new change: %v", err)
		}
		if focus {
			bringToFront(w)
		}
		return nil
	}
	options := mygo.WindowOptions{
		Title:           prefs.T(p.a.locale(), "host.window.pluginApproval"),
		URL:             "/",
		Width:           pluginApprovalWidth,
		Height:          pluginApprovalHeight,
		UseContentSize:  true,
		MinWidth:        pluginApprovalMinWidth,
		MinHeight:       pluginApprovalMinHeight,
		Hidden:          true,
		AlwaysOnTop:     true,
		BackgroundColor: windowBackground,
		Page:            mygo.PageOptions{PreloadScript: p.a.h.PreloadScript(control.PluginApprovalLabel)},
	}
	chrome(&options)
	w := mygo.NewWindow(options)
	host.Register(w, control.PluginApprovalLabel)
	w.OnReadyToShow(func() {
		if focus {
			w.Show()
		} else {
			w.ShowInactive()
		}
	})
	return nil
}

// Decided tells every window what the user decided and closes the
// confirmation window.
func (p pluginApproval) Decided(finished control.PluginApprovalFinished) {
	if err := host.PluginApprovalFinished.Broadcast(finished); err != nil {
		log.Printf("astrlink: unable to announce a plugin confirmation: %v", err)
	}
	if w := windowByLabel(control.PluginApprovalLabel); w != nil {
		w.Close()
	}
}
