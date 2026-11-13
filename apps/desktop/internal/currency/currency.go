// Package currency picks the currency amounts estimated at official API
// prices are shown in, and keeps one daily exchange rate table. Core
// accounts in US dollars and every bridge field stays in dollars.
package currency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/netproxy"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

// Auto is the setting value that follows the system region.
const Auto = "auto"

const (
	usd      = "USD"
	fileName = "exchange-rates.json"
	// refreshAfter: the sources publish once a day; refetch twice as often.
	refreshAfter = 12 * time.Hour
	// checkInterval is how soon a failed or skipped refresh is tried again.
	checkInterval  = 30 * time.Minute
	fetchTimeout   = 20 * time.Second
	maxSourceBytes = 1 << 20
)

// rateSources are free daily USD rates (CC0, no key). The project asks
// clients to fall back to its Cloudflare copy when jsDelivr fails.
var rateSources = []string{
	"https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/usd.min.json",
	"https://latest.currency-api.pages.dev/v1/currencies/usd.min.json",
}

type offering struct {
	code, symbol string
	digits       int
}

func offered(code string) (offering, bool) {
	for _, c := range prefs.Currencies {
		if c.Code == code {
			return offering{c.Code, c.Symbol, c.Decimals}, true
		}
	}
	return offering{}, false
}

// localeRegion is the region of a locale name such as zh_CN.UTF-8, en-US
// or zh-Hans-CN, "" for none.
func localeRegion(locale string) string {
	name := locale
	if i := strings.IndexAny(name, ".@"); i >= 0 {
		name = name[:i]
	}
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	for _, part := range parts[min(1, len(parts)):] {
		if len(part) == 2 && part[0] >= 'A' && part[0] <= 'Z' && part[1] >= 'A' && part[1] <= 'Z' {
			return part
		}
	}
	return ""
}

// regionCurrency is the currency of a region the setting offers, "" for
// the rest, which show USD.
func regionCurrency(region string) string {
	switch region {
	case "US":
		return "USD"
	case "CN":
		return "CNY"
	case "HK":
		return "HKD"
	case "TW":
		return "TWD"
	case "JP":
		return "JPY"
	case "KR":
		return "KRW"
	case "SG":
		return "SGD"
	case "GB":
		return "GBP"
	case "CH", "LI":
		return "CHF"
	case "CA":
		return "CAD"
	case "AU":
		return "AUD"
	case "IN":
		return "INR"
	case "RU":
		return "RUB"
	case "BR":
		return "BRL"
	case "AT", "BE", "BG", "CY", "DE", "EE", "ES", "FI", "FR", "GR", "HR", "IE",
		"IT", "LT", "LU", "LV", "MT", "NL", "PT", "SI", "SK", "AD", "MC", "SM",
		"VA", "ME", "XK":
		return "EUR"
	}
	return ""
}

// SystemCurrency is the currency of the first of locales whose region the
// setting offers a currency for, otherwise USD. locales are the region of
// the system's formats first (an English system can be set to China), then
// the POSIX monetary locale and the user's languages.
func SystemCurrency(locales []string) string {
	for _, locale := range locales {
		if code := regionCurrency(localeRegion(locale)); code != "" {
			return code
		}
	}
	return usd
}

// PosixLocale is the monetary locale in the order the C library applies
// it, "" for none.
func PosixLocale(getenv func(string) string) string {
	for _, name := range []string{"LC_ALL", "LC_MONETARY", "LANG"} {
		if value := getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// rateTable is the cached table.
type rateTable struct {
	// Date is the day the source published the rates, YYYY-MM-DD.
	Date      string    `json:"date"`
	FetchedAt time.Time `json:"fetched_at"`
	// PerUSD is units per US dollar for the offered currencies the source
	// had.
	PerUSD map[string]float64 `json:"per_usd"`
}

func (t *rateTable) rate(code string) (float64, bool) {
	if t == nil {
		return 0, false
	}
	rate, ok := t.PerUSD[code]
	return rate, ok && usable(rate)
}

func usable(rate float64) bool { return rate > 0 && !math.IsInf(rate, 0) }

// parseSource reads a source body:
// {"date": "2026-11-01", "usd": {"cny": 7.1, ...}}.
func parseSource(body []byte, fetchedAt time.Time) (*rateTable, error) {
	var source struct {
		Date *string                    `json:"date"`
		USD  map[string]json.RawMessage `json:"usd"`
	}
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	if source.Date == nil || source.USD == nil {
		return nil, errors.New("the rate table has no date or rates")
	}
	if _, err := time.Parse("2006-01-02", *source.Date); err != nil {
		return nil, fmt.Errorf("unexpected rate date %q", *source.Date)
	}
	perUSD := map[string]float64{}
	for _, c := range prefs.Currencies {
		if c.Code == usd {
			continue
		}
		raw, ok := source.USD[strings.ToLower(c.Code)]
		if !ok {
			continue
		}
		var rate float64
		if json.Unmarshal(raw, &rate) == nil && usable(rate) {
			perUSD[c.Code] = rate
		}
	}
	if len(perUSD) == 0 {
		return nil, errors.New("the rate table lists none of the offered currencies")
	}
	return &rateTable{Date: *source.Date, FetchedAt: fetchedAt.UTC(), PerUSD: perUSD}, nil
}

// Display is what the interface shows amounts in, sent to every window.
type Display struct {
	// Code is the currency amounts are shown in; USD until Wanted has a
	// rate.
	Code   string `json:"code"`
	Symbol string `json:"symbol"`
	Digits int    `json:"digits"`
	// PerUSD is units of Code per US dollar.
	PerUSD float64 `json:"per_usd"`
	// RateDate is the day the rate was published; nil for USD.
	RateDate *string `json:"rate_date"`
	// Wanted is the currency the setting resolves to.
	Wanted string `json:"wanted"`
	// System is what the automatic setting resolves to on this system.
	System string `json:"system"`
}

// USD is the display before any setting or rate.
func USD() Display { return resolve(Auto, usd, nil) }

func resolve(choice, system string, table *rateTable) Display {
	wanted := choice
	if choice == Auto {
		wanted = system
	}
	if _, ok := offered(wanted); !ok {
		wanted = usd
	}
	code, perUSD := usd, 1.0
	var rateDate *string
	if wanted != usd {
		if rate, ok := table.rate(wanted); ok {
			code, perUSD = wanted, rate
			date := table.Date
			rateDate = &date
		}
	}
	o, _ := offered(code)
	return Display{
		Code: code, Symbol: o.symbol, Digits: o.digits, PerUSD: perUSD,
		RateDate: rateDate, Wanted: wanted, System: system,
	}
}

// Format is an estimate for the menu bar: ¥5.55, <¥0.01, ¥0.00.
func (d Display) Format(amountUSD float64) string {
	amount := amountUSD * d.PerUSD
	unit := math.Pow(10, -float64(d.Digits))
	switch {
	case math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0:
		return d.Symbol + strconv.FormatFloat(0, 'f', d.Digits, 64)
	case amount < unit:
		return "<" + d.Symbol + strconv.FormatFloat(unit, 'f', d.Digits, 64)
	default:
		return d.Symbol + strconv.FormatFloat(amount, 'f', d.Digits, 64)
	}
}

// Rates keeps the last rate table, cached next to the preferences so a
// restart offline keeps the previous day's rates.
type Rates struct {
	path string
	// Choice is the display currency setting and UseSystemProxy whether
	// requests take the system proxy (the preferences).
	Choice         func() string
	UseSystemProxy func() bool
	// System is the system's currency (SystemCurrency).
	System func() string
	// Changed runs after a new table arrived.
	Changed func()
	// client fetches the rates, set for tests.
	client  func(useSystemProxy bool) *http.Client
	sources []string
	now     func() time.Time

	mu    sync.Mutex
	table *rateTable
	wake  chan struct{}
}

// Load reads the cached table in configDirectory.
func Load(configDirectory string) *Rates {
	r := &Rates{
		path:    filepath.Join(configDirectory, fileName),
		client:  httpClient,
		sources: rateSources,
		now:     time.Now,
		wake:    make(chan struct{}, 1),
	}
	data, err := os.ReadFile(r.path)
	switch {
	case err == nil:
		var table rateTable
		if err := json.Unmarshal(data, &table); err != nil {
			log.Printf("astrlink: ignoring cached exchange rates: %v", err)
		} else {
			r.table = &table
		}
	case !errors.Is(err, os.ErrNotExist):
		log.Printf("astrlink: unable to read cached exchange rates: %v", err)
	}
	return r
}

// httpClient goes through the system's proxy, or straight out.
func httpClient(useSystemProxy bool) *http.Client {
	transport := netproxy.Transport().Clone()
	if !useSystemProxy {
		transport.Proxy = nil
	}
	return &http.Client{Transport: transport, Timeout: fetchTimeout}
}

// Display is the display currency from the setting and the cached rates.
func (r *Rates) Display() Display {
	r.mu.Lock()
	table := r.table
	r.mu.Unlock()
	return resolve(r.Choice(), r.System(), table)
}

// Wake checks again now, after the setting changed.
func (r *Rates) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Rates) needsRefresh(wanted string, now time.Time) bool {
	if wanted == usd {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.table.rate(wanted)
	return !ok || now.Sub(r.table.FetchedAt) >= refreshAfter
}

// Run refreshes the table every checkInterval, and when woken, until ctx
// ends.
func (r *Rates) Run(ctx context.Context) {
	for {
		r.tick(ctx)
		timer := time.NewTimer(checkInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-r.wake:
			timer.Stop()
		}
	}
}

func (r *Rates) tick(ctx context.Context) {
	wanted := resolve(r.Choice(), r.System(), nil).Wanted
	// USD needs no rate, so it sends nothing anywhere.
	if !r.needsRefresh(wanted, r.now()) {
		return
	}
	table, err := r.fetch(ctx)
	if err != nil {
		log.Printf("astrlink: unable to refresh exchange rates: %v", err)
		return
	}
	if data, err := json.MarshalIndent(table, "", "  "); err == nil {
		if err := prefs.WriteFileAtomic(r.path, data); err != nil {
			log.Printf("astrlink: unable to cache exchange rates: %v", err)
		}
	}
	r.mu.Lock()
	r.table = table
	r.mu.Unlock()
	if r.Changed != nil {
		r.Changed()
	}
}

func (r *Rates) fetch(ctx context.Context) (*rateTable, error) {
	client := r.client(r.UseSystemProxy())
	var failures []string
	for _, url := range r.sources {
		table, err := r.fetchFrom(ctx, client, url)
		if err == nil {
			return table, nil
		}
		failures = append(failures, url+": "+err.Error())
	}
	return nil, errors.New(strings.Join(failures, "; "))
}

func (r *Rates) fetchFrom(ctx context.Context, client *http.Client, url string) (*rateTable, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP status %s", resp.Status)
	}
	if resp.ContentLength > maxSourceBytes {
		return nil, errors.New("the rate table is too large")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSourceBytes {
		return nil, errors.New("the rate table is too large")
	}
	return parseSource(body, r.now())
}
