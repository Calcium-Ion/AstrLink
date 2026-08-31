#![cfg(debug_assertions)]

use std::time::Duration;

use reqwest::{Client, Url};
use tauri::{AppHandle, Manager};

pub const BUILD_ID_PATH: &str = "/__astrlink_build";
const POLL_INTERVAL: Duration = Duration::from_millis(500);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(2);
const UNREACHABLE_WARN_AFTER: u32 = 20;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PollResult {
    Ok { generation: String },
    Http { status: u16 },
    Unreachable,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SupervisorAction {
    Remember { generation: String },
    Reload { generation: String },
    StopHttp { status: u16 },
    WarnUnreachable,
    Idle,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Tick {
    pub action: SupervisorAction,
    pub recovered: bool,
}

#[derive(Debug, Default)]
pub struct SupervisorState {
    seen: Option<String>,
    consecutive_failures: u32,
    warned_unreachable: bool,
    stopped: bool,
}

impl SupervisorState {
    pub fn apply(&mut self, result: PollResult) -> Tick {
        if self.stopped {
            return Tick {
                action: SupervisorAction::Idle,
                recovered: false,
            };
        }

        match result {
            PollResult::Ok { generation } => {
                let recovered = self.warned_unreachable;
                self.consecutive_failures = 0;
                self.warned_unreachable = false;
                let action = match &self.seen {
                    None => {
                        self.seen = Some(generation.clone());
                        SupervisorAction::Remember { generation }
                    }
                    Some(previous) if previous == &generation => SupervisorAction::Idle,
                    Some(_) => SupervisorAction::Reload { generation },
                };
                Tick { action, recovered }
            }
            PollResult::Http { status } => {
                self.stopped = true;
                Tick {
                    action: SupervisorAction::StopHttp { status },
                    recovered: false,
                }
            }
            PollResult::Unreachable => {
                self.consecutive_failures = self.consecutive_failures.saturating_add(1);
                let action = if !self.warned_unreachable
                    && self.consecutive_failures >= UNREACHABLE_WARN_AFTER
                {
                    self.warned_unreachable = true;
                    SupervisorAction::WarnUnreachable
                } else {
                    SupervisorAction::Idle
                };
                Tick {
                    action,
                    recovered: false,
                }
            }
        }
    }

    pub fn commit_seen(&mut self, generation: String) {
        self.seen = Some(generation);
    }
}

pub fn build_generation_url(dev_url: &str) -> Result<Url, String> {
    let base = Url::parse(dev_url)
        .map_err(|error| format!("invalid build.dev_url for reload supervisor: {error}"))?;
    base.join(BUILD_ID_PATH.trim_start_matches('/'))
        .map_err(|error| format!("unable to join {BUILD_ID_PATH} onto {dev_url}: {error}"))
}

pub fn start(app: &AppHandle) {
    let Some(dev_url) = app.config().build.dev_url.as_ref() else {
        eprintln!("[astrlink dev] build.dev_url 未配置，未启动前端热加载");
        return;
    };
    let endpoint = match build_generation_url(dev_url.as_str()) {
        Ok(url) => url,
        Err(error) => {
            eprintln!("[astrlink dev] {error}");
            return;
        }
    };
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        supervise(app, endpoint).await;
    });
}

pub fn reload_main_window(app: &AppHandle) {
    match app.get_webview_window("main") {
        Some(window) => {
            if let Err(error) = window.reload() {
                eprintln!("[astrlink dev] webview.reload failed: {error}");
            }
        }
        None => eprintln!("[astrlink dev] main window is not available to reload"),
    }
}

async fn supervise(app: AppHandle, endpoint: Url) {
    let client = match Client::builder()
        .timeout(REQUEST_TIMEOUT)
        .redirect(reqwest::redirect::Policy::none())
        .no_proxy()
        .build()
    {
        Ok(client) => client,
        Err(error) => {
            eprintln!("[astrlink dev] unable to build reload HTTP client: {error}");
            return;
        }
    };

    let mut state = SupervisorState::default();
    loop {
        let result = poll_once(&client, &endpoint).await;
        let tick = state.apply(result);
        if !apply_tick(&app, &mut state, &endpoint, tick) {
            return;
        }
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

fn apply_tick(app: &AppHandle, state: &mut SupervisorState, endpoint: &Url, tick: Tick) -> bool {
    if tick.recovered {
        eprintln!("[astrlink dev] /__astrlink_build 已恢复");
    }

    match tick.action {
        SupervisorAction::Remember { .. } | SupervisorAction::Idle => true,
        SupervisorAction::Reload { generation } => {
            if let Some(window) = app.get_webview_window("main") {
                eprintln!("[astrlink dev] frontend rebuild #{generation} -> reloading webview");
                match window.reload() {
                    Ok(()) => state.commit_seen(generation),
                    Err(error) => {
                        eprintln!("[astrlink dev] webview.reload failed: {error}");
                    }
                }
            }
            true
        }
        SupervisorAction::StopHttp { status } => {
            eprintln!(
                "[astrlink dev] /__astrlink_build 返回 HTTP {status}，热加载已停止（rsbuild 配置可能变了）"
            );
            false
        }
        SupervisorAction::WarnUnreachable => {
            eprintln!("[astrlink dev] 连续无法连接 /__astrlink_build（{endpoint}），热加载已暂停");
            true
        }
    }
}

async fn poll_once(client: &Client, endpoint: &Url) -> PollResult {
    match client.get(endpoint.clone()).send().await {
        Ok(response) => {
            let status = response.status();
            if status.as_u16() != 200 {
                return PollResult::Http {
                    status: status.as_u16(),
                };
            }
            match response.text().await {
                Ok(body) => {
                    let generation = body.trim().to_string();
                    if generation.is_empty() {
                        PollResult::Unreachable
                    } else {
                        PollResult::Ok { generation }
                    }
                }
                Err(_) => PollResult::Unreachable,
            }
        }
        Err(_) => PollResult::Unreachable,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn joins_build_id_onto_dev_url() {
        let url = build_generation_url("http://127.0.0.1:1420").expect("dev url");
        assert_eq!(url.as_str(), "http://127.0.0.1:1420/__astrlink_build");

        let url = build_generation_url("http://127.0.0.1:1420/").expect("dev url with slash");
        assert_eq!(url.as_str(), "http://127.0.0.1:1420/__astrlink_build");
    }

    #[test]
    fn first_generation_is_remembered_without_reload() {
        let mut state = SupervisorState::default();
        let tick = state.apply(PollResult::Ok {
            generation: "0".to_string(),
        });
        assert_eq!(
            tick,
            Tick {
                action: SupervisorAction::Remember {
                    generation: "0".to_string()
                },
                recovered: false,
            }
        );
        assert_eq!(
            state.apply(PollResult::Ok {
                generation: "0".to_string()
            }),
            Tick {
                action: SupervisorAction::Idle,
                recovered: false,
            }
        );
    }

    #[test]
    fn generation_change_requests_reload_until_committed() {
        let mut state = SupervisorState::default();
        state.apply(PollResult::Ok {
            generation: "0".to_string(),
        });
        let tick = state.apply(PollResult::Ok {
            generation: "3".to_string(),
        });
        assert_eq!(
            tick.action,
            SupervisorAction::Reload {
                generation: "3".to_string()
            }
        );
        assert_eq!(
            state
                .apply(PollResult::Ok {
                    generation: "3".to_string()
                })
                .action,
            SupervisorAction::Reload {
                generation: "3".to_string()
            }
        );
        state.commit_seen("3".to_string());
        assert_eq!(
            state
                .apply(PollResult::Ok {
                    generation: "3".to_string()
                })
                .action,
            SupervisorAction::Idle
        );
    }

    #[test]
    fn non_200_stops_supervisor() {
        let mut state = SupervisorState::default();
        assert_eq!(
            state.apply(PollResult::Http { status: 404 }).action,
            SupervisorAction::StopHttp { status: 404 }
        );
        assert_eq!(
            state
                .apply(PollResult::Ok {
                    generation: "1".to_string()
                })
                .action,
            SupervisorAction::Idle
        );
    }

    #[test]
    fn unreachable_warns_once_after_ten_seconds_then_recovers() {
        let mut state = SupervisorState::default();
        for _ in 0..(UNREACHABLE_WARN_AFTER - 1) {
            assert_eq!(
                state.apply(PollResult::Unreachable).action,
                SupervisorAction::Idle
            );
        }
        assert_eq!(
            state.apply(PollResult::Unreachable).action,
            SupervisorAction::WarnUnreachable
        );
        assert_eq!(
            state.apply(PollResult::Unreachable).action,
            SupervisorAction::Idle
        );

        let tick = state.apply(PollResult::Ok {
            generation: "4".to_string(),
        });
        assert!(tick.recovered);
        assert_eq!(
            tick.action,
            SupervisorAction::Remember {
                generation: "4".to_string()
            }
        );
    }
}
