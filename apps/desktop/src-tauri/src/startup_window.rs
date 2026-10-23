use std::sync::{Mutex, MutexGuard};

use tauri::{LogicalSize, Manager, PhysicalPosition, PhysicalRect, PhysicalSize, WebviewWindow};

const WORK_AREA_FRACTION: f64 = 0.82;
const ASPECT_RATIO: f64 = 19.0 / 12.0;
// Keep the opening width while giving lists and editors more vertical room.
// The height stays below 85% of the work area (0.82 × 1.03).
const DEFAULT_HEIGHT_SCALE: f64 = 1.03;
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
    Ok(())
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
        assert_eq!(geometry.inner_size, PhysicalSize::new(2313, 1504));
        assert_eq!(geometry.position, PhysicalPosition::new(355, 205));
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
        assert_eq!(geometry.inner_size, PhysicalSize::new(747, 485));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(747, 485));
        assert_eq!(geometry.position, PhysicalPosition::new(138, 69));
    }

    #[test]
    fn scales_with_the_display_and_centers_on_offset_monitors() {
        for (scale, width, height, x, y) in [
            (1, 1298, 844, -1609, 118),
            (2, 2596, 1688, -3218, 196),
            (3, 3895, 2533, -4828, 273),
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
        assert_eq!(geometry.inner_size, PhysicalSize::new(2400, 1561));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(1520, 1200));
        assert_eq!(geometry.position, PhysicalPosition::new(1360, 324));
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
        assert_eq!(geometry.inner_size, PhysicalSize::new(804, 495));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(760, 495));
        assert_eq!(geometry.position, PhysicalPosition::new(2010, 333));
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
