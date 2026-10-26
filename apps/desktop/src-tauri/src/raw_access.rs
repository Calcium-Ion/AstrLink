//! Desktop side of raw audit content (plan §5.11.6, §5.11.9). The operator
//! decides an agent's pending request in the approval window, revokes the
//! lasting grants still running, unlocks raw parts for their own reading, and
//! manages the raw password and Touch ID here. Every proof is forwarded once
//! and never kept.

use std::time::Duration;

use base64::{engine::general_purpose::STANDARD, Engine};
use reqwest::{Method, StatusCode};
use serde::{Deserialize, Serialize};
use zeroize::Zeroizing;

pub const RAW_ACCESS_PATH: &str = "/control/v1/audit/raw-access";
pub const RAW_SEALING_PATH: &str = "/control/v1/audit/raw-sealing";
pub const RAW_PASSWORD_PATH: &str = "/control/v1/audit/raw-password";
pub const RAW_UNLOCK_PATH: &str = "/control/v1/audit/raw-unlock";
pub const RAW_LOCK_PATH: &str = "/control/v1/audit/raw-lock";
/// Checks a proof without starting or touching Core's unlock session.
pub const RAW_VERIFY_PATH: &str = "/control/v1/audit/raw-verify";
/// Adds (POST) or takes out (DELETE) the envelope the Touch ID key opens.
pub const RAW_BIOMETRIC_PATH: &str = "/control/v1/audit/raw-biometric";
/// The size of the key the keychain keeps behind Touch ID, as Core expects.
pub const BIOMETRIC_KEY_BYTES: usize = 32;
/// Proof checks run a memory-hard KDF in Core; a slow machine needs longer
/// than an ordinary control call.
pub const PROOF_TIMEOUT: Duration = Duration::from_secs(20);
/// Core refuses longer raw passwords, so one cannot be correct.
const MAX_PASSWORD_CHARS: usize = 128;
const GRANT_PREFIX: &str = "rawgrant_";

/// What a proof-carrying call ended in. Refusals the dialog can recover from
/// are outcomes rather than errors, so it stays open for another attempt.
#[derive(Debug, PartialEq, Serialize)]
#[serde(tag = "outcome", rename_all = "snake_case")]
pub enum ProofOutcome {
    Decided {
        grant: serde_json::Value,
    },
    /// An unlock or password action succeeded; `status` is the new raw
    /// sealing state.
    Sealing {
        status: serde_json::Value,
    },
    /// The call carried no proof Core accepts there.
    ProofRequired,
    PasswordInvalid,
    Backoff {
        retry_after_seconds: u64,
    },
    NotPending,
    /// The Touch ID prompt was dismissed.
    BiometricCancelled,
    /// Touch ID cannot stand in for the password right now: none on this
    /// Mac, none set up for raw content, or its key no longer opens it.
    BiometricUnavailable,
}

/// The proof the dialog collected. The password is wiped when this drops;
/// `biometric` asks the host for Touch ID, so no key passes the webview.
#[derive(Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ProofArg {
    Password { password: Zeroizing<String> },
    Biometric {},
}

/// A proof ready for Core. Its secret is wiped when this drops.
pub enum Proof {
    Password(Zeroizing<String>),
    /// The key the keychain released after Touch ID.
    Biometric(Zeroizing<Vec<u8>>),
}

pub fn validate_grant_id(id: &str) -> Result<(), String> {
    match id.strip_prefix(GRANT_PREFIX) {
        Some(hex)
            if hex.len() == 16
                && hex
                    .bytes()
                    .all(|byte| matches!(byte, b'0'..=b'9' | b'a'..=b'f')) =>
        {
            Ok(())
        }
        _ => Err("raw access grant id is invalid".to_string()),
    }
}

pub fn decision_path(grant_id: &str) -> String {
    format!("{RAW_ACCESS_PATH}/{grant_id}/decision")
}

/// DELETE revokes one running lasting grant.
pub fn grant_path(grant_id: &str) -> Result<String, String> {
    validate_grant_id(grant_id)?;
    Ok(format!("{RAW_ACCESS_PATH}/{grant_id}"))
}

/// Proof-carrying calls are sent once: a transport retry could spend a
/// second password attempt against the backoff. They also run a
/// memory-hard KDF, so they get the longer timeout.
pub fn is_proof_request(method: &Method, path: &str) -> bool {
    *method == Method::POST
        && (path == RAW_PASSWORD_PATH
            || path == RAW_UNLOCK_PATH
            || path == RAW_VERIFY_PATH
            || path == RAW_BIOMETRIC_PATH
            || path
                .strip_prefix(RAW_ACCESS_PATH)
                .and_then(|rest| rest.strip_prefix('/'))
                .is_some_and(|rest| rest.ends_with("/decision")))
}

#[derive(Serialize)]
#[serde(untagged)]
enum ProofBody<'a> {
    Password { password: &'a str },
    Biometric { biometric: Zeroizing<String> },
}

impl Proof {
    fn body(&self) -> Result<ProofBody<'_>, String> {
        match self {
            Proof::Password(password) => Ok(ProofBody::Password {
                password: checked_password(password)?,
            }),
            Proof::Biometric(key) => Ok(ProofBody::Biometric {
                biometric: encode_key(key)?,
            }),
        }
    }

    /// Everything the body carries besides fixed text.
    fn secret_len(&self) -> usize {
        match self {
            Proof::Password(password) => password.len(),
            Proof::Biometric(key) => key.len().div_ceil(3) * 4,
        }
    }
}

/// Encodes a Touch ID key as Core reads it, into a buffer sized up front.
fn encode_key(key: &[u8]) -> Result<Zeroizing<String>, String> {
    if key.len() != BIOMETRIC_KEY_BYTES {
        return Err("the Touch ID key is invalid".to_string());
    }
    let mut encoded = Zeroizing::new(String::with_capacity(key.len().div_ceil(3) * 4));
    STANDARD.encode_string(key, &mut encoded);
    Ok(encoded)
}

fn checked_password(password: &str) -> Result<&str, String> {
    if password.is_empty() {
        return Err("the raw password is required".to_string());
    }
    if password.chars().count() > MAX_PASSWORD_CHARS {
        return Err("raw password is incorrect".to_string());
    }
    Ok(password)
}

#[derive(Serialize)]
struct DecisionBody<'a> {
    decision: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    proof: Option<ProofBody<'a>>,
}

/// Encodes one decision. A denial never carries a proof; Core refuses an
/// approval without one, unlocked or not.
pub fn decision_body(
    grant_id: &str,
    decision: &str,
    proof: Option<&Proof>,
) -> Result<Zeroizing<Vec<u8>>, String> {
    validate_grant_id(grant_id)?;
    let proof = match decision {
        "deny" => None,
        "once" | "until_lock" | "window_1h" => proof,
        _ => {
            return Err(
                "raw access decision must be once, until_lock, window_1h, or deny".to_string(),
            )
        }
    };
    let secret_len = proof.map_or(0, Proof::secret_len);
    let proof = proof.map(Proof::body).transpose()?;
    encode_secret_json(&DecisionBody { decision, proof }, secret_len)
}

#[derive(Serialize)]
struct UnlockBody<'a> {
    proof: ProofBody<'a>,
}

pub fn unlock_body(proof: &Proof) -> Result<Zeroizing<Vec<u8>>, String> {
    encode_secret_json(
        &UnlockBody {
            proof: proof.body()?,
        },
        proof.secret_len(),
    )
}

#[derive(Serialize)]
struct BiometricBody<'a> {
    proof: ProofBody<'a>,
    key: Zeroizing<String>,
}

/// Encodes a Touch ID enrollment: the raw password opens the key, which is
/// then wrapped under `key`.
pub fn biometric_body(password: &str, key: &[u8]) -> Result<Zeroizing<Vec<u8>>, String> {
    let proof = ProofBody::Password {
        password: checked_password(password)?,
    };
    encode_secret_json(
        &BiometricBody {
            proof,
            key: encode_key(key)?,
        },
        password.len() + key.len().div_ceil(3) * 4,
    )
}

#[derive(Serialize)]
struct PasswordBody<'a> {
    action: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    password: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    proof: Option<ProofBody<'a>>,
}

/// Encodes a raw password action. `password` is the new password; `proof`
/// opens the existing key where Core needs it.
pub fn password_body(
    action: &str,
    password: Option<&str>,
    proof: Option<&Proof>,
) -> Result<Zeroizing<Vec<u8>>, String> {
    if !matches!(action, "set" | "change" | "reset") {
        return Err("raw password action must be set, change, or reset".to_string());
    }
    let password = password.filter(|password| !password.is_empty());
    if password.is_some_and(|password| password.chars().count() > MAX_PASSWORD_CHARS) {
        return Err(format!(
            "the raw password must contain at most {MAX_PASSWORD_CHARS} characters"
        ));
    }
    let secret_len = password.map_or(0, str::len) + proof.map_or(0, Proof::secret_len);
    let proof = proof.map(Proof::body).transpose()?;
    encode_secret_json(
        &PasswordBody {
            action,
            password,
            proof,
        },
        secret_len,
    )
}

/// Serialises into a buffer sized for the worst-case escaping up front, so
/// the encoder never reallocates and leaves a stray copy of the secret.
fn encode_secret_json(
    value: &impl Serialize,
    secret_len: usize,
) -> Result<Zeroizing<Vec<u8>>, String> {
    let mut buffer = Zeroizing::new(Vec::with_capacity(secret_len * 6 + 256));
    serde_json::to_writer(&mut *buffer, value)
        .map_err(|error| format!("unable to encode control request: {error}"))?;
    Ok(buffer)
}

fn error_code(body: &[u8]) -> Option<(String, Option<u64>)> {
    let value: serde_json::Value = serde_json::from_slice(body).ok()?;
    let error = value.get("error")?;
    let code = error.get("code")?.as_str()?.to_string();
    let retry_after = error
        .get("details")
        .and_then(serde_json::Value::as_array)
        .and_then(|details| {
            details
                .iter()
                .find_map(|detail| detail.get("retry_after_seconds")?.as_u64())
        });
    Some((code, retry_after))
}

/// The proof refusals shared by every proof-carrying call.
fn proof_refusal(status: StatusCode, body: &[u8]) -> Option<ProofOutcome> {
    let (code, retry_after) = error_code(body)?;
    match (status, code.as_str()) {
        (StatusCode::FORBIDDEN, "raw_password_invalid") => Some(ProofOutcome::PasswordInvalid),
        (StatusCode::TOO_MANY_REQUESTS, "raw_password_backoff") => Some(ProofOutcome::Backoff {
            retry_after_seconds: retry_after.unwrap_or(1).max(1),
        }),
        (StatusCode::FORBIDDEN, "raw_biometric_invalid")
        | (StatusCode::CONFLICT, "raw_biometric_unavailable") => {
            Some(ProofOutcome::BiometricUnavailable)
        }
        _ => None,
    }
}

/// Maps a decision response onto an outcome, or None for a failure the
/// caller reports as an error.
pub fn decision_outcome(status: StatusCode, body: &[u8]) -> Option<ProofOutcome> {
    if status == StatusCode::OK {
        let grant: serde_json::Value = serde_json::from_slice(body).ok()?;
        grant.get("grant_id")?.as_str()?;
        return Some(ProofOutcome::Decided { grant });
    }
    if let Some(refusal) = proof_refusal(status, body) {
        return Some(refusal);
    }
    let (code, _) = error_code(body)?;
    match (status, code.as_str()) {
        (StatusCode::CONFLICT, "raw_access_not_pending") | (StatusCode::NOT_FOUND, "not_found") => {
            Some(ProofOutcome::NotPending)
        }
        (StatusCode::UNPROCESSABLE_ENTITY, "raw_proof_required") => {
            Some(ProofOutcome::ProofRequired)
        }
        _ => None,
    }
}

/// Maps an unlock or password response onto an outcome, or None for a
/// failure the caller reports as an error.
pub fn sealing_outcome(status: StatusCode, body: &[u8]) -> Option<ProofOutcome> {
    if status == StatusCode::OK {
        return Some(ProofOutcome::Sealing {
            status: parse_sealing_status(body).ok()?,
        });
    }
    proof_refusal(status, body)
}

pub fn parse_sealing_status(body: &[u8]) -> Result<serde_json::Value, String> {
    let value: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("raw sealing state returned invalid JSON: {error}"))?;
    if !value
        .get("configured")
        .is_some_and(serde_json::Value::is_boolean)
    {
        return Err("raw sealing state omitted configured".to_string());
    }
    Ok(value)
}

/// The pending requests and the lasting grants still running.
pub fn parse_list(body: &[u8]) -> Result<serde_json::Value, String> {
    let value: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("raw access list returned invalid JSON: {error}"))?;
    for key in ["items", "active"] {
        if !value.get(key).is_some_and(serde_json::Value::is_array) {
            return Err(format!("raw access list omitted {key}"));
        }
    }
    Ok(value)
}

#[cfg(test)]
mod tests {
    use super::*;

    const GRANT: &str = "rawgrant_0123456789abcdef";

    #[test]
    fn grant_ids_must_match_the_core_format() {
        assert!(validate_grant_id(GRANT).is_ok());
        for invalid in [
            "",
            "rawgrant_",
            "rawgrant_0123456789ABCDEF",
            "rawgrant_0123456789abcde",
            "rawgrant_0123456789abcdef0",
            "rawgrant_0123456789abcdef/../x",
            "grant_0123456789abcdef",
        ] {
            assert!(validate_grant_id(invalid).is_err(), "{invalid}");
        }
    }

    fn password(value: &str) -> Proof {
        Proof::Password(Zeroizing::new(value.to_string()))
    }

    fn json(body: &[u8]) -> serde_json::Value {
        serde_json::from_slice(body).unwrap()
    }

    #[test]
    fn decisions_carry_a_proof_only_when_approving() {
        let body = decision_body(GRANT, "once", Some(&password("pa\"ss"))).unwrap();
        assert_eq!(
            json(&body),
            serde_json::json!({"decision":"once","proof":{"password":"pa\"ss"}})
        );
        let body = decision_body(GRANT, "window_1h", Some(&password("secret"))).unwrap();
        assert_eq!(
            json(&body),
            serde_json::json!({"decision":"window_1h","proof":{"password":"secret"}})
        );
        let body = decision_body(GRANT, "deny", Some(&password("ignored"))).unwrap();
        assert_eq!(json(&body), serde_json::json!({"decision":"deny"}));
        let key = Proof::Biometric(Zeroizing::new(vec![0xfb; BIOMETRIC_KEY_BYTES]));
        let body = decision_body(GRANT, "until_lock", Some(&key)).unwrap();
        assert_eq!(
            json(&body),
            serde_json::json!({"decision":"until_lock","proof":{"biometric":STANDARD.encode([0xfb; BIOMETRIC_KEY_BYTES])}})
        );

        assert!(decision_body(GRANT, "window_5m", Some(&password("secret"))).is_err());
        assert!(decision_body(GRANT, "until_lock", Some(&password(""))).is_err());
        let short = Proof::Biometric(Zeroizing::new(vec![0; 16]));
        assert!(decision_body(GRANT, "once", Some(&short)).is_err());
        assert!(decision_body(GRANT, "always", Some(&password("secret"))).is_err());
        assert!(decision_body("rawgrant_bad", "deny", None).is_err());
        let long = "x".repeat(MAX_PASSWORD_CHARS + 1);
        assert!(decision_body(GRANT, "once", Some(&password(&long))).is_err());
    }

    #[test]
    fn unlock_and_password_bodies_match_the_contract() {
        assert_eq!(
            json(&unlock_body(&password("open sesame")).unwrap()),
            serde_json::json!({"proof":{"password":"open sesame"}})
        );
        assert!(unlock_body(&password("")).is_err());
        let key = Proof::Biometric(Zeroizing::new(vec![7; BIOMETRIC_KEY_BYTES]));
        assert_eq!(
            json(&unlock_body(&key).unwrap()),
            serde_json::json!({"proof":{"biometric":STANDARD.encode([7; BIOMETRIC_KEY_BYTES])}})
        );
        assert_eq!(
            json(&biometric_body("open sesame", &[9; BIOMETRIC_KEY_BYTES]).unwrap()),
            serde_json::json!({"proof":{"password":"open sesame"},"key":STANDARD.encode([9; BIOMETRIC_KEY_BYTES])})
        );
        assert!(biometric_body("", &[9; BIOMETRIC_KEY_BYTES]).is_err());
        assert!(biometric_body("open sesame", &[9; 31]).is_err());

        assert_eq!(
            json(&password_body("set", Some("new phrase"), None).unwrap()),
            serde_json::json!({"action":"set","password":"new phrase"})
        );
        assert_eq!(
            json(
                &password_body("change", Some("new phrase"), Some(&password("old phrase")))
                    .unwrap()
            ),
            serde_json::json!({"action":"change","password":"new phrase","proof":{"password":"old phrase"}})
        );
        assert_eq!(
            json(&password_body("reset", Some(""), None).unwrap()),
            serde_json::json!({"action":"reset"})
        );
        assert!(password_body("rotate", Some("new phrase"), None).is_err());
        let long = "x".repeat(MAX_PASSWORD_CHARS + 1);
        assert!(password_body("set", Some(&long), None).is_err());
    }

    #[test]
    fn secret_encoding_never_reallocates() {
        // Every byte of a control-character password escapes to six bytes.
        let secret = "\u{1}".repeat(MAX_PASSWORD_CHARS);
        let body = decision_body(GRANT, "once", Some(&password(&secret))).unwrap();
        assert!(body.len() <= secret.len() * 6 + 256);
        assert_eq!(body.capacity(), secret.len() * 6 + 256);
        let body = password_body("change", Some(&secret), Some(&password(&secret))).unwrap();
        assert_eq!(body.capacity(), secret.len() * 2 * 6 + 256);
    }

    #[test]
    fn proof_arguments_take_a_password_or_ask_for_touch_id() {
        let arg: ProofArg =
            serde_json::from_str(r#"{"kind":"password","password":"open sesame"}"#).unwrap();
        assert!(
            matches!(arg, ProofArg::Password { ref password } if password.as_str() == "open sesame")
        );
        // The webview only asks for Touch ID; it never carries the key.
        let arg: ProofArg = serde_json::from_str(r#"{"kind":"biometric"}"#).unwrap();
        assert!(matches!(arg, ProofArg::Biometric {}));
        for invalid in [
            r#"{"password":"open sesame"}"#,
            r#"{"kind":"password","password":"open sesame","extra":"x"}"#,
            r#"{"kind":"password"}"#,
            r#"{"kind":"biometric","biometric":"AAAA"}"#,
            r#"{"kind":"token"}"#,
        ] {
            assert!(
                serde_json::from_str::<ProofArg>(invalid).is_err(),
                "{invalid}"
            );
        }
    }

    #[test]
    fn proof_passwords_are_zeroizing() {
        // The deserialised password is wiped on drop, not just freed.
        fn assert_zeroizing(_: &Zeroizing<String>) {}
        let ProofArg::Password { password } =
            serde_json::from_str(r#"{"kind":"password","password":"p"}"#).unwrap()
        else {
            panic!("a password proof parsed as another kind");
        };
        assert_zeroizing(&password);
    }

    #[test]
    fn sealing_responses_map_onto_outcomes() {
        let status = br#"{"raw_available":true,"configured":true,"unlocked":true}"#;
        assert!(matches!(
            sealing_outcome(StatusCode::OK, status),
            Some(ProofOutcome::Sealing { .. })
        ));
        assert_eq!(
            sealing_outcome(StatusCode::OK, br#"{"raw_available":true}"#),
            None
        );
        assert_eq!(
            sealing_outcome(
                StatusCode::FORBIDDEN,
                br#"{"error":{"code":"raw_password_invalid"}}"#
            ),
            Some(ProofOutcome::PasswordInvalid)
        );
        assert_eq!(
            sealing_outcome(
                StatusCode::UNPROCESSABLE_ENTITY,
                br#"{"error":{"code":"raw_proof_required"}}"#
            ),
            None
        );
    }

    #[test]
    fn decision_responses_map_onto_outcomes() {
        let grant = br#"{"grant_id":"rawgrant_0123456789abcdef","status":"approved"}"#;
        assert!(matches!(
            decision_outcome(StatusCode::OK, grant),
            Some(ProofOutcome::Decided { .. })
        ));
        assert_eq!(
            decision_outcome(
                StatusCode::FORBIDDEN,
                br#"{"error":{"code":"raw_password_invalid","message":"x"}}"#
            ),
            Some(ProofOutcome::PasswordInvalid)
        );
        assert_eq!(
            decision_outcome(
                StatusCode::TOO_MANY_REQUESTS,
                br#"{"error":{"code":"raw_password_backoff","details":[{"reason":"retry_after","retry_after_seconds":8}]}}"#
            ),
            Some(ProofOutcome::Backoff {
                retry_after_seconds: 8
            })
        );
        assert_eq!(
            decision_outcome(
                StatusCode::CONFLICT,
                br#"{"error":{"code":"raw_access_not_pending"}}"#
            ),
            Some(ProofOutcome::NotPending)
        );
        assert_eq!(
            decision_outcome(StatusCode::NOT_FOUND, br#"{"error":{"code":"not_found"}}"#),
            Some(ProofOutcome::NotPending)
        );
        assert_eq!(
            decision_outcome(
                StatusCode::UNPROCESSABLE_ENTITY,
                br#"{"error":{"code":"raw_proof_required"}}"#
            ),
            Some(ProofOutcome::ProofRequired)
        );
        assert_eq!(
            decision_outcome(
                StatusCode::CONFLICT,
                br#"{"error":{"code":"raw_access_unavailable"}}"#
            ),
            None
        );
        for (status, code) in [
            (StatusCode::FORBIDDEN, "raw_biometric_invalid"),
            (StatusCode::CONFLICT, "raw_biometric_unavailable"),
        ] {
            let body = format!(r#"{{"error":{{"code":"{code}"}}}}"#);
            assert_eq!(
                decision_outcome(status, body.as_bytes()),
                Some(ProofOutcome::BiometricUnavailable)
            );
        }
        assert_eq!(decision_outcome(StatusCode::OK, b"{}"), None);
        let outcome = serde_json::to_value(ProofOutcome::Backoff {
            retry_after_seconds: 2,
        })
        .unwrap();
        assert_eq!(
            outcome,
            serde_json::json!({"outcome":"backoff","retry_after_seconds":2})
        );
    }

    #[test]
    fn only_proof_calls_are_single_attempt_requests() {
        assert!(is_proof_request(&Method::POST, &decision_path(GRANT)));
        assert!(is_proof_request(&Method::POST, RAW_UNLOCK_PATH));
        assert!(is_proof_request(&Method::POST, RAW_VERIFY_PATH));
        assert!(is_proof_request(&Method::POST, RAW_PASSWORD_PATH));
        assert!(is_proof_request(&Method::POST, RAW_BIOMETRIC_PATH));
        assert!(!is_proof_request(&Method::DELETE, RAW_BIOMETRIC_PATH));
        assert!(!is_proof_request(&Method::POST, RAW_LOCK_PATH));
        assert!(!is_proof_request(&Method::GET, RAW_SEALING_PATH));
        assert!(!is_proof_request(&Method::GET, &decision_path(GRANT)));
        assert!(!is_proof_request(&Method::GET, RAW_ACCESS_PATH));
        assert!(!is_proof_request(
            &Method::POST,
            "/control/v1/requests/request_1/audit/raw-access"
        ));
    }

    #[test]
    fn lists_must_carry_pending_and_active() {
        assert!(parse_list(br#"{"items":[],"active":[]}"#).is_ok());
        assert!(parse_list(br#"{"items":[]}"#).is_err());
        assert!(parse_list(br#"{}"#).is_err());
        assert!(parse_list(b"not json").is_err());
    }

    #[test]
    fn grant_paths_take_valid_ids_only() {
        assert_eq!(
            grant_path(GRANT).unwrap(),
            format!("{RAW_ACCESS_PATH}/{GRANT}")
        );
        assert!(grant_path("rawgrant_bad/../x").is_err());
    }
}
