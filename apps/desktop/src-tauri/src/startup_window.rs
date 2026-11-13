use std::sync::{Mutex, MutexGuard};

use tauri::{LogicalSize, Manager, PhysicalPosition, PhysicalRect, PhysicalSize, WebviewWindow};

const WORK_AREA_FRACTION: f64 = 0.82;
const ASPECT_RATIO: f64 = 19.0 / 12.0;
// Keep the opening width while giving lists and editors more vertical room.
// The height stays below 89% of the work area (0.82 × 1.08).
const DEFAULT_HEIGHT_SCALE: f64 = 1.08;
// Large and ultrawide displays would otherwise open a window whose extra width
// only spreads fixed-size content apart.
const MAX_LOGICAL_WIDTH: f64 = 1200.0;

#[derive(Clone, Copy, Debug)]
struct Placement {
    position: PhysicalPosition<i32>,
    size: PhysicalSize<u32>,
}

/// Where the operator left the main window, so a main window built again
/// after the hidden one was released opens in the same place.
#[derive(Clone, Copy)]
struct Remembered {
    /// The minimum `fit_to_monitor` chose; a small display lowers it.
    minimum_size: Option<PhysicalSize<u32>>,
    /// The last place the window had while not maximized, which it returns
    /// to when un-maximized.
    placement: Option<Placement>,
    maximized: bool,
}

static REMEMBERED: Mutex<Remembered> = Mutex::new(Remembered {
    minimum_size: None,
    placement: None,
    maximized: false,
});

fn remembered() -> MutexGuard<'static, Remembered> {
    REMEMBERED
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// What `fit_to_monitor` left behind for the overview to finish: its
/// height follows a fixed ratio, so the page reports how tall its first two
/// modules are and a window taller than that shrinks to it once.
#[derive(Clone, Copy, Debug)]
struct PendingContentFit {
    /// The window as `fit_to_monitor` placed it. A window that no longer
    /// matches was moved or resized by the operator and keeps its place.
    placed: Placement,
    minimum_height: u32,
}

static PENDING_CONTENT_FIT: Mutex<Option<PendingContentFit>> = Mutex::new(None);

fn pending_content_fit() -> MutexGuard<'static, Option<PendingContentFit>> {
    PENDING_CONTENT_FIT
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// How far, in physical pixels, a window may drift from where it was placed
/// and still count as untouched. Rounding a scaled size can move an edge by
/// a pixel.
const UNTOUCHED_TOLERANCE: u32 = 2;

fn untouched(placed: &Placement, current: &Placement) -> bool {
    placed.size.width.abs_diff(current.size.width) <= UNTOUCHED_TOLERANCE
        && placed.size.height.abs_diff(current.size.height) <= UNTOUCHED_TOLERANCE
        && placed.position.x.abs_diff(current.position.x) <= UNTOUCHED_TOLERANCE
        && placed.position.y.abs_diff(current.position.y) <= UNTOUCHED_TOLERANCE
}

/// The window `current` shortened to page content `content_height` physical
/// pixels tall, no lower than `minimum_height`, around its own middle. None
/// when that would not make it shorter: a page taller than the window keeps
/// the opening height and scrolls.
fn content_fit_placement(
    current: &Placement,
    minimum_height: u32,
    content_height: f64,
) -> Option<Placement> {
    if !content_height.is_finite() || content_height <= 0.0 || current.size.width == 0 {
        return None;
    }
    let height = (content_height.floor().min(f64::from(u32::MAX)) as u32).max(minimum_height);
    if height >= current.size.height {
        return None;
    }
    Some(Placement {
        position: PhysicalPosition::new(
            current.position.x,
            current.position.y + ((current.size.height - height) / 2) as i32,
        ),
        size: PhysicalSize::new(current.size.width, height),
    })
}

#[derive(Debug)]
struct StartupGeometry {
    inner_size: PhysicalSize<u32>,
    minimum_size: PhysicalSize<u32>,
    position: PhysicalPosition<i32>,
}

fn startup_geometry(
    work_area: &PhysicalRect<i32, u32>,
    frame: PhysicalSize<u32>,
    minimum_size: PhysicalSize<u32>,
    maximum_width: u32,
) -> Option<StartupGeometry> {
    // Fit the entire window, including native decorations, inside the usable
    // desktop. Physical coordinates preserve placement on mixed-DPI monitors.
    let width = (f64::from(work_area.size.width) * WORK_AREA_FRACTION)
        .min(f64::from(work_area.size.height) * WORK_AREA_FRACTION * ASPECT_RATIO)
        .min(f64::from(maximum_width))
        .floor() as u32;
    let height = (f64::from(width) / ASPECT_RATIO * DEFAULT_HEIGHT_SCALE).floor() as u32;
    if width <= frame.width || height <= frame.height {
        return None;
    }
    let inner_size = PhysicalSize::new(width - frame.width, height - frame.height);
    Some(StartupGeometry {
        inner_size,
        // A fixed minimum must not force a small screen's window past its
        // calculated bounds. Normal screens retain the configured minimum.
        minimum_size: PhysicalSize::new(
            minimum_size.width.min(inner_size.width),
            minimum_size.height.min(inner_size.height),
        ),
        position: PhysicalPosition::new(
            work_area.position.x + ((work_area.size.width - width) / 2) as i32,
            work_area.position.y + ((work_area.size.height - height) / 2) as i32,
        ),
    })
}

/// Called during setup, before the hidden main window is shown, and for a
/// rebuilt main window with no place to return to. Tray restores and
/// frontend reloads must preserve the user's resized window.
pub fn fit_to_monitor(window: &WebviewWindow) -> Result<(), Box<dyn std::error::Error>> {
    let monitor = match window.current_monitor() {
        Ok(Some(monitor)) => monitor,
        _ => window
            .primary_monitor()?
            .ok_or("no monitor is available for the main window")?,
    };
    let config = window
        .app_handle()
        .config()
        .app
        .windows
        .iter()
        .find(|config| config.label == window.label())
        .ok_or("main window configuration is missing")?;
    let scale = window.scale_factor()?;
    let minimum_size = LogicalSize::new(
        config.min_width.unwrap_or(0.0),
        config.min_height.unwrap_or(0.0),
    )
    .to_physical(scale);
    let maximum_width = (MAX_LOGICAL_WIDTH * scale).floor() as u32;
    let outer_size = window.outer_size()?;
    let inner_size = window.inner_size()?;
    let frame = PhysicalSize::new(
        outer_size.width.saturating_sub(inner_size.width),
        outer_size.height.saturating_sub(inner_size.height),
    );
    let geometry = startup_geometry(monitor.work_area(), frame, minimum_size, maximum_width)
        .ok_or("monitor work area is too small for the main window")?;

    window.set_min_size(Some(geometry.minimum_size))?;
    window.set_size(geometry.inner_size)?;
    window.set_position(geometry.position)?;
    remembered().minimum_size = Some(geometry.minimum_size);
    let minimum_height = geometry.minimum_size.height;
    *pending_content_fit() = None;
    after_placement_applies(window, move |window| {
        // Read back what the platform applied; it may round a scaled size. A
        // window whose place cannot be read keeps its opening height.
        *pending_content_fit() = match (window.outer_position(), window.inner_size()) {
            (Ok(position), Ok(size)) => Some(PendingContentFit {
                placed: Placement { position, size },
                minimum_height,
            }),
            _ => None,
        };
    });
    Ok(())
}

/// Runs `read` once the size and position just requested have taken effect.
/// macOS applies them on the main dispatch queue after the main thread's
/// current call returns, so reading them at once still sees the window as
/// it was built, and the overview's fit would take that for a window the
/// operator moved. Requests from another thread first travel through the
/// event loop; a task sent the same way follows them, and a block it queues
/// on the main dispatch queue reads the new place.
#[cfg(target_os = "macos")]
fn after_placement_applies(
    window: &WebviewWindow,
    read: impl FnOnce(&WebviewWindow) + Send + 'static,
) {
    let target = window.clone();
    let queued = window.run_on_main_thread(move || {
        dispatch2::DispatchQueue::main().exec_async(move || read(&target));
    });
    if let Err(error) = queued {
        eprintln!("unable to read where AstrLink placed its window: {error}");
    }
}

/// Windows applies a size and position before the call returns. Where they
/// land later, as on Linux, the read sees the window as built and the
/// overview leaves the opening height alone.
#[cfg(not(target_os = "macos"))]
fn after_placement_applies(window: &WebviewWindow, read: impl FnOnce(&WebviewWindow)) {
    read(window);
}

/// Shortens a window placed by `fit_to_monitor` to the overview's first two
/// modules, `height_css` CSS pixels drawn at `device_pixel_ratio`, so the
/// next module does not peek in. Never makes it taller. Applies once; a
/// window the operator moved or resized meanwhile keeps its place. Returns
/// whether the window changed.
pub fn fit_content_height(
    window: &WebviewWindow,
    height_css: f64,
    device_pixel_ratio: f64,
) -> Result<bool, Box<dyn std::error::Error>> {
    let Some(pending) = pending_content_fit().take() else {
        return Ok(false);
    };
    if !height_css.is_finite()
        || height_css <= 0.0
        || !device_pixel_ratio.is_finite()
        || device_pixel_ratio <= 0.0
    {
        return Err("content height and pixel ratio must be positive".into());
    }
    if window.is_maximized()? || window.is_fullscreen()? || window.is_minimized()? {
        return Ok(false);
    }
    let current = Placement {
        position: window.outer_position()?,
        size: window.inner_size()?,
    };
    if !untouched(&pending.placed, &current) {
        return Ok(false);
    }
    // Windows folds its Text size setting into the page's pixel ratio but
    // not into the window's scale factor, so CSS pixels times the page's
    // ratio are the window's physical pixels. Rounding down keeps the next
    // module's top edge outside the window.
    let Some(placement) = content_fit_placement(
        &current,
        pending.minimum_height,
        (height_css * device_pixel_ratio).floor(),
    ) else {
        return Ok(false);
    };
    window.set_size(placement.size)?;
    window.set_position(placement.position)?;
    Ok(true)
}

/// Records where the main window is before it hides.
pub fn remember(window: &WebviewWindow) {
    let read = (|| {
        let maximized = window.is_maximized()?;
        // A maximized window's frame is the display's; keep the one it
        // returns to.
        if maximized {
            return Ok::<_, tauri::Error>((true, None));
        }
        let placement = Placement {
            position: window.outer_position()?,
            size: window.inner_size()?,
        };
        Ok((false, Some(placement)))
    })();
    match read {
        Ok((maximized, placement)) => {
            let mut remembered = remembered();
            remembered.maximized = maximized;
            if placement.is_some() {
                remembered.placement = placement;
            }
        }
        Err(error) => eprintln!("unable to remember where the AstrLink window was: {error}"),
    }
}

/// Places a main window built again after the hidden one was released:
/// where the last one was while that is still on a display, otherwise as
/// at startup.
pub fn restore(window: &WebviewWindow) -> Result<(), Box<dyn std::error::Error>> {
    let Remembered {
        minimum_size,
        placement,
        maximized,
    } = *remembered();
    let work_areas = window
        .available_monitors()?
        .iter()
        .map(|monitor| *monitor.work_area())
        .collect::<Vec<_>>();
    match placement.filter(|placement| on_a_display(placement, &work_areas)) {
        Some(placement) => {
            // The operator's own place wins over fitting the page.
            pending_content_fit().take();
            if let Some(minimum_size) = minimum_size {
                window.set_min_size(Some(minimum_size))?;
            }
            // Position first: moving to a display with another scale
            // rescales the window, and the size must hold where it ends up.
            window.set_position(placement.position)?;
            window.set_size(placement.size)?;
        }
        None => fit_to_monitor(window)?,
    }
    if maximized {
        window.maximize()?;
    }
    Ok(())
}

/// Whether the middle of the window lies on a display still connected.
fn on_a_display(placement: &Placement, work_areas: &[PhysicalRect<i32, u32>]) -> bool {
    let x = i64::from(placement.position.x) + i64::from(placement.size.width / 2);
    let y = i64::from(placement.position.y) + i64::from(placement.size.height / 2);
    work_areas.iter().any(|area| {
        let left = i64::from(area.position.x);
        let top = i64::from(area.position.y);
        (left..left + i64::from(area.size.width)).contains(&x)
            && (top..top + i64::from(area.size.height)).contains(&y)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn work_area(x: i32, y: i32, width: u32, height: u32) -> PhysicalRect<i32, u32> {
        PhysicalRect {
            position: PhysicalPosition::new(x, y),
            size: PhysicalSize::new(width, height),
        }
    }

    #[test]
    fn retina_laptop_leaves_space_beyond_the_menu_bar_and_dock() {
        // 1512 × 982 logical display; usable area starts below the 33-point
        // menu bar and ends above the 58-point Dock, at a 2× scale factor.
        let geometry = startup_geometry(
            &work_area(0, 66, 3024, 1782),
            PhysicalSize::new(0, 0),
            PhysicalSize::new(1520, 1200),
            2400,
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(2313, 1577));
        assert_eq!(geometry.position, PhysicalPosition::new(355, 168));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(1520, 1200));
    }

    #[test]
    fn small_displays_can_start_below_the_normal_minimum_size() {
        let geometry = startup_geometry(
            &work_area(0, 24, 1024, 576),
            PhysicalSize::new(0, 0),
            PhysicalSize::new(760, 600),
            1200,
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(747, 509));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(747, 509));
        assert_eq!(geometry.position, PhysicalPosition::new(138, 57));
    }

    #[test]
    fn scales_with_the_display_and_centers_on_offset_monitors() {
        for (scale, width, height, x, y) in [
            (1, 1298, 885, -1609, 97),
            (2, 2596, 1770, -3218, 155),
            (3, 3895, 2656, -4828, 212),
        ] {
            let area = work_area(-1920 * scale as i32, 40, 1920 * scale, 1000 * scale);
            let geometry = startup_geometry(
                &area,
                PhysicalSize::new(0, 0),
                PhysicalSize::new(760 * scale, 600 * scale),
                1400 * scale,
            )
            .unwrap();
            assert_eq!(geometry.inner_size, PhysicalSize::new(width, height));
            assert_eq!(geometry.position, PhysicalPosition::new(x, y));
        }
    }

    #[test]
    fn wide_displays_stop_at_the_maximum_width() {
        // 2560 × 1080 logical ultrawide below a 25-point menu bar, at 2×.
        let geometry = startup_geometry(
            &work_area(0, 50, 5120, 2110),
            PhysicalSize::new(0, 0),
            PhysicalSize::new(1520, 1200),
            2400,
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(2400, 1637));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(1520, 1200));
        assert_eq!(geometry.position, PhysicalPosition::new(1360, 286));
    }

    #[test]
    fn portrait_displays_are_limited_by_width_and_include_native_borders() {
        let geometry = startup_geometry(
            &work_area(1920, -200, 1000, 1600),
            PhysicalSize::new(16, 38),
            PhysicalSize::new(760, 600),
            1200,
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(804, 521));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(760, 521));
        assert_eq!(geometry.position, PhysicalPosition::new(2010, 320));
    }

    fn placement(x: i32, y: i32, width: u32, height: u32) -> Placement {
        Placement {
            position: PhysicalPosition::new(x, y),
            size: PhysicalSize::new(width, height),
        }
    }

    #[test]
    fn a_window_returns_while_its_display_is_connected() {
        let primary = work_area(0, 50, 2560, 1550);
        let left = work_area(-1920, 0, 1920, 1040);
        // Mostly past the right edge, but its middle is still on screen.
        assert!(on_a_display(&placement(2000, 400, 1000, 600), &[primary]));
        assert!(on_a_display(
            &placement(-1700, 100, 1200, 800),
            &[primary, left]
        ));
        // The left display was unplugged while the window was released.
        assert!(!on_a_display(&placement(-1700, 100, 1200, 800), &[primary]));
        assert!(!on_a_display(&placement(0, 0, 1200, 800), &[]));
    }

    /// The window `fit_to_monitor` opens on `area` at `scale` with the
    /// configured 760 × 600 minimum, and the minimum height it keeps.
    fn opening(area: &PhysicalRect<i32, u32>, scale: f64) -> (Placement, u32) {
        let minimum = LogicalSize::new(760.0, 600.0).to_physical::<u32>(scale);
        let geometry = startup_geometry(
            area,
            PhysicalSize::new(0, 0),
            minimum,
            (MAX_LOGICAL_WIDTH * scale).floor() as u32,
        )
        .unwrap();
        (
            Placement {
                position: geometry.position,
                size: geometry.inner_size,
            },
            geometry.minimum_size.height,
        )
    }

    #[test]
    fn windows_displays_shrink_to_the_first_two_modules() {
        // 2560 × 1440 at 150% and 1920 × 1080 at 125%, above the taskbar.
        for (area, scale, css, height, x, y) in [
            (work_area(0, 0, 2560, 1368), 1.5, 785.0, 1177, 392, 95),
            (work_area(0, 0, 1920, 1020), 1.25, 700.0, 875, 298, 72),
        ] {
            let (opened, minimum_height) = opening(&area, scale);
            let fitted =
                content_fit_placement(&opened, minimum_height, (css * scale).floor()).unwrap();
            assert_eq!(fitted.size, PhysicalSize::new(opened.size.width, height));
            assert_eq!(fitted.position, PhysicalPosition::new(x, y));
        }
    }

    #[test]
    fn a_page_taller_than_the_window_keeps_the_opening_height() {
        // 1920 × 1080 and 1366 × 768 at 100%, where the first two modules
        // need more than the window opens with.
        for (area, content) in [
            (work_area(0, 0, 1920, 1032), 832.0),
            (work_area(0, 0, 1366, 728), 812.0),
        ] {
            let (opened, minimum_height) = opening(&area, 1.0);
            assert!(content_fit_placement(&opened, minimum_height, content).is_none());
            let exact = f64::from(opened.size.height);
            assert!(content_fit_placement(&opened, minimum_height, exact).is_none());
        }
    }

    #[test]
    fn retina_windows_shrink_around_their_middle() {
        let (opened, minimum_height) = opening(&work_area(0, 66, 3024, 1782), 2.0);
        let fitted = content_fit_placement(&opened, minimum_height, 700.5 * 2.0).unwrap();
        assert_eq!(fitted.size, PhysicalSize::new(2313, 1401));
        assert_eq!(fitted.position, PhysicalPosition::new(355, 256));
    }

    #[test]
    fn a_window_on_a_left_display_keeps_its_column() {
        let fitted = content_fit_placement(&placement(-1618, 40, 1300, 1002), 600, 900.0).unwrap();
        assert_eq!(fitted.size, PhysicalSize::new(1300, 900));
        assert_eq!(fitted.position, PhysicalPosition::new(-1618, 91));
    }

    #[test]
    fn a_short_page_stops_at_the_minimum_height() {
        let fitted = content_fit_placement(&placement(298, 58, 1324, 903), 750, 300.0).unwrap();
        assert_eq!(fitted.size, PhysicalSize::new(1324, 750));
        assert_eq!(fitted.position, PhysicalPosition::new(298, 134));

        // 1920 × 1080 at 150% already opens at the minimum height.
        let (opened, minimum_height) = opening(&work_area(0, 0, 1920, 1008), 1.5);
        assert_eq!(minimum_height, opened.size.height);
        assert!(content_fit_placement(&opened, minimum_height, 840.0).is_none());
    }

    #[test]
    fn rejects_unusable_page_heights() {
        let opened = placement(298, 58, 1324, 903);
        for height in [0.0, -10.0, f64::NAN, f64::INFINITY] {
            assert!(content_fit_placement(&opened, 750, height).is_none());
        }
        assert!(content_fit_placement(&placement(298, 58, 0, 903), 750, 800.0).is_none());
    }

    #[test]
    fn a_window_the_operator_moved_or_resized_is_no_longer_untouched() {
        let placed = placement(298, 58, 1324, 903);
        assert!(untouched(&placed, &placed));
        assert!(untouched(&placed, &placement(300, 56, 1322, 905)));
        assert!(!untouched(&placed, &placement(301, 58, 1324, 903)));
        assert!(!untouched(&placed, &placement(298, 58, 1324, 906)));
        assert!(!untouched(&placed, &placement(298, 58, 1320, 903)));
        // Dragged to the display on the left.
        assert!(!untouched(&placed, &placement(-1622, 58, 1324, 903)));
    }

    #[test]
    fn rejects_unusable_work_areas() {
        for area in [work_area(0, 0, 0, 0), work_area(0, 0, 10, 10)] {
            assert!(startup_geometry(
                &area,
                PhysicalSize::new(16, 38),
                PhysicalSize::new(760, 600),
                1200,
            )
            .is_none());
        }
    }
}
