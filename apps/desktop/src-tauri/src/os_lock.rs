//! Locks raw content when the operator's session does (plan §5.11.6). The
//! raw unlock and every agent grant end when the screen locks, the computer
//! sleeps, or the user switches away; hiding the window to the tray or
//! minimizing it locks nothing. Quitting ends Core and both with it.
//!
//! Core keeps an unlock open until this lock where the host can see the
//! session lock: macOS and Windows start it with `--raw-unlock-until-lock`.
//! Linux follows logind where it runs, and Core keeps its idle timeout there
//! too, since a desktop without logind sends no signal at all.

use std::sync::Arc;

use tauri::{AppHandle, Manager};

use crate::sidecar::{CoreManager, CorePhase};

/// Whether this platform reports the session locking reliably enough for
/// Core to drop its idle timeout.
pub const FOLLOWS_SESSION: bool = cfg!(any(target_os = "macos", windows));

/// Starts following the session. Call once, from the main thread: macOS and
/// Linux deliver the notifications through its run loop.
pub fn watch(app: &AppHandle) {
    platform::watch(app.clone());
}

/// Ends the unlock and every agent grant, as the operator's own Lock does.
fn lock(app: &AppHandle, reason: &'static str) {
    let Some(manager) = app.try_state::<Arc<CoreManager>>() else {
        return;
    };
    if manager.view().phase != CorePhase::Ready {
        return;
    }
    let manager = Arc::clone(manager.inner());
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        let result = manager.lock_raw().await;
        if let Err(error) = &result {
            eprintln!("unable to lock raw content on {reason}: {error}");
        }
        let _ = crate::broadcast_raw_sealing_change(&app, result);
    });
}

#[cfg(target_os = "macos")]
mod platform {
    use std::ptr::NonNull;

    use block2::RcBlock;
    use objc2_app_kit::{
        NSWorkspace, NSWorkspaceSessionDidResignActiveNotification,
        NSWorkspaceWillSleepNotification,
    };
    use objc2_foundation::{NSDistributedNotificationCenter, NSNotification, NSString};

    /// Posted when the screen locks, whatever locked it.
    const SCREEN_LOCKED: &str = "com.apple.screenIsLocked";

    pub fn watch(app: tauri::AppHandle) {
        let observe = |center: &objc2_foundation::NSNotificationCenter,
                       name: &objc2_foundation::NSNotificationName,
                       reason: &'static str| {
            let app = app.clone();
            let block = RcBlock::new(move |_: NonNull<NSNotification>| super::lock(&app, reason));
            // SAFETY: no object filter and no queue, so the block runs on the
            // posting thread; it only clones an AppHandle, which is Send and
            // Sync. The observer stays for the life of the app, so its token
            // is never removed.
            let token = unsafe {
                center.addObserverForName_object_queue_usingBlock(Some(name), None, None, &block)
            };
            std::mem::forget(token);
        };
        let workspace = NSWorkspace::sharedWorkspace().notificationCenter();
        // SAFETY: AppKit's notification names are immutable statics.
        let (sleep, switched) = unsafe {
            (
                NSWorkspaceWillSleepNotification,
                NSWorkspaceSessionDidResignActiveNotification,
            )
        };
        observe(&workspace, sleep, "sleep");
        observe(&workspace, switched, "a user switch");
        let distributed = NSDistributedNotificationCenter::defaultCenter();
        observe(
            &distributed,
            &NSString::from_str(SCREEN_LOCKED),
            "screen lock",
        );
    }
}

#[cfg(windows)]
mod platform {
    use std::sync::OnceLock;

    use windows_sys::Win32::{
        Foundation::{HWND, LPARAM, LRESULT, WPARAM},
        System::{
            LibraryLoader::GetModuleHandleW,
            RemoteDesktop::{WTSRegisterSessionNotification, NOTIFY_FOR_THIS_SESSION},
        },
        UI::WindowsAndMessaging::{
            CreateWindowExW, DefWindowProcW, DispatchMessageW, GetMessageW, RegisterClassW,
            TranslateMessage, MSG, PBT_APMSUSPEND, WM_POWERBROADCAST, WM_WTSSESSION_CHANGE,
            WNDCLASSW, WTS_SESSION_LOCK,
        },
    };

    static APP: OnceLock<tauri::AppHandle> = OnceLock::new();

    pub fn watch(app: tauri::AppHandle) {
        if APP.set(app).is_err() {
            return;
        }
        let spawned = std::thread::Builder::new()
            .name("astrlink-session-lock".into())
            .spawn(|| {
                if let Err(error) = run() {
                    eprintln!("unable to follow the Windows session lock: {error}");
                }
            });
        if let Err(error) = spawned {
            eprintln!("unable to follow the Windows session lock: {error}");
        }
    }

    fn wide(text: &str) -> Vec<u16> {
        text.encode_utf16().chain(std::iter::once(0)).collect()
    }

    /// A top-level window that is never shown: the session notifications go
    /// to the window registered for them, and the power broadcast reaches
    /// top-level windows only, which a message-only window is not.
    fn run() -> Result<(), String> {
        let class_name = wide("AstrLinkSessionLock");
        // SAFETY: plain Win32 calls with valid, NUL-terminated strings; the
        // window and its message loop live on this thread until it ends.
        unsafe {
            let instance = GetModuleHandleW(std::ptr::null());
            let class = WNDCLASSW {
                lpfnWndProc: Some(window_proc),
                hInstance: instance,
                lpszClassName: class_name.as_ptr(),
                ..std::mem::zeroed()
            };
            if RegisterClassW(&class) == 0 {
                return Err("the window class could not be registered".to_string());
            }
            let window = CreateWindowExW(
                0,
                class_name.as_ptr(),
                class_name.as_ptr(),
                0,
                0,
                0,
                0,
                0,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                instance,
                std::ptr::null(),
            );
            if window.is_null() {
                return Err("the window could not be created".to_string());
            }
            if WTSRegisterSessionNotification(window, NOTIFY_FOR_THIS_SESSION) == 0 {
                eprintln!(
                    "unable to follow the Windows session lock; sleep still locks raw content"
                );
            }
            let mut message: MSG = std::mem::zeroed();
            while GetMessageW(&mut message, std::ptr::null_mut(), 0, 0) > 0 {
                TranslateMessage(&message);
                DispatchMessageW(&message);
            }
        }
        Ok(())
    }

    unsafe extern "system" fn window_proc(
        window: HWND,
        message: u32,
        wparam: WPARAM,
        lparam: LPARAM,
    ) -> LRESULT {
        let reason = match (message, wparam as u32) {
            (WM_WTSSESSION_CHANGE, WTS_SESSION_LOCK) => Some("screen lock"),
            (WM_POWERBROADCAST, PBT_APMSUSPEND) => Some("sleep"),
            _ => None,
        };
        if let (Some(reason), Some(app)) = (reason, APP.get()) {
            super::lock(app, reason);
        }
        // The arguments are the ones Windows passed in.
        DefWindowProcW(window, message, wparam, lparam)
    }
}

#[cfg(target_os = "linux")]
mod platform {
    use gtk::{gio, glib, glib::ToVariant};

    const LOGIND: &str = "org.freedesktop.login1";
    const MANAGER_PATH: &str = "/org/freedesktop/login1";
    const MANAGER: &str = "org.freedesktop.login1.Manager";
    const SESSION: &str = "org.freedesktop.login1.Session";

    pub fn watch(app: tauri::AppHandle) {
        let connection = match gio::bus_get_sync(gio::BusType::System, gio::Cancellable::NONE) {
            Ok(connection) => connection,
            Err(error) => {
                eprintln!(
                    "logind is unavailable, so raw content locks after idle time only: {error}"
                );
                return;
            }
        };
        let sleeping = app.clone();
        connection.signal_subscribe(
            Some(LOGIND),
            Some(MANAGER),
            Some("PrepareForSleep"),
            Some(MANAGER_PATH),
            None,
            gio::DBusSignalFlags::NONE,
            move |_, _, _, _, _, parameters| {
                // true comes before sleep, false after waking.
                if parameters.child_value(0).get::<bool>() == Some(true) {
                    super::lock(&sleeping, "sleep");
                }
            },
        );
        // Without our own session, any session's lock locks: a spare lock is
        // the safe way to be wrong.
        let session = own_session(&connection);
        let locking = app.clone();
        connection.signal_subscribe(
            Some(LOGIND),
            Some(SESSION),
            Some("Lock"),
            session.as_deref(),
            None,
            gio::DBusSignalFlags::NONE,
            move |_, _, _, _, _, _| super::lock(&locking, "screen lock"),
        );
        // Desktops that lock the screen themselves report it as LockedHint.
        connection.signal_subscribe(
            Some(LOGIND),
            Some("org.freedesktop.DBus.Properties"),
            Some("PropertiesChanged"),
            session.as_deref(),
            Some(SESSION),
            gio::DBusSignalFlags::NONE,
            move |_, _, _, _, _, parameters| {
                let changed = glib::VariantDict::new(Some(&parameters.child_value(1)));
                if matches!(changed.lookup::<bool>("LockedHint"), Ok(Some(true))) {
                    super::lock(&app, "screen lock");
                }
            },
        );
        // The subscriptions live as long as the connection; keep it for the
        // life of the app.
        std::mem::forget(connection);
    }

    fn own_session(connection: &gio::DBusConnection) -> Option<String> {
        let reply = connection
            .call_sync(
                Some(LOGIND),
                MANAGER_PATH,
                MANAGER,
                "GetSessionByPID",
                Some(&(std::process::id(),).to_variant()),
                None,
                gio::DBusCallFlags::NONE,
                2_000,
                gio::Cancellable::NONE,
            )
            .map_err(|error| eprintln!("unable to find this logind session: {error}"))
            .ok()?;
        reply.child_value(0).str().map(str::to_string)
    }
}

#[cfg(not(any(target_os = "macos", windows, target_os = "linux")))]
mod platform {
    pub fn watch(_app: tauri::AppHandle) {}
}
