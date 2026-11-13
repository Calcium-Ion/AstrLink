//! A test-only client written in two places, like a client's two editions.

use std::path::PathBuf;

use super::{push_present, ClientSpec, ConfigFile};
use crate::{
    agent_home::Home,
    client_config::{
        client::Client,
        config::Config,
        format::{Document, Format},
        models::{Catalog, CurrentModels},
        plan::Connection,
        records::ClientRecord,
    },
};

pub(crate) const CLIENT: Client = Client::new(&Pair);

struct Pair;

/// `config.json`, strict JSON.
struct PairConfig;

impl ClientSpec for Pair {
    fn id(&self) -> &'static str {
        "pair"
    }

    fn name(&self) -> &'static str {
        "Pair"
    }

    fn protocols(&self) -> &'static [&'static str] {
        &["openai.chat", "anthropic.messages"]
    }

    fn targets(&self) -> &'static [&'static str] {
        &["cn", "intl"]
    }

    fn dir(&self, home: Home, target: Option<&str>) -> PathBuf {
        home.join(format!(".pair-{}", target.unwrap_or("none")))
    }

    fn files(&self) -> &'static [&'static dyn ConfigFile] {
        &[&PairConfig]
    }

    fn config_paths(
        &self,
        home: Home,
        target: Option<&str>,
        _record: Option<&ClientRecord>,
    ) -> Vec<PathBuf> {
        vec![self.dir(home, target).join("config.json")]
    }

    fn connection_keys(&self) -> &'static [&'static str] {
        &["baseUrl", "apiKey"]
    }

    fn current_models(&self, config: &Config) -> CurrentModels {
        CurrentModels {
            model: config.model("model"),
            ..Default::default()
        }
    }

    fn desired_keys(&self, connection: &Connection, _details: &Catalog) -> Vec<(String, String)> {
        let mut keys = vec![
            ("baseUrl".into(), self.base_url(connection.origin)),
            ("apiKey".into(), connection.token.into()),
        ];
        if let Some(model) = connection.model("model") {
            keys.push(("model".into(), model));
        }
        keys
    }

    fn base_url_key(&self) -> &'static str {
        "baseUrl"
    }
}

impl ConfigFile for PairConfig {
    fn format(&self) -> Format {
        Format::Json {
            syntax: None,
            objects: &[],
            root_list: false,
        }
    }

    fn group(&self, document: &Document) -> Vec<(String, Option<String>)> {
        let mut keys = Vec::new();
        for key in ["baseUrl", "apiKey", "model"] {
            push_present(&mut keys, self, document, key);
        }
        keys
    }
}

#[cfg(test)]
mod tests {
    use crate::client_config::test_support::*;

    fn records(home: &Path) -> serde_json::Value {
        json(&fs::read_to_string(records_path(home)).unwrap())
    }

    fn config(home: &Path, target: &str) -> Option<serde_json::Value> {
        fs::read_to_string(home.join(format!(".pair-{target}/config.json")))
            .ok()
            .map(|text| json(&text))
    }

    #[test]
    fn each_target_is_a_place_of_its_own() {
        let home = unique_home("pair");
        let local = Home::local(&home);
        let cn = Place::new(PAIR, Some("cn")).unwrap();
        let intl = Place::new(PAIR, Some("intl")).unwrap();
        let models = vec![("model", "pair-route".to_string())];
        let statuses = || {
            status_of(
                local,
                [PAIR],
                Some("http://127.0.0.1:18317/"),
                Some(&tokens()),
            )
            .unwrap()
            .into_iter()
            .map(|status| (status.target, status.detected, status.state))
            .collect::<Vec<_>>()
        };
        assert_eq!(
            statuses(),
            [
                (Some("cn"), false, ClientState::NotConfigured),
                (Some("intl"), false, ClientState::NotConfigured),
            ]
        );
        assert!(write(local, cn, &connection(&models), &tokens(), false).is_err());
        fs::create_dir_all(home.join(".pair-cn")).unwrap();
        fs::create_dir_all(home.join(".pair-intl")).unwrap();

        // Version 1 holds the clients released builds know, and nothing else.
        write(local, CLAUDE, &connection(&[]), &tokens(), false).unwrap();
        assert_eq!(records(&home)["version"], 1);
        assert_eq!(
            asked(write(local, cn, &connection(&models), &tokens(), false).unwrap()),
            None
        );
        let written = records(&home);
        assert_eq!(written["version"], 2);
        assert_eq!(written["pair:cn"]["token_id"], "token_01");
        assert!(written.get("pair:intl").is_none() && written.get("pair").is_none());
        assert_eq!(
            config(&home, "cn"),
            Some(serde_json::json!({
                "baseUrl": format!("{ORIGIN}/v1"),
                "apiKey": TOKEN,
                "model": "pair-route",
            }))
        );
        assert_eq!(config(&home, "intl"), None);
        assert_eq!(
            statuses(),
            [
                (Some("cn"), true, ClientState::Configured),
                (Some("intl"), true, ClientState::NotConfigured),
            ]
        );
        let shown = status_of(local, [PAIR], None, None).unwrap();
        assert_eq!(serde_json::to_value(&shown[0]).unwrap()["target"], "cn");
        assert_eq!(serde_json::to_value(&shown[1]).unwrap()["client"], "pair");

        // A batch names the place by its target.
        let secret = Secret {
            id: "token_01".into(),
            token: TOKEN.into(),
        };
        let selection = Selection::of(
            &serde_json::from_value(serde_json::json!({"model": "intl-route"})).unwrap(),
            PAIR,
        )
        .unwrap();
        let jobs = [Job {
            home: Some(local),
            client: PAIR,
            target: Some("intl"),
            wsl: None,
            action: Action::Write,
            selection: Some(selection),
            details: Catalog::new(),
        }];
        let outcome = run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), false);
        assert!(matches!(outcome, BatchOutcome::Done { results } if results[0].ok));
        assert_eq!(config(&home, "intl").unwrap()["model"], "intl-route");
        assert_eq!(config(&home, "cn").unwrap()["model"], "pair-route");
        assert_eq!(
            read_records(&home).unwrap().places(),
            [CLAUDE.into(), cn, intl]
        );

        // Port sync follows every place.
        sync(local, "http://127.0.0.1:9000/", &tokens()).unwrap();
        for target in ["cn", "intl"] {
            assert_eq!(
                config(&home, target).unwrap()["baseUrl"],
                "http://127.0.0.1:9000/v1"
            );
        }
        let preview = preview_place(local, intl, ORIGIN, None).unwrap();
        assert_eq!(preview.files.len(), 1);
        assert!(preview.files[0].path.ends_with("config.json"));

        // Removing one target leaves the other alone, and the file goes back
        // to version 1 once only the released keys are left.
        remove(local, cn).unwrap();
        assert_eq!(config(&home, "cn"), None);
        assert!(config(&home, "intl").is_some());
        let left = records(&home);
        assert!(left.get("pair:cn").is_none());
        assert_eq!(left["pair:intl"]["token_id"], "token_01");
        remove(local, intl).unwrap();
        assert_eq!(records(&home)["version"], 1);
        assert!(read_records(&home).unwrap().get(intl).is_none());
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn a_batch_tells_the_targets_it_asks_about_apart() {
        let home = unique_home("pair-batch");
        let local = Home::local(&home);
        for target in ["cn", "intl"] {
            fs::create_dir_all(home.join(format!(".pair-{target}"))).unwrap();
            fs::write(
                home.join(format!(".pair-{target}/config.json")),
                format!("{{\"apiKey\": \"sk-{target}-secret\"}}\n"),
            )
            .unwrap();
        }
        write(local, CLAUDE, &connection(&[]), &tokens(), false).unwrap();
        let secret = Secret {
            id: "token_01".into(),
            token: TOKEN.into(),
        };
        let job = |client, target, action| Job {
            home: Some(local),
            client,
            target,
            wsl: None,
            action,
            selection: (action == Action::Write).then(|| {
                Selection::of(
                    &serde_json::from_value(serde_json::json!({"model": "pair-route"})).unwrap(),
                    client,
                )
                .unwrap()
            }),
            details: Catalog::new(),
        };
        let jobs = [
            job(PAIR, Some("cn"), Action::Write),
            job(PAIR, Some("intl"), Action::Write),
            job(CLAUDE, None, Action::Remove),
        ];
        let places = |value: &serde_json::Value, list: &str| {
            value[list]
                .as_array()
                .unwrap()
                .iter()
                .map(|item| (item["client"].clone(), item["target"].clone()))
                .collect::<Vec<_>>()
        };
        let expected = [
            ("pair".into(), "cn".into()),
            ("pair".into(), "intl".into()),
            ("claude".into(), serde_json::Value::Null),
        ];

        let asked = serde_json::to_value(run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), false))
            .unwrap();
        assert_eq!(asked["status"], "needs_confirmation");
        assert_eq!(places(&asked, "items"), expected);
        for (item, target) in asked["items"]
            .as_array()
            .unwrap()
            .iter()
            .zip(["cn", "intl"])
        {
            assert_eq!(item["keys"][0]["key"], "apiKey");
            assert!(item["dir"]
                .as_str()
                .unwrap()
                .ends_with(&format!(".pair-{target}")));
        }
        assert!(asked["items"][2]["remove"].as_bool().unwrap());
        assert_eq!(config(&home, "cn").unwrap()["apiKey"], "sk-cn-secret");

        let done =
            serde_json::to_value(run(&jobs, Some(&secret), ORIGIN, Some(&tokens()), true)).unwrap();
        assert_eq!(done["status"], "done");
        assert_eq!(places(&done, "results"), expected);
        assert!(done["results"]
            .as_array()
            .unwrap()
            .iter()
            .all(|result| result["ok"] == true));
        for target in ["cn", "intl"] {
            assert_eq!(config(&home, target).unwrap()["apiKey"], TOKEN);
        }
        fs::remove_dir_all(home).unwrap();
    }

    #[test]
    fn items_name_a_target_and_a_known_client() {
        let item = |value: serde_json::Value| serde_json::from_value::<ApplyItem>(value);
        let named = item(serde_json::json!({
            "client": "pair", "wsl": null, "target": "cn", "action": "remove"
        }))
        .unwrap();
        assert_eq!(
            Place::new(named.client, named.target.as_deref()).unwrap(),
            Place::new(PAIR, Some("cn")).unwrap()
        );
        // The round-1 shape still reads, without a target.
        let plain =
            item(serde_json::json!({"client": "codex", "wsl": null, "action": "remove"})).unwrap();
        assert_eq!(plain.target, None);
        assert!(
            item(serde_json::json!({"client": "cursor", "wsl": null, "action": "remove"})).is_err()
        );
    }
}
