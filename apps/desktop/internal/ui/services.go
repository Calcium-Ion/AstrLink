package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/currency"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// ---- MainWindow ----

// MainWindow is the main window's placement and release.
type MainWindow struct{ a *App }

// FitMainWindowToContent shortens a freshly placed main window to the
// overview's first two modules, height CSS pixels drawn at
// devicePixelRatio. It applies once and never makes the window taller.
func (m MainWindow) FitMainWindowToContent(height, devicePixelRatio float64) (bool, error) {
	w := m.a.h.MainWindow()
	if w == nil {
		return false, nil
	}
	return m.a.startup.FitContentHeight(w, height, devicePixelRatio)
}

// SetMainWindowUnsaved keeps the hidden main window from being released
// while it holds unsaved edits.
func (m MainWindow) SetMainWindowUnsaved(unsaved bool) {
	m.a.reclaim.Hold(host.MainLabel, unsaved)
}

// ---- Files ----

// Files saves what pages export.
type Files struct{}

// SaveTextFile asks where to save contents, offering defaultFilename, and
// writes it there. It returns the path, nil when the operator canceled.
func (Files) SaveTextFile(defaultFilename, contents string) (*string, error) {
	options := mygo.SaveDialogOptions{DefaultPath: defaultFilename}
	// Windows adds the filter's extension to a name typed without one.
	switch strings.ToLower(filepath.Ext(defaultFilename)) {
	case ".md":
		options.Filters = []mygo.FileFilter{{Name: "Markdown (*.md)", Extensions: []string{"md"}}}
	case ".txt":
		options.Filters = []mygo.FileFilter{{Name: "TXT (*.txt)", Extensions: []string{"txt"}}}
	}
	path, err := mygo.Dialog.Save(options)
	if err != nil || path == "" {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return nil, err
	}
	return &path, nil
}

// ---- Currency ----

// Currency is the currency amounts estimated at official API prices show
// in.
type Currency struct{ a *App }

// DisplayCurrency returns the display currency and its rate to USD.
func (c Currency) DisplayCurrency() currency.Display { return c.a.display() }

// ---- Tray ----

// Tray is the tray popover's and the tray settings' view of the tray.
type Tray struct{ a *App }

// TrayState returns what the popover renders, for the stored tray
// preferences or tray, a draft the settings page previews.
func (t Tray) TrayState(tray *prefs.TrayPreferences) (TrayStateSnapshot, error) {
	if tray != nil {
		if err := tray.Validate(t.a.locale()); err != nil {
			return TrayStateSnapshot{}, err
		}
	}
	return t.a.tray.snapshot(tray), nil
}

// TrayAction performs an action the popover asks for.
func (t Tray) TrayAction(action TrayAction) error { return t.a.tray.perform(action) }

// TrayPopoverResize follows the popover's content height, height CSS
// pixels drawn at devicePixelRatio.
func (t Tray) TrayPopoverResize(height, devicePixelRatio float64) error {
	return t.a.tray.resize(height, devicePixelRatio)
}

// TrayPopoverHide closes the popover: Escape or a click beside the panel.
func (t Tray) TrayPopoverHide() { t.a.tray.dismiss() }

// TakeTrayNavigation hands over the page the tray last asked the main
// window to open, once.
func (t Tray) TakeTrayNavigation() *string { return t.a.tray.takeNavigation() }

// ---- TrajectoryInspector ----

// TrajectoryInspector is the trajectory inspector windows.
type TrajectoryInspector struct{ a *App }

// ShowTrajectoryInspector shows selection in the inspector following the
// list, opening one when there is none, and returns its label.
func (i TrajectoryInspector) ShowTrajectoryInspector(selection json.RawMessage) (string, error) {
	return i.a.showInspector(selection)
}

// UpdateTrajectoryInspector refreshes the inspector following the list,
// if one is open.
func (i TrajectoryInspector) UpdateTrajectoryInspector(selection json.RawMessage) error {
	return i.a.updateInspector(selection)
}

// TrajectoryInspectorState is what the calling inspector shows, which it
// pulls as it mounts.
func (i TrajectoryInspector) TrajectoryInspectorState(ctx context.Context) InspectorState {
	return i.a.inspectorState(ctx)
}

// SetTrajectoryInspectorPinned pins or unpins the calling inspector and
// returns the state that took effect.
func (i TrajectoryInspector) SetTrajectoryInspectorPinned(ctx context.Context, pinned bool) (bool, error) {
	return i.a.pinInspector(ctx, pinned)
}

// SetTrajectoryInspectorWide widens or narrows the calling inspector and
// returns the state that took effect.
func (i TrajectoryInspector) SetTrajectoryInspectorWide(ctx context.Context, wide bool) (bool, error) {
	return i.a.widenInspector(ctx, wide)
}

// CloseTrajectoryInspectors closes the inspectors that are not pinned.
func (i TrajectoryInspector) CloseTrajectoryInspectors() { i.a.closeUnpinnedInspectors() }
