package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"

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
	release := updates.NewRelease(models)
	h.OnStarted(func() {
		app := newAppUpdates(h)
		updates.Attach(release, app)
		go app.Run(context.Background())
		go models.Run(context.Background())
	})
	mygo.Bind(release)
}

// newAppUpdates builds the app updater once the host has its preferences
// and Core.
func newAppUpdates(h *host.Host) *updates.Manager {
	cache, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		log.Printf("astrlink: unable to resolve the cache directory: %v", err)
	}
	store := h.PreferencesStore()
	return updates.NewManager(updates.Config{
		CurrentVersion:   mygo.App.Version(),
		Platform:         host.PlatformName(),
		Arch:             updates.Arch(),
		Target:           updates.Target(),
		Development:      mygo.IsDev(),
		InstallSupported: updates.InstallSupported(),
		PublicKey:        updates.PublicKey(),
		CacheDir:         filepath.Join(cache, "updates"),
		Preferences:      store.Values,
		SaveUpdates:      store.ReplaceUpdates,
		Publish: func(snapshot updates.UpdateSnapshot) {
			if main := h.MainWindow(); main != nil {
				if err := host.AppUpdateStatus.Emit(main, snapshot); err != nil {
					log.Printf("astrlink: unable to publish update state: %v", err)
				}
			}
		},
		Announce: func(kind, version string) {
			if mainWindowAttended(h) {
				return
			}
			locale := store.Values().Locale
			showNotification(prefs.T(locale, "about.phase."+kind),
				prefs.T(locale, "about."+kind+"Notification", "version", version))
		},
		Core:     h.CoreManager(),
		Updater:  updates.MyGo{},
		Relaunch: func() { mygo.App.Relaunch() },
		Transport: func(useSystemProxy bool) http.RoundTripper {
			transport := netproxy.Transport().Clone()
			if !useSystemProxy {
				transport.Proxy = nil
			}
			return transport
		},
	})
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
