use tauri::{LogicalSize, Manager, PhysicalPosition, PhysicalRect, PhysicalSize, WebviewWindow};

const WORK_AREA_FRACTION: f64 = 0.85;
const ASPECT_RATIO: f64 = 16.0 / 10.0;

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
) -> Option<StartupGeometry> {
    // Fit the entire window, including native decorations, inside the usable
    // desktop. Physical coordinates preserve placement on mixed-DPI monitors.
    let width = (f64::from(work_area.size.width) * WORK_AREA_FRACTION)
        .min(f64::from(work_area.size.height) * WORK_AREA_FRACTION * ASPECT_RATIO)
        .floor() as u32;
    let height = (f64::from(width) / ASPECT_RATIO).floor() as u32;
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

/// Called once during setup, before the hidden main window is shown. Tray
/// restores and frontend reloads must preserve the user's resized window.
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
    let minimum_size = LogicalSize::new(
        config.min_width.unwrap_or(0.0),
        config.min_height.unwrap_or(0.0),
    )
    .to_physical(window.scale_factor()?);
    let outer_size = window.outer_size()?;
    let inner_size = window.inner_size()?;
    let frame = PhysicalSize::new(
        outer_size.width.saturating_sub(inner_size.width),
        outer_size.height.saturating_sub(inner_size.height),
    );
    let geometry = startup_geometry(monitor.work_area(), frame, minimum_size)
        .ok_or("monitor work area is too small for the main window")?;

    window.set_min_size(Some(geometry.minimum_size))?;
    window.set_size(geometry.inner_size)?;
    window.set_position(geometry.position)?;
    Ok(())
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
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(2423, 1514));
        assert_eq!(geometry.position, PhysicalPosition::new(300, 200));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(1520, 1200));
    }

    #[test]
    fn small_displays_can_start_below_the_normal_minimum_height() {
        let geometry = startup_geometry(
            &work_area(0, 24, 1280, 696),
            PhysicalSize::new(0, 0),
            PhysicalSize::new(760, 600),
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(946, 591));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(760, 591));
        assert_eq!(geometry.position, PhysicalPosition::new(167, 76));
    }

    #[test]
    fn scales_with_the_display_and_centers_on_offset_monitors() {
        for scale in [1, 2, 3] {
            let area = work_area(-1920 * scale as i32, 40, 1920 * scale, 1000 * scale);
            let geometry = startup_geometry(
                &area,
                PhysicalSize::new(0, 0),
                PhysicalSize::new(760 * scale, 600 * scale),
            )
            .unwrap();
            assert_eq!(
                geometry.inner_size,
                PhysicalSize::new(1360 * scale, 850 * scale)
            );
            assert_eq!(
                geometry.position,
                PhysicalPosition::new(-1640 * scale as i32, 40 + 75 * scale as i32)
            );
        }
    }

    #[test]
    fn portrait_displays_are_limited_by_width_and_include_native_borders() {
        let geometry = startup_geometry(
            &work_area(1920, -200, 1000, 1600),
            PhysicalSize::new(16, 38),
            PhysicalSize::new(760, 600),
        )
        .unwrap();
        assert_eq!(geometry.inner_size, PhysicalSize::new(834, 493));
        assert_eq!(geometry.minimum_size, PhysicalSize::new(760, 493));
        assert_eq!(geometry.position, PhysicalPosition::new(1995, 334));
    }

    #[test]
    fn rejects_unusable_work_areas() {
        for area in [work_area(0, 0, 0, 0), work_area(0, 0, 10, 10)] {
            assert!(startup_geometry(
                &area,
                PhysicalSize::new(16, 38),
                PhysicalSize::new(760, 600),
            )
            .is_none());
        }
    }
}
