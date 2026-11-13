use super::*;
use crate::client_config::test_support::*;
use rusqlite::Connection as Sqlite;

// DimAgent 0.5.24's tables and the Rust store's v34 provider tables.
const STABLE: &str = "
CREATE TABLE providers (
 providerId TEXT PRIMARY KEY, source TEXT NOT NULL, displayName TEXT NOT NULL,
 driverKind TEXT NOT NULL, defaultBaseUrl TEXT, baseUrl TEXT, auth TEXT NOT NULL,
 credential TEXT, accountUsageSettings TEXT, headers TEXT, proxy TEXT,
 timeoutMs INTEGER, retryConfig TEXT, models TEXT NOT NULL, modelConfigOverrides TEXT,
 capabilityDefaults TEXT, activeModelId TEXT, modelsSource TEXT NOT NULL,
 catalogVersion TEXT, modelsUpdatedAt TEXT, metadata TEXT, enabled INTEGER NOT NULL,
 createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL, version INTEGER NOT NULL
);
CREATE TABLE provider_selections (
 selectionId TEXT PRIMARY KEY, scope TEXT NOT NULL, cwd TEXT, providerId TEXT NOT NULL,
 modelId TEXT, createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL, version INTEGER NOT NULL
);
CREATE UNIQUE INDEX global_selection ON provider_selections(scope) WHERE scope='global';
";
const NATIVE: &str = "
CREATE TABLE agent_store_format(version INTEGER NOT NULL);
INSERT INTO agent_store_format VALUES(34);
CREATE TABLE providers(provider_id TEXT PRIMARY KEY, version INTEGER NOT NULL, record TEXT NOT NULL);
CREATE TABLE provider_selections(scope TEXT PRIMARY KEY, version INTEGER NOT NULL, provider_id TEXT NOT NULL, model_id TEXT NOT NULL);
";

fn fixture(target: &str) -> (PathBuf, PathBuf, Sqlite) {
    let home = unique_home("dimagent");
    let path = CLIENT
        .config_paths(Home::local(&home), Some(target), None)
        .remove(0);
    fs::create_dir_all(path.parent().unwrap()).unwrap();
    let db = Sqlite::open(&path).unwrap();
    db.execute_batch("PRAGMA journal_mode=WAL;").unwrap();
    db.execute_batch(if target.ends_with("standard") {
        STABLE
    } else {
        NATIVE
    })
    .unwrap();
    db.execute_batch("CREATE TABLE sessions(id TEXT, body TEXT); INSERT INTO sessions VALUES('session','keep conversation');").unwrap();
    (home, path, db)
}

fn saved(path: &Path) -> Value {
    serde_json::from_str(&store::read(path).unwrap().unwrap()).unwrap()
}

fn desired(target: &str) -> Value {
    let models = codex_models();
    let plan = plan_write(
        CLIENT,
        &[Some("{}")],
        &Connection {
            target: Some(target),
            ..connection(&models)
        },
        None,
    )
    .unwrap();
    serde_json::from_str(&plan.contents[0]).unwrap()
}

#[test]
fn writes_all_protocols_in_both_stores_and_restores_empty_settings() {
    for target in CLIENT.targets() {
        let (home, path, db) = fixture(target);
        let place = Place::new(CLIENT, Some(target)).unwrap();
        let models = codex_models();
        let list: Vec<_> = [
            "gpt-route",
            "responses-model",
            "claude-model",
            "gemini-model",
        ]
        .map(str::to_string)
        .into();
        let protocols = list
            .iter()
            .zip(ROUTES)
            .map(|(model, (protocol, _, _))| (model.clone(), protocol))
            .collect();
        let conn = Connection {
            target: Some(target),
            list: &list,
            protocols: &protocols,
            ..connection(&models)
        };
        assert_eq!(
            write(Home::local(&home), place, &conn, &tokens(), false).unwrap(),
            ApplyOutcome::Applied
        );
        if target.ends_with("standard") {
            db.execute_batch("INSERT INTO providers(providerId,source,displayName,driverKind,auth,models,modelsSource,enabled,createdAt,updatedAt,version) VALUES('other','custom','Other','openai-compatible','{}','[]','user',1,'created','updated',1);").unwrap();
        } else {
            db.execute(
                "INSERT INTO providers VALUES('other',77,'other private settings')",
                [],
            )
            .unwrap();
        }
        let value = saved(&path);
        assert_eq!(
            value["selection"],
            json!({"providerId":"local-chat", "modelId":"gpt-route"})
        );
        for ((protocol, id, driver), model) in ROUTES.into_iter().zip(&list) {
            let provider = &value["providers"][id];
            assert_eq!(provider["driverKind"], driver);
            assert_eq!(provider["baseUrl"], address(protocol, ORIGIN));
            assert_eq!(provider["headers"], json!({"User-Agent":"DimAgent"}));
            assert_eq!(
                provider["auth"],
                json!({"type":"api_key", "headerName":"Authorization", "prefix":"Bearer "})
            );
            assert_eq!(provider["models"][0]["modelId"], *model);
            assert_eq!(provider["models"][0]["capabilities"]["toolCalling"], true);
            if target.ends_with("standard") {
                assert_eq!(provider["credential"]["apiKey"], TOKEN);
                assert_eq!(provider["activeModelId"], *model);
            } else {
                assert_eq!(provider["credential"], TOKEN);
                let (version, raw): (u32, String) = db
                    .query_row(
                        "SELECT version,record FROM providers WHERE provider_id=?1",
                        [id],
                        |r| Ok((r.get(0)?, r.get(1)?)),
                    )
                    .unwrap();
                assert_eq!(json(&raw)["version"], version);
            }
        }
        let statuses =
            status_of(Home::local(&home), [CLIENT], Some(ORIGIN), Some(&tokens())).unwrap();
        let status = statuses.iter().find(|s| s.target == Some(target)).unwrap();
        assert_eq!(status.state, ClientState::Configured);
        assert_eq!(status.models.model.as_deref(), Some("gpt-route"));
        assert_eq!(status.models.models.as_ref(), Some(&list));
        sync(Home::local(&home), "http://127.0.0.1:19999", &tokens()).unwrap();
        assert_eq!(
            saved(&path)["providers"]["local-gemini"]["baseUrl"],
            "http://127.0.0.1:19999/v1beta"
        );
        remove(Home::local(&home), place).unwrap();
        assert_eq!(saved(&path), json!({}));
        assert_eq!(
            db.query_row("SELECT version FROM providers", [], |r| r.get::<_, u32>(0))
                .unwrap(),
            if target.ends_with("standard") { 1 } else { 77 }
        );
        assert_eq!(
            db.query_row("SELECT body FROM sessions", [], |r| r.get::<_, String>(0))
                .unwrap(),
            "keep conversation"
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(
                fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                0o600
            );
            for suffix in ["-wal", "-shm"] {
                let mut sidecar = path.as_os_str().to_os_string();
                sidecar.push(suffix);
                assert_eq!(
                    fs::metadata(sidecar).unwrap().permissions().mode() & 0o777,
                    0o600
                );
            }
        }
        drop(db);
        fs::remove_dir_all(home).unwrap();
    }
}

#[test]
fn confirms_replacement_masks_secrets_and_restores_existing_provider_and_selection() {
    for target in ["standard", "rust"] {
        let (home, path, db) = fixture(target);
        let mut original = desired(target);
        let provider = &mut original["providers"]["local-chat"];
        provider["baseUrl"] = "https://example.test/v1".into();
        provider["credential"] = if target == "standard" {
            json!({"type":"apiKey", "apiKey":"previous-private-token"})
        } else {
            json!("previous-private-token")
        };
        provider["headers"] = json!({"Authorization":"Bearer another-private-secret", "X-Custom":"custom-private-value"});
        provider["proxy"] = "http://user:proxy-password@example.test".into();
        if target == "standard" {
            provider["timeoutMs"] = json!(1250.5);
        }
        original["selection"] = json!({"providerId":"other", "modelId":"other-model"});
        store::write(&path, Some("{}"), Some(&original.to_string())).unwrap();
        let place = Place::new(CLIENT, Some(target)).unwrap();
        let models = codex_models();
        let conn = Connection {
            target: Some(target),
            ..connection(&models)
        };
        assert!(matches!(
            write(Home::local(&home), place, &conn, &tokens(), false).unwrap(),
            ApplyOutcome::NeedsConfirmation(_)
        ));
        assert_eq!(saved(&path), original);
        let preview = preview_place(
            Home::local(&home),
            place,
            ORIGIN,
            Some(PreviewWrite {
                selection: Selection::of(
                    &Models {
                        model: Some("gpt-route".into()),
                        ..Default::default()
                    },
                    CLIENT,
                )
                .unwrap(),
                secret: Secret {
                    id: "token_01".into(),
                    token: TOKEN.into(),
                },
                hint: "astr_…AAA0".into(),
                details: Catalog::new(),
            }),
        )
        .unwrap();
        let shown = format!("{preview:?}");
        assert!(
            !shown.contains(TOKEN)
                && !shown.contains("previous-private-token")
                && !shown.contains("another-private-secret")
        );
        assert!(shown.contains("astr_…AAA0"));
        assert!(!shown.contains("custom-private-value") && !shown.contains("proxy-password"));
        write(Home::local(&home), place, &conn, &tokens(), true).unwrap();
        // Moving to another protocol gives the replaced provider back.
        // Port sync must follow the provider still managed by AstrLink.
        let protocols = BTreeMap::from([("gpt-route".to_string(), "openai.responses")]);
        write(
            Home::local(&home),
            place,
            &Connection {
                protocols: &protocols,
                ..conn
            },
            &tokens(),
            true,
        )
        .unwrap();
        sync(Home::local(&home), "http://127.0.0.1:19999", &tokens()).unwrap();
        assert_eq!(
            saved(&path)["providers"]["local-chat"],
            original["providers"]["local-chat"]
        );
        assert_eq!(
            saved(&path)["providers"]["local-responses"]["baseUrl"],
            "http://127.0.0.1:19999/v1"
        );
        remove(Home::local(&home), place).unwrap();
        assert_eq!(saved(&path), original);
        drop(db);
        fs::remove_dir_all(home).unwrap();
    }
}

#[test]
fn keeps_user_changes_and_other_rows() {
    for target in ["standard", "rust"] {
        let (home, path, db) = fixture(target);
        let place = Place::new(CLIENT, Some(target)).unwrap();
        let models = codex_models();
        write(
            Home::local(&home),
            place,
            &Connection {
                target: Some(target),
                ..connection(&models)
            },
            &tokens(),
            false,
        )
        .unwrap();
        let original = store::read(&path).unwrap();
        let mut changed = saved(&path);
        changed["providers"]["local-chat"]["baseUrl"] = "https://changed.test/v1".into();
        changed["selection"] = json!({"providerId":"other", "modelId":"chosen-by-user"});
        store::write(&path, original.as_deref(), Some(&changed.to_string())).unwrap();
        let status =
            status_of(Home::local(&home), [CLIENT], Some(ORIGIN), Some(&tokens())).unwrap();
        assert_eq!(
            status
                .iter()
                .find(|s| s.target == Some(target))
                .unwrap()
                .state,
            ClientState::Modified
        );
        sync(Home::local(&home), "http://127.0.0.1:19999", &tokens()).unwrap();
        remove(Home::local(&home), place).unwrap();
        assert_eq!(saved(&path), changed);
        drop(db);
        fs::remove_dir_all(home).unwrap();
    }
}

#[test]
fn concurrent_changes_and_sql_errors_roll_back_the_whole_write() {
    for target in ["standard", "rust"] {
        let (home, path, db) = fixture(target);
        let next = desired(target).to_string();
        db.execute_batch("CREATE TRIGGER reject_selection BEFORE INSERT ON provider_selections BEGIN SELECT RAISE(ABORT, 'private database detail'); END;").unwrap();
        let error = store::write(&path, Some("{}"), Some(&next)).unwrap_err();
        assert!(!error.contains("private database detail") && !error.contains(TOKEN));
        assert_eq!(saved(&path), json!({}));
        db.execute_batch("DROP TRIGGER reject_selection;").unwrap();
        store::write(&path, Some("{}"), Some(&next)).unwrap();
        assert!(store::write(&path, Some("{}"), Some(&next))
            .unwrap_err()
            .contains("changed"));
        assert_eq!(saved(&path), json(&next));
        drop(db);
        fs::remove_dir_all(home).unwrap();
    }
}

#[test]
fn removal_keeps_workspace_selections_and_rolls_back() {
    let (home, path, db) = fixture("rust");
    let next = desired("rust").to_string();
    store::write(&path, Some("{}"), Some(&next)).unwrap();
    db.execute(
        "INSERT INTO provider_selections VALUES('workspace:test',1,'local-chat','gpt-route')",
        [],
    )
    .unwrap();
    assert!(store::write(&path, Some(&next), None)
        .unwrap_err()
        .contains("workspace"));
    assert_eq!(saved(&path), json(&next));
    assert_eq!(
        db.query_row("SELECT COUNT(*) FROM provider_selections", [], |r| r
            .get::<_, u32>(0))
            .unwrap(),
        2
    );
    drop(db);
    fs::remove_dir_all(home).unwrap();
}

#[test]
fn unsupported_and_oversized_stores_stay_untouched_and_do_not_hide_other_clients() {
    let (home, path, db) = fixture("rust");
    db.execute("UPDATE agent_store_format SET version=999", [])
        .unwrap();
    assert!(
        store::write(&path, Some("{}"), Some(&desired("rust").to_string()))
            .unwrap_err()
            .contains("unsupported")
    );
    let statuses = status(Home::local(&home), Some(ORIGIN), Some(&tokens())).unwrap();
    assert_eq!(
        statuses
            .iter()
            .find(|s| s.client == CLIENT && s.target == Some("rust"))
            .unwrap()
            .state,
        ClientState::Invalid
    );
    assert!(statuses.iter().any(|s| s.client == CODEX));
    db.execute("UPDATE agent_store_format SET version=34", [])
        .unwrap();
    let large = " ".repeat(4_194_305);
    db.execute("INSERT INTO providers VALUES('local-chat',1,?1)", [&large])
        .unwrap();
    assert!(store::read(&path).is_err());
    assert!(store::write(&path, Some("{}"), Some(&desired("rust").to_string())).is_err());
    assert_eq!(
        db.query_row("SELECT length(record) FROM providers", [], |r| r
            .get::<_, i64>(0))
            .unwrap(),
        large.len() as i64
    );
    drop(db);
    fs::remove_dir_all(home).unwrap();
    assert!(store::read(&path).unwrap().is_none());
    assert!(store::write(&path, None, Some("{}")).is_err());
    assert!(!path.exists());
}
