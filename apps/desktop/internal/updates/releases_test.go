package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/desktop/internal/prefs"
)

func TestNoticesSkipStatesThatResolveOnTheirOwn(t *testing.T) {
	cases := []struct {
		phase        UpdatePhase
		autoDownload bool
		want         string
	}{
		{PhaseAvailable, false, "available"},
		{PhaseAvailable, true, ""},
		{PhaseManual, true, "manual"},
		{PhaseReady, true, "ready"},
		{PhaseError, true, "error"},
	}
	for _, c := range cases {
		if got := notice(c.phase, c.autoDownload); got != c.want {
			t.Errorf("notice(%s, %v) = %q, want %q", c.phase, c.autoDownload, got, c.want)
		}
	}
	for _, phase := range []UpdatePhase{PhaseIdle, PhaseChecking, PhaseUpToDate, PhaseDownloading} {
		if got := notice(phase, false); got != "" {
			t.Errorf("notice(%s) = %q", phase, got)
		}
	}
	// Native notifications read the same catalogs; a missing key would
	// surface raw, and another language would silently fall back to
	// English.
	for _, kind := range []string{"available", "manual", "ready", "error"} {
		title, body := "about.phase."+kind, "about."+kind+"Notification"
		en := prefs.T(prefs.LocaleEn, body, "version", "1.1.0")
		ja := prefs.T(prefs.LocaleJa, body, "version", "1.1.0")
		zh := prefs.T(prefs.LocaleZhCN, body, "version", "1.1.0")
		if !strings.Contains(en, "1.1.0") || !strings.Contains(ja, "1.1.0") || !strings.Contains(zh, "1.1.0") || en == zh || en == ja {
			t.Errorf("%s: %q %q %q", body, en, ja, zh)
		}
		if prefs.T(prefs.LocaleEn, title) == prefs.T(prefs.LocaleZhCN, title) || prefs.T(prefs.LocaleEn, title) == prefs.T(prefs.LocaleJa, title) {
			t.Errorf("%s is not translated", title)
		}
	}
}

var noon = time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC)

func TestFailedChecksRetrySoonThenBackOffAndResetAfterSuccess(t *testing.T) {
	s := newSchedule(noon)
	if got := s.delay(noon, time.Time{}); got != 30*time.Second {
		t.Fatalf("first check after %v", got)
	}
	for _, seconds := range []int{60, 300, 900, 10800, 10800} {
		s.completed(noon, true)
		if got := s.delay(noon, time.Time{}); got != time.Duration(seconds)*time.Second {
			t.Fatalf("retry after %v, want %ds", got, seconds)
		}
	}
	s.completed(noon, false)
	if got := s.delay(noon, time.Time{}); got != checkInterval {
		t.Fatalf("after success %v", got)
	}
	s.completed(noon, true)
	if got := s.delay(noon, time.Time{}); got != time.Minute {
		t.Fatalf("after a new failure %v", got)
	}
	// Manual success cancels an outstanding short retry.
	s.completed(noon, false)
	if got := s.delay(noon, time.Time{}); got != checkInterval {
		t.Fatalf("after manual success %v", got)
	}
}

func TestAutomaticRetriesRespectRateLimitsAndExpiredWallClockDeadlines(t *testing.T) {
	s := newSchedule(noon)
	s.completed(noon, true)
	retryAfter := noon.Add(20 * time.Minute)
	if got := s.delay(noon, retryAfter); got != 20*time.Minute {
		t.Fatalf("rate limited: %v", got)
	}
	// Changing preferences must not let a check bypass the server's wait.
	s.restart(noon)
	if got := s.delay(noon, retryAfter); got != 20*time.Minute {
		t.Fatalf("restarted under a rate limit: %v", got)
	}
	if got := s.delay(noon, time.Time{}); got != 0 {
		t.Fatalf("restarted: %v", got)
	}
	if got := s.delay(noon.Add(21*time.Minute), retryAfter); got != 0 {
		t.Fatalf("after the rate limit: %v", got)
	}
	// A new configuration gets the short first retry again.
	s.completed(noon, true)
	if got := s.delay(noon, time.Time{}); got != time.Minute {
		t.Fatalf("new configuration: %v", got)
	}
}

// testKey signs packages like `mygo build` with a key made for the test.
type testKey struct {
	private ed25519.PrivateKey
	public  string
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{private: private, public: base64.StdEncoding.EncodeToString(public)}
}

func (k testKey) sign(data []byte) string {
	digest := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.private, digest[:]))
}

func TestDownloadsAreVerifiedAgainstTheBuildsKey(t *testing.T) {
	key := newTestKey(t)
	public, err := parsePublicKey(key.public)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("the update archive")
	digest := sha256.Sum256(data)
	if err := verifyDigest(public, digest[:], key.sign(data)); err != nil {
		t.Fatalf("a signed archive is refused: %v", err)
	}
	corrupted := append([]byte{}, data...)
	corrupted[0] ^= 1
	digest = sha256.Sum256(corrupted)
	if verifyDigest(public, digest[:], key.sign(data)) == nil {
		t.Fatal("a corrupted archive verifies")
	}
	other := newTestKey(t)
	digest = sha256.Sum256(data)
	if verifyDigest(public, digest[:], other.sign(data)) == nil {
		t.Fatal("another key's signature verifies")
	}
}

func TestInvalidSignaturesAndKeysFailClosed(t *testing.T) {
	if _, err := parsePublicKey("invalid"); err == nil {
		t.Fatal("an invalid key parses")
	}
	if _, err := parsePublicKey(""); err == nil {
		t.Fatal("an empty key parses")
	}
	if verifyDigest(nil, []byte("tampered"), "invalid") == nil || verifyDigest(nil, nil, "") == nil {
		t.Fatal("verification without a key passes")
	}
}

func TestSwitchingOnlyTheMirrorLeavesTheRetryToItsCaller(t *testing.T) {
	current := prefs.DefaultUpdatePreferences()
	mirror := current
	mirror.OfficialMirror = true
	if reschedules(current, mirror) {
		t.Fatal("switching the mirror reschedules")
	}
	if !reschedules(current, current) {
		t.Fatal("saving the same settings does not reschedule")
	}
	preview := mirror
	preview.Channel = prefs.ChannelPreview
	if !reschedules(current, preview) {
		t.Fatal("switching channel and mirror does not reschedule")
	}
	manual := current
	manual.AutoDownload = false
	if !reschedules(current, manual) {
		t.Fatal("turning downloads off does not reschedule")
	}
}

func release(tag string, prerelease bool) githubRelease {
	return githubRelease{TagName: tag, Prerelease: prerelease}
}

func TestChannelsSelectBySemverNotDatesAndNeverAcceptDrafts(t *testing.T) {
	draft := release("9.0.0", false)
	draft.Draft = true
	releases := []githubRelease{
		release("v1.9.0", false),
		release("1.10.0", false),
		release("2.0.0-beta.2", false),
		release("2.0.0-beta.10", true),
		release("invalid", false),
		draft,
	}
	if got := selectRelease(releases, false); got == nil || got.TagName != "1.10.0" {
		t.Fatalf("stable picked %+v", got)
	}
	if got := selectRelease(releases, true); got == nil || got.TagName != "2.0.0-beta.10" {
		t.Fatalf("preview picked %+v", got)
	}
	a, _ := tagVersion("v1.10.0")
	b, _ := tagVersion("2.0.0")
	if a.compare(b) >= 0 {
		t.Fatal("1.10.0 does not precede 2.0.0")
	}
	if selectRelease(nil, false) != nil {
		t.Fatal("no releases selected one")
	}
}

func TestVersionsFollowSemanticVersioning(t *testing.T) {
	for _, invalid := range []string{"1.2", "01.2.3", "1.2.3-01", "1.2.3-", "1.2.3+", "1.2.3-a..b", "v1.2.3", "1.2.3.4", "1.2.x"} {
		if _, ok := parseVersion(invalid); ok {
			t.Errorf("%s parses", invalid)
		}
	}
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i := 1; i < len(order); i++ {
		a, okA := parseVersion(order[i-1])
		b, okB := parseVersion(order[i])
		if !okA || !okB || a.compare(b) >= 0 || b.compare(a) <= 0 {
			t.Errorf("%s does not precede %s", order[i-1], order[i])
		}
	}
	a, _ := parseVersion("1.0.0+build.1")
	b, _ := parseVersion("1.0.0+build.2")
	if a.compare(b) != 0 || a.String() != "1.0.0" {
		t.Error("build metadata orders versions")
	}
}

func TestAssetURLsMustBelongToTheSelectedRelease(t *testing.T) {
	if !githubAsset("https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/update-linux-amd64.json", "v1.0.0") {
		t.Fatal("a release asset is refused")
	}
	if !downloadSiteAsset("https://download.astrlink.com/releases/download/v1.0.0/astrlink-1.0.0-windows-amd64.tar.gz", "v1.0.0") {
		t.Fatal("a download site asset is refused")
	}
	for _, raw := range []string{
		"http://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
		"https://example.org/a",
		"https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.0/a",
		"https://github.com.evil/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
		"https://github.com:8443/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
		"https://user@github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/a",
		"http://download.astrlink.com/releases/download/v1.0.0/a",
		"https://download.astrlink.com.evil/releases/download/v1.0.0/a",
		"https://download.astrlink.com/releases/download/v0.1.0/a",
		"https://download.astrlink.com/releases/download/v1.0.0/a?x=1",
		"https://download.astrlink.com/releases/download/v1.0.0/a#x",
	} {
		if officialAsset(raw, "v1.0.0") {
			t.Errorf("%s is accepted", raw)
		}
	}
}

func TestDownloadCandidateFollowsTheOfficialMirrorPreference(t *testing.T) {
	github := "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/astrlink-1.0.0-darwin-arm64.tar.gz"
	site := "https://download.astrlink.com/releases/download/v1.0.0/astrlink-1.0.0-darwin-arm64.tar.gz"
	for _, source := range []string{github, site} {
		if got, ok := downloadCandidate(source, "v1.0.0", false); !ok || got != github {
			t.Errorf("%s without the mirror: %s", source, got)
		}
		if got, ok := downloadCandidate(source, "v1.0.0", true); !ok || got != site {
			t.Errorf("%s with the mirror: %s", source, got)
		}
	}
}

func TestDownloadCandidatePercentEncodesPlusTags(t *testing.T) {
	github := "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0+build.1/AstrLink.tar.gz"
	if got, _ := downloadCandidate(github, "v1.0.0+build.1", false); got != "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0%2Bbuild.1/AstrLink.tar.gz" {
		t.Errorf("GitHub: %s", got)
	}
	if got, _ := downloadCandidate(github, "v1.0.0+build.1", true); got != "https://download.astrlink.com/releases/download/v1.0.0%2Bbuild.1/AstrLink.tar.gz" {
		t.Errorf("mirror: %s", got)
	}
}

func TestDownloadCandidateRejectsUntrustedOrMalformedAssets(t *testing.T) {
	for _, source := range []string{
		"https://example.org/releases/download/v1.0.0/AstrLink.tar.gz",
		"https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.0/AstrLink.tar.gz",
		"https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/dir/AstrLink.tar.gz",
		"https://download.astrlink.com/releases/download/v1.0.0/a?x=1",
		"https://download.astrlink.com/releases/download/v1.0.0/a#x",
		"https://download.astrlink.com/releases/download/v1.0.0/dir%2FAstrLink.tar.gz",
		"https://download.astrlink.com/releases/download/v1.0.0/..",
	} {
		if got, ok := downloadCandidate(source, "v1.0.0", false); ok {
			t.Errorf("%s became %s", source, got)
		}
	}
}

func TestPreferencesMigrateDefaultsAndRejectUnknownChannels(t *testing.T) {
	values, err := ParsePreferences([]byte("{}"))
	if err != nil || values != prefs.DefaultUpdatePreferences() {
		t.Fatalf("{} = %+v, %v", values, err)
	}
	if _, err := ParsePreferences([]byte(`{"channel":"nightly"}`)); err == nil {
		t.Fatal("an unknown channel is accepted")
	}
	if _, err := ParsePreferences([]byte(`{"auto_check":true,"surprise":1}`)); err == nil {
		t.Fatal("an unknown field is accepted")
	}
}

func TestLatestManifestsMustNameTheReleaseTheyWerePublishedIn(t *testing.T) {
	const name = "update-linux-amd64.json"
	at := func(tag string) *url.URL {
		u, _ := url.Parse("https://github.com/Calcium-Ion/AstrLink/releases/download/" + tag + "/" + name)
		return u
	}
	if tag, ok := manifestTag(at("v0.1.8"), name); !ok || tag != "v0.1.8" {
		t.Fatalf("tag %q", tag)
	}
	if tag, ok := manifestTag(at("v1.0.0%2Bbuild.1"), name); !ok || tag != "v1.0.0+build.1" {
		t.Fatalf("tag %q", tag)
	}
	for _, wrong := range []string{
		"https://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.8/latest.json",
		"https://github.com/Calcium-Ion/AstrLink/releases/download/a/b/" + name,
		"https://github.com/Calcium-Ion/AstrLink/releases/download//" + name,
		"https://github.com.evil/Calcium-Ion/AstrLink/releases/download/v0.1.8/" + name,
		"http://github.com/Calcium-Ion/AstrLink/releases/download/v0.1.8/" + name,
	} {
		u, _ := url.Parse(wrong)
		if tag, ok := manifestTag(u, name); ok {
			t.Errorf("%s names %s", wrong, tag)
		}
	}
	r, err := latestRelease(at("v0.1.8").String(), "v0.1.8", name, []byte(`{"version":"0.1.8","notes":"notes"}`))
	if err != nil || r.TagName != "v0.1.8" || r.Body == nil || *r.Body != "notes" || !githubAsset(r.Assets[0].BrowserDownloadURL, "v0.1.8") {
		t.Fatalf("release %+v, %v", r, err)
	}
	if got := selectRelease([]githubRelease{r}, false); got == nil || got.TagName != "v0.1.8" {
		t.Fatal("the latest release is not selected")
	}
	for _, wrong := range []string{`{"version":"0.1.7"}`, `{"version":"invalid"}`, `invalid`} {
		if _, err := latestRelease(at("v0.1.8").String(), "v0.1.8", name, []byte(wrong)); codeOf(err) != "manifest" {
			t.Errorf("%s: %v", wrong, err)
		}
	}
}

func codeOf(err error) string {
	if failed, ok := err.(*checkError); ok {
		return failed.code
	}
	if err == nil {
		return ""
	}
	return "untyped: " + err.Error()
}

// respond answers one request with status, headers and body.
func respond(status int, headers map[string]string, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func testLookup(follow bool) lookup {
	client := &http.Client{Timeout: 10 * time.Second}
	direct := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if !follow {
		client = direct
	}
	return lookup{follow: client, direct: direct, now: time.Now, manifestName: "update-linux-amd64.json"}
}

func TestGitHubFailuresAreDistinctFromNoReleasesAndRateLimitsBackOff(t *testing.T) {
	reset := fmt.Sprint(time.Now().Unix() + 600)
	exhausted := map[string]string{"X-RateLimit-Limit": "60", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}
	cases := []struct {
		status           int
		headers          map[string]string
		body             string
		code             string
		minWait, maxWait int64
	}{
		{200, nil, "[]", "", 0, 0},
		{500, nil, "{}", "network", 0, 0},
		{200, nil, "invalid", "manifest", 0, 0},
		{429, map[string]string{"Retry-After": "60"}, "{}", "rate_limit", 60, 60},
		{403, exhausted, "{}", "rate_limit", 590, 600},
		{403, nil, `{"message":"You have exceeded a secondary rate limit"}`, "rate_limit", 60, 60},
		// A blocked network or a revoked repository is not worth waiting
		// on.
		{403, map[string]string{"X-RateLimit-Remaining": "59", "X-RateLimit-Reset": "1"}, `{"message":"Forbidden"}`, "network", 0, 0},
	}
	for _, c := range cases {
		server := respond(c.status, c.headers, c.body)
		var retry int64
		releases, err := testLookup(true).fetchReleases(context.Background(), server.URL, &retry)
		server.Close()
		if codeOf(err) != c.code {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if c.code == "" && len(releases) != 0 {
			t.Errorf("%d %s: releases %v", c.status, c.body, releases)
		}
		if retry < c.minWait || retry > c.maxWait {
			t.Errorf("%d %s: retry after %d", c.status, c.body, retry)
		}
	}
	var retry int64
	if _, err := testLookup(true).fetchReleases(context.Background(), "http://127.0.0.1:0", &retry); codeOf(err) != "network" || retry != 0 {
		t.Errorf("unreachable: %v, retry %d", err, retry)
	}
}

func TestMirrorChecksReadGitHubShapedReleasesAndNeverBackOff(t *testing.T) {
	listed := `[{"tag_name":"v1.1.0-beta.1","draft":false,"prerelease":true,
		"body":"notes","body_html":"<p>notes</p>","html_url":"https://github.com/x",
		"published_at":"2026-10-13T05:08:04Z","assets":[{"name":"update-linux-amd64.json",
		"browser_download_url":"https://github.com/Calcium-Ion/AstrLink/releases/download/v1.1.0-beta.1/update-linux-amd64.json",
		"download_url":"https://download.astrlink.com/releases/download/v1.1.0-beta.1/update-linux-amd64.json",
		"digest":"sha256:00"}]}]`
	server := respond(200, nil, listed)
	releases, err := testLookup(true).fetchMirrorReleases(context.Background(), server.URL)
	server.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := selectRelease(releases, true); got == nil || got.TagName != "v1.1.0-beta.1" {
		t.Fatalf("preview picked %+v", got)
	}
	if selectRelease(releases, false) != nil {
		t.Fatal("stable picked a prerelease")
	}
	for _, c := range []struct {
		status  int
		headers map[string]string
		body    string
		code    string
	}{
		{503, nil, "{}", "network"},
		{429, map[string]string{"Retry-After": "60"}, "{}", "network"},
		{200, nil, "invalid", "manifest"},
	} {
		server := respond(c.status, c.headers, c.body)
		_, err := testLookup(true).fetchMirrorReleases(context.Background(), server.URL)
		server.Close()
		if codeOf(err) != c.code {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
	}
}

func TestStableChecksFollowOnlyTheLatestReleaseManifestRedirect(t *testing.T) {
	elsewhere := map[string]string{"Location": "https://example.org/update-linux-amd64.json"}
	otherRepository := map[string]string{"Location": "https://github.com/someone/AstrLink/releases/download/v1.0.0/update-linux-amd64.json"}
	otherPlatform := map[string]string{"Location": "https://github.com/Calcium-Ion/AstrLink/releases/download/v1.0.0/update-darwin-arm64.json"}
	for _, c := range []struct {
		status  int
		headers map[string]string
		code    string
	}{
		{404, nil, ""},
		{302, elsewhere, "manifest"},
		{302, otherRepository, "manifest"},
		{302, otherPlatform, "manifest"},
		{200, nil, "manifest"},
		{429, map[string]string{"Retry-After": "30"}, "rate_limit"},
	} {
		server := respond(c.status, c.headers, "{}")
		var retry int64
		releases, err := testLookup(false).fetchLatest(context.Background(), server.URL, &retry)
		server.Close()
		if codeOf(err) != c.code || (c.code == "" && len(releases) != 0) {
			t.Errorf("%d %v: %v %v", c.status, c.headers, releases, err)
		}
		if (retry != 0) != (c.code == "rate_limit") {
			t.Errorf("%d %v: retry after %d", c.status, c.headers, retry)
		}
	}
}
