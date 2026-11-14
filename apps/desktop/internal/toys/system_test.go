package toys

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestThePetWindowNeverActivatesAstrLink(t *testing.T) {
	b := mygo.Rectangle{X: 1920, Y: 0, Width: 1280, Height: 719}
	for _, goos := range []string{"darwin", "windows", "linux"} {
		o := petWindowOptions(goos, b, "window.x = 1;")
		if !o.Transparent || !o.Frameless || !o.AlwaysOnTop || !o.SkipTaskbar || !o.Hidden || !o.DisableShadow {
			t.Fatalf("%s: %+v", goos, o)
		}
		if o.X != b.X || o.Y != b.Y || o.Width != b.Width || o.Height != b.Height || o.Page.PreloadScript != "window.x = 1;" {
			t.Fatalf("%s: %+v", goos, o)
		}
		// Only macOS takes a panel's activation from its creation; Linux
		// would make a layer surface of it, and Windows a tool window
		// owned by the hidden application window.
		if o.Panel != (goos == "darwin") {
			t.Fatalf("%s: panel %v", goos, o.Panel)
		}
		if o.Page.BackgroundThrottling != mygo.ThrottlingOff {
			t.Fatalf("%s: her page would slow down behind other apps", goos)
		}
	}
}
