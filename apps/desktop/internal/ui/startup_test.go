package ui

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
)

func work(x, y, width, height int) mygo.Rectangle {
	return mygo.Rectangle{X: x, Y: y, Width: width, Height: height}
}

func size(width, height int) mygo.Size { return mygo.Size{Width: width, Height: height} }

func geometry(t *testing.T, area mygo.Rectangle, frame, minimum mygo.Size, maximumWidth int) startupGeometry {
	t.Helper()
	g, ok := computeStartupGeometry(area, frame, minimum, maximumWidth)
	if !ok {
		t.Fatalf("computeStartupGeometry(%v) found no room", area)
	}
	return g
}

func TestRetinaLaptopLeavesSpaceBeyondTheMenuBarAndDock(t *testing.T) {
	// 1512 × 982 logical display; usable area starts below the 33-point
	// menu bar and ends above the 58-point Dock, at a 2× scale factor.
	g := geometry(t, work(0, 66, 3024, 1782), size(0, 0), size(1520, 1200), 2400)
	if g.Content != size(2313, 1577) || g.Position != (mygo.Point{X: 355, Y: 168}) || g.Minimum != size(1520, 1200) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestSmallDisplaysCanStartBelowTheNormalMinimumSize(t *testing.T) {
	g := geometry(t, work(0, 24, 1024, 576), size(0, 0), size(760, 600), 1200)
	if g.Content != size(747, 509) || g.Minimum != size(747, 509) || g.Position != (mygo.Point{X: 138, Y: 57}) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestScalesWithTheDisplayAndCentersOnOffsetMonitors(t *testing.T) {
	for _, c := range []struct{ scale, width, height, x, y int }{
		{1, 1298, 885, -1609, 97},
		{2, 2596, 1770, -3218, 155},
		{3, 3895, 2656, -4828, 212},
	} {
		area := work(-1920*c.scale, 40, 1920*c.scale, 1000*c.scale)
		g := geometry(t, area, size(0, 0), size(760*c.scale, 600*c.scale), 1400*c.scale)
		if g.Content != size(c.width, c.height) || g.Position != (mygo.Point{X: c.x, Y: c.y}) {
			t.Fatalf("scale %d: geometry = %+v", c.scale, g)
		}
	}
}

func TestA1080pDisplayOpensALargerWindowAndSmallOnesKeepTheirs(t *testing.T) {
	for _, c := range []struct {
		area          mygo.Rectangle
		width, height int
		x, y          int
	}{
		// 1920 × 1080 above a 30-pixel taskbar: its height sets the size,
		// under the maximum width.
		{work(0, 0, 1920, 1050), 1363, 929, 278, 60},
		// Smaller displays open as before.
		{work(0, 0, 1280, 690), 895, 610, 192, 40},
		{work(0, 0, 1024, 570), 740, 504, 142, 33},
	} {
		g := geometry(t, c.area, size(0, 0), size(mainMinWidth, mainMinHeight), maxStartupWidth)
		if g.Content != size(c.width, c.height) || g.Position != (mygo.Point{X: c.x, Y: c.y}) {
			t.Fatalf("%v: geometry = %+v", c.area, g)
		}
	}
	// The overview still fits it to its first two modules, around its
	// middle.
	opened, minimumHeight := opening(t, work(0, 0, 1920, 1032), 1)
	if fitted, ok := contentFitPlacement(opened, minimumHeight, 832); !ok || fitted != placement(290, 99, 1339, 832) {
		t.Fatalf("fitted = %+v, %v", fitted, ok)
	}
	// A 1440p display stops at the maximum width.
	if g := geometry(t, work(0, 0, 2560, 1400), size(0, 0), size(mainMinWidth, mainMinHeight), maxStartupWidth); g.Content != size(1440, 982) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestWideDisplaysStopAtTheMaximumWidth(t *testing.T) {
	// 2560 × 1080 logical ultrawide below a 25-point menu bar, at 2×.
	g := geometry(t, work(0, 50, 5120, 2110), size(0, 0), size(1520, 1200), 2400)
	if g.Content != size(2400, 1637) || g.Minimum != size(1520, 1200) || g.Position != (mygo.Point{X: 1360, Y: 286}) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestPortraitDisplaysAreLimitedByWidthAndIncludeNativeBorders(t *testing.T) {
	g := geometry(t, work(1920, -200, 1000, 1600), size(16, 38), size(760, 600), 1200)
	if g.Content != size(804, 521) || g.Minimum != size(760, 521) || g.Position != (mygo.Point{X: 2010, Y: 320}) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestRejectsUnusableWorkAreas(t *testing.T) {
	for _, area := range []mygo.Rectangle{work(0, 0, 0, 0), work(0, 0, 10, 10)} {
		if _, ok := computeStartupGeometry(area, size(16, 38), size(760, 600), 1200); ok {
			t.Fatalf("work area %v fitted a window", area)
		}
	}
}

func placement(x, y, width, height int) Placement {
	return Placement{X: x, Y: y, Width: width, Height: height}
}

func TestAWindowReturnsWhileItsDisplayIsConnected(t *testing.T) {
	primary := work(0, 50, 2560, 1550)
	left := work(-1920, 0, 1920, 1040)
	// Mostly past the right edge, but its middle is still on screen.
	if !onADisplay(placement(2000, 400, 1000, 600), []mygo.Rectangle{primary}) {
		t.Fatal("a window whose middle is on screen counted as off it")
	}
	if !onADisplay(placement(-1700, 100, 1200, 800), []mygo.Rectangle{primary, left}) {
		t.Fatal("a window on the left display counted as off it")
	}
	// The left display was unplugged while the window was released.
	if onADisplay(placement(-1700, 100, 1200, 800), []mygo.Rectangle{primary}) {
		t.Fatal("a window on an unplugged display counted as on one")
	}
	if onADisplay(placement(0, 0, 1200, 800), nil) {
		t.Fatal("a window counted as on a display with none connected")
	}
}

// opening is the window FitToDisplay opens on area at scale with the
// configured 760 × 600 minimum, in physical pixels as the Tauri shell
// placed it, and the minimum height it keeps. The arithmetic is the same
// in any unit.
func opening(t *testing.T, area mygo.Rectangle, scale float64) (Placement, int) {
	t.Helper()
	minimum := size(int(math.Round(760*scale)), int(math.Round(600*scale)))
	g := geometry(t, area, size(0, 0), minimum, int(math.Floor(maxStartupWidth*scale)))
	return placement(g.Position.X, g.Position.Y, g.Content.Width, g.Content.Height), g.Minimum.Height
}

func TestWindowsDisplaysShrinkToTheFirstTwoModules(t *testing.T) {
	// 2560 × 1440 at 150% and 1920 × 1080 at 125%, above the taskbar.
	for _, c := range []struct {
		area         mygo.Rectangle
		scale, css   float64
		height, x, y int
	}{
		{work(0, 0, 2560, 1368), 1.5, 785, 1177, 392, 95},
		{work(0, 0, 1920, 1020), 1.25, 700, 875, 298, 72},
	} {
		opened, minimumHeight := opening(t, c.area, c.scale)
		fitted, ok := contentFitPlacement(opened, minimumHeight, math.Floor(c.css*c.scale))
		if !ok || fitted != placement(c.x, c.y, opened.Width, c.height) {
			t.Fatalf("fitted = %+v, %v", fitted, ok)
		}
	}
}

func TestAPageTallerThanTheWindowKeepsTheOpeningHeight(t *testing.T) {
	// 1920 × 1080 and 1366 × 768 at 100%, where the first two modules need
	// more than the window opens with.
	for _, c := range []struct {
		area    mygo.Rectangle
		content float64
	}{
		{work(0, 0, 1920, 1032), 960},
		{work(0, 0, 1366, 728), 812},
	} {
		opened, minimumHeight := opening(t, c.area, 1)
		if _, ok := contentFitPlacement(opened, minimumHeight, c.content); ok {
			t.Fatalf("a %v page shrank a %d window", c.content, opened.Height)
		}
		if _, ok := contentFitPlacement(opened, minimumHeight, float64(opened.Height)); ok {
			t.Fatal("a page exactly as tall as the window shrank it")
		}
	}
}

func TestRetinaWindowsShrinkAroundTheirMiddle(t *testing.T) {
	opened, minimumHeight := opening(t, work(0, 66, 3024, 1782), 2)
	fitted, ok := contentFitPlacement(opened, minimumHeight, 700.5*2)
	if !ok || fitted != placement(355, 256, 2313, 1401) {
		t.Fatalf("fitted = %+v, %v", fitted, ok)
	}
}

func TestAWindowOnALeftDisplayKeepsItsColumn(t *testing.T) {
	fitted, ok := contentFitPlacement(placement(-1618, 40, 1300, 1002), 600, 900)
	if !ok || fitted != placement(-1618, 91, 1300, 900) {
		t.Fatalf("fitted = %+v, %v", fitted, ok)
	}
}

func TestAShortPageStopsAtTheMinimumHeight(t *testing.T) {
	fitted, ok := contentFitPlacement(placement(298, 58, 1324, 903), 750, 300)
	if !ok || fitted != placement(298, 134, 1324, 750) {
		t.Fatalf("fitted = %+v, %v", fitted, ok)
	}
	// 1920 × 1080 at 150% already opens at the minimum height.
	opened, minimumHeight := opening(t, work(0, 0, 1920, 1008), 1.5)
	if minimumHeight != opened.Height {
		t.Fatalf("minimum height %d, window %d", minimumHeight, opened.Height)
	}
	if _, ok := contentFitPlacement(opened, minimumHeight, 840); ok {
		t.Fatal("a window at its minimum height shrank")
	}
}

func TestRejectsUnusablePageHeights(t *testing.T) {
	opened := placement(298, 58, 1324, 903)
	for _, height := range []float64{0, -10, math.NaN(), math.Inf(1)} {
		if _, ok := contentFitPlacement(opened, 750, height); ok {
			t.Fatalf("height %v fitted", height)
		}
	}
	if _, ok := contentFitPlacement(placement(298, 58, 0, 903), 750, 800); ok {
		t.Fatal("a window without width fitted")
	}
}

func TestAWindowTheOperatorMovedOrResizedIsNoLongerUntouched(t *testing.T) {
	placed := placement(298, 58, 1324, 903)
	for _, c := range []struct {
		current Placement
		want    bool
	}{
		{placed, true},
		{placement(300, 56, 1322, 905), true},
		{placement(301, 58, 1324, 903), false},
		{placement(298, 58, 1324, 906), false},
		{placement(298, 58, 1320, 903), false},
		// Dragged to the display on the left.
		{placement(-1622, 58, 1324, 903), false},
	} {
		if got := untouched(placed, c.current); got != c.want {
			t.Fatalf("untouched(%+v) = %v, want %v", c.current, got, c.want)
		}
	}
}
