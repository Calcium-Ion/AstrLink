package ui

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo"

	"github.com/QuantumNous/astrlink/desktop/internal/control"
	"github.com/QuantumNous/astrlink/desktop/internal/core"
	"github.com/QuantumNous/astrlink/desktop/internal/host"
	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
	"github.com/QuantumNous/astrlink/desktop/internal/toys"
)

// toysPreload tells every page whether the pet can come out here.
func toysPreload() string { return toys.InitializationScript(toys.Available()) }

// startToys runs the desktop pet once the host started.
func (a *App) startToys(ctx context.Context) {
	api := &control.API{Caller: a.h}
	runtime := toys.New(ctx, toys.Deps{
		Prefs: a.toyPrefs,
		Activity: func(ctx context.Context, since string) (json.RawMessage, bool) {
			if manager := a.h.CoreManager(); manager == nil {
				return nil, false
			} else if view, _ := manager.View(); view.Phase != core.PhaseReady {
				return nil, false
			}
			activity, err := control.Records{API: api}.GetRequestActivity(ctx, since)
			return activity, err == nil
		},
		Today: func(ctx context.Context) (*toys.Today, bool) {
			windows := localWindowsAt(time.Now(), localTimeZone())
			raw, err := control.Usage{API: api}.GetUsageSummary(ctx, windows.todayFrom, windows.todayTo, windows.timeZone, "hour")
			if err != nil {
				return nil, false
			}
			summary, ok := objectOf(raw)
			if !ok {
				return nil, true
			}
			return todayFrom(summary), true
		},
		ConfigDir: a.h.Directories().Config,
		Debug:     mygo.IsDev(),
		Preload:   a.h.PreloadScript,
	})
	a.toys.Store(runtime)
	go runtime.Apply()
}

// todayFrom is the day's usage in a usage summary for today, or nil
// without its totals.
func todayFrom(summary object) *toys.Today {
	totals := totalsFrom(summary)
	if totals == nil {
		return nil
	}
	today := &toys.Today{Tokens: totals.TotalTokens, Hours: hourlyFrom(summary)}
	if top := topGroup(summary, totals.TotalTokens); top != nil {
		today.Model = &top.Name
	}
	today.Client = topClient(summary)
	return today
}

// topClient is the client that used the most tokens, or nil when
// unknown clients did or nothing was used.
func topClient(summary object) *string {
	groups, ok := field[[]object](summary, "by_client")
	if !ok || len(groups) == 0 {
		return nil
	}
	if tokens, _ := field[int64](groups[0], "total_tokens"); tokens <= 0 {
		return nil
	}
	if id, ok := field[string](groups[0], "id"); ok && id != "" && id != "unknown" {
		return &id
	}
	return nil
}

func (a *App) toyPrefs() toys.Prefs {
	values := a.prefs()
	return toys.Prefs{DesktopToysPreferences: values.DesktopToys, Locale: values.Locale}
}

// ---- Toys ----

// Toys are the desktop pet's commands.
type Toys struct{ a *App }

var errNoToys = errors.New("the desktop toys have not started")

// ToysSnapshot is what the calling toy window's page needs once loaded.
func (t Toys) ToysSnapshot(ctx context.Context) (toys.Snapshot, error) {
	runtime := t.a.toys.Load()
	if runtime == nil {
		return toys.Snapshot{}, errNoToys
	}
	return runtime.Snapshot(host.CallerLabel(ctx)), nil
}

// UpdateDesktopToys saves the pet's preferences and applies them at once.
func (t Toys) UpdateDesktopToys(input prefs.DesktopToysPreferences) (host.SettingsSnapshot, error) {
	store := t.a.h.PreferencesStore()
	if store == nil {
		return host.SettingsSnapshot{}, errors.New("the preferences have not loaded")
	}
	if _, err := store.ReplaceDesktopToys(input); err != nil {
		return host.SettingsSnapshot{}, err
	}
	if runtime := t.a.toys.Load(); runtime != nil {
		runtime.Apply()
	}
	return t.a.h.SettingsSnapshot(), nil
}

// ToysHit takes which parts of the calling toy window hover or take
// clicks; hold keeps all of it taking the mouse while something is pressed
// or dragged, and carry (hold if null) lets a dragged pet take her window
// to another screen.
func (t Toys) ToysHit(ctx context.Context, rects []toys.HitRect, hold bool, carry *bool) {
	label := host.CallerLabel(ctx)
	if runtime := t.a.toys.Load(); runtime != nil && toys.IsToy(label) {
		runtime.Hit(label, rects, hold, carry)
	}
}
