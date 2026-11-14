//! Each place's config as the dialog shows it.

use std::collections::BTreeMap;

use serde::Serialize;

use super::{
    apply::{read_files, texts},
    client::{Client, Place},
    config::Config,
    gateway::{local_origin, Tokens},
    models::{Catalog, CurrentModels},
    plan::{current_models, place_state, ClientState, Connection},
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
        let read = read_files(client, &paths);
        let unreadable = read.is_err();
        let existing = read.unwrap_or_else(|_| vec![None; paths.len()]);
        let mut state = if unreadable {
            ClientState::Invalid
        } else {
            place_state(
                &records,
                place,
                &texts(&existing),
                origin.as_deref(),
                tokens,
            )
        };
        let config = Config::parse(client, &texts(&existing)).ok();
        let manual = match (&config, origin.as_deref(), tokens) {
            (Some(config), Some(origin), Some(tokens))
                if record.is_none()
                    && state == ClientState::NotConfigured
                    && existing.iter().any(Option::is_some) =>
            {
                manual_token(place, config, origin, tokens)
            }
            _ => None,
        };
        if manual.is_some() {
            state = ClientState::Manual;
        }
        // Only a config AstrLink wrote or the user set up from its manual
        // setup names its token and models.
        let token_id = record
            .filter(|_| state != ClientState::NotConfigured)
            .map(|record| record.token_id.clone())
            .or(manual);
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
            exists: paths.iter().map(|path| path.exists()).collect(),
            notes: {
                let mut notes = client.notes(home, place.target, config.as_ref());
                if unreadable {
                    notes.push("unreadable");
                }
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

/// A stand-in token that marks where a client's keys hold the token. Letters,
/// digits and underscores only, so no format escapes it.
const SENTINEL: &str = "ASTRLINK_MANUAL_SETUP_TOKEN_0";

/// The id of the gateway token a config AstrLink did not record holds, when
/// the config is what AstrLink's manual setup shows for this gateway: every
/// key holding the token, the connection keys and the address key hold what
/// a write with that token would. The token never leaves this function; only
/// its hint is compared. A client whose keys this cannot tell apart, like
/// one that lists its token per model, is never manual.
fn manual_token(place: Place, config: &Config, origin: &str, tokens: &Tokens) -> Option<String> {
    let client = place.client;
    let protocols = BTreeMap::new();
    let render = |token: &str| {
        client.desired_keys(
            &Connection {
                target: place.target,
                token_id: "",
                token,
                origin,
                models: &[],
                list: &[],
                protocols: &protocols,
            },
            &Catalog::new(),
        )
    };
    let mut token_keys = Vec::new();
    let mut candidate: Option<String> = None;
    for (key, value) in render(SENTINEL) {
        if value.matches(SENTINEL).count() != 1 {
            continue;
        }
        let (before, after) = value.split_once(SENTINEL)?;
        let held = config.get(&key).flatten()?;
        let token = held.strip_prefix(before)?.strip_suffix(after)?;
        if candidate.as_deref().is_some_and(|known| known != token) {
            return None;
        }
        candidate = Some(token.to_string());
        token_keys.push(key);
    }
    let candidate = candidate.filter(|token| token.is_ascii() && token.starts_with("astr_"))?;
    let token_id = tokens.matching(&candidate)?;
    let desired = render(&candidate);
    let base_url_key = Some(client.base_url_key()).filter(|key| !key.is_empty());
    let mut addressed = false;
    for key in token_keys
        .iter()
        .map(String::as_str)
        .chain(client.connection_keys().iter().copied())
        .chain(base_url_key)
    {
        let (_, want) = desired.iter().find(|(known, _)| known == key)?;
        if config.get(key).flatten().as_deref() != Some(want.as_str()) {
            return None;
        }
        addressed |= want.contains(origin);
    }
    addressed.then(|| token_id.to_string())
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
        for place in Place::all(super::super::clients::dimagent::CLIENT) {
            let path = place
                .client
                .config_paths(Home::local(&home), place.target, None)
                .remove(0);
            fs::write(path, []).unwrap();
        }
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
    fn a_config_pasted_from_the_manual_setup_names_its_token() {
        use crate::client_config::snippet::snippet;

        let home = unique_home("manual");
        let local = Home::local(&home);
        let url = "http://127.0.0.1:18317/";
        let state = |client: Client, url: &str, tokens: &Tokens| {
            status(local, Some(url), Some(tokens))
                .unwrap()
                .into_iter()
                .find(|status| status.client == client)
                .map(|status| (status.state, status.token_id, status.models))
                .unwrap()
        };
        for (client, file, models) in [
            (CLAUDE, ".claude/settings.json", Vec::new()),
            (CODEX, ".codex/config.toml", codex_models()),
        ] {
            let path = home.join(file);
            let paste = |token: &str| {
                let connection = Connection {
                    token,
                    ..connection(&models)
                };
                let files = snippet(client, local, &connection, &Catalog::new()).unwrap();
                assert_eq!(files.len(), 1);
                fs::write(&path, &files[0].contents).unwrap();
            };
            paste(TOKEN);
            let (found, token_id, current) = state(client, url, &tokens());
            assert_eq!(
                (found, token_id.as_deref()),
                (ClientState::Manual, Some("token_01")),
                "{client:?}"
            );
            assert_eq!(
                current,
                current_models(client, &[fs::read_to_string(&path).ok().as_deref()])
            );
            // Writing over it is a plain write: nothing to confirm.
            assert_eq!(
                asked(write(local, client, &connection(&models), &tokens(), false).unwrap()),
                None
            );
            assert_eq!(
                state(client, url, &tokens()).0,
                ClientState::Configured,
                "{client:?}"
            );
            // Removing it brings back what the user pasted.
            remove(local, client).unwrap();
            assert_eq!(
                state(client, url, &tokens()).0,
                ClientState::Manual,
                "{client:?}"
            );

            // A token whose hint no gateway token has, or a hint two tokens
            // share, names none.
            paste(OTHER_TOKEN);
            assert_eq!(
                state(client, url, &tokens()),
                (ClientState::NotConfigured, None, CurrentModels::default())
            );
            paste(TOKEN);
            let twice = Tokens(BTreeMap::from([
                ("token_01".to_string(), hint(TOKEN)),
                ("token_02".to_string(), hint(TOKEN)),
            ]));
            assert_eq!(state(client, url, &twice).0, ClientState::NotConfigured);
            // Another port is another gateway's.
            assert_eq!(
                state(client, "http://127.0.0.1:9000/", &tokens()).0,
                ClientState::NotConfigured
            );
            // A changed connection key is not what the manual setup shows.
            let text = fs::read_to_string(&path).unwrap();
            fs::write(&path, text.replace(TOKEN, &format!("{TOKEN}x"))).unwrap();
            assert_eq!(state(client, url, &tokens()).0, ClientState::NotConfigured);
            fs::remove_file(&path).unwrap();
        }
        fs::remove_dir_all(home).unwrap();
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
