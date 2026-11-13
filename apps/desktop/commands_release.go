package main

import (
	"context"
	"log"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/updates"
)

// registerReleaseCommands binds the services of app and privacy model
// updates (src/native-host/commands-release.ts).
func registerReleaseCommands(h *host.Host) {
	models := &updates.ModelUpdates{
		Source: updates.CoreModels{Privacy: control.Privacy{API: &control.API{Caller: h}}},
		Ready:  func() bool { return coreReady(h) },
		Publish: func(update *updates.PrivacyModelUpdate) {
			if main := h.MainWindow(); main != nil {
				if err := host.PrivacyModelUpdate.Emit(main, update); err != nil {
					log.Printf("astrlink: unable to publish the privacy model update: %v", err)
				}
			}
		},
		Announce: func(update updates.PrivacyModelUpdate) {
			if mainWindowAttended(h) {
				return
			}
			locale := h.PreferencesStore().Values().Locale
			key := "safety.modelUpdate." + string(update.Phase)
			showNotification(prefs.T(locale, key+".title"),
				prefs.T(locale, key+".notification", "name", update.Name, "version", update.Version))
		},
	}
	h.OnPrivacyPolicyChanged(models.Wake)
	h.OnPrivacyReleasesRead(models.Recheck)
	h.OnStarted(func() { go models.Run(context.Background()) })
	mygo.Bind(updates.Release{Models: models})
}

func coreReady(h *host.Host) bool {
	manager := h.CoreManager()
	if manager == nil {
		return false
	}
	view, _ := manager.View()
	return view.Phase == core.PhaseReady
}

// mainWindowAttended is false while the main window is hidden to the tray,
// minimized or behind other apps, where its toasts go unseen.
func mainWindowAttended(h *host.Host) bool {
	main := h.MainWindow()
	return main != nil && main.IsVisible() && main.IsFocused() && !main.IsMinimized()
}

func showNotification(title, body string) {
	if err := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}).Show(); err != nil {
		log.Printf("astrlink: unable to show a notification: %v", err)
	}
}
