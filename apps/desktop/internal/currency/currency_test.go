package currency

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func table(rates map[string]float64) *rateTable {
	return &rateTable{
		Date:      "2026-11-01",
		FetchedAt: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC),
		PerUSD:    rates,
	}
}

func TestSourceKeepsTheOfferedCurrenciesWithUsableRates(t *testing.T) {
	body := []byte(`{"date":"2026-11-01","usd":{"usd":1,"cny":6.65644497,"jpy":157.57,"eur":-1,"krw":"1336","btc":0.00001}}`)
	parsed, err := parseSource(body, table(nil).FetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Date != "2026-11-01" || !reflect.DeepEqual(parsed.PerUSD, map[string]float64{"CNY": 6.65644497, "JPY": 157.57}) {
		t.Fatalf("parsed = %+v", parsed)
	}
	for _, body := range []string{
		`{"date":"yesterday","usd":{"cny":6.6}}`,
		`{"date":"2026-11-01","usd":{"btc":0.00001}}`,
		`{"date":"2026-11-01"}`,
		`<html>`,
	} {
		if _, err := parseSource([]byte(body), time.Now()); err == nil {
			t.Fatalf("%s parsed", body)
		}
	}
}

func TestCachedTableRoundTrips(t *testing.T) {
	cached := table(map[string]float64{"CNY": 6.65})
	encoded, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	var decoded rateTable
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&decoded, cached) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestLocaleNamesGiveTheirRegion(t *testing.T) {
	for locale, region := range map[string]string{
		"zh_CN.UTF-8": "CN", "en-US": "US", "zh-Hans-CN": "CN", "de_DE@euro": "DE",
		"es-419": "", "C": "", "POSIX": "", "": "",
	} {
		if got := localeRegion(locale); got != region {
			t.Fatalf("localeRegion(%q) = %q, want %q", locale, got, region)
		}
	}
	if regionCurrency("CN") != "CNY" || regionCurrency("FR") != "EUR" || regionCurrency("TH") != "" {
		t.Fatal("region currencies are wrong")
	}
	for _, region := range []string{"US", "CN", "HK", "TW", "JP", "KR", "DE", "CH", "BR"} {
		if _, ok := offered(regionCurrency(region)); !ok {
			t.Fatalf("the currency of %s is not offered", region)
		}
	}
}

func TestTheSystemCurrencyIsTheFirstRegionTheSettingOffers(t *testing.T) {
	// An English system set to the region of China shows yuan.
	if got := SystemCurrency([]string{"en-CN", "en-US"}); got != "CNY" {
		t.Fatalf("SystemCurrency = %s", got)
	}
	if got := SystemCurrency([]string{"th-TH", "", "de_DE.UTF-8"}); got != "EUR" {
		t.Fatalf("SystemCurrency = %s", got)
	}
	if got := SystemCurrency(nil); got != "USD" {
		t.Fatalf("SystemCurrency = %s", got)
	}
	env := map[string]string{"LC_MONETARY": "ja_JP.UTF-8", "LANG": "en_US.UTF-8"}
	if got := PosixLocale(func(name string) string { return env[name] }); got != "ja_JP.UTF-8" {
		t.Fatalf("PosixLocale = %s", got)
	}
}

func TestDisplayFollowsTheSettingAndFallsBackToUSDWithoutARate(t *testing.T) {
	rates := table(map[string]float64{"CNY": 6.65, "JPY": 157})
	auto := resolve(Auto, "CNY", rates)
	if auto.Code != "CNY" || auto.Wanted != "CNY" || auto.System != "CNY" || auto.PerUSD != 6.65 ||
		auto.RateDate == nil || *auto.RateDate != "2026-11-01" {
		t.Fatalf("auto = %+v", auto)
	}
	picked := resolve("JPY", "CNY", rates)
	if picked.Code != "JPY" || picked.Symbol != "¥" || picked.Digits != 0 {
		t.Fatalf("picked = %+v", picked)
	}
	missing := resolve("EUR", "CNY", rates)
	if missing.Code != "USD" || missing.Wanted != "EUR" || missing.PerUSD != 1 || missing.RateDate != nil {
		t.Fatalf("missing = %+v", missing)
	}
	if offline := resolve(Auto, "CNY", nil); offline.Code != "USD" || offline.Wanted != "CNY" {
		t.Fatalf("offline = %+v", offline)
	}
	if u := resolve("USD", "CNY", rates); u.Code != "USD" || u.PerUSD != 1 || u.RateDate != nil {
		t.Fatalf("usd = %+v", u)
	}
	// A setting from a newer build that this one does not offer.
	if got := resolve("THB", "CNY", nil).Wanted; got != "USD" {
		t.Fatalf("wanted = %s", got)
	}
	encoded, _ := json.Marshal(USD())
	if string(encoded) != `{"code":"USD","symbol":"$","digits":2,"per_usd":1,"rate_date":null,"wanted":"USD","system":"USD"}` {
		t.Fatalf("USD encodes as %s", encoded)
	}
}

func TestAmountsFormatInTheDisplayCurrency(t *testing.T) {
	u := USD()
	for amount, want := range map[float64]string{0: "$0.00", 0.004: "<$0.01", 0.834: "$0.83"} {
		if got := u.Format(amount); got != want {
			t.Fatalf("Format(%v) = %s, want %s", amount, got, want)
		}
	}
	if got := u.Format(math.NaN()); got != "$0.00" {
		t.Fatalf("Format(NaN) = %s", got)
	}
	rates := table(map[string]float64{"CNY": 6.65, "JPY": 157, "CHF": 0.8})
	for _, c := range []struct {
		code   string
		amount float64
		want   string
	}{
		{"CNY", 0.834, "¥5.55"}, {"CNY", 0.001, "<¥0.01"},
		{"JPY", 0.834, "¥131"}, {"JPY", 0.002, "<¥1"},
		{"CHF", 2, "CHF 1.60"},
	} {
		if got := resolve(c.code, usd, rates).Format(c.amount); got != c.want {
			t.Fatalf("%s Format(%v) = %s, want %s", c.code, c.amount, got, c.want)
		}
	}
}

func TestRefreshWaitsForAStaleOrIncompleteTable(t *testing.T) {
	r := &Rates{}
	fetched := table(nil).FetchedAt
	if r.needsRefresh(usd, fetched) || !r.needsRefresh("CNY", fetched) {
		t.Fatal("an empty table")
	}
	r.table = table(map[string]float64{"CNY": 6.65})
	if r.needsRefresh("CNY", fetched.Add(11*time.Hour)) {
		t.Fatal("a fresh table refreshed")
	}
	if !r.needsRefresh("CNY", fetched.Add(refreshAfter)) || !r.needsRefresh("EUR", fetched) {
		t.Fatal("a stale or incomplete table waited")
	}
}

// TestATickFetchesCachesAndAnnouncesTheRates serves the rates from a test
// server: the first source fails, the fallback answers, and the table is
// cached for the next start.
func TestATickFetchesCachesAndAnnouncesTheRates(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer failing.Close()
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"date":"2026-11-12","usd":{"cny":7.1}}`))
	}))
	defer working.Close()

	dir := t.TempDir()
	r := Load(dir)
	r.sources = []string{failing.URL, working.URL}
	r.client = func(bool) *http.Client { return http.DefaultClient }
	r.Choice = func() string { return Auto }
	r.System = func() string { return "CNY" }
	r.UseSystemProxy = func() bool { return true }
	changed := 0
	r.Changed = func() { changed++ }
	if got := r.Display().Code; got != "USD" {
		t.Fatalf("before a fetch the display is %s", got)
	}
	r.tick(context.Background())
	if changed != 1 {
		t.Fatalf("changed %d times", changed)
	}
	if d := r.Display(); d.Code != "CNY" || d.PerUSD != 7.1 || *d.RateDate != "2026-11-12" {
		t.Fatalf("display = %+v", d)
	}
	// Fresh now: a second tick sends nothing.
	r.tick(context.Background())
	if changed != 1 {
		t.Fatal("a fresh table was fetched again")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatal(err)
	}
	reloaded := Load(dir)
	reloaded.Choice, reloaded.System = r.Choice, r.System
	if d := reloaded.Display(); d.Code != "CNY" {
		t.Fatalf("the cached table was not read back: %+v", d)
	}
}

func TestATickWithUSDSendsNothing(t *testing.T) {
	r := Load(t.TempDir())
	r.sources = nil
	r.client = func(bool) *http.Client {
		t.Fatal("USD fetched rates")
		return nil
	}
	r.Choice = func() string { return "USD" }
	r.System = func() string { return "CNY" }
	r.tick(context.Background())
}

func TestRunEndsWithItsContext(t *testing.T) {
	r := Load(t.TempDir())
	r.Choice = func() string { return "USD" }
	r.System = func() string { return "USD" }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	r.Wake()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept running after its context ended")
	}
}
