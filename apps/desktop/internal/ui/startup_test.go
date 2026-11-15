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
	// menu bar and ends above the 58-point Dock, at a 2× scale factor. Its
	// height sets the window's, and the width takes the maximum.
	g := geometry(t, work(0, 66, 3024, 1782), size(0, 0), size(1520, 1200), 2400)
	if g.Content != size(2400, 1577) || g.Position != (mygo.Point{X: 312, Y: 168}) || g.Minimum != size(1520, 1200) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestSmallDisplaysCanStartBelowTheNormalMinimumSize(t *testing.T) {
	g := geometry(t, work(0, 24, 1024, 576), size(0, 0), size(760, 600), 1200)
	if g.Content != size(839, 509) || g.Minimum != size(760, 509) || g.Position != (mygo.Point{X: 92, Y: 57}) {
		t.Fatalf("geometry = %+v", g)
	}
}

func TestScalesWithTheDisplayAndCentersOnOffsetMonitors(t *testing.T) {
	for _, c := range []struct{ scale, width, height, x, y int }{
		{1, 1400, 885, -1660, 97},
		{2, 2800, 1770, -3320, 155},
		{3, 4200, 2656, -4980, 212},
	} {
		area := work(-1920*c.scale, 40, 1920*c.scale, 1000*c.scale)
		g := geometry(t, area, size(0, 0), size(760*c.scale, 600*c.scale), 1400*c.scale)
		if g.Content != size(c.width, c.height) || g.Position != (mygo.Point{X: c.x, Y: c.y}) {
			t.Fatalf("scale %d: geometry = %+v", c.scale, g)
		}
	}
}

// A work area too short for the window's ratio sets its height, as before;
// its width takes the usual share of the work area's width, up to the
// maximum, instead of following the ratio. Taller work areas open as
// before.
func TestShortWorkAreasOpenWiderWindowsOfTheSameHeight(t *testing.T) {
	for _, c := range []struct {
		name          string
		area          mygo.Rectangle
		width, height int
		minimumHeight int
		x, y          int
	}{
		// A 1920 × 1080 panel macOS runs as 1280 × 720, below the menu
		// bar and above the Dock. It opened 824 × 562.
		{"1280 × 720 macOS", work(0, 0, 1280, 635), 1049, 562, 562, 115, 36},
		// 1152 × 648 on macOS; it opened 810 × 552.
		{"1152 × 648 macOS", work(0, 0, 1152, 624), 944, 552, 552, 104, 36},
		// 1366 × 768 at 100% above the Windows taskbar; it opened 945 wide.
		{"1366 × 768 Windows", work(0, 0, 1366, 728), 1120, 644, 600, 123, 42},
		// 1920 × 1080 at 125% and 150%; they opened 1069 and 882 wide.
		{"1080p at 125%", work(0, 0, 1536, 824), 1200, 729, 600, 168, 47},
		{"1080p at 150%", work(0, 0, 1280, 680), 1049, 601, 600, 115, 39},
		// 1280 × 720 above a 30-point panel; it opened 895 wide.
		{"1280 × 720 Linux", work(0, 0, 1280, 690), 1049, 610, 600, 115, 40},
		// 1024 × 600; it opened 740 wide.
		{"1024 × 600", work(0, 0, 1024, 570), 839, 504, 504, 92, 33},
		// A 1080p and a 1440p display at 100% stop at the maximum width,
		// as before.
		{"1080p", work(0, 0, 1920, 1050), 1200, 818, 600, 360, 116},
		{"1440p", work(0, 0, 2560, 1400), 1200, 818, 600, 680, 291},
	} {
		g := geometry(t, c.area, size(0, 0), size(mainMinWidth, mainMinHeight), maxStartupWidth)
		if g.Content != size(c.width, c.height) || g.Minimum != size(mainMinWidth, c.minimumHeight) ||
			g.Position != (mygo.Point{X: c.x, Y: c.y}) {
			t.Errorf("%s %v: geometry = %+v", c.name, c.area, g)
		}
	}
	// The overview still fits a short window to its first two modules,
	// around its middle, and keeps the wider width.
	opened, minimumHeight := opening(t, work(0, 0, 1366, 728), 1)
	if fitted, ok := contentFitPlacement(opened, minimumHeight, 620); !ok || fitted != placement(123, 54, 1120, 620) {
		t.Fatalf("fitted = %+v, %v", fitted, ok)
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
		{work(0, 0, 2560, 1368), 1.5, 785, 1177, 380, 95},
		{work(0, 0, 1920, 1020), 1.25, 700, 875, 210, 72},
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
		{work(0, 0, 1920, 1032), 832},
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
	if !ok || fitted != placement(312, 256, 2400, 1401) {
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
