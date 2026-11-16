package ui

import (
	"errors"
	"log"
	"math"
	"sync"

	"github.com/egoist/mygo"
)

// The main window's opening size: a share of the display's work area at a
// fixed ratio, its width kept between minStartupWidth and maxStartupWidth.
const (
	workAreaFraction = 0.82
	aspectRatio      = 19.0 / 12.0
	// Keep the opening width while giving lists and editors more vertical
	// room. The height stays below 89% of the work area (0.82 × 1.08).
	defaultHeightScale = 1.08
	// A narrower window stacks the settings' pictures under their controls.
	// A work area whose share is narrower still opens at that share.
	minStartupWidth = 1024
	// Large and ultrawide displays would otherwise open a window whose extra
	// width only spreads fixed-size content apart.
	maxStartupWidth = 1200
	// mainMinWidth and mainMinHeight are the main window's configured
	// minimum; a small display lowers it.
	mainMinWidth  = 760
	mainMinHeight = 600
	// untouchedTolerance is how far a window may drift from where it was
	// placed and still count as untouched. Rounding a scaled size can move
	// an edge by a pixel.
	untouchedTolerance = 2
)

// Placement is a window's top-left corner and the size of its page area,
// in DIPs.
type Placement struct {
	X, Y          int
	Width, Height int
}

// startupGeometry is where the main window opens on a display.
type startupGeometry struct {
	// Content is the page area's size.
	Content mygo.Size
	// Minimum is the minimum size, lowered on a small display.
	Minimum  mygo.Size
	Position mygo.Point
}

// computeStartupGeometry fits the whole window, its frame included, in
// work: frame is what the window adds around its page area. It reports
// false when the work area is too small for any window.
func computeStartupGeometry(work mygo.Rectangle, frame, minimum mygo.Size, minimumWidth, maximumWidth int) (startupGeometry, bool) {
	share := math.Min(float64(work.Width)*workAreaFraction, float64(maximumWidth))
	// The ratio's width, which the work area's height may limit, sets the
	// height. The width only moves up to the minimum, within the share.
	ratioWidth := math.Floor(math.Min(share, float64(work.Height)*workAreaFraction*aspectRatio))
	height := int(math.Floor(ratioWidth / aspectRatio * defaultHeightScale))
	opening := int(math.Floor(math.Min(math.Max(ratioWidth, float64(minimumWidth)), share)))
	if opening <= frame.Width || height <= frame.Height {
		return startupGeometry{}, false
	}
	content := mygo.Size{Width: opening - frame.Width, Height: height - frame.Height}
	return startupGeometry{
		Content: content,
		// A fixed minimum must not force a small screen's window past its
		// calculated bounds. Normal screens retain the configured minimum.
		Minimum: mygo.Size{
			Width:  min(minimum.Width, content.Width),
			Height: min(minimum.Height, content.Height),
		},
		Position: mygo.Point{
			X: work.X + (work.Width-opening)/2,
			Y: work.Y + (work.Height-height)/2,
		},
	}, true
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// untouched reports whether current is still where placed put it.
func untouched(placed, current Placement) bool {
	return absDiff(placed.Width, current.Width) <= untouchedTolerance &&
		absDiff(placed.Height, current.Height) <= untouchedTolerance &&
		absDiff(placed.X, current.X) <= untouchedTolerance &&
		absDiff(placed.Y, current.Y) <= untouchedTolerance
}

// contentFitPlacement is current shortened to page content contentHeight
// tall, no lower than minimumHeight, around its own middle. It reports
// false when that would not make it shorter: a page taller than the window
// keeps the opening height and scrolls.
func contentFitPlacement(current Placement, minimumHeight int, contentHeight float64) (Placement, bool) {
	if math.IsNaN(contentHeight) || math.IsInf(contentHeight, 0) || contentHeight <= 0 || current.Width == 0 {
		return Placement{}, false
	}
	height := max(int(math.Min(math.Floor(contentHeight), math.MaxInt32)), minimumHeight)
	if height >= current.Height {
		return Placement{}, false
	}
	return Placement{
		X:      current.X,
		Y:      current.Y + (current.Height-height)/2,
		Width:  current.Width,
		Height: height,
	}, true
}

// onADisplay reports whether the middle of the window lies in one of the
// work areas.
func onADisplay(p Placement, work []mygo.Rectangle) bool {
	x := p.X + p.Width/2
	y := p.Y + p.Height/2
	for _, area := range work {
		if x >= area.X && x < area.X+area.Width && y >= area.Y && y < area.Y+area.Height {
			return true
		}
	}
	return false
}

// pendingContentFit is what fitting to the display left for the overview
// to finish: its height follows a fixed ratio, so the page reports how
// tall its first two modules are and a window taller than that shrinks to
// it once.
type pendingContentFit struct {
	// placed is the window as it was fitted. A window that no longer
	// matches was moved or resized by the operator and keeps its place.
	placed        Placement
	minimumHeight int
}

// remembered is where the operator left the main window, so a main window
// built again after the hidden one was released opens in the same place.
type remembered struct {
	// minimum is the minimum fitting chose; nil before the first fit.
	minimum *mygo.Size
	// placement is the last place the window had while not maximized,
	// which it returns to when unmaximized.
	placement *Placement
	maximized bool
}

// Startup places the main window: on the display at startup, shortened to
// the overview once, and where it was when it is built again.
type Startup struct {
	mu         sync.Mutex
	remembered remembered
	pending    *pendingContentFit
}

// placementOf reads a window's corner and page area size.
func placementOf(w *mygo.Window) Placement {
	bounds := w.Bounds()
	width, height := w.ContentSize()
	return Placement{X: bounds.X, Y: bounds.Y, Width: width, Height: height}
}

// displayOf is the display the window is on, else the primary one.
func displayOf(w *mygo.Window) mygo.Display {
	bounds := w.Bounds()
	if display := mygo.Screen.DisplayMatching(bounds); display.WorkArea.Width > 0 {
		return display
	}
	return mygo.Screen.PrimaryDisplay()
}

// FitToDisplay sizes and centers the hidden main window on its display.
// It runs before the window first shows, and for a window built again
// with no place to return to; a window the operator resized keeps its
// size otherwise.
func (s *Startup) FitToDisplay(w *mygo.Window) error {
	display := displayOf(w)
	bounds := w.Bounds()
	contentWidth, contentHeight := w.ContentSize()
	frame := mygo.Size{Width: max(bounds.Width-contentWidth, 0), Height: max(bounds.Height-contentHeight, 0)}
	geometry, ok := computeStartupGeometry(display.WorkArea, frame,
		mygo.Size{Width: mainMinWidth, Height: mainMinHeight}, minStartupWidth, maxStartupWidth)
	if !ok {
		return errors.New("the display's work area is too small for the main window")
	}
	w.SetMinimumSize(geometry.Minimum.Width, geometry.Minimum.Height)
	w.SetContentSize(geometry.Content.Width, geometry.Content.Height)
	w.SetPosition(geometry.Position.X, geometry.Position.Y)
	// Read back what the platform applied; it may round a scaled size.
	placed := placementOf(w)
	s.mu.Lock()
	minimum := geometry.Minimum
	s.remembered.minimum = &minimum
	s.pending = &pendingContentFit{placed: placed, minimumHeight: geometry.Minimum.Height}
	s.mu.Unlock()
	return nil
}

// FitContentHeight shortens a window FitToDisplay placed to the overview's
// first two modules, heightCSS CSS pixels drawn at devicePixelRatio, so
// the next module does not peek in. It never makes the window taller and
// applies once; a window the operator moved or resized meanwhile keeps its
// place. It reports whether the window changed.
func (s *Startup) FitContentHeight(w *mygo.Window, heightCSS, devicePixelRatio float64) (bool, error) {
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	if pending == nil {
		return false, nil
	}
	if !positive(heightCSS) || !positive(devicePixelRatio) {
		return false, errors.New("content height and pixel ratio must be positive")
	}
	if w.IsMaximized() || w.IsFullScreen() || w.IsMinimized() {
		return false, nil
	}
	current := placementOf(w)
	if !untouched(pending.placed, current) {
		return false, nil
	}
	// Windows folds its Text size setting into the page's pixel ratio but
	// not into the display's scale factor, so CSS pixels times the page's
	// ratio over the display's are the window's DIPs. Rounding down keeps
	// the next module's top edge outside the window.
	scale := displayOf(w).ScaleFactor
	if !positive(scale) {
		scale = 1
	}
	placement, ok := contentFitPlacement(current, pending.minimumHeight,
		math.Floor(heightCSS*devicePixelRatio/scale))
	if !ok {
		return false, nil
	}
	w.SetContentSize(placement.Width, placement.Height)
	w.SetPosition(placement.X, placement.Y)
	return true, nil
}

func positive(v float64) bool { return v > 0 && !math.IsInf(v, 0) }

// Remember records where the main window is before it hides.
func (s *Startup) Remember(w *mygo.Window) {
	maximized := w.IsMaximized()
	var placement *Placement
	// A maximized window's frame is the display's; keep the one it returns
	// to.
	if !maximized {
		p := placementOf(w)
		placement = &p
	}
	s.mu.Lock()
	s.remembered.maximized = maximized
	if placement != nil {
		s.remembered.placement = placement
	}
	s.mu.Unlock()
}

// Restore places a main window built again after the hidden one was
// released: where the last one was while that is still on a display,
// otherwise as at startup.
func (s *Startup) Restore(w *mygo.Window) {
	s.mu.Lock()
	r := s.remembered
	s.mu.Unlock()
	var work []mygo.Rectangle
	for _, display := range mygo.Screen.Displays() {
		work = append(work, display.WorkArea)
	}
	if r.placement != nil && onADisplay(*r.placement, work) {
		// The operator's own place wins over fitting the page.
		s.mu.Lock()
		s.pending = nil
		s.mu.Unlock()
		if r.minimum != nil {
			w.SetMinimumSize(r.minimum.Width, r.minimum.Height)
		}
		// Position first: moving to a display with another scale rescales
		// the window, and the size must hold where it ends up.
		w.SetPosition(r.placement.X, r.placement.Y)
		w.SetContentSize(r.placement.Width, r.placement.Height)
	} else if err := s.FitToDisplay(w); err != nil {
		log.Printf("astrlink: unable to place the main window on the current display: %v", err)
	}
	if r.maximized {
		w.Maximize()
	}
}
