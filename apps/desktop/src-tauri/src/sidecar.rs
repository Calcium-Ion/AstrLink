use std::{
    collections::HashSet,
    fmt::Write as _,
    path::Path,
    sync::{Arc, Mutex, MutexGuard},
    time::{Duration, Instant},
};

use reqwest::{header, Client, Method};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::{
    process::{CommandChild, CommandEvent, TerminatedPayload},
    ShellExt,
};

// Legacy installation migration and ordinary startup filesystem work can still
// take longer on slow disks even though model weights are verified lazily.
const READY_TIMEOUT: Duration = Duration::from_secs(120);
const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(12);
const STOP_TIMEOUT: Duration = Duration::from_secs(3);
const STOP_POLL_DELAY: Duration = Duration::from_millis(25);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(2);
// Policy changes may synchronously stop a worker for up to five seconds, and
// model deletion also waits for download cancellation and filesystem cleanup.
const PRIVACY_MUTATION_TIMEOUT: Duration = Duration::from_secs(30);
// Probe and install may pin a large tokenizer/configuration set over a slow
// connection. The installation call returns before model-weight downloading.
const PRIVACY_MODEL_METADATA_TIMEOUT: Duration = Duration::from_secs(10 * 60);
const HEALTH_ATTEMPTS: usize = 8;
const HEALTH_RETRY_DELAY: Duration = Duration::from_millis(250);
const MAX_ERROR_BODY: usize = 512;
// A valid 100-installation model directory can exceed 2 MiB when every
// installation carries the maximum 256-entry label mapping.
const MAX_CONTROL_BODY: usize = 8 * 1024 * 1024;
// Decrypted audit content may reach response_content_max_bytes (64 MiB).
const MAX_AUDIT_CONTENT_BODY: usize = 96 * 1024 * 1024;
const AUDIT_CONTENT_TIMEOUT: Duration = Duration::from_secs(60);
const SUPPORTED_CONTROL_API_VERSION: &str = "v1";
const SUPPORTED_PROTOCOL_CONTRACT_VERSION: &str = "v1";
const PRIVACY_MODEL_CATALOG_PATH: &str = "/control/v1/privacy-model-catalog";
const PRIVACY_MODELS_PATH: &str = "/control/v1/privacy-models";
const PRIVACY_MODEL_PROBE_PATH: &str = "/control/v1/privacy-models/probe";
const POLICY_DRY_RUN_PATH: &str = "/control/v1/policies/policy_privacy_default/dry-run";

#[derive(Clone, Copy, Debug, Default, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum CorePhase {
    #[default]
    Stopped,
    Spawning,
    WaitingForReady,
    Handshaking,
    Ready,
    Stopping,
    Exited,
    Error,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct LifecycleState {
    generation: u64,
    phase: CorePhase,
    pid: Option<u32>,
    last_error: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum LifecycleEvent {
    StopRequested,
    StopRequestFailed(String),
    FailureStopRequested(String),
    FailureStopRequestFailed { failure: String, kill_error: String },
    FailureWithoutChild(String),
    ProcessErrorWhileStopping,
    StopWaitTimedOut,
    Terminated(String),
}

fn transition_lifecycle(
    current: &LifecycleState,
    event_generation: u64,
    event: LifecycleEvent,
) -> LifecycleState {
    if current.generation != event_generation {
        return current.clone();
    }

    let mut next = current.clone();
    match event {
        LifecycleEvent::StopRequested => {
            next.phase = CorePhase::Stopping;
            next.last_error = None;
        }
        LifecycleEvent::StopRequestFailed(error) => {
            next.phase = CorePhase::Error;
            next.last_error = Some(manual_termination_message(
                "unable to send astrlink-core termination request",
                &error,
                next.pid,
            ));
        }
        LifecycleEvent::FailureStopRequested(failure) => {
            next.phase = CorePhase::Error;
            next.pid = None;
            next.last_error = Some(format!(
                "{failure}; astrlink-core termination request succeeded"
            ));
        }
        LifecycleEvent::FailureStopRequestFailed {
            failure,
            kill_error,
        } => {
            next.phase = CorePhase::Error;
            next.last_error = Some(manual_termination_message(&failure, &kill_error, next.pid));
        }
        LifecycleEvent::FailureWithoutChild(failure) => {
            next.phase = CorePhase::Error;
            next.last_error = Some(match next.pid {
                Some(pid) => format!(
                    "{failure}; process handle is unavailable; manually terminate PID {pid} before retrying"
                ),
                None => failure,
            });
        }
        LifecycleEvent::ProcessErrorWhileStopping => {
            if current.phase == CorePhase::Stopping {
                next.phase = CorePhase::Stopped;
                next.pid = None;
                next.last_error = None;
            }
        }
        LifecycleEvent::StopWaitTimedOut => {
            if current.phase == CorePhase::Stopping {
                next.phase = CorePhase::Error;
                next.last_error = Some(match next.pid {
                    Some(pid) => format!(
                        "timed out waiting for astrlink-core to terminate; manually verify or terminate PID {pid} before retrying"
                    ),
                    None => "timed out waiting for astrlink-core to terminate".to_string(),
                });
            }
        }
        LifecycleEvent::Terminated(termination) => {
            next.pid = None;
            if current.phase == CorePhase::Stopping {
                next.phase = CorePhase::Stopped;
                next.last_error = None;
            } else {
                next.phase = CorePhase::Exited;
                next.last_error = Some(match current.last_error.as_deref() {
                    Some(previous) => format!("{previous}; {termination}"),
                    None => termination,
                });
            }
        }
    }
    next
}

fn manual_termination_message(prefix: &str, error: &str, pid: Option<u32>) -> String {
    match pid {
        Some(pid) => format!("{prefix}: {error}; manually terminate PID {pid} before retrying"),
        None => format!("{prefix}: {error}"),
    }
}

fn start_allowed(state: &LifecycleState, has_child: bool) -> bool {
    !has_child
        && state.pid.is_none()
        && matches!(
            state.phase,
            CorePhase::Stopped | CorePhase::Exited | CorePhase::Error
        )
}

fn sidecar_args(parent_pid: u32, data_directory: &Path) -> Result<Vec<String>, String> {
    let data_directory = data_directory
        .to_str()
        .ok_or_else(|| "AstrLink data directory is not valid UTF-8".to_string())?;
    Ok(vec![
        "--parent-pid".to_string(),
        parent_pid.to_string(),
        "--data-dir".to_string(),
        data_directory.to_string(),
        "--control-token-stdin".to_string(),
    ])
}

fn generate_control_token() -> Result<String, String> {
    let mut random = [0_u8; 32];
    getrandom::getrandom(&mut random)
        .map_err(|error| format!("unable to generate local control token: {error}"))?;
    let mut token = String::with_capacity(random.len() * 2);
    for byte in random {
        write!(&mut token, "{byte:02x}").expect("writing to a String cannot fail");
    }
    Ok(token)
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct ReadyAnnouncement {
    pub event: String,
    pub core_version: String,
    pub control_api_version: String,
    pub protocol_contract_version: String,
    pub inference_url: String,
    pub control_url: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct HealthResponse {
    pub status: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct VersionResponse {
    pub core_version: String,
    pub control_api_version: String,
    pub protocol_contract_version: String,
    pub build_commit: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct ProtocolCapability {
    pub id: String,
    pub phase: String,
    pub primary: bool,
    pub streaming: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub struct PlanTypeCapability {
    pub id: String,
    pub available_in_alpha: bool,
    pub uses_local_conversion: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
pub struct ConversionEngineCapability {
    pub name: String,
    pub version: Option<String>,
    pub available: bool,
    pub edges: Vec<serde_json::Value>,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
pub struct CapabilitiesResponse {
    pub protocol_contract_version: String,
    pub protocols: Vec<ProtocolCapability>,
    pub plan_types: Vec<PlanTypeCapability>,
    pub conversion_engine: ConversionEngineCapability,
}

#[derive(Clone, Debug, Serialize, PartialEq)]
pub struct CoreSnapshot {
    pub phase: CorePhase,
    pub pid: Option<u32>,
    pub ready: Option<ReadyAnnouncement>,
    pub health: Option<HealthResponse>,
    pub version: Option<VersionResponse>,
    pub capabilities: Option<CapabilitiesResponse>,
    pub last_error: Option<String>,
}

struct CoreInner {
    generation: u64,
    phase: CorePhase,
    child: Option<CommandChild>,
    pid: Option<u32>,
    ready: Option<ReadyAnnouncement>,
    health: Option<HealthResponse>,
    version: Option<VersionResponse>,
    capabilities: Option<CapabilitiesResponse>,
    control_token: Option<String>,
    last_error: Option<String>,
    #[cfg(windows)]
    job: Option<windows_job::JobObject>,
}

impl Default for CoreInner {
    fn default() -> Self {
        Self {
            generation: 0,
            phase: CorePhase::Stopped,
            child: None,
            pid: None,
            ready: None,
            health: None,
            version: None,
            capabilities: None,
            control_token: None,
            last_error: None,
            #[cfg(windows)]
            job: None,
        }
    }
}

impl CoreInner {
    fn lifecycle(&self) -> LifecycleState {
        LifecycleState {
            generation: self.generation,
            phase: self.phase,
            pid: self.pid,
            last_error: self.last_error.clone(),
        }
    }

    fn apply_lifecycle(&mut self, state: LifecycleState) {
        self.generation = state.generation;
        self.phase = state.phase;
        self.pid = state.pid;
        self.last_error = state.last_error;
    }

    fn clear_handshake(&mut self) {
        self.ready = None;
        self.health = None;
        self.version = None;
        self.capabilities = None;
        self.control_token = None;
    }

    fn clear_process_guard(&mut self) {
        #[cfg(windows)]
        self.job.take();
    }
}

pub struct CoreManager {
    inner: Mutex<CoreInner>,
    client: Client,
}

#[derive(Serialize)]
pub struct EndpointRecordResponse {
    pub endpoint: serde_json::Value,
    pub etag: String,
}

#[derive(Serialize)]
pub struct PolicyRecordResponse {
    pub policy: serde_json::Value,
    pub etag: String,
}

impl CoreManager {
    pub fn new() -> Self {
        let client = Client::builder()
            .timeout(REQUEST_TIMEOUT)
            .redirect(reqwest::redirect::Policy::none())
            .no_proxy()
            .build()
            .expect("reqwest client configuration is valid");

        Self {
            inner: Mutex::new(CoreInner::default()),
            client,
        }
    }

    pub fn start(self: &Arc<Self>, app: &AppHandle) -> Result<(), String> {
        // Keep the manager lock from publishing Spawning until the child and its
        // platform process guard are published. A concurrent stop therefore
        // cannot observe "no child" and return before this start completes.
        let (generation, receiver) = {
            let mut inner = self.lock_inner();
            if !start_allowed(&inner.lifecycle(), inner.child.is_some()) {
                return Err(inner.last_error.clone().unwrap_or_else(|| {
                    "astrlink-core is already running or stopping".to_string()
                }));
            }

            inner.generation = inner.generation.wrapping_add(1);
            inner.phase = CorePhase::Spawning;
            inner.pid = None;
            inner.clear_handshake();
            inner.last_error = None;
            inner.clear_process_guard();
            let generation = inner.generation;
            let data_directory = match app.path().app_data_dir() {
                Ok(path) => path,
                Err(error) => {
                    let message = format!("unable to resolve AstrLink data directory: {error}");
                    Self::fail_generation_locked(&mut inner, generation, message.clone());
                    return Err(message);
                }
            };
            let control_token = match generate_control_token() {
                Ok(token) => token,
                Err(message) => {
                    Self::fail_generation_locked(&mut inner, generation, message.clone());
                    return Err(message);
                }
            };
            let arguments = match sidecar_args(std::process::id(), &data_directory) {
                Ok(arguments) => arguments,
                Err(message) => {
                    Self::fail_generation_locked(&mut inner, generation, message.clone());
                    return Err(message);
                }
            };

            let command = match app.shell().sidecar("astrlink-core") {
                Ok(command) => command.args(arguments),
                Err(error) => {
                    let message = format!("unable to resolve astrlink-core sidecar: {error}");
                    Self::fail_generation_locked(&mut inner, generation, message.clone());
                    return Err(message);
                }
            };

            let (receiver, mut child) = match command.spawn() {
                Ok(process) => process,
                Err(error) => {
                    let message = format!("unable to spawn astrlink-core: {error}");
                    Self::fail_generation_locked(&mut inner, generation, message.clone());
                    return Err(message);
                }
            };

            if let Err(error) = child.write(format!("{control_token}\n").as_bytes()) {
                let pid = child.pid();
                let message =
                    format!("unable to deliver local control token to astrlink-core: {error}");
                inner.pid = Some(pid);
                let next = match child.kill() {
                    Ok(()) => transition_lifecycle(
                        &inner.lifecycle(),
                        generation,
                        LifecycleEvent::FailureStopRequested(message),
                    ),
                    Err(kill_error) => transition_lifecycle(
                        &inner.lifecycle(),
                        generation,
                        LifecycleEvent::FailureStopRequestFailed {
                            failure: message,
                            kill_error: kill_error.to_string(),
                        },
                    ),
                };
                inner.apply_lifecycle(next);
                return Err(inner
                    .last_error
                    .clone()
                    .expect("failed control token delivery records an error"));
            }

            #[cfg(windows)]
            let job = match windows_job::JobObject::attach(child.pid()) {
                Ok(job) => job,
                Err(error) => {
                    let pid = child.pid();
                    let message = format!(
                        "unable to assign astrlink-core PID {pid} to the desktop Job Object: {error}"
                    );
                    inner.pid = Some(pid);
                    let next = match child.kill() {
                        Ok(()) => transition_lifecycle(
                            &inner.lifecycle(),
                            generation,
                            LifecycleEvent::FailureStopRequested(message),
                        ),
                        Err(kill_error) => transition_lifecycle(
                            &inner.lifecycle(),
                            generation,
                            LifecycleEvent::FailureStopRequestFailed {
                                failure: message,
                                kill_error: kill_error.to_string(),
                            },
                        ),
                    };
                    inner.apply_lifecycle(next);
                    return Err(inner
                        .last_error
                        .clone()
                        .expect("failed Job Object attachment records an error"));
                }
            };

            inner.pid = Some(child.pid());
            inner.child = Some(child);
            inner.control_token = Some(control_token);
            inner.phase = CorePhase::WaitingForReady;
            #[cfg(windows)]
            {
                inner.job = Some(job);
            }
            (generation, receiver)
        };

        let monitor = Arc::clone(self);
        tauri::async_runtime::spawn(async move {
            monitor.monitor_process(generation, receiver).await;
        });

        let watchdog = Arc::clone(self);
        tauri::async_runtime::spawn(async move {
            tokio::time::sleep(READY_TIMEOUT).await;
            let timed_out = {
                let inner = watchdog.lock_inner();
                inner.generation == generation && inner.phase == CorePhase::WaitingForReady
            };
            if timed_out {
                watchdog.fail_generation_and_stop(
                    generation,
                    "timed out waiting for astrlink-core ready signal".to_string(),
                );
            }
        });

        Ok(())
    }

    pub async fn restart(self: &Arc<Self>, app: &AppHandle) -> Result<(), String> {
        self.stop_and_wait().await?;
        self.start(app)
    }

    pub async fn stop_and_wait(&self) -> Result<(), String> {
        if let Some(generation) = self.request_stop()? {
            self.wait_until_stopped(generation).await?;
        }
        Ok(())
    }

    fn request_stop(&self) -> Result<Option<u64>, String> {
        let mut inner = self.lock_inner();
        let generation = inner.generation;
        if inner.phase == CorePhase::Stopping {
            return Ok(Some(generation));
        }
        if inner.child.is_none() {
            if inner.pid.is_some() {
                return Err(inner.last_error.clone().unwrap_or_else(|| {
                    format!(
                        "astrlink-core process handle is unavailable; manually terminate PID {} before retrying",
                        inner.pid.expect("PID presence checked")
                    )
                }));
            }
            inner.phase = CorePhase::Stopped;
            inner.clear_handshake();
            inner.last_error = None;
            inner.clear_process_guard();
            return Ok(None);
        }

        // CommandChild::kill consumes the handle. Keep the state mutex held until the
        // result is reflected so no observer can see an unconfirmed Stopping state.
        let child = inner.child.take().expect("child presence checked");
        inner.clear_handshake();
        match child.kill() {
            Ok(()) => {
                let next = transition_lifecycle(
                    &inner.lifecycle(),
                    generation,
                    LifecycleEvent::StopRequested,
                );
                inner.apply_lifecycle(next);
                Ok(Some(generation))
            }
            Err(error) => {
                let next = transition_lifecycle(
                    &inner.lifecycle(),
                    generation,
                    LifecycleEvent::StopRequestFailed(error.to_string()),
                );
                inner.apply_lifecycle(next);
                Err(inner
                    .last_error
                    .clone()
                    .expect("failed stop transition records an error"))
            }
        }
    }

    async fn wait_until_stopped(&self, generation: u64) -> Result<(), String> {
        self.wait_until_stopped_with_timeout(generation, STOP_TIMEOUT)
            .await
    }

    async fn wait_until_stopped_with_timeout(
        &self,
        generation: u64,
        timeout: Duration,
    ) -> Result<(), String> {
        let deadline = Instant::now() + timeout;
        loop {
            let state = {
                let inner = self.lock_inner();
                if inner.generation != generation {
                    return Err("astrlink-core stop was superseded".to_string());
                }
                (inner.phase, inner.last_error.clone())
            };
            match state.0 {
                CorePhase::Stopped => return Ok(()),
                CorePhase::Error | CorePhase::Exited => {
                    return Err(state
                        .1
                        .unwrap_or_else(|| "astrlink-core did not stop cleanly".to_string()));
                }
                _ => {}
            }
            if Instant::now() >= deadline {
                let mut inner = self.lock_inner();
                if inner.generation == generation {
                    let next = transition_lifecycle(
                        &inner.lifecycle(),
                        generation,
                        LifecycleEvent::StopWaitTimedOut,
                    );
                    inner.apply_lifecycle(next);
                }
                return Err(inner.last_error.clone().unwrap_or_else(|| {
                    "timed out waiting for astrlink-core to terminate".to_string()
                }));
            }
            tokio::time::sleep(STOP_POLL_DELAY).await;
        }
    }

    pub fn snapshot(&self) -> CoreSnapshot {
        let inner = self.lock_inner();
        CoreSnapshot {
            phase: inner.phase,
            pid: inner.pid,
            ready: inner.ready.clone(),
            health: inner.health.clone(),
            version: inner.version.clone(),
            capabilities: inner.capabilities.clone(),
            last_error: inner.last_error.clone(),
        }
    }

    async fn monitor_process(
        self: Arc<Self>,
        generation: u64,
        mut receiver: tauri::async_runtime::Receiver<CommandEvent>,
    ) {
        while let Some(event) = receiver.recv().await {
            match event {
                CommandEvent::Stdout(bytes) => {
                    // Shell commands are line-buffered unless raw output is explicitly enabled.
                    // AstrLink never enables raw output, so one Core ready line is one event.
                    // Run the HTTP handshake independently so Terminated/Error events remain
                    // consumable while control requests are in flight.
                    let line = String::from_utf8_lossy(&bytes).trim().to_string();
                    let handshake = Arc::clone(&self);
                    tauri::async_runtime::spawn(async move {
                        handshake.handle_stdout(generation, &line).await;
                    });
                }
                CommandEvent::Stderr(bytes) => {
                    let line = String::from_utf8_lossy(&bytes);
                    eprintln!("{}", line.trim());
                }
                CommandEvent::Error(error) => {
                    self.handle_process_error(generation, format!("sidecar event error: {error}"));
                    break;
                }
                CommandEvent::Terminated(payload) => {
                    self.handle_terminated(generation, payload);
                    break;
                }
                _ => {}
            }
        }
    }

    async fn handle_stdout(&self, generation: u64, line: &str) {
        let should_parse = {
            let inner = self.lock_inner();
            inner.generation == generation && inner.phase == CorePhase::WaitingForReady
        };

        if !should_parse {
            return;
        }

        let ready = match parse_ready_announcement(line) {
            Ok(ready) => ready,
            Err(error) => {
                self.fail_generation_and_stop(generation, error);
                return;
            }
        };

        {
            let mut inner = self.lock_inner();
            if inner.generation != generation || inner.phase != CorePhase::WaitingForReady {
                return;
            }
            inner.ready = Some(ready.clone());
            inner.phase = CorePhase::Handshaking;
        }

        let handshake =
            tokio::time::timeout(HANDSHAKE_TIMEOUT, self.perform_handshake(&ready)).await;
        match handshake {
            Ok(Ok((health, version, capabilities))) => {
                let mut inner = self.lock_inner();
                if inner.generation != generation || inner.phase != CorePhase::Handshaking {
                    return;
                }
                inner.health = Some(health);
                inner.version = Some(version);
                inner.capabilities = Some(capabilities);
                inner.phase = CorePhase::Ready;
                inner.last_error = None;
            }
            Ok(Err(error)) => self.fail_generation_and_stop(generation, error),
            Err(_) => self.fail_generation_and_stop(
                generation,
                "timed out completing the astrlink-core control handshake".to_string(),
            ),
        }
    }

    async fn perform_handshake(
        &self,
        ready: &ReadyAnnouncement,
    ) -> Result<(HealthResponse, VersionResponse, CapabilitiesResponse), String> {
        let mut health_error = "health request was not attempted".to_string();
        let mut health = None;

        for attempt in 0..HEALTH_ATTEMPTS {
            match self
                .get_control::<HealthResponse>(&ready.control_url, "/control/v1/health")
                .await
            {
                Ok(response) if response.status == "ok" => {
                    health = Some(response);
                    break;
                }
                Ok(response) => {
                    health_error = format!("Core health is {:?}, expected \"ok\"", response.status);
                }
                Err(error) => health_error = error,
            }

            if attempt + 1 < HEALTH_ATTEMPTS {
                tokio::time::sleep(HEALTH_RETRY_DELAY).await;
            }
        }

        let health =
            health.ok_or_else(|| format!("Core health handshake failed: {health_error}"))?;
        let version = self
            .get_control::<VersionResponse>(&ready.control_url, "/control/v1/version")
            .await?;
        let capabilities = self
            .get_control::<CapabilitiesResponse>(&ready.control_url, "/control/v1/capabilities")
            .await?;

        verify_contract(ready, &version, &capabilities)?;
        Ok((health, version, capabilities))
    }

    async fn get_control<T: DeserializeOwned>(
        &self,
        base_url: &str,
        path: &str,
    ) -> Result<T, String> {
        let url = format!("{}{}", base_url.trim_end_matches('/'), path);
        let response = self
            .client
            .get(&url)
            .send()
            .await
            .map_err(|error| format!("GET {path} failed: {error}"))?;
        let status = response.status();
        let body = response
            .bytes()
            .await
            .map_err(|error| format!("GET {path} body failed: {error}"))?;

        if !status.is_success() {
            let text = String::from_utf8_lossy(&body);
            let preview: String = text.chars().take(MAX_ERROR_BODY).collect();
            return Err(format!("GET {path} returned {status}: {preview}"));
        }

        serde_json::from_slice(&body)
            .map_err(|error| format!("GET {path} returned invalid JSON: {error}"))
    }

    pub async fn list_endpoints(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, "/control/v1/endpoints?limit=200", None, None)
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("endpoint list returned invalid JSON: {error}"))
    }

    pub async fn list_access_tokens(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, "/control/v1/access-tokens", None, None)
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("access token list returned invalid JSON: {error}"))
    }

    pub async fn create_access_token(&self, name: &str) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(
                Method::POST,
                "/control/v1/access-tokens",
                Some(serde_json::json!({ "name": name })),
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("access token create returned invalid JSON: {error}"))
    }

    pub async fn reveal_access_token(&self, token_id: &str) -> Result<serde_json::Value, String> {
        validate_resource_id(token_id)?;
        let (_, body) = self
            .authenticated_control(
                Method::GET,
                &format!("/control/v1/access-tokens/{token_id}/secret"),
                None,
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("access token reveal returned invalid JSON: {error}"))
    }

    pub async fn delete_access_token(&self, token_id: &str) -> Result<(), String> {
        validate_resource_id(token_id)?;
        self.authenticated_control(
            Method::DELETE,
            &format!("/control/v1/access-tokens/{token_id}"),
            None,
            None,
        )
        .await?;
        Ok(())
    }

    pub async fn list_privacy_policies(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, "/control/v1/policies", None, None)
            .await?;
        parse_privacy_policy_page(&body)
    }

    pub async fn get_privacy_policy(&self) -> Result<PolicyRecordResponse, String> {
        let (etag, body) = self
            .authenticated_control(
                Method::GET,
                "/control/v1/policies/policy_privacy_default",
                None,
                None,
            )
            .await?;
        policy_record(etag, &body)
    }

    pub async fn update_privacy_policy(
        &self,
        etag: &str,
        patch: serde_json::Value,
    ) -> Result<PolicyRecordResponse, String> {
        validate_strong_etag(etag)?;
        let patch = validate_privacy_policy_patch(patch)?;
        let (etag, body) = self
            .authenticated_control(
                Method::PATCH,
                "/control/v1/policies/policy_privacy_default",
                Some(patch),
                Some(etag),
            )
            .await?;
        policy_record(etag, &body)
    }

    pub async fn dry_run_privacy_policy(
        &self,
        input: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let input = validate_privacy_dry_run_input(input)?;
        let (_, body) = self
            .authenticated_control(Method::POST, POLICY_DRY_RUN_PATH, Some(input), None)
            .await?;
        parse_privacy_dry_run_result(&body)
    }

    pub async fn get_privacy_model_catalog(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, PRIVACY_MODEL_CATALOG_PATH, None, None)
            .await?;
        parse_privacy_model_catalog(&body)
    }

    pub async fn probe_privacy_model(
        &self,
        input: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let input = validate_privacy_model_probe_input(input)?;
        let (_, body) = self
            .authenticated_control(Method::POST, PRIVACY_MODEL_PROBE_PATH, Some(input), None)
            .await?;
        parse_privacy_model_probe(&body)
    }

    pub async fn list_privacy_model_installations(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, PRIVACY_MODELS_PATH, None, None)
            .await?;
        parse_privacy_model_installation_list(&body)
    }

    pub async fn install_privacy_model(
        &self,
        input: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let input = validate_privacy_model_install_input(input)?;
        let (_, body) = self
            .authenticated_control(Method::POST, PRIVACY_MODELS_PATH, Some(input), None)
            .await?;
        parse_privacy_model_installation(&body)
    }

    pub async fn get_privacy_model_installation(
        &self,
        installation_id: &str,
    ) -> Result<serde_json::Value, String> {
        validate_privacy_model_id(installation_id)?;
        let (_, body) = self
            .authenticated_control(
                Method::GET,
                &format!("{PRIVACY_MODELS_PATH}/{installation_id}"),
                None,
                None,
            )
            .await?;
        parse_privacy_model_installation(&body)
    }

    pub async fn delete_privacy_model_installation(
        &self,
        installation_id: &str,
    ) -> Result<(), String> {
        validate_privacy_model_id(installation_id)?;
        self.authenticated_control(
            Method::DELETE,
            &format!("{PRIVACY_MODELS_PATH}/{installation_id}"),
            None,
            None,
        )
        .await?;
        Ok(())
    }

    pub async fn get_endpoint(&self, endpoint_id: &str) -> Result<EndpointRecordResponse, String> {
        validate_resource_id(endpoint_id)?;
        let (etag, body) = self
            .authenticated_control(
                Method::GET,
                &format!("/control/v1/endpoints/{endpoint_id}"),
                None,
                None,
            )
            .await?;
        endpoint_record(etag, &body)
    }

    pub async fn create_endpoint(
        &self,
        input: serde_json::Value,
    ) -> Result<EndpointRecordResponse, String> {
        let (etag, body) = self
            .authenticated_control(Method::POST, "/control/v1/endpoints", Some(input), None)
            .await?;
        endpoint_record(etag, &body)
    }

    pub async fn update_endpoint(
        &self,
        endpoint_id: &str,
        etag: &str,
        patch: serde_json::Value,
    ) -> Result<EndpointRecordResponse, String> {
        validate_resource_id(endpoint_id)?;
        validate_etag(etag)?;
        let (etag, body) = self
            .authenticated_control(
                Method::PATCH,
                &format!("/control/v1/endpoints/{endpoint_id}"),
                Some(patch),
                Some(etag),
            )
            .await?;
        endpoint_record(etag, &body)
    }

    pub async fn delete_endpoint(&self, endpoint_id: &str, etag: &str) -> Result<(), String> {
        validate_resource_id(endpoint_id)?;
        validate_etag(etag)?;
        self.authenticated_control(
            Method::DELETE,
            &format!("/control/v1/endpoints/{endpoint_id}"),
            None,
            Some(etag),
        )
        .await?;
        Ok(())
    }

    pub async fn list_request_records(
        &self,
        query: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let qs = build_request_record_query(&query)?;
        let (_, body) = self
            .authenticated_control(
                Method::GET,
                &format!("/control/v1/requests{qs}"),
                None,
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("request record list returned invalid JSON: {error}"))
    }

    pub async fn get_request_record(&self, request_id: &str) -> Result<serde_json::Value, String> {
        validate_resource_id(request_id)?;
        let (_, body) = self
            .authenticated_control(
                Method::GET,
                &format!("/control/v1/requests/{request_id}"),
                None,
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("request record returned invalid JSON: {error}"))
    }

    pub async fn delete_request_record(&self, request_id: &str) -> Result<(), String> {
        validate_resource_id(request_id)?;
        self.authenticated_control(
            Method::DELETE,
            &format!("/control/v1/requests/{request_id}"),
            None,
            None,
        )
        .await?;
        Ok(())
    }

    pub async fn purge_request_records(
        &self,
        input: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        let input = validate_purge_request_records_input(&input)?;
        let (_, body) = self
            .authenticated_control(
                Method::POST,
                "/control/v1/requests/purge",
                Some(input),
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("request purge returned invalid JSON: {error}"))
    }

    pub async fn get_request_audit_content(
        &self,
        request_id: &str,
    ) -> Result<serde_json::Value, String> {
        validate_resource_id(request_id)?;
        let (_, body) = self
            .authenticated_control(
                Method::GET,
                &format!("/control/v1/requests/{request_id}/audit"),
                None,
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("request audit content returned invalid JSON: {error}"))
    }

    pub async fn get_audit_settings(&self) -> Result<serde_json::Value, String> {
        let (_, body) = self
            .authenticated_control(Method::GET, "/control/v1/audit-settings", None, None)
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("audit settings returned invalid JSON: {error}"))
    }

    pub async fn update_audit_settings(
        &self,
        patch: serde_json::Value,
    ) -> Result<serde_json::Value, String> {
        validate_audit_settings_patch(&patch)?;
        let (_, body) = self
            .authenticated_control(
                Method::PATCH,
                "/control/v1/audit-settings",
                Some(patch),
                None,
            )
            .await?;
        serde_json::from_slice(&body)
            .map_err(|error| format!("audit settings update returned invalid JSON: {error}"))
    }

    async fn authenticated_control(
        &self,
        method: Method,
        path: &str,
        body: Option<serde_json::Value>,
        if_match: Option<&str>,
    ) -> Result<(Option<String>, Vec<u8>), String> {
        let (base_url, control_token) = {
            let inner = self.lock_inner();
            if inner.phase != CorePhase::Ready {
                return Err("Core is not ready for authenticated control operations".to_string());
            }
            let base_url = inner
                .ready
                .as_ref()
                .map(|ready| ready.control_url.clone())
                .ok_or_else(|| "Core ready announcement is unavailable".to_string())?;
            let control_token = inner
                .control_token
                .clone()
                .ok_or_else(|| "Core local control token is unavailable".to_string())?;
            (base_url, control_token)
        };

        let url = format!("{}{}", base_url.trim_end_matches('/'), path);
        let mut request = self
            .client
            .request(method.clone(), &url)
            .timeout(control_request_timeout(&method, path))
            .header(header::AUTHORIZATION, format!("Bearer {control_token}"));
        if let Some(etag) = if_match {
            request = request.header(header::IF_MATCH, etag);
        }
        if let Some(body) = body {
            let encoded = serde_json::to_vec(&body)
                .map_err(|error| format!("unable to encode endpoint request: {error}"))?;
            let content_type = if method == Method::PATCH {
                "application/merge-patch+json"
            } else {
                "application/json"
            };
            request = request
                .header(header::CONTENT_TYPE, content_type)
                .body(encoded);
        }
        let response = request
            .send()
            .await
            .map_err(|error| format!("{} {path} failed: {error}", method.as_str()))?;
        let status = response.status();
        let etag = response
            .headers()
            .get(header::ETAG)
            .map(|value| {
                value
                    .to_str()
                    .map(str::to_string)
                    .map_err(|_| "Core returned a non-text ETag".to_string())
            })
            .transpose()?;
        let body_limit = control_body_limit(path);
        if response
            .content_length()
            .is_some_and(|length| length > body_limit as u64)
        {
            return Err(format!("{} {path} response is too large", method.as_str()));
        }
        let body = response
            .bytes()
            .await
            .map_err(|error| format!("{} {path} body failed: {error}", method.as_str()))?;
        if body.len() > body_limit {
            return Err(format!("{} {path} response is too large", method.as_str()));
        }
        if !status.is_success() {
            let text = String::from_utf8_lossy(&body);
            let preview: String = text.chars().take(MAX_ERROR_BODY).collect();
            return Err(format!(
                "{} {path} returned {status}: {preview}",
                method.as_str()
            ));
        }
        Ok((etag, body.to_vec()))
    }

    fn handle_terminated(&self, generation: u64, payload: TerminatedPayload) {
        let mut inner = self.lock_inner();
        if inner.generation != generation {
            return;
        }

        let termination = match (payload.code, payload.signal) {
            (Some(code), _) => format!("astrlink-core exited with code {code}"),
            (_, Some(signal)) => format!("astrlink-core exited after signal {signal}"),
            _ => "astrlink-core exited unexpectedly".to_string(),
        };
        let next = transition_lifecycle(
            &inner.lifecycle(),
            generation,
            LifecycleEvent::Terminated(termination),
        );
        inner.child.take();
        inner.apply_lifecycle(next);
        inner.clear_handshake();
        inner.clear_process_guard();
    }

    fn handle_process_error(&self, generation: u64, message: String) {
        let mut inner = self.lock_inner();
        if inner.generation != generation {
            return;
        }

        if inner.phase == CorePhase::Stopping {
            let next = transition_lifecycle(
                &inner.lifecycle(),
                generation,
                LifecycleEvent::ProcessErrorWhileStopping,
            );
            inner.child.take();
            inner.apply_lifecycle(next);
            inner.clear_handshake();
            inner.clear_process_guard();
            return;
        }

        Self::fail_generation_and_stop_locked(&mut inner, generation, message);
    }

    fn fail_generation_locked(inner: &mut CoreInner, generation: u64, message: String) {
        if inner.generation == generation {
            inner.phase = CorePhase::Error;
            inner.child.take();
            inner.pid = None;
            inner.clear_handshake();
            inner.last_error = Some(message);
            inner.clear_process_guard();
        }
    }

    fn fail_generation_and_stop(&self, generation: u64, message: String) {
        let mut inner = self.lock_inner();
        if inner.generation != generation
            || !matches!(
                inner.phase,
                CorePhase::Spawning
                    | CorePhase::WaitingForReady
                    | CorePhase::Handshaking
                    | CorePhase::Ready
            )
        {
            return;
        }
        Self::fail_generation_and_stop_locked(&mut inner, generation, message);
    }

    fn fail_generation_and_stop_locked(inner: &mut CoreInner, generation: u64, message: String) {
        inner.clear_handshake();
        let next = match inner.child.take() {
            Some(child) => match child.kill() {
                Ok(()) => {
                    inner.clear_process_guard();
                    transition_lifecycle(
                        &inner.lifecycle(),
                        generation,
                        LifecycleEvent::FailureStopRequested(message),
                    )
                }
                Err(error) => transition_lifecycle(
                    &inner.lifecycle(),
                    generation,
                    LifecycleEvent::FailureStopRequestFailed {
                        failure: message,
                        kill_error: error.to_string(),
                    },
                ),
            },
            None => transition_lifecycle(
                &inner.lifecycle(),
                generation,
                LifecycleEvent::FailureWithoutChild(message),
            ),
        };
        inner.apply_lifecycle(next);
    }

    fn lock_inner(&self) -> MutexGuard<'_, CoreInner> {
        self.inner
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
    }
}

fn control_request_timeout(method: &Method, path: &str) -> Duration {
    if method == Method::POST && (path == PRIVACY_MODEL_PROBE_PATH || path == PRIVACY_MODELS_PATH) {
        return PRIVACY_MODEL_METADATA_TIMEOUT;
    }
    if method == Method::POST && path == POLICY_DRY_RUN_PATH {
        return PRIVACY_MUTATION_TIMEOUT;
    }
    if (method == Method::PATCH && path == "/control/v1/policies/policy_privacy_default")
        || (method == Method::DELETE && path.starts_with("/control/v1/privacy-models/"))
    {
        return PRIVACY_MUTATION_TIMEOUT;
    }
    if method == Method::GET
        && path.starts_with("/control/v1/requests/")
        && path.ends_with("/audit")
    {
        return AUDIT_CONTENT_TIMEOUT;
    }
    REQUEST_TIMEOUT
}

fn control_body_limit(path: &str) -> usize {
    if path.starts_with("/control/v1/requests/") && path.ends_with("/audit") {
        MAX_AUDIT_CONTENT_BODY
    } else {
        MAX_CONTROL_BODY
    }
}

fn endpoint_record(etag: Option<String>, body: &[u8]) -> Result<EndpointRecordResponse, String> {
    let etag = etag.ok_or_else(|| "Core endpoint response omitted ETag".to_string())?;
    validate_etag(&etag)?;
    let endpoint = serde_json::from_slice(body)
        .map_err(|error| format!("endpoint response returned invalid JSON: {error}"))?;
    Ok(EndpointRecordResponse { endpoint, etag })
}

fn policy_record(etag: Option<String>, body: &[u8]) -> Result<PolicyRecordResponse, String> {
    let etag = etag.ok_or_else(|| "Core policy response omitted ETag".to_string())?;
    validate_strong_etag(&etag)?;
    let policy = parse_privacy_policy(body)?;
    Ok(PolicyRecordResponse { policy, etag })
}

fn parse_privacy_policy_page(body: &[u8]) -> Result<serde_json::Value, String> {
    let page: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("policy list returned invalid JSON: {error}"))?;
    validate_exact_object_keys(&page, &["items", "next_cursor"], "policy list")?;
    let object = page
        .as_object()
        .ok_or_else(|| "policy list must be an object".to_string())?;
    let items = object["items"]
        .as_array()
        .ok_or_else(|| "policy list items must be an array".to_string())?;
    if items.len() != 1 {
        return Err("policy list must contain the singleton privacy policy".to_string());
    }
    if object["next_cursor"] != serde_json::Value::Null {
        return Err("policy list next_cursor must be null".to_string());
    }
    for item in items {
        validate_privacy_policy_value(item)?;
    }
    Ok(page)
}

fn parse_privacy_policy(body: &[u8]) -> Result<serde_json::Value, String> {
    let policy: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("policy response returned invalid JSON: {error}"))?;
    validate_privacy_policy_value(&policy)?;
    Ok(policy)
}

fn validate_privacy_policy_value(policy: &serde_json::Value) -> Result<(), String> {
    validate_exact_object_keys(
        policy,
        &[
            "id",
            "name",
            "enabled",
            "priority",
            "detector",
            "local_model_id",
            "match",
            "request_action",
            "response_action",
            "response_restore",
        ],
        "privacy policy",
    )?;
    let object = policy
        .as_object()
        .ok_or_else(|| "privacy policy must be an object".to_string())?;
    if object["id"] != "policy_privacy_default"
        || object["name"] != "隐私保护"
        || object["priority"] != 0
    {
        return Err("Core returned an unexpected privacy policy singleton".to_string());
    }
    if !object["enabled"].is_boolean() {
        return Err("privacy policy enabled must be a boolean".to_string());
    }
    if !object["response_restore"].is_boolean() {
        return Err("privacy policy response_restore must be a boolean".to_string());
    }
    validate_privacy_detector(&object["detector"])?;
    let local_model_id = if let Some(local_model_id) = object["local_model_id"].as_str() {
        validate_privacy_model_id(local_model_id)?;
        Some(local_model_id)
    } else if object["local_model_id"].is_null() {
        None
    } else {
        return Err("privacy policy local_model_id must be an installation ID or null".to_string());
    };
    match (object["detector"].as_str(), local_model_id) {
        (Some("regex"), None) | (Some("local_model"), Some(_)) => {}
        _ => return Err("privacy policy detector and local_model_id are inconsistent".to_string()),
    }
    validate_privacy_action(&object["request_action"])?;
    validate_privacy_action(&object["response_action"])?;
    if object["response_action"] != "allow" {
        return Err("privacy policy response_action must remain allow".to_string());
    }
    validate_exact_object_keys(&object["match"], &[], "privacy policy match")?;
    Ok(())
}

fn validate_privacy_policy_patch(patch: serde_json::Value) -> Result<serde_json::Value, String> {
    let object = patch
        .as_object()
        .ok_or_else(|| "privacy policy patch must be an object".to_string())?;
    if object.is_empty() {
        return Err("privacy policy patch must change at least one field".to_string());
    }
    for (field, value) in object {
        match field.as_str() {
            "enabled" if value.is_boolean() => {}
            "detector" => validate_privacy_detector(value)?,
            "local_model_id" if value.is_null() => {}
            "local_model_id" => {
                let local_model_id = value.as_str().ok_or_else(|| {
                    "privacy policy local_model_id patch must be an installation ID or null"
                        .to_string()
                })?;
                validate_privacy_model_id(local_model_id)?;
            }
            "request_action" => validate_privacy_action(value)?,
            "response_restore" if value.is_boolean() => {}
            "enabled" => {
                return Err("privacy policy enabled patch must be a boolean".to_string());
            }
            "response_restore" => {
                return Err("privacy policy response_restore patch must be a boolean".to_string());
            }
            _ => {
                return Err(format!(
                    "privacy policy patch contains unexpected field {field}"
                ))
            }
        }
    }
    Ok(patch)
}

fn validate_privacy_dry_run_input(input: serde_json::Value) -> Result<serde_json::Value, String> {
    let object = input
        .as_object()
        .ok_or_else(|| "privacy policy dry-run must be an object".to_string())?;
    for key in object.keys() {
        match key.as_str() {
            "protocol" | "sample_text" | "policy" => {}
            other => {
                return Err(format!(
                    "privacy policy dry-run contains unexpected field {other}"
                ));
            }
        }
    }
    for required in ["protocol", "sample_text"] {
        if !object.contains_key(required) {
            return Err(format!("privacy policy dry-run omitted {required}"));
        }
    }
    let protocol = object["protocol"]
        .as_str()
        .ok_or_else(|| "privacy policy dry-run protocol must be a string".to_string())?;
    match protocol {
        "openai.chat"
        | "openai.completions"
        | "openai.responses"
        | "openai.responses.compact"
        | "anthropic.messages"
        | "google.generate_content" => {}
        _ => {
            return Err("privacy policy dry-run protocol is unsupported".to_string());
        }
    }
    let sample = object["sample_text"]
        .as_str()
        .ok_or_else(|| "privacy policy dry-run sample_text must be a string".to_string())?;
    const MAX_DRY_RUN_SAMPLE_BYTES: usize = 256 * 1024;
    if sample.is_empty() || sample.len() > MAX_DRY_RUN_SAMPLE_BYTES {
        return Err(format!(
            "privacy policy dry-run sample_text must contain 1 to {MAX_DRY_RUN_SAMPLE_BYTES} bytes"
        ));
    }
    if let Some(policy) = object.get("policy") {
        validate_privacy_policy_patch(policy.clone())?;
    }
    Ok(input)
}

fn parse_privacy_dry_run_result(body: &[u8]) -> Result<serde_json::Value, String> {
    let result: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("policy dry-run returned invalid JSON: {error}"))?;
    let object = result
        .as_object()
        .ok_or_else(|| "privacy policy dry-run result must be an object".to_string())?;
    for key in object.keys() {
        match key.as_str() {
            "decision" | "findings_summary" | "findings" | "inspected_body" | "redacted_body"
            | "redactions" => {}
            other => {
                return Err(format!(
                    "privacy policy dry-run result contains unexpected field {other}"
                ));
            }
        }
    }
    for required in ["decision", "findings_summary", "findings", "inspected_body"] {
        if !object.contains_key(required) {
            return Err(format!("privacy policy dry-run result omitted {required}"));
        }
    }
    validate_privacy_action(&object["decision"])?;
    if !object["findings_summary"].is_string() {
        return Err("privacy policy dry-run findings_summary must be a string".to_string());
    }
    if !object["inspected_body"].is_string() {
        return Err("privacy policy dry-run inspected_body must be a string".to_string());
    }
    if let Some(redacted) = object.get("redacted_body") {
        if !redacted.is_string() {
            return Err("privacy policy dry-run redacted_body must be a string".to_string());
        }
    }
    if let Some(redactions) = object.get("redactions") {
        let redactions = redactions
            .as_array()
            .ok_or_else(|| "privacy policy dry-run redactions must be an array".to_string())?;
        if redactions.len() > 4096 {
            return Err("privacy policy dry-run redactions exceed the limit".to_string());
        }
        for redaction in redactions {
            validate_exact_object_keys(
                redaction,
                &["placeholder", "kind", "value"],
                "privacy policy dry-run redaction",
            )?;
            let redaction = redaction
                .as_object()
                .ok_or_else(|| "privacy policy dry-run redaction must be an object".to_string())?;
            if redaction["placeholder"]
                .as_str()
                .filter(|placeholder| !placeholder.is_empty())
                .is_none()
            {
                return Err(
                    "privacy policy dry-run redaction placeholder must be a non-empty string"
                        .to_string(),
                );
            }
            let kind = redaction["kind"].as_str().ok_or_else(|| {
                "privacy policy dry-run redaction kind must be a string".to_string()
            })?;
            validate_privacy_dry_run_kind(kind)?;
            if !redaction["value"].is_string() {
                return Err("privacy policy dry-run redaction value must be a string".to_string());
            }
        }
    }
    let findings = object["findings"]
        .as_array()
        .ok_or_else(|| "privacy policy dry-run findings must be an array".to_string())?;
    if findings.len() > 4096 {
        return Err("privacy policy dry-run findings exceed the limit".to_string());
    }
    for finding in findings {
        validate_exact_object_keys(
            finding,
            &["kind", "path", "start", "end"],
            "privacy policy dry-run finding",
        )?;
        let finding = finding
            .as_object()
            .ok_or_else(|| "privacy policy dry-run finding must be an object".to_string())?;
        let kind = finding["kind"]
            .as_str()
            .ok_or_else(|| "privacy policy dry-run finding kind must be a string".to_string())?;
        validate_privacy_dry_run_kind(kind)?;
        if finding["path"]
            .as_str()
            .filter(|path| !path.is_empty())
            .is_none()
        {
            return Err(
                "privacy policy dry-run finding path must be a non-empty string".to_string(),
            );
        }
        let start = finding["start"]
            .as_u64()
            .ok_or_else(|| "privacy policy dry-run finding start must be an integer".to_string())?;
        let end = finding["end"]
            .as_u64()
            .ok_or_else(|| "privacy policy dry-run finding end must be an integer".to_string())?;
        if end <= start {
            return Err(
                "privacy policy dry-run finding end must be greater than start".to_string(),
            );
        }
    }
    Ok(result)
}

fn validate_privacy_dry_run_kind(kind: &str) -> Result<(), String> {
    match kind {
        "email" | "phone" | "account" | "payment_card" | "ip_address" | "url" | "common_secret"
        | "private_address" | "private_date" | "private_person" => Ok(()),
        _ => Err("privacy policy dry-run kind is unknown".to_string()),
    }
}

fn validate_privacy_detector(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("regex" | "local_model") => Ok(()),
        _ => Err("privacy policy detector is invalid".to_string()),
    }
}

fn validate_privacy_action(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("allow" | "warn" | "block" | "redact") => Ok(()),
        _ => Err("privacy policy action is invalid".to_string()),
    }
}

fn parse_privacy_model_catalog(body: &[u8]) -> Result<serde_json::Value, String> {
    let catalog: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("privacy model catalog returned invalid JSON: {error}"))?;
    validate_exact_object_keys(&catalog, &["items"], "privacy model catalog")?;
    let items = catalog["items"]
        .as_array()
        .ok_or_else(|| "privacy model catalog items must be an array".to_string())?;
    if items.len() > 100 {
        return Err("privacy model catalog contains too many items".to_string());
    }
    let mut ids = HashSet::new();
    for item in items {
        validate_privacy_catalog_model(item)?;
        let id = item["id"].as_str().expect("validated catalog ID");
        if !ids.insert(id) {
            return Err("privacy model catalog contains duplicate IDs".to_string());
        }
    }
    Ok(catalog)
}

fn parse_privacy_model_probe(body: &[u8]) -> Result<serde_json::Value, String> {
    let probe: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("privacy model probe returned invalid JSON: {error}"))?;
    validate_exact_object_keys(
        &probe,
        &[
            "repo_id",
            "requested_revision",
            "revision",
            "name",
            "license",
            "languages",
            "adapter",
            "variants",
            "labels",
            "requires_label_mapping",
        ],
        "privacy model probe",
    )?;
    let object = probe
        .as_object()
        .ok_or_else(|| "privacy model probe must be an object".to_string())?;
    validate_repo_id(&object["repo_id"], "privacy model probe repo_id")?;
    validate_requested_revision(
        &object["requested_revision"],
        "privacy model probe requested_revision",
    )?;
    validate_commit(&object["revision"], "privacy model probe revision")?;
    validate_metadata_string(&object["name"], 1, 128, "privacy model probe name")?;
    if !object["license"].is_null() {
        validate_metadata_string(&object["license"], 1, 64, "privacy model probe license")?;
    }
    validate_string_array(&object["languages"], 32, "privacy model probe languages")?;
    validate_privacy_model_adapter(&object["adapter"])?;
    validate_privacy_model_variants(&object["variants"])?;
    let labels = object["labels"]
        .as_array()
        .ok_or_else(|| "privacy model probe labels must be an array".to_string())?;
    if labels.is_empty() || labels.len() > 256 {
        return Err("privacy model probe must contain 1 to 256 labels".to_string());
    }
    let mut seen = HashSet::new();
    let mut requires_label_mapping = false;
    for label in labels {
        validate_exact_object_keys(
            label,
            &["label", "suggested_kind"],
            "privacy model probe label",
        )?;
        let source_label = validate_model_label(&label["label"], "privacy model probe label name")?;
        if !seen.insert(source_label) {
            return Err("privacy model probe contains duplicate labels".to_string());
        }
        if label["suggested_kind"].is_null() {
            requires_label_mapping = true;
        } else {
            validate_canonical_privacy_kind(&label["suggested_kind"])?;
        }
    }
    match object["requires_label_mapping"].as_bool() {
        Some(value) if value == requires_label_mapping => {}
        Some(_) => {
            return Err("privacy model probe requires_label_mapping is inconsistent".to_string())
        }
        None => {
            return Err("privacy model probe requires_label_mapping must be boolean".to_string())
        }
    }
    Ok(probe)
}

fn parse_privacy_model_installation_list(body: &[u8]) -> Result<serde_json::Value, String> {
    let list: serde_json::Value = serde_json::from_slice(body).map_err(|error| {
        format!("privacy model installation list returned invalid JSON: {error}")
    })?;
    validate_exact_object_keys(&list, &["items"], "privacy model installation list")?;
    let items = list["items"]
        .as_array()
        .ok_or_else(|| "privacy model installation items must be an array".to_string())?;
    if items.len() > 100 {
        return Err("privacy model installation list contains too many items".to_string());
    }
    let mut ids = HashSet::new();
    for item in items {
        validate_privacy_model_installation(item)?;
        let id = item["id"].as_str().expect("validated installation ID");
        if !ids.insert(id) {
            return Err("privacy model installation list contains duplicate IDs".to_string());
        }
    }
    Ok(list)
}

fn parse_privacy_model_installation(body: &[u8]) -> Result<serde_json::Value, String> {
    let installation: serde_json::Value = serde_json::from_slice(body)
        .map_err(|error| format!("privacy model installation returned invalid JSON: {error}"))?;
    validate_privacy_model_installation(&installation)?;
    Ok(installation)
}

fn validate_privacy_model_probe_input(
    input: serde_json::Value,
) -> Result<serde_json::Value, String> {
    validate_exact_object_keys(
        &input,
        &["repo_id", "revision"],
        "privacy model probe input",
    )?;
    validate_repo_id(&input["repo_id"], "privacy model probe repo_id")?;
    validate_requested_revision(&input["revision"], "privacy model probe revision")?;
    Ok(input)
}

fn validate_privacy_model_install_input(
    input: serde_json::Value,
) -> Result<serde_json::Value, String> {
    validate_exact_object_keys(
        &input,
        &["repo_id", "revision", "variant_id", "label_mapping"],
        "privacy model install input",
    )?;
    validate_repo_id(&input["repo_id"], "privacy model install repo_id")?;
    validate_commit(&input["revision"], "privacy model install revision")?;
    validate_variant_id(&input["variant_id"], "privacy model install variant_id")?;
    validate_label_mapping(&input["label_mapping"])?;
    Ok(input)
}

fn validate_privacy_catalog_model(model: &serde_json::Value) -> Result<(), String> {
    validate_exact_object_keys(
        model,
        &[
            "id",
            "name",
            "summary",
            "source",
            "repo_id",
            "revision",
            "license",
            "languages",
            "adapter",
            "variants",
        ],
        "privacy catalog model",
    )?;
    let object = model
        .as_object()
        .ok_or_else(|| "privacy catalog model must be an object".to_string())?;
    validate_privacy_catalog_id(
        object["id"]
            .as_str()
            .ok_or_else(|| "privacy catalog model id must be a string".to_string())?,
    )?;
    validate_metadata_string(&object["name"], 1, 128, "privacy catalog model name")?;
    validate_metadata_string(&object["summary"], 1, 512, "privacy catalog model summary")?;
    if !matches!(object["source"].as_str(), Some("official" | "community")) {
        return Err("privacy catalog model source is invalid".to_string());
    }
    validate_repo_id(&object["repo_id"], "privacy catalog model repo_id")?;
    validate_commit(&object["revision"], "privacy catalog model revision")?;
    validate_metadata_string(&object["license"], 1, 64, "privacy catalog model license")?;
    validate_string_array(&object["languages"], 32, "privacy catalog model languages")?;
    validate_privacy_model_adapter(&object["adapter"])?;
    validate_privacy_model_variants(&object["variants"])
}

fn validate_privacy_model_variants(value: &serde_json::Value) -> Result<(), String> {
    let variants = value
        .as_array()
        .ok_or_else(|| "privacy model variants must be an array".to_string())?;
    if variants.is_empty() || variants.len() > 32 {
        return Err("privacy model must contain 1 to 32 variants".to_string());
    }
    let mut ids = HashSet::new();
    for variant in variants {
        validate_exact_object_keys(
            variant,
            &[
                "id",
                "name",
                "quantization",
                "bytes_total",
                "estimated_ram_bytes",
                "recommended",
                "supported",
                "unsupported_reason",
            ],
            "privacy model variant",
        )?;
        let id = validate_variant_id(&variant["id"], "privacy model variant id")?;
        if !ids.insert(id) {
            return Err("privacy model contains duplicate variant IDs".to_string());
        }
        validate_metadata_string(&variant["name"], 1, 64, "privacy model variant name")?;
        validate_metadata_string(
            &variant["quantization"],
            1,
            32,
            "privacy model variant quantization",
        )?;
        let bytes_total =
            safe_json_integer(&variant["bytes_total"], "privacy model variant bytes_total")?;
        safe_json_integer(
            &variant["estimated_ram_bytes"],
            "privacy model variant estimated_ram_bytes",
        )?;
        if !variant["recommended"].is_boolean() || !variant["supported"].is_boolean() {
            return Err("privacy model variant flags must be boolean".to_string());
        }
        let supported = variant["supported"].as_bool().expect("validated boolean");
        let reason = match variant["unsupported_reason"].as_str() {
            Some("cpu_only") => Some("cpu_only"),
            None if variant["unsupported_reason"].is_null() => None,
            _ => return Err("privacy model variant unsupported_reason is invalid".to_string()),
        };
        if supported != reason.is_none() {
            return Err("privacy model variant support fields are inconsistent".to_string());
        }
        if supported && bytes_total == 0 {
            return Err("supported privacy model variant must have content".to_string());
        }
    }
    Ok(())
}

fn validate_privacy_model_installation(installation: &serde_json::Value) -> Result<(), String> {
    validate_exact_object_keys(
        installation,
        &[
            "id",
            "source",
            "catalog_id",
            "catalog_source",
            "name",
            "license",
            "languages",
            "repo_id",
            "revision",
            "variant_id",
            "variant_name",
            "quantization",
            "adapter",
            "status",
            "bytes_downloaded",
            "bytes_total",
            "estimated_ram_bytes",
            "error",
            "label_mapping",
            "installed_at",
        ],
        "privacy model installation",
    )?;
    let object = installation
        .as_object()
        .ok_or_else(|| "privacy model installation must be an object".to_string())?;
    validate_privacy_model_id(
        object["id"]
            .as_str()
            .ok_or_else(|| "privacy model installation id must be a string".to_string())?,
    )?;
    let source = object["source"]
        .as_str()
        .ok_or_else(|| "privacy model installation source must be a string".to_string())?;
    let catalog_id = object["catalog_id"].as_str();
    let catalog_source = object["catalog_source"].as_str();
    match (source, catalog_id, catalog_source) {
        ("catalog", Some(id), Some("official" | "community")) => validate_privacy_catalog_id(id)?,
        ("custom", None, None)
            if object["catalog_id"].is_null() && object["catalog_source"].is_null() => {}
        _ => {
            return Err("privacy model installation catalog provenance is inconsistent".to_string())
        }
    }
    validate_metadata_string(&object["name"], 1, 128, "privacy model installation name")?;
    if !object["license"].is_null() {
        validate_metadata_string(
            &object["license"],
            1,
            64,
            "privacy model installation license",
        )?;
    }
    validate_string_array(
        &object["languages"],
        32,
        "privacy model installation languages",
    )?;
    validate_repo_id(&object["repo_id"], "privacy model installation repo_id")?;
    validate_commit(&object["revision"], "privacy model installation revision")?;
    validate_variant_id(
        &object["variant_id"],
        "privacy model installation variant_id",
    )?;
    validate_metadata_string(
        &object["variant_name"],
        1,
        64,
        "privacy model installation variant_name",
    )?;
    validate_metadata_string(
        &object["quantization"],
        1,
        32,
        "privacy model installation quantization",
    )?;
    validate_privacy_model_adapter(&object["adapter"])?;
    let status = object["status"]
        .as_str()
        .ok_or_else(|| "privacy model installation status must be a string".to_string())?;
    if !matches!(status, "downloading" | "ready" | "error") {
        return Err("privacy model installation status is invalid".to_string());
    }
    let downloaded = safe_json_integer(
        &object["bytes_downloaded"],
        "privacy model installation bytes_downloaded",
    )?;
    let total = safe_json_integer(
        &object["bytes_total"],
        "privacy model installation bytes_total",
    )?;
    safe_json_integer(
        &object["estimated_ram_bytes"],
        "privacy model installation estimated_ram_bytes",
    )?;
    if downloaded > total {
        return Err("privacy model installation resource fields are inconsistent".to_string());
    }
    let error = match object["error"].as_str() {
        Some("download_failed" | "integrity_failed" | "incompatible_model") => {
            object["error"].as_str()
        }
        None if object["error"].is_null() => None,
        _ => return Err("privacy model installation error is invalid".to_string()),
    };
    let installed_at = match object["installed_at"].as_str() {
        Some(_) => Some(validate_rfc3339_timestamp(
            &object["installed_at"],
            "privacy model installation installed_at",
        )?),
        None if object["installed_at"].is_null() => None,
        _ => return Err("privacy model installation installed_at is invalid".to_string()),
    };
    match status {
        "downloading" if error.is_none() && installed_at.is_none() => {}
        "ready"
            if error.is_none() && installed_at.is_some() && total > 0 && downloaded == total => {}
        "error" if error.is_some() && installed_at.is_none() => {}
        _ => {
            return Err("privacy model installation lifecycle fields are inconsistent".to_string())
        }
    }
    validate_label_mapping(&object["label_mapping"])
}

fn validate_label_mapping(value: &serde_json::Value) -> Result<(), String> {
    let mapping = value
        .as_object()
        .ok_or_else(|| "privacy model label_mapping must be an object".to_string())?;
    if mapping.len() > 256 {
        return Err("privacy model label_mapping contains too many entries".to_string());
    }
    for (label, kind) in mapping {
        if !valid_model_label(label) {
            return Err("privacy model label_mapping contains an invalid label".to_string());
        }
        if !kind.is_null() {
            validate_canonical_privacy_kind(kind)?;
        }
    }
    Ok(())
}

fn validate_canonical_privacy_kind(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some(
            "email" | "phone" | "account" | "payment_card" | "ip_address" | "url" | "common_secret"
            | "private_address" | "private_date" | "private_person",
        ) => Ok(()),
        _ => Err("privacy model canonical label kind is invalid".to_string()),
    }
}

fn validate_privacy_model_adapter(value: &serde_json::Value) -> Result<(), String> {
    match value.as_str() {
        Some("openai_bioes_viterbi" | "hf_token_classification") => Ok(()),
        _ => Err("privacy model adapter is invalid".to_string()),
    }
}

fn validate_repo_id(value: &serde_json::Value, field: &str) -> Result<(), String> {
    let repo_id = validate_string(value, 3, 193, field)?;
    let mut parts = repo_id.split('/');
    let owner = parts.next().unwrap_or_default();
    let repository = parts.next().unwrap_or_default();
    if parts.next().is_some()
        || repo_id.contains("..")
        || !valid_hugging_face_name(owner)
        || !valid_hugging_face_name(repository)
    {
        return Err(format!("{field} is invalid"));
    }
    Ok(())
}

fn valid_hugging_face_name(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 96
        && value
            .bytes()
            .next()
            .is_some_and(|byte| byte.is_ascii_alphanumeric())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
}

fn validate_commit(value: &serde_json::Value, field: &str) -> Result<(), String> {
    let revision = validate_string(value, 40, 40, field)?;
    if !revision
        .bytes()
        .all(|byte| byte.is_ascii_digit() || matches!(byte, b'a'..=b'f'))
    {
        return Err(format!("{field} must be a lowercase 40-character commit"));
    }
    Ok(())
}

fn validate_requested_revision(value: &serde_json::Value, field: &str) -> Result<(), String> {
    let revision = validate_string(value, 1, 128, field)?;
    let valid_characters = revision.bytes().enumerate().all(|(index, byte)| {
        byte.is_ascii_alphanumeric() || (index > 0 && matches!(byte, b'.' | b'_' | b'/' | b'-'))
    });
    if !valid_characters
        || revision.contains("..")
        || revision.contains("//")
        || revision.ends_with('/')
    {
        return Err(format!("{field} is invalid"));
    }
    Ok(())
}

fn validate_privacy_model_id(value: &str) -> Result<(), String> {
    let Some(suffix) = value.strip_prefix("model_") else {
        return Err("privacy model installation id is invalid".to_string());
    };
    if suffix.len() != 32
        || !suffix
            .bytes()
            .all(|byte| byte.is_ascii_digit() || matches!(byte, b'a'..=b'f'))
    {
        return Err("privacy model installation id is invalid".to_string());
    }
    Ok(())
}

fn validate_privacy_catalog_id(value: &str) -> Result<(), String> {
    let Some(suffix) = value.strip_prefix("catalog_") else {
        return Err("privacy model catalog id is invalid".to_string());
    };
    if !(3..=80).contains(&suffix.len())
        || !suffix
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
    {
        return Err("privacy model catalog id is invalid".to_string());
    }
    Ok(())
}

fn validate_variant_id<'a>(value: &'a serde_json::Value, field: &str) -> Result<&'a str, String> {
    let variant = validate_string(value, 2, 64, field)?;
    if !variant
        .bytes()
        .next()
        .is_some_and(|byte| byte.is_ascii_lowercase())
        || !variant
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
    {
        return Err(format!("{field} is invalid"));
    }
    Ok(variant)
}

fn validate_model_label<'a>(value: &'a serde_json::Value, field: &str) -> Result<&'a str, String> {
    let label = validate_string(value, 1, 128, field)?;
    if !valid_model_label(label) {
        return Err(format!("{field} is invalid"));
    }
    Ok(label)
}

fn valid_model_label(value: &str) -> bool {
    value
        .bytes()
        .next()
        .is_some_and(|byte| byte.is_ascii_alphanumeric())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'.' | b'-'))
}

fn validate_string_array(
    value: &serde_json::Value,
    maximum_items: usize,
    field: &str,
) -> Result<(), String> {
    let values = value
        .as_array()
        .ok_or_else(|| format!("{field} must be an array"))?;
    if values.len() > maximum_items {
        return Err(format!("{field} contains too many values"));
    }
    let mut seen = HashSet::new();
    for value in values {
        let parsed = validate_metadata_string(value, 1, 64, field)?;
        if !seen.insert(parsed) {
            return Err(format!("{field} contains duplicate values"));
        }
    }
    Ok(())
}

fn validate_string<'a>(
    value: &'a serde_json::Value,
    minimum: usize,
    maximum: usize,
    field: &str,
) -> Result<&'a str, String> {
    let text = value
        .as_str()
        .ok_or_else(|| format!("{field} must be a string"))?;
    if !(minimum..=maximum).contains(&text.chars().count()) {
        return Err(format!(
            "{field} must contain {minimum} through {maximum} characters"
        ));
    }
    Ok(text)
}

fn validate_metadata_string<'a>(
    value: &'a serde_json::Value,
    minimum: usize,
    maximum: usize,
    field: &str,
) -> Result<&'a str, String> {
    let text = validate_string(value, minimum, maximum, field)?;
    if text.trim() != text || text.chars().any(char::is_control) {
        return Err(format!(
            "{field} must be trimmed and contain no control characters"
        ));
    }
    Ok(text)
}

fn validate_rfc3339_timestamp<'a>(
    value: &'a serde_json::Value,
    field: &str,
) -> Result<&'a str, String> {
    let timestamp = validate_string(value, 20, 35, field)?;
    let bytes = timestamp.as_bytes();
    let invalid = || format!("{field} must be a valid RFC3339 timestamp");
    if !timestamp.is_ascii()
        || bytes.get(4) != Some(&b'-')
        || bytes.get(7) != Some(&b'-')
        || bytes.get(10) != Some(&b'T')
        || bytes.get(13) != Some(&b':')
        || bytes.get(16) != Some(&b':')
        || !bytes[0..4].iter().all(u8::is_ascii_digit)
        || !bytes[5..7].iter().all(u8::is_ascii_digit)
        || !bytes[8..10].iter().all(u8::is_ascii_digit)
        || !bytes[11..13].iter().all(u8::is_ascii_digit)
        || !bytes[14..16].iter().all(u8::is_ascii_digit)
        || !bytes[17..19].iter().all(u8::is_ascii_digit)
    {
        return Err(invalid());
    }
    let parse = |range: std::ops::Range<usize>| {
        std::str::from_utf8(&bytes[range])
            .ok()
            .and_then(|part| part.parse::<u32>().ok())
    };
    let year = parse(0..4).ok_or_else(&invalid)?;
    let month = parse(5..7).ok_or_else(&invalid)?;
    let day = parse(8..10).ok_or_else(&invalid)?;
    let hour = parse(11..13).ok_or_else(&invalid)?;
    let minute = parse(14..16).ok_or_else(&invalid)?;
    let second = parse(17..19).ok_or_else(&invalid)?;
    let leap_year = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    let days_in_month = match month {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        2 if leap_year => 29,
        2 => 28,
        _ => return Err(invalid()),
    };
    if day == 0 || day > days_in_month || hour > 23 || minute > 59 || second > 59 {
        return Err(invalid());
    }

    let mut cursor = 19;
    if bytes.get(cursor) == Some(&b'.') {
        cursor += 1;
        let fraction_start = cursor;
        while bytes.get(cursor).is_some_and(u8::is_ascii_digit) {
            cursor += 1;
        }
        if cursor == fraction_start || cursor - fraction_start > 9 {
            return Err(invalid());
        }
    }
    match bytes.get(cursor) {
        Some(b'Z') if cursor + 1 == bytes.len() => {}
        Some(b'+' | b'-')
            if cursor + 6 == bytes.len()
                && bytes.get(cursor + 3) == Some(&b':')
                && bytes[cursor + 1..cursor + 3].iter().all(u8::is_ascii_digit)
                && bytes[cursor + 4..cursor + 6].iter().all(u8::is_ascii_digit) =>
        {
            let offset_hour = std::str::from_utf8(&bytes[cursor + 1..cursor + 3])
                .ok()
                .and_then(|part| part.parse::<u32>().ok())
                .ok_or_else(&invalid)?;
            let offset_minute = std::str::from_utf8(&bytes[cursor + 4..cursor + 6])
                .ok()
                .and_then(|part| part.parse::<u32>().ok())
                .ok_or_else(&invalid)?;
            if offset_hour > 23 || offset_minute > 59 {
                return Err(invalid());
            }
        }
        _ => return Err(invalid()),
    }
    Ok(timestamp)
}

fn safe_json_integer(value: &serde_json::Value, field: &str) -> Result<u64, String> {
    const MAX_SAFE_INTEGER: u64 = 9_007_199_254_740_991;
    let parsed = value
        .as_u64()
        .ok_or_else(|| format!("{field} must be a non-negative integer"))?;
    if parsed > MAX_SAFE_INTEGER {
        return Err(format!("{field} exceeds the IPC safe integer range"));
    }
    Ok(parsed)
}

fn validate_exact_object_keys(
    value: &serde_json::Value,
    expected: &[&str],
    context: &str,
) -> Result<(), String> {
    let object = value
        .as_object()
        .ok_or_else(|| format!("{context} must be an object"))?;
    if object.len() != expected.len() {
        return Err(format!("{context} has missing or unexpected fields"));
    }
    for field in expected {
        if !object.contains_key(*field) {
            return Err(format!("{context} omitted {field}"));
        }
    }
    Ok(())
}

fn validate_resource_id(value: &str) -> Result<(), String> {
    if !(3..=96).contains(&value.len())
        || !value.bytes().enumerate().all(|(index, byte)| match byte {
            b'a'..=b'z' => true,
            b'0'..=b'9' | b'_' | b'-' => index > 0,
            _ => false,
        })
    {
        return Err("endpoint id is invalid".to_string());
    }
    Ok(())
}

fn percent_encode_query(value: &str) -> String {
    let mut encoded = String::with_capacity(value.len());
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'~' => {
                encoded.push(byte as char);
            }
            _ => {
                write!(&mut encoded, "%{byte:02X}").expect("writing to a String cannot fail");
            }
        }
    }
    encoded
}

fn timestamp_query_charset_ok(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|byte| {
            matches!(
                byte,
                b'0'..=b'9' | b'T' | b'Z' | b'z' | b':' | b'+' | b'.' | b'-'
            )
        })
}

fn build_request_record_query(query: &serde_json::Value) -> Result<String, String> {
    if query.is_null() {
        return Ok(String::new());
    }
    let object = query
        .as_object()
        .ok_or_else(|| "request record query must be an object".to_string())?;

    let mut limit: Option<u64> = None;
    let mut cursor: Option<&str> = None;
    let mut from: Option<&str> = None;
    let mut to: Option<&str> = None;
    let mut protocol: Option<&str> = None;
    let mut endpoint_id: Option<&str> = None;
    let mut status: Option<&str> = None;

    for (key, value) in object {
        match key.as_str() {
            "limit" => {
                let parsed = value
                    .as_u64()
                    .ok_or_else(|| "request record query limit must be an integer".to_string())?;
                if !(1..=200).contains(&parsed) {
                    return Err("request record query limit must be between 1 and 200".to_string());
                }
                limit = Some(parsed);
            }
            "cursor" => {
                let text = value
                    .as_str()
                    .ok_or_else(|| "request record query cursor must be a string".to_string())?;
                if text.is_empty() || text.len() > 512 {
                    return Err(
                        "request record query cursor must be 1 to 512 characters".to_string()
                    );
                }
                cursor = Some(text);
            }
            "from" => {
                let text = value
                    .as_str()
                    .ok_or_else(|| "request record query from must be a string".to_string())?;
                if !timestamp_query_charset_ok(text) {
                    return Err(
                        "request record query from has an invalid timestamp charset".to_string()
                    );
                }
                from = Some(text);
            }
            "to" => {
                let text = value
                    .as_str()
                    .ok_or_else(|| "request record query to must be a string".to_string())?;
                if !timestamp_query_charset_ok(text) {
                    return Err(
                        "request record query to has an invalid timestamp charset".to_string()
                    );
                }
                to = Some(text);
            }
            "protocol" => {
                let text = value
                    .as_str()
                    .ok_or_else(|| "request record query protocol must be a string".to_string())?;
                if text.is_empty()
                    || text.len() > 64
                    || !text.bytes().enumerate().all(|(index, byte)| match byte {
                        b'a'..=b'z' => true,
                        b'0'..=b'9' | b'_' => index > 0,
                        _ => false,
                    })
                {
                    return Err("request record query protocol is invalid".to_string());
                }
                protocol = Some(text);
            }
            "endpoint_id" => {
                let text = value.as_str().ok_or_else(|| {
                    "request record query endpoint_id must be a string".to_string()
                })?;
                validate_resource_id(text)?;
                endpoint_id = Some(text);
            }
            "status" => {
                let text = value
                    .as_str()
                    .ok_or_else(|| "request record query status must be a string".to_string())?;
                match text {
                    "pending" | "succeeded" | "failed" | "cancelled" | "blocked" => {
                        status = Some(text);
                    }
                    _ => return Err("request record query status is invalid".to_string()),
                }
            }
            other => {
                return Err(format!("request record query contains unknown key {other}"));
            }
        }
    }

    let mut pairs = Vec::new();
    if let Some(value) = limit {
        pairs.push(format!("limit={value}"));
    }
    if let Some(value) = cursor {
        pairs.push(format!("cursor={}", percent_encode_query(value)));
    }
    if let Some(value) = from {
        pairs.push(format!("from={}", percent_encode_query(value)));
    }
    if let Some(value) = to {
        pairs.push(format!("to={}", percent_encode_query(value)));
    }
    if let Some(value) = protocol {
        pairs.push(format!("protocol={}", percent_encode_query(value)));
    }
    if let Some(value) = endpoint_id {
        pairs.push(format!("endpoint_id={}", percent_encode_query(value)));
    }
    if let Some(value) = status {
        pairs.push(format!("status={}", percent_encode_query(value)));
    }
    if pairs.is_empty() {
        return Ok(String::new());
    }
    Ok(format!("?{}", pairs.join("&")))
}

fn validate_purge_request_records_input(
    input: &serde_json::Value,
) -> Result<serde_json::Value, String> {
    let object = input
        .as_object()
        .ok_or_else(|| "request purge input must be an object".to_string())?;
    for key in object.keys() {
        match key.as_str() {
            "scope" | "confirm" | "before" => {}
            other => {
                return Err(format!("request purge input contains unknown key {other}"));
            }
        }
    }
    let scope = object
        .get("scope")
        .and_then(|value| value.as_str())
        .ok_or_else(|| "request purge input scope must be a string".to_string())?;
    let confirm = object
        .get("confirm")
        .ok_or_else(|| "request purge input omitted confirm".to_string())?;
    if confirm.as_bool() != Some(true) {
        return Err("request purge input confirm must be true".to_string());
    }
    match scope {
        "all" => {
            if object.contains_key("before") {
                return Err("request purge input before is forbidden when scope is all".to_string());
            }
            if object.len() != 2 {
                return Err("request purge input has missing or unexpected fields".to_string());
            }
        }
        "before" => {
            let before = object
                .get("before")
                .and_then(|value| value.as_str())
                .ok_or_else(|| {
                    "request purge input before is required when scope is before".to_string()
                })?;
            if !timestamp_query_charset_ok(before) {
                return Err(
                    "request purge input before has an invalid timestamp charset".to_string(),
                );
            }
            if object.len() != 3 {
                return Err("request purge input has missing or unexpected fields".to_string());
            }
        }
        _ => return Err("request purge input scope must be all or before".to_string()),
    }
    Ok(input.clone())
}

fn validate_audit_settings_patch(patch: &serde_json::Value) -> Result<(), String> {
    let object = patch
        .as_object()
        .ok_or_else(|| "audit settings patch must be an object".to_string())?;
    if object.is_empty() {
        return Err("audit settings patch must change at least one field".to_string());
    }

    let mut enabling_capture = false;
    for (key, value) in object {
        match key.as_str() {
            "request_body_enabled" | "response_content_enabled" => {
                let enabled = value
                    .as_bool()
                    .ok_or_else(|| format!("audit settings patch {key} must be a boolean"))?;
                if enabled {
                    enabling_capture = true;
                }
            }
            "request_body_max_bytes" => {
                let parsed = value.as_u64().ok_or_else(|| {
                    "audit settings patch request_body_max_bytes must be an integer".to_string()
                })?;
                if !(1024..=16_777_216).contains(&parsed) {
                    return Err(
                        "audit settings patch request_body_max_bytes must be between 1024 and 16777216"
                            .to_string(),
                    );
                }
            }
            "response_content_max_bytes" => {
                let parsed = value.as_u64().ok_or_else(|| {
                    "audit settings patch response_content_max_bytes must be an integer".to_string()
                })?;
                if !(1024..=67_108_864).contains(&parsed) {
                    return Err(
                        "audit settings patch response_content_max_bytes must be between 1024 and 67108864"
                            .to_string(),
                    );
                }
            }
            "metadata_retention_days" => {
                let parsed = value.as_u64().ok_or_else(|| {
                    "audit settings patch metadata_retention_days must be an integer".to_string()
                })?;
                if !(1..=3650).contains(&parsed) {
                    return Err(
                        "audit settings patch metadata_retention_days must be between 1 and 3650"
                            .to_string(),
                    );
                }
            }
            "content_retention_days" => {
                let parsed = value.as_u64().ok_or_else(|| {
                    "audit settings patch content_retention_days must be an integer".to_string()
                })?;
                if !(1..=365).contains(&parsed) {
                    return Err(
                        "audit settings patch content_retention_days must be between 1 and 365"
                            .to_string(),
                    );
                }
            }
            "audit_risk_acknowledged" => {
                if !value.is_boolean() {
                    return Err(
                        "audit settings patch audit_risk_acknowledged must be a boolean"
                            .to_string(),
                    );
                }
            }
            other => {
                return Err(format!("audit settings patch contains unknown key {other}"));
            }
        }
    }

    if enabling_capture
        && object
            .get("audit_risk_acknowledged")
            .and_then(|v| v.as_bool())
            != Some(true)
    {
        return Err("enabling body audit requires audit_risk_acknowledged=true".to_string());
    }
    Ok(())
}

fn validate_etag(value: &str) -> Result<(), String> {
    if value.len() < 3 || value.len() > 128 || !value.starts_with('"') || !value.ends_with('"') {
        return Err("endpoint ETag is invalid".to_string());
    }
    Ok(())
}

fn validate_strong_etag(value: &str) -> Result<(), String> {
    const PREFIX: &str = "\"sha256:";
    if value.len() != PREFIX.len() + 64 + 1
        || !value.starts_with(PREFIX)
        || !value.ends_with('"')
        || !value[PREFIX.len()..value.len() - 1]
            .bytes()
            .all(|byte| byte.is_ascii_digit() || matches!(byte, b'a'..=b'f'))
    {
        return Err("policy ETag is invalid".to_string());
    }
    Ok(())
}

fn parse_ready_announcement(line: &str) -> Result<ReadyAnnouncement, String> {
    let mut ready: ReadyAnnouncement = serde_json::from_str(line)
        .map_err(|error| format!("invalid astrlink-core ready signal: {error}"))?;

    if ready.event != "ready" {
        return Err(format!(
            "unexpected astrlink-core event {:?}; expected \"ready\"",
            ready.event
        ));
    }

    for (name, value) in [
        ("core_version", ready.core_version.as_str()),
        ("control_api_version", ready.control_api_version.as_str()),
        (
            "protocol_contract_version",
            ready.protocol_contract_version.as_str(),
        ),
    ] {
        if value.trim().is_empty() {
            return Err(format!("astrlink-core ready signal has empty {name}"));
        }
    }

    ready.inference_url = validate_loopback_url(&ready.inference_url, "inference_url")?;
    ready.control_url = validate_loopback_url(&ready.control_url, "control_url")?;
    Ok(ready)
}

fn validate_loopback_url(value: &str, field: &str) -> Result<String, String> {
    const PREFIX: &str = "http://127.0.0.1:";
    let port_text = value
        .strip_prefix(PREFIX)
        .ok_or_else(|| format!("{field} must be exactly http://127.0.0.1:<port>"))?;
    if port_text.is_empty()
        || !port_text.bytes().all(|byte| byte.is_ascii_digit())
        || (port_text.len() > 1 && port_text.starts_with('0'))
    {
        return Err(format!(
            "{field} must use a canonical decimal port from 1 through 65535"
        ));
    }
    let port = port_text
        .parse::<u16>()
        .map_err(|_| format!("{field} port must be between 1 and 65535"))?;
    if port == 0 {
        return Err(format!("{field} port must be between 1 and 65535"));
    }
    Ok(value.to_string())
}

fn verify_contract(
    ready: &ReadyAnnouncement,
    version: &VersionResponse,
    capabilities: &CapabilitiesResponse,
) -> Result<(), String> {
    if ready.control_api_version != SUPPORTED_CONTROL_API_VERSION {
        return Err(format!(
            "unsupported Core control API version {:?}; desktop supports {:?}",
            ready.control_api_version, SUPPORTED_CONTROL_API_VERSION
        ));
    }
    if ready.protocol_contract_version != SUPPORTED_PROTOCOL_CONTRACT_VERSION {
        return Err(format!(
            "unsupported Core protocol contract version {:?}; desktop supports {:?}",
            ready.protocol_contract_version, SUPPORTED_PROTOCOL_CONTRACT_VERSION
        ));
    }
    if ready.core_version != version.core_version {
        return Err("Core version changed between ready and version handshake".to_string());
    }
    if ready.control_api_version != version.control_api_version {
        return Err("control API version changed between ready and version handshake".to_string());
    }
    if ready.protocol_contract_version != version.protocol_contract_version
        || ready.protocol_contract_version != capabilities.protocol_contract_version
    {
        return Err("protocol contract version mismatch during Core handshake".to_string());
    }
    verify_alpha_capabilities(capabilities)?;
    Ok(())
}

fn verify_alpha_capabilities(capabilities: &CapabilitiesResponse) -> Result<(), String> {
    let engine = &capabilities.conversion_engine;
    if engine.name != "relaykit"
        || engine.version.is_some()
        || engine.available
        || !engine.edges.is_empty()
    {
        return Err("Core advertised local conversion that is unavailable in Alpha".to_string());
    }

    let expected_plans = [
        ("native", true, false),
        ("delegated", true, false),
        ("relaykit", false, true),
    ];
    if capabilities.plan_types.len() != expected_plans.len() {
        return Err("Core returned an unexpected Alpha plan registry".to_string());
    }
    for (id, available, local_conversion) in expected_plans {
        let matches: Vec<_> = capabilities
            .plan_types
            .iter()
            .filter(|plan| plan.id == id)
            .collect();
        if matches.len() != 1
            || matches[0].available_in_alpha != available
            || matches[0].uses_local_conversion != local_conversion
        {
            return Err(format!(
                "Core returned invalid Alpha plan semantics for {id}"
            ));
        }
    }

    let required_protocols = [
        ("openai.responses", true, true),
        ("openai.responses.compact", false, false),
        ("anthropic.messages", false, true),
        ("google.generate_content", false, true),
        ("openai.chat", false, true),
        ("openai.completions", false, true),
        ("openai.models", false, false),
        ("google.models", false, false),
    ];
    for (id, primary, streaming) in required_protocols {
        let matches: Vec<_> = capabilities
            .protocols
            .iter()
            .filter(|protocol| protocol.id == id)
            .collect();
        if matches.len() != 1
            || matches[0].phase != "alpha"
            || matches[0].primary != primary
            || matches[0].streaming != streaming
        {
            return Err(format!(
                "Core returned invalid Alpha protocol semantics for {id}"
            ));
        }
    }
    Ok(())
}

#[cfg(windows)]
mod windows_job {
    use std::{ffi::c_void, mem::size_of, ptr};

    use windows_sys::Win32::{
        Foundation::{CloseHandle, HANDLE},
        System::{
            JobObjects::{
                AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
                SetInformationJobObject, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
                JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
            },
            Threading::{OpenProcess, PROCESS_SET_QUOTA, PROCESS_TERMINATE},
        },
    };

    #[derive(Debug)]
    pub(super) struct JobObject(HANDLE);

    // The handle is owned by this value, and all mutation happens through the
    // CoreManager mutex. Windows kernel handles may be closed from any thread.
    unsafe impl Send for JobObject {}
    unsafe impl Sync for JobObject {}

    impl JobObject {
        pub(super) fn attach(pid: u32) -> Result<Self, String> {
            let job = Self::new()?;
            let process = unsafe { OpenProcess(PROCESS_SET_QUOTA | PROCESS_TERMINATE, 0, pid) };
            if process.is_null() {
                return Err(format!(
                    "OpenProcess failed: {}",
                    std::io::Error::last_os_error()
                ));
            }

            let assigned = unsafe { AssignProcessToJobObject(job.0, process) };
            unsafe {
                CloseHandle(process);
            }
            if assigned == 0 {
                return Err(format!(
                    "AssignProcessToJobObject failed: {}",
                    std::io::Error::last_os_error()
                ));
            }
            Ok(job)
        }

        fn new() -> Result<Self, String> {
            let handle = unsafe { CreateJobObjectW(ptr::null(), ptr::null()) };
            if handle.is_null() {
                return Err(format!(
                    "CreateJobObjectW failed: {}",
                    std::io::Error::last_os_error()
                ));
            }
            let job = Self(handle);

            let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
            limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            let configured = unsafe {
                SetInformationJobObject(
                    job.0,
                    JobObjectExtendedLimitInformation,
                    &limits as *const _ as *const c_void,
                    size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
                )
            };
            if configured == 0 {
                return Err(format!(
                    "SetInformationJobObject failed: {}",
                    std::io::Error::last_os_error()
                ));
            }
            Ok(job)
        }
    }

    impl Drop for JobObject {
        fn drop(&mut self) {
            unsafe {
                CloseHandle(self.0);
            }
        }
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn creates_a_kill_on_close_job_object() {
            let job = JobObject::new().expect("a configured Job Object should be creatable");
            drop(job);
        }

        #[test]
        fn rejects_a_missing_process() {
            let error = JobObject::attach(u32::MAX)
                .expect_err("an impossible PID must not be assignable to a Job Object");
            assert!(error.contains("OpenProcess") || error.contains("AssignProcessToJobObject"));
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn privacy_mutations_use_extended_request_and_startup_timeouts() {
        assert_eq!(
            control_request_timeout(
                &Method::PATCH,
                "/control/v1/policies/policy_privacy_default"
            ),
            PRIVACY_MUTATION_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(
                &Method::DELETE,
                "/control/v1/privacy-models/model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
            ),
            PRIVACY_MUTATION_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(&Method::POST, POLICY_DRY_RUN_PATH),
            PRIVACY_MUTATION_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(&Method::POST, PRIVACY_MODEL_PROBE_PATH),
            PRIVACY_MODEL_METADATA_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(&Method::POST, PRIVACY_MODELS_PATH),
            PRIVACY_MODEL_METADATA_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(&Method::GET, PRIVACY_MODEL_CATALOG_PATH),
            REQUEST_TIMEOUT
        );
        assert_eq!(
            control_request_timeout(
                &Method::GET,
                "/control/v1/requests/req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/audit"
            ),
            AUDIT_CONTENT_TIMEOUT
        );
        assert!(PRIVACY_MUTATION_TIMEOUT >= Duration::from_secs(10));
        assert!(PRIVACY_MODEL_METADATA_TIMEOUT >= Duration::from_secs(5 * 60));
        assert!(READY_TIMEOUT >= Duration::from_secs(120));
    }

    #[test]
    fn percent_encode_query_leaves_unreserved_and_encodes_specials() {
        assert_eq!(percent_encode_query("Aa0-._~"), "Aa0-._~");
        assert_eq!(percent_encode_query("+"), "%2B");
        assert_eq!(percent_encode_query("="), "%3D");
        assert_eq!(percent_encode_query("&"), "%26");
        assert_eq!(percent_encode_query("/"), "%2F");
        assert_eq!(percent_encode_query(" "), "%20");
        assert_eq!(percent_encode_query("猫"), "%E7%8C%AB");
    }

    #[test]
    fn build_request_record_query_is_deterministic_and_strict() {
        assert_eq!(
            build_request_record_query(&serde_json::json!({})).unwrap(),
            ""
        );
        assert_eq!(
            build_request_record_query(&serde_json::json!({
                "status": "succeeded",
                "endpoint_id": "endpoint_01",
                "protocol": "openai_responses",
                "to": "2026-07-25T12:00:00Z",
                "from": "2026-07-24T00:00:00Z",
                "cursor": "a+b=c&d/e",
                "limit": 50
            }))
            .unwrap(),
            "?limit=50&cursor=a%2Bb%3Dc%26d%2Fe&from=2026-07-24T00%3A00%3A00Z&to=2026-07-25T12%3A00%3A00Z&protocol=openai_responses&endpoint_id=endpoint_01&status=succeeded"
        );
        assert!(
            build_request_record_query(&serde_json::json!({"unknown": 1}))
                .unwrap_err()
                .contains("unknown")
        );
        assert!(build_request_record_query(&serde_json::json!({"limit": 0})).is_err());
        assert!(build_request_record_query(&serde_json::json!({"limit": 201})).is_err());
        assert!(build_request_record_query(&serde_json::json!({"status": "ok"})).is_err());
        assert!(build_request_record_query(&serde_json::json!({"protocol": "OpenAI"})).is_err());
        assert!(build_request_record_query(&serde_json::json!({
            "cursor": "x".repeat(513)
        }))
        .is_err());
    }

    #[test]
    fn validates_purge_request_records_input() {
        assert!(validate_purge_request_records_input(&serde_json::json!({
            "scope": "all",
            "confirm": true
        }))
        .is_ok());
        assert!(validate_purge_request_records_input(&serde_json::json!({
            "scope": "all",
            "confirm": false
        }))
        .is_err());
        assert!(validate_purge_request_records_input(&serde_json::json!({
            "scope": "before",
            "confirm": true
        }))
        .is_err());
        assert!(validate_purge_request_records_input(&serde_json::json!({
            "scope": "all",
            "confirm": true,
            "before": "2026-07-01T00:00:00Z"
        }))
        .is_err());
        assert!(validate_purge_request_records_input(&serde_json::json!({
            "scope": "all",
            "confirm": true,
            "extra": true
        }))
        .is_err());
    }

    #[test]
    fn validates_audit_settings_patch_risk_acknowledgement() {
        assert!(validate_audit_settings_patch(&serde_json::json!({})).is_err());
        assert!(validate_audit_settings_patch(&serde_json::json!({
            "unknown": true
        }))
        .is_err());
        assert_eq!(
            validate_audit_settings_patch(&serde_json::json!({
                "request_body_enabled": true
            }))
            .unwrap_err(),
            "enabling body audit requires audit_risk_acknowledged=true"
        );
        assert!(validate_audit_settings_patch(&serde_json::json!({
            "request_body_enabled": false,
            "response_content_enabled": false
        }))
        .is_ok());
    }

    #[test]
    fn control_body_limit_raises_only_for_audit_content_paths() {
        assert_eq!(
            control_body_limit("/control/v1/requests/req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/audit"),
            MAX_AUDIT_CONTENT_BODY
        );
        assert_eq!(control_body_limit("/control/v1/requests"), MAX_CONTROL_BODY);
        assert_eq!(
            control_body_limit("/control/v1/requests/req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
            MAX_CONTROL_BODY
        );
        assert_eq!(
            control_body_limit("/control/v1/audit-settings"),
            MAX_CONTROL_BODY
        );
    }

    fn ready_line(control_url: &str) -> String {
        format!(
            r#"{{"event":"ready","core_version":"0.1.0-dev","control_api_version":"v1","protocol_contract_version":"v1","inference_url":"http://127.0.0.1:8317","control_url":"{control_url}"}}"#
        )
    }

    fn alpha_capabilities() -> CapabilitiesResponse {
        CapabilitiesResponse {
            protocol_contract_version: "v1".to_string(),
            protocols: [
                ("openai.responses", true, true),
                ("openai.responses.compact", false, false),
                ("anthropic.messages", false, true),
                ("google.generate_content", false, true),
                ("openai.chat", false, true),
                ("openai.completions", false, true),
                ("openai.models", false, false),
                ("google.models", false, false),
            ]
            .into_iter()
            .map(|(id, primary, streaming)| ProtocolCapability {
                id: id.to_string(),
                phase: "alpha".to_string(),
                primary,
                streaming,
            })
            .collect(),
            plan_types: vec![
                PlanTypeCapability {
                    id: "native".to_string(),
                    available_in_alpha: true,
                    uses_local_conversion: false,
                },
                PlanTypeCapability {
                    id: "delegated".to_string(),
                    available_in_alpha: true,
                    uses_local_conversion: false,
                },
                PlanTypeCapability {
                    id: "relaykit".to_string(),
                    available_in_alpha: false,
                    uses_local_conversion: true,
                },
            ],
            conversion_engine: ConversionEngineCapability {
                name: "relaykit".to_string(),
                version: None,
                available: false,
                edges: vec![],
            },
        }
    }

    #[test]
    fn parses_the_frozen_ready_contract() {
        let ready = parse_ready_announcement(&ready_line("http://127.0.0.1:43210"))
            .expect("ready contract should parse");

        assert_eq!(ready.event, "ready");
        assert_eq!(ready.control_url, "http://127.0.0.1:43210");
        assert_eq!(ready.inference_url, "http://127.0.0.1:8317");
    }

    #[test]
    fn accepts_ready_port_boundaries() {
        for url in ["http://127.0.0.1:1", "http://127.0.0.1:65535"] {
            let ready = parse_ready_announcement(&ready_line(url))
                .unwrap_or_else(|error| panic!("{url} should be valid: {error}"));
            assert_eq!(ready.control_url, url);
        }
    }

    #[test]
    fn rejects_noncanonical_ready_urls() {
        for url in [
            "http://127.0.0.1:0",
            "http://127.0.0.1:01",
            "http://127.0.0.1:65536",
            "http://127.0.0.1:99999",
            "http://localhost:8080",
            "http://[::1]:8080",
            "http://127.0.0.2:8080",
            "http://user:secret@127.0.0.1:8080",
            "https://127.0.0.1:8080",
            "http://127.0.0.1:8080/",
            "http://127.0.0.1:8080/path",
            "http://127.0.0.1:8080?query=true",
            "http://127.0.0.1:8080#fragment",
        ] {
            if parse_ready_announcement(&ready_line(url)).is_ok() {
                panic!("{url} must be rejected");
            }
        }
    }

    #[test]
    fn detects_contract_version_mismatches() {
        let ready = parse_ready_announcement(&ready_line("http://127.0.0.1:43210")).unwrap();
        let version = VersionResponse {
            core_version: ready.core_version.clone(),
            control_api_version: ready.control_api_version.clone(),
            protocol_contract_version: "v2".to_string(),
            build_commit: "unknown".to_string(),
        };
        let capabilities = CapabilitiesResponse {
            protocol_contract_version: "v1".to_string(),
            protocols: vec![],
            plan_types: vec![],
            conversion_engine: ConversionEngineCapability {
                name: "relaykit".to_string(),
                version: None,
                available: false,
                edges: vec![],
            },
        };

        assert!(verify_contract(&ready, &version, &capabilities).is_err());
    }

    #[test]
    fn rejects_consistent_but_unsupported_contract_versions() {
        let mut ready = parse_ready_announcement(&ready_line("http://127.0.0.1:43210")).unwrap();
        ready.control_api_version = "v2".to_string();
        ready.protocol_contract_version = "v2".to_string();
        let version = VersionResponse {
            core_version: ready.core_version.clone(),
            control_api_version: "v2".to_string(),
            protocol_contract_version: "v2".to_string(),
            build_commit: "unknown".to_string(),
        };
        let mut capabilities = alpha_capabilities();
        capabilities.protocol_contract_version = "v2".to_string();

        let error = verify_contract(&ready, &version, &capabilities)
            .expect_err("unsupported but internally consistent versions must fail");
        assert!(error.contains("unsupported"));
    }

    #[test]
    fn rejects_relaykit_or_invalid_alpha_capabilities() {
        let ready = parse_ready_announcement(&ready_line("http://127.0.0.1:43210")).unwrap();
        let version = VersionResponse {
            core_version: ready.core_version.clone(),
            control_api_version: ready.control_api_version.clone(),
            protocol_contract_version: ready.protocol_contract_version.clone(),
            build_commit: "unknown".to_string(),
        };
        let mut capabilities = alpha_capabilities();
        capabilities.conversion_engine.available = true;

        let error = verify_contract(&ready, &version, &capabilities)
            .expect_err("Alpha must reject local conversion capability");
        assert!(error.contains("local conversion"));
    }

    fn privacy_policy_value() -> serde_json::Value {
        serde_json::json!({
            "id": "policy_privacy_default",
            "name": "隐私保护",
            "enabled": false,
            "priority": 0,
            "detector": "regex",
            "local_model_id": null,
            "match": {},
            "request_action": "redact",
            "response_action": "allow",
            "response_restore": true
        })
    }

    #[test]
    fn strictly_parses_the_privacy_policy_control_contract() {
        let page = serde_json::to_vec(&serde_json::json!({
            "items": [privacy_policy_value()],
            "next_cursor": null
        }))
        .unwrap();
        assert!(parse_privacy_policy_page(&page).is_ok());

        let record = policy_record(
            Some(format!("\"sha256:{}\"", "a".repeat(64))),
            &serde_json::to_vec(&privacy_policy_value()).unwrap(),
        )
        .expect("the frozen policy response should parse");
        assert_eq!(record.policy["id"], "policy_privacy_default");

        let mut unexpected = privacy_policy_value();
        unexpected["secret"] = serde_json::json!("must-not-cross-ipc");
        assert!(validate_privacy_policy_value(&unexpected).is_err());
        assert!(policy_record(
            Some("\"weak\"".to_string()),
            &serde_json::to_vec(&privacy_policy_value()).unwrap(),
        )
        .is_err());
    }

    #[test]
    fn validates_selectable_privacy_policy_patch_fields() {
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "enabled": true,
            "detector": "local_model",
            "local_model_id": "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "request_action": "block",
            "response_restore": false
        }))
        .is_ok());
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "detector": "regex",
            "local_model_id": null
        }))
        .is_ok());
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "enabled": null
        }))
        .is_err());
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "name": "renamed"
        }))
        .is_err());
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "response_action": "warn"
        }))
        .is_err());
        assert!(validate_privacy_policy_patch(serde_json::json!({
            "response_restore": "yes"
        }))
        .is_err());
        assert!(validate_privacy_policy_patch(serde_json::json!({})).is_err());
    }

    fn privacy_variant_value() -> serde_json::Value {
        serde_json::json!({
            "id": "cpu_int8",
            "name": "CPU INT8",
            "quantization": "int8",
            "bytes_total": 180_000_000,
            "estimated_ram_bytes": 420_000_000,
            "recommended": true,
            "supported": true,
            "unsupported_reason": null
        })
    }

    fn privacy_installation_value() -> serde_json::Value {
        serde_json::json!({
            "id": "model_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "source": "catalog",
            "catalog_id": "catalog_example_privacy",
            "catalog_source": "community",
            "name": "Example Privacy",
            "license": "apache-2.0",
            "languages": ["en"],
            "repo_id": "example/privacy-filter",
            "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
            "variant_id": "cpu_int8",
            "variant_name": "CPU INT8",
            "quantization": "int8",
            "adapter": "hf_token_classification",
            "status": "downloading",
            "bytes_downloaded": 45,
            "bytes_total": 180_000_000,
            "estimated_ram_bytes": 420_000_000,
            "error": null,
            "label_mapping": {"EMAIL": "email", "MISC": null},
            "installed_at": null
        })
    }

    #[test]
    fn strictly_parses_privacy_model_catalog_probe_and_installations() {
        let catalog = serde_json::to_vec(&serde_json::json!({
            "items": [{
                "id": "catalog_example_privacy",
                "name": "Example Privacy",
                "summary": "Small compatible privacy model.",
                "source": "community",
                "repo_id": "example/privacy-filter",
                "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
                "license": "apache-2.0",
                "languages": ["en"],
                "adapter": "hf_token_classification",
                "variants": [privacy_variant_value()]
            }]
        }))
        .unwrap();
        assert!(parse_privacy_model_catalog(&catalog).is_ok());

        let probe = serde_json::to_vec(&serde_json::json!({
            "repo_id": "example/privacy-filter",
            "requested_revision": "main",
            "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
            "name": "Example Privacy",
            "license": "apache-2.0",
            "languages": ["en"],
            "adapter": "hf_token_classification",
            "variants": [privacy_variant_value()],
            "labels": [
                {"label": "EMAIL", "suggested_kind": "email"},
                {"label": "MISC", "suggested_kind": null}
            ],
            "requires_label_mapping": true
        }))
        .unwrap();
        assert!(parse_privacy_model_probe(&probe).is_ok());

        let installation = privacy_installation_value();
        assert!(validate_privacy_model_installation(&installation).is_ok());
        let list =
            serde_json::to_vec(&serde_json::json!({"items": [installation.clone()]})).unwrap();
        assert!(parse_privacy_model_installation_list(&list).is_ok());

        let mut unsafe_integer = installation.clone();
        unsafe_integer["bytes_downloaded"] = serde_json::json!(9_007_199_254_740_992_u64);
        assert!(validate_privacy_model_installation(&unsafe_integer).is_err());

        let mut leaked_path = installation;
        leaked_path["path"] = serde_json::json!("/private/model");
        assert!(validate_privacy_model_installation(&leaked_path).is_err());
    }

    #[test]
    fn accepts_the_largest_valid_privacy_model_directory_response() {
        let mapping = (0..256)
            .map(|index| {
                (
                    format!("LABEL_{index:03}_{}", "X".repeat(118)),
                    serde_json::Value::Null,
                )
            })
            .collect::<serde_json::Map<_, _>>();
        let items = (0_u128..100)
            .map(|index| {
                let mut installation = privacy_installation_value();
                installation["id"] = serde_json::json!(format!("model_{index:032x}"));
                installation["label_mapping"] = serde_json::Value::Object(mapping.clone());
                installation
            })
            .collect::<Vec<_>>();
        let body = serde_json::to_vec(&serde_json::json!({"items": items})).unwrap();

        assert!(body.len() > 2 * 1024 * 1024);
        assert!(body.len() <= MAX_CONTROL_BODY);
        assert!(parse_privacy_model_installation_list(&body).is_ok());
    }

    #[test]
    fn rejects_privacy_model_contract_boundary_drift() {
        let empty_labels = serde_json::to_vec(&serde_json::json!({
            "repo_id": "example/privacy-filter",
            "requested_revision": "main",
            "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
            "name": "Example Privacy",
            "license": null,
            "languages": [],
            "adapter": "hf_token_classification",
            "variants": [privacy_variant_value()],
            "labels": [],
            "requires_label_mapping": false
        }))
        .unwrap();
        assert!(parse_privacy_model_probe(&empty_labels).is_err());

        let mut ready_without_content = privacy_installation_value();
        ready_without_content["status"] = serde_json::json!("ready");
        ready_without_content["bytes_downloaded"] = serde_json::json!(0);
        ready_without_content["bytes_total"] = serde_json::json!(0);
        ready_without_content["installed_at"] = serde_json::json!("2026-07-24T10:30:00Z");
        assert!(validate_privacy_model_installation(&ready_without_content).is_err());

        let mut long_variant_name = privacy_installation_value();
        long_variant_name["variant_name"] = serde_json::json!("x".repeat(65));
        assert!(validate_privacy_model_installation(&long_variant_name).is_err());

        let mut padded_model_name = privacy_installation_value();
        padded_model_name["name"] = serde_json::json!(" Example Privacy");
        assert!(validate_privacy_model_installation(&padded_model_name).is_err());
        let mut controlled_variant_name = privacy_installation_value();
        controlled_variant_name["variant_name"] = serde_json::json!("CPU INT8\n");
        assert!(validate_privacy_model_installation(&controlled_variant_name).is_err());
        let mut padded_quantization = privacy_installation_value();
        padded_quantization["quantization"] = serde_json::json!(" int8");
        assert!(validate_privacy_model_installation(&padded_quantization).is_err());

        let mut padded_catalog_variant = privacy_variant_value();
        padded_catalog_variant["name"] = serde_json::json!(" CPU INT8");
        let catalog_with_padded_variant = serde_json::to_vec(&serde_json::json!({
            "items": [{
                "id": "catalog_example_privacy",
                "name": "Example Privacy",
                "summary": "Small compatible privacy model.",
                "source": "community",
                "repo_id": "example/privacy-filter",
                "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
                "license": "apache-2.0",
                "languages": ["en"],
                "adapter": "hf_token_classification",
                "variants": [padded_catalog_variant]
            }]
        }))
        .unwrap();
        assert!(parse_privacy_model_catalog(&catalog_with_padded_variant).is_err());

        let mut controlled_catalog_variant = privacy_variant_value();
        controlled_catalog_variant["quantization"] = serde_json::json!("int8\u{0}");
        let catalog_with_controlled_variant = serde_json::to_vec(&serde_json::json!({
            "items": [{
                "id": "catalog_example_privacy",
                "name": "Example Privacy",
                "summary": "Small compatible privacy model.",
                "source": "community",
                "repo_id": "example/privacy-filter",
                "revision": "53d55aa8dbb28efaa4e9cf6b4b6015d00e43c088",
                "license": "apache-2.0",
                "languages": ["en"],
                "adapter": "hf_token_classification",
                "variants": [controlled_catalog_variant]
            }]
        }))
        .unwrap();
        assert!(parse_privacy_model_catalog(&catalog_with_controlled_variant).is_err());

        let mut custom_installation = privacy_installation_value();
        custom_installation["source"] = serde_json::json!("custom");
        custom_installation["catalog_id"] = serde_json::Value::Null;
        custom_installation["catalog_source"] = serde_json::Value::Null;
        custom_installation["license"] = serde_json::Value::Null;
        custom_installation["languages"] = serde_json::json!([]);
        assert!(validate_privacy_model_installation(&custom_installation).is_ok());

        let mut missing_catalog_source = privacy_installation_value();
        missing_catalog_source["catalog_source"] = serde_json::Value::Null;
        assert!(validate_privacy_model_installation(&missing_catalog_source).is_err());
        let mut duplicate_languages = privacy_installation_value();
        duplicate_languages["languages"] = serde_json::json!(["en", "en"]);
        assert!(validate_privacy_model_installation(&duplicate_languages).is_err());

        let mut malformed_timestamp = privacy_installation_value();
        malformed_timestamp["status"] = serde_json::json!("ready");
        malformed_timestamp["bytes_downloaded"] = malformed_timestamp["bytes_total"].clone();
        malformed_timestamp["installed_at"] = serde_json::json!("watTeverZ");
        assert!(validate_privacy_model_installation(&malformed_timestamp).is_err());

        malformed_timestamp["installed_at"] = serde_json::json!("2026-02-30T10:30:00Z");
        assert!(validate_privacy_model_installation(&malformed_timestamp).is_err());
        malformed_timestamp["installed_at"] =
            serde_json::json!("2026-07-24T18:30:00.123456789+08:00");
        assert!(validate_privacy_model_installation(&malformed_timestamp).is_ok());
    }

    #[test]
    fn privacy_model_control_paths_are_separate_and_pluralized() {
        assert_eq!(
            PRIVACY_MODEL_CATALOG_PATH,
            "/control/v1/privacy-model-catalog"
        );
        assert_eq!(PRIVACY_MODEL_PROBE_PATH, "/control/v1/privacy-models/probe");
        assert_eq!(PRIVACY_MODELS_PATH, "/control/v1/privacy-models");
        assert_ne!(PRIVACY_MODEL_CATALOG_PATH, PRIVACY_MODELS_PATH);
    }

    #[test]
    fn termination_clears_stale_handshake_and_distinguishes_requested_stop() {
        let manager = CoreManager::new();
        let ready = parse_ready_announcement(&ready_line("http://127.0.0.1:43210")).unwrap();
        {
            let mut inner = manager.lock_inner();
            inner.generation = 7;
            inner.phase = CorePhase::Ready;
            inner.pid = Some(42);
            inner.ready = Some(ready.clone());
            inner.health = Some(HealthResponse {
                status: "ok".to_string(),
            });
            inner.version = Some(VersionResponse {
                core_version: ready.core_version.clone(),
                control_api_version: ready.control_api_version.clone(),
                protocol_contract_version: ready.protocol_contract_version.clone(),
                build_commit: "unknown".to_string(),
            });
            inner.capabilities = Some(alpha_capabilities());
            inner.control_token = Some("control-token".to_string());
        }
        manager.handle_terminated(
            7,
            TerminatedPayload {
                code: Some(1),
                signal: None,
            },
        );
        let snapshot = manager.snapshot();
        assert_eq!(snapshot.phase, CorePhase::Exited);
        assert!(snapshot.pid.is_none());
        assert!(snapshot.ready.is_none());
        assert!(snapshot.health.is_none());
        assert!(snapshot.version.is_none());
        assert!(snapshot.capabilities.is_none());
        assert!(snapshot.last_error.is_some());
        {
            let inner = manager.lock_inner();
            assert!(inner.control_token.is_none());
        }

        {
            let mut inner = manager.lock_inner();
            inner.generation = 8;
            inner.phase = CorePhase::Stopping;
            inner.pid = Some(43);
        }
        manager.handle_terminated(
            8,
            TerminatedPayload {
                code: None,
                signal: Some(9),
            },
        );
        let snapshot = manager.snapshot();
        assert_eq!(snapshot.phase, CorePhase::Stopped);
        assert!(snapshot.last_error.is_none());
    }

    fn lifecycle(phase: CorePhase, pid: Option<u32>) -> LifecycleState {
        LifecycleState {
            generation: 7,
            phase,
            pid,
            last_error: None,
        }
    }

    #[test]
    fn sidecar_receives_pid_and_data_path_but_not_control_token_in_arguments() {
        let arguments = sidecar_args(4242, Path::new("/tmp/astrlink-data"))
            .expect("test path should be valid UTF-8");
        assert_eq!(
            arguments,
            [
                "--parent-pid".to_string(),
                "4242".to_string(),
                "--data-dir".to_string(),
                "/tmp/astrlink-data".to_string(),
                "--control-token-stdin".to_string(),
            ]
        );
    }

    #[test]
    fn control_token_is_cryptographically_generated_and_argument_safe() {
        let first = generate_control_token().expect("control token generation should work");
        let second = generate_control_token().expect("control token generation should work");
        assert_eq!(first.len(), 64);
        assert!(first.bytes().all(|byte| byte.is_ascii_hexdigit()));
        assert_ne!(first, second);
    }

    #[test]
    fn lifecycle_transitions_make_failure_kill_success_restartable() {
        let current = lifecycle(CorePhase::Handshaking, Some(42));
        let next = transition_lifecycle(
            &current,
            7,
            LifecycleEvent::FailureStopRequested("handshake failed".to_string()),
        );

        assert_eq!(next.phase, CorePhase::Error);
        assert!(next.pid.is_none());
        assert!(next
            .last_error
            .as_deref()
            .unwrap()
            .contains("termination request succeeded"));
        assert!(start_allowed(&next, false));
    }

    #[test]
    fn lifecycle_transitions_preserve_pid_when_kill_fails() {
        let current = lifecycle(CorePhase::Ready, Some(4242));
        let next = transition_lifecycle(
            &current,
            7,
            LifecycleEvent::FailureStopRequestFailed {
                failure: "control handshake failed".to_string(),
                kill_error: "access denied".to_string(),
            },
        );

        assert_eq!(next.phase, CorePhase::Error);
        assert_eq!(next.pid, Some(4242));
        assert!(next.last_error.as_deref().unwrap().contains("PID 4242"));
        assert!(!start_allowed(&next, false));
    }

    #[test]
    fn stopping_process_error_completes_the_confirmed_stop() {
        let current = lifecycle(CorePhase::Stopping, Some(52));
        let next = transition_lifecycle(&current, 7, LifecycleEvent::ProcessErrorWhileStopping);

        assert_eq!(next.phase, CorePhase::Stopped);
        assert!(next.pid.is_none());
        assert!(next.last_error.is_none());
        assert!(start_allowed(&next, false));
    }

    #[test]
    fn expired_generation_events_do_not_change_lifecycle() {
        let current = lifecycle(CorePhase::Ready, Some(88));
        let next = transition_lifecycle(
            &current,
            6,
            LifecycleEvent::FailureStopRequested("stale failure".to_string()),
        );
        assert_eq!(next, current);
    }

    #[test]
    fn start_is_allowed_only_from_inactive_recoverable_states() {
        for phase in [CorePhase::Stopped, CorePhase::Exited, CorePhase::Error] {
            assert!(start_allowed(&lifecycle(phase, None), false));
        }
        for phase in [
            CorePhase::Spawning,
            CorePhase::WaitingForReady,
            CorePhase::Handshaking,
            CorePhase::Ready,
            CorePhase::Stopping,
        ] {
            assert!(!start_allowed(&lifecycle(phase, None), false));
        }
        assert!(!start_allowed(
            &lifecycle(CorePhase::Error, Some(44)),
            false
        ));
        assert!(!start_allowed(&lifecycle(CorePhase::Stopped, None), true));
    }

    #[test]
    fn handshake_failure_cannot_override_inactive_phases() {
        for phase in [
            CorePhase::Stopping,
            CorePhase::Stopped,
            CorePhase::Exited,
            CorePhase::Error,
        ] {
            let manager = CoreManager::new();
            {
                let mut inner = manager.lock_inner();
                inner.generation = 7;
                inner.phase = phase;
                inner.pid = Some(123);
            }

            manager.fail_generation_and_stop(7, "late handshake failure".to_string());
            let snapshot = manager.snapshot();
            assert_eq!(snapshot.phase, phase);
            assert_eq!(snapshot.pid, Some(123));
            assert!(snapshot.last_error.is_none());
        }
    }

    #[test]
    fn process_monitor_consumes_termination_while_handshake_is_in_flight() {
        let manager = Arc::new(CoreManager::new());
        {
            let mut inner = manager.lock_inner();
            inner.generation = 7;
            inner.phase = CorePhase::WaitingForReady;
            inner.pid = Some(123);
        }
        let (sender, receiver) = tauri::async_runtime::channel(2);
        // Port 1 has no AstrLink control server. The first failed health attempt
        // enters the retry delay, which used to block process-event consumption.
        let stdout = CommandEvent::Stdout(ready_line("http://127.0.0.1:1").into_bytes());
        let terminated = CommandEvent::Terminated(TerminatedPayload {
            code: Some(0),
            signal: None,
        });

        tauri::async_runtime::block_on(async {
            sender.send(stdout).await.unwrap();
            sender.send(terminated).await.unwrap();
            drop(sender);
            tokio::time::timeout(
                Duration::from_millis(100),
                Arc::clone(&manager).monitor_process(7, receiver),
            )
            .await
            .expect("process monitor was blocked by the control handshake");
        });

        let snapshot = manager.snapshot();
        assert_eq!(snapshot.phase, CorePhase::Exited);
        assert!(snapshot.pid.is_none());
    }

    #[test]
    fn command_error_finishes_a_stopping_generation() {
        let manager = CoreManager::new();
        {
            let mut inner = manager.lock_inner();
            inner.generation = 7;
            inner.phase = CorePhase::Stopping;
            inner.pid = Some(123);
        }

        manager.handle_process_error(7, "pipe closed".to_string());
        let snapshot = manager.snapshot();
        assert_eq!(snapshot.phase, CorePhase::Stopped);
        assert!(snapshot.pid.is_none());
        assert!(snapshot.last_error.is_none());
    }

    #[test]
    fn stop_wait_reports_timeout_and_keeps_pid_blocking_restart() {
        let manager = CoreManager::new();
        {
            let mut inner = manager.lock_inner();
            inner.generation = 7;
            inner.phase = CorePhase::Stopping;
            inner.pid = Some(123);
        }

        let error = tauri::async_runtime::block_on(
            manager.wait_until_stopped_with_timeout(7, Duration::from_millis(1)),
        )
        .expect_err("an unconfirmed stop must time out");
        assert!(error.contains("PID 123"));
        let snapshot = manager.snapshot();
        assert_eq!(snapshot.phase, CorePhase::Error);
        assert_eq!(snapshot.pid, Some(123));
    }

    #[test]
    fn stop_and_wait_handles_stopped_and_unrecoverable_states() {
        let manager = CoreManager::new();
        tauri::async_runtime::block_on(manager.stop_and_wait())
            .expect("an already stopped manager should stop cleanly");

        {
            let mut inner = manager.lock_inner();
            inner.generation = 8;
            inner.phase = CorePhase::Error;
            inner.pid = Some(99);
            inner.last_error = Some("manually terminate PID 99 before retrying".to_string());
        }
        let error = tauri::async_runtime::block_on(manager.stop_and_wait())
            .expect_err("a missing process handle must remain unrecoverable");
        assert!(error.contains("PID 99"));
    }
}
