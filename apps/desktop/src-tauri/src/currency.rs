//! The currency amounts estimated at official API prices are shown in. Core
//! accounts in US dollars and every bridge field stays in dollars; this module
//! only picks the display currency and keeps one daily exchange rate table.

use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
    sync::{Arc, Mutex, MutexGuard, PoisonError},
    time::Duration,
};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager};

use crate::preferences::PreferencesStore;

/// The setting value that follows the system region.
pub const AUTO: &str = "auto";
pub const DISPLAY_CURRENCY_EVENT: &str = "display-currency-changed";
const USD: &str = "USD";
const FILE_NAME: &str = "exchange-rates.json";

/// Currencies the setting offers, with the symbol and decimals shown. Mirrors
/// `DISPLAY_CURRENCIES` in `src/currency-display.ts`.
const CURRENCIES: &[(&str, &str, u8)] = &[
    ("USD", "$", 2),
    ("CNY", "¥", 2),
    ("HKD", "HK$", 2),
    ("TWD", "NT$", 2),
    ("JPY", "¥", 0),
    ("KRW", "₩", 0),
    ("SGD", "S$", 2),
    ("EUR", "€", 2),
    ("GBP", "£", 2),
    ("CHF", "CHF ", 2),
    ("CAD", "CA$", 2),
    ("AUD", "A$", 2),
    ("INR", "₹", 2),
    ("RUB", "₽", 2),
    ("BRL", "R$", 2),
];

/// Free daily USD rates (CC0, no key). The project asks clients to fall back
/// to its Cloudflare copy when jsDelivr fails.
const RATE_SOURCES: [&str; 2] = [
    "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/usd.min.json",
    "https://latest.currency-api.pages.dev/v1/currencies/usd.min.json",
];
/// The sources publish once a day; refetch twice as often.
const REFRESH_AFTER: chrono::TimeDelta = chrono::TimeDelta::hours(12);
/// How soon a failed or skipped refresh is tried again.
const CHECK_INTERVAL: Duration = Duration::from_secs(30 * 60);
const FETCH_TIMEOUT: Duration = Duration::from_secs(20);
const MAX_SOURCE_BYTES: usize = 1 << 20;

fn offered(code: &str) -> Option<&'static (&'static str, &'static str, u8)> {
    CURRENCIES.iter().find(|(offered, ..)| *offered == code)
}

/// Whether `value` is a valid display currency setting.
pub fn is_choice(value: &str) -> bool {
    value == AUTO || offered(value).is_some()
}

/// The system's currency when the setting offers it, otherwise USD.
pub fn system_currency() -> &'static str {
    platform_currency()
        .as_deref()
        .and_then(offered)
        .or_else(|| {
            sys_locale::get_locale()
                .as_deref()
                .and_then(locale_region)
                .and_then(region_currency)
                .and_then(offered)
        })
        .map_or(USD, |(code, ..)| code)
}

/// The currency of the region picked in System Settings, which the interface
/// language does not tell (an English system can be set to China).
#[cfg(target_os = "macos")]
fn platform_currency() -> Option<String> {
    objc2_foundation::NSLocale::currentLocale()
        .currencyCode()
        .map(|code| code.to_string())
}

#[cfg(windows)]
fn platform_currency() -> Option<String> {
    use windows_sys::Win32::Globalization::{GetLocaleInfoEx, LOCALE_SINTLSYMBOL};
    let mut buffer = [0u16; 16];
    // A null name is LOCALE_NAME_USER_DEFAULT, the user's region format.
    let written = unsafe {
        GetLocaleInfoEx(
            std::ptr::null(),
            LOCALE_SINTLSYMBOL,
            buffer.as_mut_ptr(),
            buffer.len() as i32,
        )
    };
    // The count includes the terminating null.
    let length = usize::try_from(written).ok()?.checked_sub(1)?;
    Some(String::from_utf16_lossy(buffer.get(..length)?))
}

/// POSIX: the monetary locale, read in the order the C library applies it.
#[cfg(not(any(target_os = "macos", windows)))]
fn platform_currency() -> Option<String> {
    ["LC_ALL", "LC_MONETARY", "LANG"]
        .into_iter()
        .find_map(|name| std::env::var(name).ok().filter(|value| !value.is_empty()))
        .as_deref()
        .and_then(locale_region)
        .and_then(region_currency)
        .map(str::to_string)
}

/// The region of a locale name such as `zh_CN.UTF-8`, `en-US` or
/// `zh-Hans-CN`.
fn locale_region(locale: &str) -> Option<&str> {
    let name = locale.split(['.', '@']).next()?;
    name.split(['-', '_'])
        .skip(1)
        .find(|part| part.len() == 2 && part.bytes().all(|byte| byte.is_ascii_uppercase()))
}

/// Only regions whose currency the setting offers; the rest show USD.
fn region_currency(region: &str) -> Option<&'static str> {
    Some(match region {
        "US" => "USD",
        "CN" => "CNY",
        "HK" => "HKD",
        "TW" => "TWD",
        "JP" => "JPY",
        "KR" => "KRW",
        "SG" => "SGD",
        "GB" => "GBP",
        "CH" | "LI" => "CHF",
        "CA" => "CAD",
        "AU" => "AUD",
        "IN" => "INR",
        "RU" => "RUB",
        "BR" => "BRL",
        "AT" | "BE" | "BG" | "CY" | "DE" | "EE" | "ES" | "FI" | "FR" | "GR" | "HR" | "IE"
        | "IT" | "LT" | "LU" | "LV" | "MT" | "NL" | "PT" | "SI" | "SK" | "AD" | "MC" | "SM"
        | "VA" | "ME" | "XK" => "EUR",
        _ => return None,
    })
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct RateTable {
    /// The day the source published the rates, `YYYY-MM-DD`.
    date: String,
    fetched_at: DateTime<Utc>,
    /// Units per US dollar for the offered currencies the source had.
    per_usd: BTreeMap<String, f64>,
}

impl RateTable {
    fn rate(&self, code: &str) -> Option<f64> {
        self.per_usd
            .get(code)
            .copied()
            .filter(|rate| rate.is_finite() && *rate > 0.0)
    }
}

/// Reads a source body: `{"date": "2026-11-01", "usd": {"cny": 7.1, ...}}`.
fn parse_source(body: &[u8], fetched_at: DateTime<Utc>) -> Result<RateTable, String> {
    #[derive(Deserialize)]
    struct Source {
        date: String,
        usd: BTreeMap<String, serde_json::Value>,
    }
    let source: Source = serde_json::from_slice(body).map_err(|error| error.to_string())?;
    if chrono::NaiveDate::parse_from_str(&source.date, "%Y-%m-%d").is_err() {
        return Err(format!("unexpected rate date {:?}", source.date));
    }
    let per_usd: BTreeMap<String, f64> = CURRENCIES
        .iter()
        .filter(|(code, ..)| *code != USD)
        .filter_map(|(code, ..)| {
            let rate = source.usd.get(&code.to_ascii_lowercase())?.as_f64()?;
            (rate.is_finite() && rate > 0.0).then(|| (code.to_string(), rate))
        })
        .collect();
    if per_usd.is_empty() {
        return Err("the rate table lists none of the offered currencies".into());
    }
    Ok(RateTable {
        date: source.date,
        fetched_at,
        per_usd,
    })
}

/// What the interface shows amounts in, sent to every window.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct DisplayCurrency {
    /// The currency amounts are shown in; USD until `wanted` has a rate.
    pub code: &'static str,
    pub symbol: &'static str,
    pub digits: u8,
    /// Units of `code` per US dollar.
    pub per_usd: f64,
    /// The day the rate was published; None for USD.
    pub rate_date: Option<String>,
    /// The currency the setting resolves to.
    pub wanted: &'static str,
    /// What the automatic setting resolves to on this system.
    pub system: &'static str,
}

impl DisplayCurrency {
    #[cfg(test)]
    pub fn usd() -> Self {
        Self::resolve(AUTO, USD, None)
    }

    fn resolve(choice: &str, system: &'static str, table: Option<&RateTable>) -> Self {
        let wanted =
            offered(if choice == AUTO { system } else { choice }).map_or(USD, |(code, ..)| code);
        let rate = if wanted == USD {
            None
        } else {
            table.and_then(|table| Some((table.rate(wanted)?, table.date.clone())))
        };
        let (code, per_usd, rate_date) = match rate {
            Some((per_usd, date)) => (wanted, per_usd, Some(date)),
            None => (USD, 1.0, None),
        };
        let &(code, symbol, digits) = offered(code).expect("USD and wanted are offered");
        Self {
            code,
            symbol,
            digits,
            per_usd,
            rate_date,
            wanted,
            system,
        }
    }

    /// An estimate for the menu bar: `¥5.55`, `<¥0.01`, `¥0.00`.
    pub fn format(&self, amount_usd: f64) -> String {
        let digits = usize::from(self.digits);
        let amount = amount_usd * self.per_usd;
        let unit = 10f64.powi(-i32::from(self.digits));
        if !amount.is_finite() || amount <= 0.0 {
            format!("{}{:.*}", self.symbol, digits, 0.0)
        } else if amount < unit {
            format!("<{}{:.*}", self.symbol, digits, unit)
        } else {
            format!("{}{:.*}", self.symbol, digits, amount)
        }
    }
}

/// The last rate table, cached next to the preferences so a restart offline
/// keeps the previous day's rates.
pub struct ExchangeRates {
    path: PathBuf,
    table: Mutex<Option<RateTable>>,
    wake: tokio::sync::Notify,
}

impl ExchangeRates {
    pub fn load(config_directory: &Path) -> Self {
        let path = config_directory.join(FILE_NAME);
        let table = match crate::host_files::read_optional(&path) {
            Ok(Some(raw)) => serde_json::from_str(&raw)
                .map_err(|error| eprintln!("ignoring cached exchange rates: {error}"))
                .ok(),
            Ok(None) => None,
            Err(error) => {
                eprintln!("{error}");
                None
            }
        };
        Self {
            path,
            table: Mutex::new(table),
            wake: tokio::sync::Notify::new(),
        }
    }

    /// Checks again now, after the setting changed.
    pub fn wake(&self) {
        self.wake.notify_one();
    }

    fn lock(&self) -> MutexGuard<'_, Option<RateTable>> {
        self.table.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn needs_refresh(&self, wanted: &str, now: DateTime<Utc>) -> bool {
        wanted != USD
            && self.lock().as_ref().map_or(true, |table| {
                table.rate(wanted).is_none() || now - table.fetched_at >= REFRESH_AFTER
            })
    }

    async fn tick(&self, app: &AppHandle) {
        let Some(preferences) = app.try_state::<Arc<PreferencesStore>>() else {
            return;
        };
        let values = preferences.snapshot().values;
        let wanted =
            DisplayCurrency::resolve(&values.display_currency, system_currency(), None).wanted;
        // USD needs no rate, so it sends nothing anywhere.
        if !self.needs_refresh(wanted, Utc::now()) {
            return;
        }
        let mut builder = reqwest::Client::builder().timeout(FETCH_TIMEOUT);
        if !values.use_system_proxy {
            builder = builder.no_proxy();
        }
        let table = match builder.build() {
            Ok(client) => fetch(&client).await,
            Err(error) => Err(error.to_string()),
        };
        let table = match table {
            Ok(table) => table,
            Err(error) => {
                eprintln!("unable to refresh exchange rates: {error}");
                return;
            }
        };
        match serde_json::to_string_pretty(&table) {
            Ok(json) => {
                if let Err(error) =
                    crate::host_files::write_text(&self.path, &json, Default::default())
                {
                    eprintln!("unable to cache exchange rates: {error}");
                }
            }
            Err(error) => eprintln!("unable to cache exchange rates: {error}"),
        }
        *self.lock() = Some(table);
        broadcast(app);
    }
}

async fn fetch(client: &reqwest::Client) -> Result<RateTable, String> {
    let mut errors = Vec::new();
    for url in RATE_SOURCES {
        match fetch_from(client, url).await {
            Ok(table) => return Ok(table),
            Err(error) => errors.push(format!("{url}: {error}")),
        }
    }
    Err(errors.join("; "))
}

async fn fetch_from(client: &reqwest::Client, url: &str) -> Result<RateTable, String> {
    let response = client
        .get(url)
        .send()
        .await
        .and_then(reqwest::Response::error_for_status)
        .map_err(|error| error.to_string())?;
    if response
        .content_length()
        .is_some_and(|length| length > MAX_SOURCE_BYTES as u64)
    {
        return Err("the rate table is too large".into());
    }
    let body = response.bytes().await.map_err(|error| error.to_string())?;
    if body.len() > MAX_SOURCE_BYTES {
        return Err("the rate table is too large".into());
    }
    parse_source(&body, Utc::now())
}

/// The display currency from the stored setting and the cached rates.
pub fn display(app: &AppHandle) -> DisplayCurrency {
    let choice = app.try_state::<Arc<PreferencesStore>>().map_or_else(
        || AUTO.to_string(),
        |store| store.snapshot().values.display_currency,
    );
    let rates = app.try_state::<Arc<ExchangeRates>>();
    let table = rates.as_ref().and_then(|rates| rates.lock().clone());
    DisplayCurrency::resolve(&choice, system_currency(), table.as_ref())
}

/// Sends the current display currency to every window and the menu bar.
pub fn broadcast(app: &AppHandle) {
    if let Err(error) = app.emit(DISPLAY_CURRENCY_EVENT, display(app)) {
        eprintln!("unable to broadcast the display currency: {error}");
    }
    crate::tray::refresh(app);
}

pub fn start(app: &AppHandle) {
    let Some(rates) = app.try_state::<Arc<ExchangeRates>>() else {
        return;
    };
    let rates = Arc::clone(rates.inner());
    let app = app.clone();
    tauri::async_runtime::spawn(async move {
        loop {
            rates.tick(&app).await;
            tokio::select! {
                _ = tokio::time::sleep(CHECK_INTERVAL) => {}
                _ = rates.wake.notified() => {}
            }
        }
    });
}

#[tauri::command]
pub fn display_currency(app: AppHandle) -> DisplayCurrency {
    display(&app)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn table(rates: &[(&str, f64)]) -> RateTable {
        RateTable {
            date: "2026-11-01".into(),
            fetched_at: DateTime::parse_from_rfc3339("2026-11-01T08:00:00Z")
                .unwrap()
                .with_timezone(&Utc),
            per_usd: rates
                .iter()
                .map(|(code, rate)| (code.to_string(), *rate))
                .collect(),
        }
    }

    #[test]
    fn source_keeps_the_offered_currencies_with_usable_rates() {
        let body = br#"{"date":"2026-11-01","usd":{"usd":1,"cny":6.65644497,"jpy":157.57,"eur":-1,"krw":"1336","btc":0.00001}}"#;
        let parsed = parse_source(body, table(&[]).fetched_at).unwrap();
        assert_eq!(parsed.date, "2026-11-01");
        assert_eq!(
            parsed.per_usd,
            BTreeMap::from([("CNY".to_string(), 6.65644497), ("JPY".to_string(), 157.57)])
        );
        for body in [
            &br#"{"date":"yesterday","usd":{"cny":6.6}}"#[..],
            br#"{"date":"2026-11-01","usd":{"btc":0.00001}}"#,
            br#"{"date":"2026-11-01"}"#,
            b"<html>",
        ] {
            assert!(parse_source(body, Utc::now()).is_err());
        }
    }

    #[test]
    fn cached_table_round_trips() {
        let cached = table(&[("CNY", 6.65)]);
        let json = serde_json::to_string(&cached).unwrap();
        assert_eq!(serde_json::from_str::<RateTable>(&json).unwrap(), cached);
    }

    #[test]
    fn locale_names_give_their_region() {
        for (locale, region) in [
            ("zh_CN.UTF-8", Some("CN")),
            ("en-US", Some("US")),
            ("zh-Hans-CN", Some("CN")),
            ("de_DE@euro", Some("DE")),
            ("es-419", None),
            ("C", None),
            ("POSIX", None),
            ("", None),
        ] {
            assert_eq!(locale_region(locale), region, "{locale}");
        }
        assert_eq!(region_currency("CN"), Some("CNY"));
        assert_eq!(region_currency("FR"), Some("EUR"));
        assert_eq!(region_currency("TH"), None);
        for region in ["US", "CN", "HK", "TW", "JP", "KR", "DE", "CH", "BR"] {
            assert!(offered(region_currency(region).unwrap()).is_some());
        }
    }

    #[test]
    fn choices_are_auto_or_an_offered_code() {
        assert!(is_choice("auto"));
        assert!(is_choice("CNY"));
        assert!(!is_choice("cny"));
        assert!(!is_choice("THB"));
        assert!(!is_choice(""));
    }

    #[test]
    fn display_follows_the_setting_and_falls_back_to_usd_without_a_rate() {
        let rates = table(&[("CNY", 6.65), ("JPY", 157.0)]);
        let auto = DisplayCurrency::resolve(AUTO, "CNY", Some(&rates));
        assert_eq!((auto.code, auto.wanted, auto.system), ("CNY", "CNY", "CNY"));
        assert_eq!(auto.per_usd, 6.65);
        assert_eq!(auto.rate_date.as_deref(), Some("2026-11-01"));

        let picked = DisplayCurrency::resolve("JPY", "CNY", Some(&rates));
        assert_eq!((picked.code, picked.symbol, picked.digits), ("JPY", "¥", 0));

        let missing = DisplayCurrency::resolve("EUR", "CNY", Some(&rates));
        assert_eq!((missing.code, missing.wanted), ("USD", "EUR"));
        assert_eq!(missing.per_usd, 1.0);
        assert_eq!(missing.rate_date, None);

        let offline = DisplayCurrency::resolve(AUTO, "CNY", None);
        assert_eq!((offline.code, offline.wanted), ("USD", "CNY"));

        let usd = DisplayCurrency::resolve("USD", "CNY", Some(&rates));
        assert_eq!((usd.code, usd.per_usd, usd.rate_date), ("USD", 1.0, None));

        // A setting from a newer build that this one does not offer.
        assert_eq!(DisplayCurrency::resolve("THB", "CNY", None).wanted, "USD");
    }

    #[test]
    fn amounts_format_in_the_display_currency() {
        let usd = DisplayCurrency::usd();
        assert_eq!(usd.format(0.0), "$0.00");
        assert_eq!(usd.format(0.004), "<$0.01");
        assert_eq!(usd.format(0.834), "$0.83");
        assert_eq!(usd.format(f64::NAN), "$0.00");

        let rates = table(&[("CNY", 6.65), ("JPY", 157.0), ("CHF", 0.8)]);
        let cny = DisplayCurrency::resolve("CNY", USD, Some(&rates));
        assert_eq!(cny.format(0.834), "¥5.55");
        assert_eq!(cny.format(0.001), "<¥0.01");
        let jpy = DisplayCurrency::resolve("JPY", USD, Some(&rates));
        assert_eq!(jpy.format(0.834), "¥131");
        assert_eq!(jpy.format(0.002), "<¥1");
        let chf = DisplayCurrency::resolve("CHF", USD, Some(&rates));
        assert_eq!(chf.format(2.0), "CHF 1.60");
    }

    #[test]
    fn refresh_waits_for_a_stale_or_incomplete_table() {
        let rates = ExchangeRates {
            path: PathBuf::new(),
            table: Mutex::new(None),
            wake: tokio::sync::Notify::new(),
        };
        let fetched = table(&[]).fetched_at;
        assert!(!rates.needs_refresh(USD, fetched));
        assert!(rates.needs_refresh("CNY", fetched));
        *rates.lock() = Some(table(&[("CNY", 6.65)]));
        assert!(!rates.needs_refresh("CNY", fetched + chrono::TimeDelta::hours(11)));
        assert!(rates.needs_refresh("CNY", fetched + REFRESH_AFTER));
        assert!(rates.needs_refresh("EUR", fetched));
    }
}
