package ui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// TestNoticesShowOnce: a notice recorded as shown stays shown, in the run
// and in the next one that reads the same file, and leaves other notices
// due.
func TestNoticesShowOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), noticesFile)
	var run notices
	if run.Seen(path, noticeHiddenToTray) {
		t.Fatal("a notice never shown counts as seen")
	}
	if err := run.Record(path, noticeHiddenToTray); err != nil {
		t.Fatal(err)
	}
	if !run.Seen(path, noticeHiddenToTray) {
		t.Fatal("the run forgot the notice it showed")
	}
	var next notices
	if !next.Seen(path, noticeHiddenToTray) {
		t.Fatal("the next run shows the notice again")
	}
	if next.Seen(path, "another") {
		t.Fatal("another notice counts as seen")
	}
}

// TestNoticesWithoutAFileLastTheRun: without a config directory, a notice
// shows once a run.
func TestNoticesWithoutAFileLastTheRun(t *testing.T) {
	var run notices
	if err := run.Record("", noticeHiddenToTray); err != nil {
		t.Fatal(err)
	}
	if !run.Seen("", noticeHiddenToTray) {
		t.Fatal("the run forgot the notice it showed")
	}
}

// TestNoticesOutliveABadFile: a file that is no list of notices counts as
// none seen and is written over; one that cannot be written still keeps
// the notice for the run.
func TestNoticesOutliveABadFile(t *testing.T) {
	for _, content := range []string{"not json", "null", `{"hidden_to_tray":false}`} {
		path := filepath.Join(t.TempDir(), noticesFile)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var run notices
		if run.Seen(path, noticeHiddenToTray) {
			t.Fatalf("%q: counts as seen", content)
		}
		if err := run.Record(path, noticeHiddenToTray); err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		var next notices
		if !next.Seen(path, noticeHiddenToTray) {
			t.Fatalf("%q: not written over", content)
		}
	}

	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, noticesFile)
	var run notices
	if err := run.Record(path, noticeHiddenToTray); err == nil {
		t.Fatal("a file under a file was written")
	}
	if !run.Seen(path, noticeHiddenToTray) {
		t.Fatal("the run forgot the notice it could not keep")
	}
}

// TestHiddenNotification: the tray notice replaces an earlier one, makes
// no sound and, outside macOS, carries the colour mark at a size the
// balloon's large icon shrinks from.
func TestHiddenNotification(t *testing.T) {
	for _, locale := range []prefs.Locale{prefs.LocaleEn, prefs.LocaleZhCN, prefs.LocaleJa} {
		options := hiddenNotification(locale)
		if options.ID != hiddenNotificationID {
			t.Fatalf("%s: ID %q", locale, options.ID)
		}
		if !options.Silent {
			t.Fatalf("%s: not silent", locale)
		}
		for _, text := range []string{options.Title, options.Body} {
			if text == "" || strings.HasPrefix(text, "host.") {
				t.Fatalf("%s: untranslated %q", locale, text)
			}
		}
		if mac {
			if options.Image != nil {
				t.Fatalf("%s: an image on macOS", locale)
			}
			continue
		}
		config, err := png.DecodeConfig(bytes.NewReader(options.Image))
		if err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		if config.Width != 256 || config.Height != 256 {
			t.Fatalf("%s: the image is %dx%d", locale, config.Width, config.Height)
		}
	}
}
