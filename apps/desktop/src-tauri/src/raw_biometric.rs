//! Touch ID for raw content on signed macOS builds (plan §5.11.9). Core keeps
//! a second envelope of the raw private key, wrapped under 32 random bytes
//! this module keeps in the login keychain. The bytes leave the keychain only
//! after LocalAuthentication confirms the operator's fingerprint in this
//! app's own prompt, and go to Core once, over the operator channel. A
//! same-user agent cannot read the keychain item without the login password,
//! and a Core it starts itself finds only the envelope. The raw password
//! always works beside it.

// The keychain flow runs only on macOS, but its callers build everywhere.
#![cfg_attr(not(target_os = "macos"), allow(dead_code))]

use zeroize::Zeroizing;

use crate::raw_access::BIOMETRIC_KEY_BYTES;

/// Keychain service of every Touch ID key entry; the account is the data
/// directory's, as for the local key.
pub const KEYCHAIN_SERVICE: &str = "com.astrlink.desktop.raw-biometric";

/// What asking for the key ended in.
pub enum Release {
    Key(Zeroizing<Vec<u8>>),
    /// The operator dismissed the prompt, or the system did.
    Cancelled,
    /// No Touch ID on this Mac or build, or no key in the keychain.
    Unavailable,
}

/// Whether Touch ID can stand in for the raw password here: a signed build
/// that keeps keys in the keychain, on a Mac with an enrolled fingerprint.
pub fn available() -> bool {
    enabled() && platform::biometry_available()
}

fn enabled() -> bool {
    #[cfg(target_os = "macos")]
    {
        // The code signature does not change while the app runs.
        static ENABLED: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
        *ENABLED.get_or_init(crate::kek_store::keychain_enabled)
    }
    #[cfg(not(target_os = "macos"))]
    {
        false
    }
}

/// A fresh key for a new envelope.
pub fn new_key() -> Result<Zeroizing<Vec<u8>>, String> {
    let mut key = Zeroizing::new(vec![0_u8; BIOMETRIC_KEY_BYTES]);
    getrandom::getrandom(&mut key)
        .map_err(|error| format!("unable to generate the Touch ID key: {error}"))?;
    Ok(key)
}

/// Stores the key for a data directory, replacing any earlier one.
pub fn store(account: &str, key: &[u8]) -> Result<(), String> {
    if !enabled() {
        return Err("Touch ID is not available in this build".to_string());
    }
    platform::store(account, key)
}

/// Shows the Touch ID prompt with `reason` and, once it is answered, reads
/// the key. Blocks until the operator answers; run it off the async runtime.
pub fn release(account: &str, reason: &str) -> Release {
    if !available() {
        return Release::Unavailable;
    }
    platform::release(account, reason)
}

/// Removes the key, so nothing the keychain holds opens raw content.
pub fn forget(account: &str) {
    if !enabled() {
        return;
    }
    if let Err(error) = platform::forget(account) {
        eprintln!("unable to remove the Touch ID key from the keychain: {error}");
    }
}

#[cfg(target_os = "macos")]
mod platform {
    use std::sync::mpsc;
    use std::time::Duration;

    use block2::RcBlock;
    use objc2::runtime::Bool;
    use objc2_foundation::{NSError, NSString};
    use objc2_local_authentication::{LAContext, LAError, LAPolicy};
    use zeroize::Zeroizing;

    use super::{Release, KEYCHAIN_SERVICE};

    /// A fingerprint only: the raw password is the fallback, not the login
    /// password.
    const POLICY: LAPolicy = LAPolicy::DeviceOwnerAuthenticationWithBiometrics;
    /// An unanswered prompt is withdrawn so the dialog recovers.
    const WAIT: Duration = Duration::from_secs(120);
    /// errSecItemNotFound
    const ITEM_NOT_FOUND: i32 = -25300;

    pub fn biometry_available() -> bool {
        // SAFETY: a fresh context is only queried on this thread.
        unsafe { LAContext::new().canEvaluatePolicy_error(POLICY) }.is_ok()
    }

    pub fn store(account: &str, key: &[u8]) -> Result<(), String> {
        security_framework::passwords::set_generic_password(KEYCHAIN_SERVICE, account, key).map_err(
            |error| {
                format!(
                    "unable to keep the Touch ID key in the keychain: OSStatus {}",
                    error.code()
                )
            },
        )
    }

    pub fn forget(account: &str) -> Result<(), String> {
        match security_framework::passwords::delete_generic_password(KEYCHAIN_SERVICE, account) {
            Ok(()) => Ok(()),
            Err(error) if error.code() == ITEM_NOT_FOUND => Ok(()),
            Err(error) => Err(format!("OSStatus {}", error.code())),
        }
    }

    pub fn release(account: &str, reason: &str) -> Release {
        if let Err(refused) = evaluate(reason) {
            return refused;
        }
        use security_framework::passwords::{generic_password, PasswordOptions};
        match generic_password(PasswordOptions::new_generic_password(
            KEYCHAIN_SERVICE,
            account,
        )) {
            Ok(key) if key.len() == super::BIOMETRIC_KEY_BYTES => Release::Key(Zeroizing::new(key)),
            Ok(mut key) => {
                zeroize::Zeroize::zeroize(&mut key);
                eprintln!("the Touch ID key in the keychain has the wrong length");
                Release::Unavailable
            }
            Err(error) if error.code() == ITEM_NOT_FOUND => Release::Unavailable,
            Err(error) => {
                eprintln!(
                    "unable to read the Touch ID key from the keychain: OSStatus {}",
                    error.code()
                );
                Release::Unavailable
            }
        }
    }

    /// Runs the prompt; Ok once the fingerprint matched.
    fn evaluate(reason: &str) -> Result<(), Release> {
        // SAFETY: the context outlives the evaluation: it is kept until the
        // reply arrives or it is invalidated below.
        let context = unsafe { LAContext::new() };
        if unsafe { context.canEvaluatePolicy_error(POLICY) }.is_err() {
            return Err(Release::Unavailable);
        }
        let (sender, receiver) = mpsc::sync_channel::<Result<(), isize>>(1);
        let reply = RcBlock::new(move |success: Bool, error: *mut NSError| {
            let outcome = if success.as_bool() {
                Ok(())
            } else {
                // SAFETY: LocalAuthentication passes nil or a valid error.
                Err(unsafe { error.as_ref() }.map_or(0, NSError::code))
            };
            let _ = sender.try_send(outcome);
        });
        let reason = NSString::from_str(reason);
        // SAFETY: the reply only moves a Send channel end, and the prompt
        // reason is a non-empty string.
        unsafe { context.evaluatePolicy_localizedReason_reply(POLICY, &reason, &reply) };
        match receiver.recv_timeout(WAIT) {
            Ok(Ok(())) => Ok(()),
            Ok(Err(code)) => Err(classify(code)),
            Err(_) => {
                // SAFETY: invalidating ends the pending prompt.
                unsafe { context.invalidate() };
                Err(Release::Cancelled)
            }
        }
    }

    fn classify(code: isize) -> Release {
        let unavailable = [
            LAError::BiometryNotAvailable,
            LAError::BiometryNotEnrolled,
            LAError::BiometryLockout,
            LAError::NotInteractive,
            LAError::InvalidContext,
        ];
        if unavailable.iter().any(|error| error.0 == code) {
            Release::Unavailable
        } else {
            Release::Cancelled
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn failures_split_into_cancelled_and_unavailable() {
            assert!(matches!(
                classify(LAError::UserCancel.0),
                Release::Cancelled
            ));
            assert!(matches!(
                classify(LAError::SystemCancel.0),
                Release::Cancelled
            ));
            assert!(matches!(
                classify(LAError::UserFallback.0),
                Release::Cancelled
            ));
            assert!(matches!(
                classify(LAError::AuthenticationFailed.0),
                Release::Cancelled
            ));
            assert!(matches!(
                classify(LAError::BiometryNotEnrolled.0),
                Release::Unavailable
            ));
            assert!(matches!(
                classify(LAError::BiometryLockout.0),
                Release::Unavailable
            ));
        }
    }
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use super::Release;

    pub fn biometry_available() -> bool {
        false
    }

    pub fn store(_account: &str, _key: &[u8]) -> Result<(), String> {
        Err("Touch ID is only available on macOS".to_string())
    }

    pub fn forget(_account: &str) -> Result<(), String> {
        Ok(())
    }

    pub fn release(_account: &str, _reason: &str) -> Release {
        Release::Unavailable
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn debug_builds_never_offer_touch_id() {
        // Tests are debug builds, which keep the local key in a file.
        assert!(!available());
        assert!(matches!(release("account", "unlock"), Release::Unavailable));
        assert!(store("account", &[0; BIOMETRIC_KEY_BYTES]).is_err());
    }

    #[test]
    fn new_keys_are_random_and_sized_for_core() {
        let first = new_key().unwrap();
        let second = new_key().unwrap();
        assert_eq!(first.len(), BIOMETRIC_KEY_BYTES);
        assert_ne!(*first, *second);
    }
}
