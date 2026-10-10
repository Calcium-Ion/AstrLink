//! Inference gives way to the user's IDE and coding agents. Only CPU priority
//! drops: background QoS, the Darwin background role and Windows background
//! mode would also throttle I/O and push work onto efficiency cores.

use ort::session::builder::SessionBuilder;

/// Runs first in `main`, before ONNX Runtime starts any thread. Linux keeps
/// nice per thread and a new thread copies its creator's; a Windows priority
/// class covers every thread in the process.
pub fn lower_process() {
    #[cfg(any(target_os = "linux", target_os = "macos"))]
    lower_current_thread();
    #[cfg(target_os = "windows")]
    {
        use windows_sys::Win32::System::Threading::{
            BELOW_NORMAL_PRIORITY_CLASS, GetCurrentProcess, SetPriorityClass,
        };
        // Failure is ignored: scanning at normal priority beats not scanning.
        unsafe {
            SetPriorityClass(GetCurrentProcess(), BELOW_NORMAL_PRIORITY_CLASS);
        }
    }
}

/// macOS starts every new thread at the default QoS class instead of its
/// creator's priority, so each pool thread lowers itself before taking work.
#[cfg(target_os = "macos")]
pub fn lower_pool_threads(builder: SessionBuilder) -> ort::Result<SessionBuilder> {
    builder.with_thread_manager(LoweredThreads)
}

// Pool threads copy the main thread's nice on Linux, and the Windows class
// already covers them.
#[cfg(not(target_os = "macos"))]
pub fn lower_pool_threads(builder: SessionBuilder) -> ort::Result<SessionBuilder> {
    Ok(builder)
}

// macOS ignores nice for threads with a QoS class, which every thread has.
// Relative priority -15 keeps the default class but runs it at 21 instead of
// 31, where nice 10 would put a thread without a class.
#[cfg(target_os = "macos")]
fn lower_current_thread() {
    // Failure is ignored: scanning at normal priority beats not scanning.
    unsafe {
        libc::pthread_set_qos_class_self_np(libc::qos_class_t::QOS_CLASS_DEFAULT, -15);
    }
}

#[cfg(target_os = "linux")]
fn lower_current_thread() {
    // Failure is ignored: scanning at normal priority beats not scanning.
    unsafe {
        libc::setpriority(libc::PRIO_PROCESS, 0, 10);
    }
}

#[cfg(target_os = "macos")]
struct LoweredThreads;

#[cfg(target_os = "macos")]
impl ort::environment::ThreadManager for LoweredThreads {
    type Thread = std::thread::JoinHandle<()>;

    fn create(&self, work: impl FnOnce() + Send + 'static) -> ort::Result<Self::Thread> {
        std::thread::Builder::new()
            .spawn(move || {
                lower_current_thread();
                work();
            })
            .map_err(ort::Error::wrap)
    }

    fn join(thread: Self::Thread) -> ort::Result<()> {
        let _ = thread.join();
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(target_os = "macos")]
    #[test]
    fn lowers_main_and_pool_threads_within_the_default_class() {
        use ort::environment::ThreadManager;

        fn current_class() -> (u32, i32) {
            let mut class = libc::qos_class_t::QOS_CLASS_UNSPECIFIED;
            let mut relative_priority = 0;
            unsafe {
                libc::pthread_get_qos_class_np(
                    libc::pthread_self(),
                    &mut class,
                    &mut relative_priority,
                );
            }
            (class as u32, relative_priority)
        }

        let lowered = (libc::qos_class_t::QOS_CLASS_DEFAULT as u32, -15);
        lower_process();
        assert_eq!(current_class(), lowered);

        let (sender, receiver) = std::sync::mpsc::channel();
        let thread = LoweredThreads
            .create(move || {
                sender
                    .send(current_class())
                    .expect("report pool thread class")
            })
            .expect("spawn pool thread");
        LoweredThreads::join(thread).expect("join pool thread");
        assert_eq!(receiver.recv().expect("pool thread class"), lowered);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn lowers_the_main_thread_and_its_later_threads() {
        fn current_nice() -> i32 {
            unsafe { libc::getpriority(libc::PRIO_PROCESS, 0) }
        }

        lower_process();
        // A runner that is already nicer than 10 keeps its own value.
        assert!(current_nice() >= 10);
        let inherited = std::thread::spawn(current_nice)
            .join()
            .expect("later thread");
        assert!(inherited >= 10);
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn lowers_the_process_class() {
        use windows_sys::Win32::System::Threading::{
            BELOW_NORMAL_PRIORITY_CLASS, GetCurrentProcess, GetPriorityClass,
        };

        lower_process();
        assert_eq!(
            unsafe { GetPriorityClass(GetCurrentProcess()) },
            BELOW_NORMAL_PRIORITY_CLASS
        );
    }
}
