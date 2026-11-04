//! Each place's config as the dialog shows it.

use serde::Serialize;

use super::{
    apply::{read_files, texts},
    client::{Client, Place},
    config::Config,
    gateway::{local_origin, Tokens},
    models::CurrentModels,
    plan::{current_models, place_state, ClientState},
    records::read_records,
};
use crate::{agent_home::Home, host_files};

#[derive(Debug, Serialize)]
pub struct ClientStatus {
    pub client: Client,
    /// The WSL distribution the client runs in; `None` for the desktop user's.
    pub wsl: Option<String>,
    /// One of the client's targets; `None` for a client without.
    pub target: Option<&'static str>,
    pub detected: bool,
    /// The home the paths lie in, for showing them from `~`.
    pub home: String,
    /// The client's config directory, where an install is looked for.
    pub dir: String,
    pub paths: Vec<String>,
    /// Whether each of `paths` exists now.
    pub exists: Vec<bool>,
    pub state: ClientState,
    pub token_id: Option<String>,
    /// The models a config AstrLink wrote holds now.
    pub models: CurrentModels,
    /// Situations the dialog explains for this place, by id.
    pub notes: Vec<&'static str>,
    /// For a written client the token is pasted into: since when a request
    /// from it with the token shows the paste worked, and the names it is
    /// pasted under.
    pub paste_since_unix: Option<u64>,
    pub paste_into: Vec<String>,
}

/// Client configs across the desktop user's home and WSL homes.
#[derive(Debug, Serialize)]
pub struct ClientOverview {
    pub clients: Vec<ClientStatus>,
    /// WSL distributions whose clients were not checked.
    pub wsl_unchecked: Vec<String>,
    /// Whether WSL clients reach the gateway at `localhost`.
    pub wsl_localhost: bool,
    /// The link clients installed on this computer.
    pub installed: Vec<String>,
}

/// Every place of each client AstrLink writes files for, in the dialog's
/// order.
pub fn status(
    home: Home,
    inference_url: Option<&str>,
    tokens: Option<&Tokens>,
) -> Result<Vec<ClientStatus>, String> {
    status_of(home, Client::all(), inference_url, tokens)
}

pub(crate) fn status_of(
    home: Home,
    clients: impl IntoIterator<Item = Client>,
    inference_url: Option<&str>,
    tokens: Option<&Tokens>,
) -> Result<Vec<ClientStatus>, String> {
    let origin = inference_url.map(local_origin).transpose()?;
    let _lock = host_files::lock();
    let records = read_records(&home)?;
    let mut statuses = Vec::new();
    for place in clients.into_iter().flat_map(Place::all) {
        let client = place.client;
        let record = records.get(place);
        let paths = client.config_paths(home, place.target, record);
        let existing = read_files(&paths)?;
        let state = place_state(
            &records,
            place,
            &texts(&existing),
            origin.as_deref(),
            tokens,
        );
        // Only a config AstrLink wrote names its token and models.
        let token_id = record
            .filter(|_| state != ClientState::NotConfigured)
            .map(|record| record.token_id.clone());
        let config = Config::parse(client, &texts(&existing)).ok();
        let paste_since_unix = record
            .filter(|_| token_id.is_some())
            .and_then(|record| record.paste_since_unix);
        let paste_into = match (&config, paste_since_unix) {
            (Some(config), Some(_)) => client.paste_into(config),
            _ => Vec::new(),
        };
        statuses.push(ClientStatus {
            client,
            wsl: home.distribution().map(str::to_string),
            target: place.target,
            detected: client.detected(home, place.target),
            home: home.display().to_string(),
            dir: client.dir(home, place.target).display().to_string(),
            paths: paths
                .iter()
                .map(|path| path.display().to_string())
                .collect(),
            exists: existing.iter().map(Option::is_some).collect(),
            notes: {
                let mut notes = client.notes(home, place.target, config.as_ref());
                if client.running(home, place.target) {
                    notes.push("running");
                }
                notes
            },
            state,
            models: if token_id.is_some() {
                current_models(client, &texts(&existing))
            } else {
                CurrentModels::default()
            },
            token_id,
            paste_since_unix,
            paste_into,
        });
    }
    Ok(statuses)
}

/// The token AstrLink last set up `place` with in `home`, whatever the
/// config holds now.
pub fn configured_token(home: Home, place: impl Into<Place>) -> Result<Option<String>, String> {
    let _lock = host_files::lock();
    Ok(read_records(&home)?
        .get(place)
        .map(|record| record.token_id.clone()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::client_config::test_support::*;

    #[test]
    fn status_lists_every_client_and_writes_only_installed_ones() {
        let home = unique_home("installed");
        for client in [GEMINI, OPENCODE, OPENCLAW] {
            let error = write(
                Home::local(&home),
                client,
                &connection(&codex_models()),
                &tokens(),
                true,
            )
            .unwrap_err();
            assert!(!error.message.contains(TOKEN));
            remove(Home::local(&home), client).unwrap();
        }
        let detected = |home: &Path| {
            status(
                Home::local(home),
                Some("http://127.0.0.1:18317/"),
                Some(&tokens()),
            )
            .unwrap()
            .into_iter()
            .map(|status| (status.client, status.detected))
            .collect::<Vec<_>>()
        };
        let found = detected(&home);
        assert_eq!(
            found[..6],
            [
                (CLAUDE, true),
                (CODEX, true),
                (GEMINI, false),
                (OPENCODE, false),
                (OPENCLAW, false),
                (PI, false),
            ]
        );
        assert!(found[6..].iter().all(|(_, detected)| !detected));
        // OpenCode's data directory marks a run before it has a config.
        for dir in [".gemini", ".local/share/opencode", ".openclaw", ".pi"] {
            fs::create_dir_all(home.join(dir)).unwrap();
        }
        for client in Client::all().skip(6) {
            for place in Place::all(client) {
                fs::create_dir_all(client.dir(Home::local(&home), place.target)).unwrap();
            }
        }
        // Copilot CLI shares its directory with VS Code; its first run
        // writes `config.json`.
        fs::write(home.join(".copilot/config.json"), "{}").unwrap();
        // Aider keeps its state in `~/.aider`, Chatbox counts once it made
        // its settings, and TRAE CLI shares `~/.trae` with the editor.
        fs::create_dir_all(home.join(".aider")).unwrap();
        let chatbox = CHATBOX.dir(Home::local(&home), None);
        fs::write(chatbox.join("config.json"), "{}").unwrap();
        fs::create_dir_all(home.join(".trae/cli")).unwrap();
        assert!(detected(&home).iter().all(|(_, detected)| *detected));
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn moved_dirs_and_wsl_homes_hold_their_own_configs() {
        use crate::{agent_home::ToolDirs, wsl::WslHome};

        let host = unique_home("moved-host");
        let claude = host.join("work").join("claude");
        let pi = host.join("work").join("pi-agent");
        fs::create_dir_all(&claude).unwrap();
        fs::create_dir_all(&pi).unwrap();
        let dirs = ToolDirs {
            claude: Some(claude.clone()),
            pi: Some(pi.clone()),
            ..Default::default()
        };
        let local = crate::agent_home::homes(&host, &dirs, &[]).next().unwrap();
        assert!(PI.detected(local, None));
        write(local, CLAUDE, &connection(&[]), &tokens(), false).unwrap();
        write(local, PI, &connection(&pi_models()), &tokens(), false).unwrap();
        assert!(claude.join("settings.json").is_file());
        assert!(pi.join("models.json").is_file() && pi.join("settings.json").is_file());
        assert!(!host.join(".claude").join("settings.json").exists());
        assert!(!host.join(".pi").exists());

        let share = unique_home("moved-share");
        let wsl = WslHome {
            distribution: "Ubuntu".to_string(),
            share: share.clone(),
            mount_root: "/mnt".to_string(),
            dirs: ToolDirs::default(),
        };
        let statuses = status(
            Home::in_wsl(&host, &wsl),
            Some("http://127.0.0.1:8317/"),
            Some(&tokens()),
        )
        .unwrap();
        assert!(statuses
            .iter()
            .all(|status| status.wsl.as_deref() == Some("Ubuntu")
                && status.state == ClientState::NotConfigured));
        let claude_status = statuses
            .iter()
            .find(|status| status.client == CLAUDE)
            .unwrap();
        assert!(claude_status.detected);
        assert_eq!(
            claude_status.paths,
            [share
                .join(".claude")
                .join("settings.json")
                .display()
                .to_string()]
        );
        // Each home keeps its own records.
        write(
            Home::in_wsl(&host, &wsl),
            CLAUDE,
            &connection(&[]),
            &tokens(),
            false,
        )
        .unwrap();
        assert!(records_path(&share).is_file());
        assert!(share.join(".claude").join("settings.json").is_file());
        remove(Home::in_wsl(&host, &wsl), CLAUDE).unwrap();
        assert!(!share.join(".claude").join("settings.json").exists());
        assert!(read_records(&host).unwrap().get(CLAUDE).is_some());
        assert!(claude.join("settings.json").is_file());
        fs::remove_dir_all(host).unwrap();
        fs::remove_dir_all(share).unwrap();
    }

    #[test]
    fn status_names_the_models_written_configs_hold_now() {
        let home = unique_home("models");
        fs::create_dir_all(home.join(".pi/agent")).unwrap();
        let settings = home.join(".claude/settings.json");
        // A model in a config AstrLink did not write is not its to show.
        fs::write(&settings, "{\"env\": {\"ANTHROPIC_MODEL\": \"own\"}}\n").unwrap();
        let models = || {
            status(Home::local(&home), None, None)
                .unwrap()
                .into_iter()
                .filter(|status| [CLAUDE, CODEX, PI].contains(&status.client))
                .map(|status| status.models)
                .collect::<Vec<_>>()
        };
        assert!(models()
            .iter()
            .all(|models| *models == CurrentModels::default()));

        let claude = [
            ("model", "main-route".to_string()),
            ("opusModel", "opus-route".to_string()),
        ];
        for (client, models) in [
            (CLAUDE, &claude[..]),
            (CODEX, &codex_models()[..]),
            (PI, &pi_models()[..]),
        ] {
            write(
                Home::local(&home),
                client,
                &connection(models),
                &tokens(),
                true,
            )
            .unwrap();
        }
        assert_eq!(
            models(),
            [
                CurrentModels {
                    model: Some("main-route".into()),
                    opus_model: Some("opus-route".into()),
                    ..Default::default()
                },
                CurrentModels {
                    model: Some("gpt-route".into()),
                    ..Default::default()
                },
                pi_current("pi-route"),
            ]
        );

        // Models the clients picked since are the current ones, and a name
        // AstrLink could not write back is left out.
        let edit = |path: PathBuf, from: &str, to: &str| {
            let text = fs::read_to_string(&path).unwrap();
            assert!(text.contains(from));
            fs::write(&path, text.replace(from, to)).unwrap();
        };
        edit(settings, "opus-route", &"x".repeat(257));
        edit(
            home.join(".codex/config.toml"),
            "gpt-route",
            "picked-in-tui",
        );
        let pi_settings = home.join(".pi/agent/settings.json");
        edit(pi_settings.clone(), "\"pi-route\"", "\"pi-mini\"");
        assert_eq!(
            models(),
            [
                CurrentModels {
                    model: Some("main-route".into()),
                    ..Default::default()
                },
                CurrentModels {
                    model: Some("picked-in-tui".into()),
                    ..Default::default()
                },
                CurrentModels {
                    model: Some("pi-mini".into()),
                    ..pi_current("pi-route")
                },
            ]
        );
        // Pi starting on another provider still lists AstrLink's model.
        edit(pi_settings, "\"astrlink\"", "\"anthropic\"");
        assert_eq!(models()[2], pi_current("pi-route"));
        fs::remove_dir_all(home).unwrap();
    }
}
