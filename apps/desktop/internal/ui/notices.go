package ui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"sync"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

const (
	// noticesFile keeps, in the config directory, the notices the operator
	// has seen.
	noticesFile = "notices.json"
	// noticeHiddenToTray says, the first time the main window hides to the
	// tray, that AstrLink still runs there and how to bring the window back.
	noticeHiddenToTray = "hidden_to_tray"
	// hiddenNotificationID is the ID of that notification, so one shown
	// again replaces the last rather than piling up.
	hiddenNotificationID = "astrlink-hidden-to-tray"
)

// notices are the notices shown once, rather than every time what they
// explain happens. The file at the path the first call names keeps them
// across runs; without a path, or when it cannot be written, they last the
// run.
type notices struct {
	mu   sync.Mutex
	seen map[string]bool
}

// Seen reports whether the notice name was shown.
func (n *notices) Seen(path, name string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.load(path)[name]
}

// Record keeps that the notice name was shown. An error is the file's; the
// run keeps it all the same.
func (n *notices) Record(path, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	seen := n.load(path)
	if seen[name] {
		return nil
	}
	seen[name] = true
	if path == "" {
		return nil
	}
	data, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	return prefs.WriteFileAtomic(path, append(data, '\n'))
}

// load reads the file at path once; a file that is missing or unreadable
// counts as no notice seen. n.mu is held.
func (n *notices) load(path string) map[string]bool {
	if n.seen != nil {
		return n.seen
	}
	n.seen = map[string]bool{}
	if path == "" {
		return n.seen
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("astrlink: unable to read the notices shown: %v", err)
		}
		return n.seen
	}
	var kept map[string]bool
	if err := json.Unmarshal(data, &kept); err != nil {
		log.Printf("astrlink: unable to read the notices shown: %v", err)
		return n.seen
	}
	for name, shown := range kept {
		if shown {
			n.seen[name] = true
		}
	}
	return n.seen
}

// hiddenNotification says the main window went to the tray (the menu bar
// on macOS). It makes no sound, since nothing waits on the operator. On
// Windows and Linux it shows the colour mark: a Windows balloon without an
// image of its own shows the tray icon, grey while the gateway is not
// running, and blurred from 32 pixels.
func hiddenNotification(locale prefs.Locale) mygo.NotificationOptions {
	titleKey, bodyKey := "host.tray.hiddenTitle", "host.tray.hiddenBody"
	if mac {
		titleKey, bodyKey = "host.tray.hiddenMenuBarTitle", "host.tray.hiddenMenuBarBody"
	}
	options := mygo.NotificationOptions{
		ID:     hiddenNotificationID,
		Title:  prefs.T(locale, titleKey),
		Body:   prefs.T(locale, bodyKey),
		Silent: true,
	}
	if !mac {
		options.Image = notificationIcon()
	}
	return options
}

// notificationIcon is the colour mark notifications show, rendered at 256
// pixels by scripts/sync-branding.mjs.
func notificationIcon() []byte {
	png, err := icons.ReadFile("icons/notification.png")
	if err != nil {
		panic(err)
	}
	return png
}
