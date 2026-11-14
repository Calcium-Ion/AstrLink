package host

import (
	"encoding/json"

	"github.com/egoist/mygo"
)

// The events the frontend listens to, under the names the Tauri shell
// emitted them with (src/native-host/events.ts maps each to the generated
// client). Payloads owned by another part of the app are declared as any: the
// part that emits them owns their type, and the frontend listener keeps its
// own parser.
var (
	// AppUpdateStatus carries the update snapshot to the main window.
	AppUpdateStatus = mygo.NewEvent[any]("app-update-status")
	// PrivacyModelUpdate tells the main window of a new privacy model release.
	PrivacyModelUpdate = mygo.NewEvent[any]("privacy-model-update")
	// ProviderImport tells the main window an astrlink:// import is pending.
	ProviderImport = mygo.NewEvent[any]("provider-import")
	// TrayNavigate asks the main window to take the page the tray chose.
	TrayNavigate = mygo.NewEvent[struct{}]("tray:navigate")
	// TrayState carries the tray popover's snapshot.
	TrayState = mygo.NewEvent[any]("tray:state")
	// TrajectoryInspectorSelect hands an inspector window its selection.
	TrajectoryInspectorSelect = mygo.NewEvent[json.RawMessage]("trajectory-inspector:select")
	// RawSealingChanged tells every window the raw content was unlocked,
	// locked or its key changed.
	RawSealingChanged = mygo.NewEvent[struct{}]("raw-sealing-changed")
	// LocalClientStatus carries the local AI clients' version snapshot.
	LocalClientStatus = mygo.NewEvent[any]("local-client-status")
	// DisplayCurrencyChanged carries the currency amounts are shown in.
	DisplayCurrencyChanged = mygo.NewEvent[any]("display-currency-changed")
	// QuotaDisplayModeChanged carries whether quotas show what is used or
	// what remains.
	QuotaDisplayModeChanged = mygo.NewEvent[string]("quota-display-mode-changed")
	// ThemePreferenceChanged carries the theme the user chose.
	ThemePreferenceChanged = mygo.NewEvent[string]("theme-preference-changed")
	// WindowResized tells a window's page that it was resized, maximized or
	// went into or out of full screen, so a custom title bar reads its
	// state again (getCurrentWindow().onResized).
	WindowResized = mygo.NewEvent[struct{}]("window:resized")

	// The desktop pet's window hears its feed, preferences, the mouse over
	// it, the other windows on its screen, the idle time and a move to
	// another screen (internal/toys owns the payloads).
	ToysFeed    = mygo.NewEvent[any]("toys:feed")
	ToysPrefs   = mygo.NewEvent[any]("toys:prefs")
	ToysPointer = mygo.NewEvent[any]("toys:pointer")
	ToysFrames  = mygo.NewEvent[any]("toys:frames")
	ToysIdle    = mygo.NewEvent[any]("toys:idle")
	ToysScreen  = mygo.NewEvent[any]("toys:screen")
)
