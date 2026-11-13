package ui

import (
	"sync"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Approving an agent's request to read raw content happens only in a small
// window of its own: it opens by itself when a request arrives, without
// taking the keyboard, so no keystroke meant for another window approves
// anything, and every approval asks for Touch ID or the raw password. It
// floats above other windows only while a request waits. Closing it
// decides nothing and ends no grant; a pending request waits until it
// expires, and the tray opens the window again.

// ApprovalLabel is the approval window's label
// (RAW_ACCESS_APPROVAL_LABEL in the frontend).
const ApprovalLabel = "raw-access-approval"

const (
	approvalWidth     = 440
	approvalHeight    = 620
	approvalMinWidth  = 380
	approvalMinHeight = 460
)

// windowBackground is a window's background before its page paints,
// following the theme.
const windowBackground = "light-dark(#ffffff, #111827)"

// approvalOpening keeps two requests from building two windows.
var approvalOpening struct {
	sync.Mutex
	building bool
}

// chrome gives a secondary window the main window's frame: macOS keeps the
// traffic lights over the page, elsewhere the page draws its title bar.
func chrome(options *mygo.WindowOptions) {
	if mac {
		options.TitleBarStyle = mygo.TitleBarHidden
	} else {
		options.Frameless = true
	}
}

// openApproval opens the approval window. One opened for a new request
// does not take the keyboard; the tray asks for focus because the
// operator clicked it.
func (a *App) openApproval(focus bool) {
	approvalOpening.Lock()
	if approvalOpening.building {
		approvalOpening.Unlock()
		return
	}
	existing := windowByLabel(ApprovalLabel)
	approvalOpening.building = existing == nil
	approvalOpening.Unlock()
	if existing != nil {
		// An open window already lists the new request; showing it again
		// would make it the key window.
		if focus {
			bringToFront(existing)
		}
		return
	}
	defer func() {
		approvalOpening.Lock()
		approvalOpening.building = false
		approvalOpening.Unlock()
	}()
	view := a.tray.view()
	options := mygo.WindowOptions{
		Title:          prefs.T(a.locale(), "host.window.rawAccessApproval"),
		URL:            "/",
		Width:          approvalWidth,
		Height:         approvalHeight,
		UseContentSize: true,
		MinWidth:       approvalMinWidth,
		MinHeight:      approvalMinHeight,
		Hidden:         true,
		// The main window may be in the tray; a request must still be
		// seen above whatever the operator works in.
		AlwaysOnTop:     view.PendingRawAccess > 0,
		BackgroundColor: windowBackground,
		Page:            mygo.PageOptions{PreloadScript: a.h.PreloadScript(ApprovalLabel)},
	}
	chrome(&options)
	w := mygo.NewWindow(options)
	host.Register(w, ApprovalLabel)
	w.OnReadyToShow(func() {
		if focus {
			w.Show()
		} else {
			w.ShowInactive()
		}
	})
}

// setApprovalFloating floats the approval window while a request waits,
// including one left open from the tray; once none is left, other windows
// cover it again.
func (a *App) setApprovalFloating(floating bool) {
	if w := windowByLabel(ApprovalLabel); w != nil {
		w.SetAlwaysOnTop(floating)
	}
}
