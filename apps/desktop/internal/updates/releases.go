package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	repository = "https://github.com/Calcium-Ion/AstrLink"
	releaseAPI = "https://api.github.com/repos/Calcium-Ion/AstrLink/releases"
	// latestDownload serves the files of the latest release.
	latestDownload = "https://github.com/Calcium-Ion/AstrLink/releases/latest/download/"
	downloadSite   = "https://download.astrlink.com"
	// The download domain's CDN serves only /releases/*, so the list lives
	// on the site.
	mirrorReleaseAPI = "https://astrlink.com/api/releases"

	repositoryPath   = "/Calcium-Ion/AstrLink/releases/download/"
	downloadSitePath = "/releases/download/"
	// maxListBytes bounds a release list page.
	maxListBytes = 32 << 20
)

// checkError is a failed check, download or install with the code About
// shows.
type checkError struct {
	code, detail string
}

func (e *checkError) Error() string { return e.detail }

func failure(code string, format string, args ...any) *checkError {
	return &checkError{code: code, detail: fmt.Sprintf(format, args...)}
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName     string         `json:"tag_name"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Body        *string        `json:"body"`
	PublishedAt *string        `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
	// manifest is the update manifest the stable check already read.
	manifest []byte
}

// selectRelease is the newest release of channel by semantic version,
// never a draft; stable skips prereleases.
func selectRelease(releases []githubRelease, preview bool) *githubRelease {
	var best *githubRelease
	var bestVersion semVersion
	for i := range releases {
		r := &releases[i]
		if r.Draft {
			continue
		}
		v, ok := tagVersion(r.TagName)
		if !ok || (!preview && (r.Prerelease || v.prerelease())) {
			continue
		}
		if best == nil || v.compare(bestVersion) >= 0 {
			best, bestVersion = r, v
		}
	}
	return best
}

// decodedPath is the path of u with an encoded + decoded, as GitHub's tags
// of build metadata are spelled either way.
func decodedPath(u *url.URL) string {
	return strings.NewReplacer("%2B", "+", "%2b", "+").Replace(u.EscapedPath())
}

func plainHTTPS(u *url.URL, host string) bool {
	return u.Scheme == "https" && u.Host == host && u.User == nil && u.RawQuery == "" &&
		!u.ForceQuery && u.Fragment == "" && u.Port() == ""
}

// githubAsset reports whether raw is a file of the release tagged tag in
// this repository.
func githubAsset(raw, tag string) bool {
	u, err := url.Parse(raw)
	return err == nil && plainHTTPS(u, "github.com") &&
		strings.HasPrefix(decodedPath(u), repositoryPath+tag+"/")
}

// downloadSiteAsset reports whether raw is a file of the release tagged tag
// on the download site.
func downloadSiteAsset(raw, tag string) bool {
	u, err := url.Parse(raw)
	return err == nil && plainHTTPS(u, "download.astrlink.com") &&
		strings.HasPrefix(decodedPath(u), downloadSitePath+tag+"/")
}

func officialAsset(raw, tag string) bool {
	return githubAsset(raw, tag) || downloadSiteAsset(raw, tag)
}

// downloadCandidate rebuilds an asset URL from constants and the validated
// tag and file name, so the original URL's query, fragment, credentials,
// port or extra path cannot leak; mirror picks the download site.
func downloadCandidate(raw, tag string, mirror bool) (string, bool) {
	if !officialAsset(raw, tag) {
		return "", false
	}
	u, _ := url.Parse(raw)
	prefix := downloadSitePath
	if githubAsset(raw, tag) {
		prefix = repositoryPath
	}
	rest, ok := strings.CutPrefix(decodedPath(u), prefix+tag+"/")
	if !ok {
		return "", false
	}
	lower := strings.ToLower(rest)
	if rest == "" || strings.ContainsAny(rest, `/\`) || strings.Contains(lower, "%2f") || strings.Contains(rest, "..") {
		return "", false
	}
	file, err := url.PathUnescape(rest)
	if err != nil || file == "" || strings.ContainsAny(file, `/\`) || strings.Contains(file, "..") {
		return "", false
	}
	path := "/releases/download/" + encodeComponent(tag) + "/" + encodeComponent(file)
	if mirror {
		return downloadSite + path, true
	}
	return repository + path, true
}

// encodeComponent percent-encodes all but unreserved characters.
func encodeComponent(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// candidateSource names where a download comes from for About.
func candidateSource(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host == "download.astrlink.com" {
		return "download_site"
	}
	return "github"
}

// githubStatus passes a successful response through. A 429, or a 403
// carrying GitHub's rate-limit signals, also reports how many seconds to
// wait in retryAfter.
func githubStatus(response *http.Response, retryAfter *int64, now time.Time) error {
	status := response.StatusCode
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		if status < 200 || status > 299 {
			return failure("network", "HTTP status %s", statusText(response))
		}
		return nil
	}
	header := func(name string) (string, bool) {
		values, ok := response.Header[http.CanonicalHeaderKey(name)]
		if !ok || len(values) == 0 {
			return "", false
		}
		return strings.TrimSpace(values[0]), true
	}
	remaining, hasRemaining := header("x-ratelimit-remaining")
	exhausted := hasRemaining && remaining == "0"
	var wait *int64
	if text, ok := header("retry-after"); ok {
		if seconds, err := strconv.ParseUint(text, 10, 64); err == nil {
			s := int64(min(seconds, 1<<62))
			wait = &s
		}
	}
	if wait == nil && exhausted {
		if text, ok := header("x-ratelimit-reset"); ok {
			if reset, err := strconv.ParseInt(text, 10, 64); err == nil {
				s := max(reset-now.Unix(), 1)
				wait = &s
			}
		}
	}
	quota := ""
	if limit, ok := header("x-ratelimit-limit"); ok && hasRemaining {
		quota = fmt.Sprintf("; %s of %s requests left", remaining, limit)
	}
	detail := "GitHub HTTP " + statusText(response) + quota
	// GitHub's message names the caller's IP address, so it stays out of
	// the detail.
	message, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if status == http.StatusForbidden && wait == nil && !exhausted &&
		!strings.Contains(strings.ToLower(string(message)), "rate limit") {
		return &checkError{code: "network", detail: detail}
	}
	// Without a hint, GitHub asks clients to wait at least a minute.
	seconds := int64(60)
	if wait != nil {
		seconds = *wait
	}
	*retryAfter = min(max(seconds, 1), 86400)
	return &checkError{code: "rate_limit", detail: detail}
}

// statusText is the status as "403 Forbidden".
func statusText(response *http.Response) string {
	if text := http.StatusText(response.StatusCode); text != "" {
		return strconv.Itoa(response.StatusCode) + " " + text
	}
	return strconv.Itoa(response.StatusCode)
}

// manifestTag is the tag of an update manifest's URL among this
// repository's releases.
func manifestTag(u *url.URL, name string) (string, bool) {
	rest, ok := strings.CutPrefix(decodedPath(u), repositoryPath)
	if !ok {
		return "", false
	}
	tag, ok := strings.CutSuffix(rest, "/"+name)
	if !ok || tag == "" || strings.Contains(tag, "/") || !githubAsset(u.String(), tag) {
		return "", false
	}
	return tag, true
}

// lookup reads the release lists and manifests with the clients a check
// uses: follow follows redirects, direct does not.
type lookup struct {
	follow, direct *http.Client
	now            func() time.Time
	// manifestName is update-<target>.json of this platform.
	manifestName string
}

func (l lookup) get(ctx context.Context, client *http.Client, endpoint string, accept string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, failure("network", "%v", err)
	}
	request.Header.Set("User-Agent", "mygo-updater")
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, failure("network", "%v", err)
	}
	return response, nil
}

// fetchLatest reads the stable release through GitHub's latest-release
// download, which is served outside the API quota that shared network
// addresses often exhaust: it redirects to the manifest of the latest
// release, which names the version.
func (l lookup) fetchLatest(ctx context.Context, endpoint string, retryAfter *int64) ([]githubRelease, error) {
	response, err := l.get(ctx, l.direct, endpoint, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode < 300 || response.StatusCode > 399 {
		if err := githubStatus(response, retryAfter, l.now()); err != nil {
			return nil, err
		}
		return nil, failure("manifest", "GitHub did not redirect to the latest release")
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return nil, failure("network", "%v", err)
	}
	location, err := base.Parse(response.Header.Get("Location"))
	if err != nil || response.Header.Get("Location") == "" {
		return nil, failure("manifest", "Latest release redirect is not a release manifest")
	}
	tag, ok := manifestTag(location, l.manifestName)
	if !ok {
		return nil, failure("manifest", "Latest release redirect is not a release manifest")
	}
	manifest, err := l.get(ctx, l.follow, location.String(), "")
	if err != nil {
		return nil, err
	}
	defer manifest.Body.Close()
	if manifest.StatusCode == http.StatusNotFound {
		// The latest release has no update for this platform, as releases
		// before the first MyGo build do. Builds that only link to the
		// release still learn its version; the check reports the missing
		// manifest to a build that would install it.
		return []githubRelease{{TagName: tag}}, nil
	}
	if err := githubStatus(manifest, retryAfter, l.now()); err != nil {
		return nil, err
	}
	body, err := readLimited(manifest.Body, maxManifestBytes)
	if err != nil {
		return nil, failure("manifest", "%v", err)
	}
	release, err := latestRelease(location.String(), tag, l.manifestName, body)
	if err != nil {
		return nil, err
	}
	return []githubRelease{release}, nil
}

// latestRelease is the release a latest manifest describes; the manifest
// must name the version of the release it was published in.
func latestRelease(manifestURL, tag, name string, body []byte) (githubRelease, error) {
	var manifest struct {
		Version string `json:"version"`
		Notes   string `json:"notes"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return githubRelease{}, failure("manifest", "%v", err)
	}
	v, ok := parseVersion(strings.TrimPrefix(manifest.Version, "v"))
	t, tagOK := tagVersion(tag)
	if !ok || !tagOK || v.compare(t) != 0 || v.String() != t.String() {
		return githubRelease{}, failure("manifest", "Update manifest does not match the latest release")
	}
	release := githubRelease{
		TagName:  tag,
		Assets:   []releaseAsset{{Name: name, BrowserDownloadURL: manifestURL}},
		manifest: body,
	}
	if manifest.Notes != "" {
		release.Body = &manifest.Notes
	}
	if manifest.Date != "" {
		release.PublishedAt = &manifest.Date
	}
	return release, nil
}

// fetchReleases pages through every public release: GitHub's date order is
// not version order.
func (l lookup) fetchReleases(ctx context.Context, endpoint string, retryAfter *int64) ([]githubRelease, error) {
	var releases []githubRelease
	for page := 1; page <= 100; page++ {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, failure("network", "%v", err)
		}
		query := u.Query()
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		u.RawQuery = query.Encode()
		batch, err := func() ([]githubRelease, error) {
			response, err := l.get(ctx, l.follow, u.String(), "application/vnd.github+json")
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			if err := githubStatus(response, retryAfter, l.now()); err != nil {
				return nil, err
			}
			return decodeReleases(response.Body)
		}()
		if err != nil {
			return nil, err
		}
		releases = append(releases, batch...)
		if len(batch) < 100 {
			return releases, nil
		}
	}
	return nil, failure("manifest", "Too many releases to select a version safely")
}

// fetchMirrorReleases reads the mirror's list of its 20 newest releases, in
// GitHub's shape, outside GitHub's quota; its failures are never GitHub
// rate limits.
func (l lookup) fetchMirrorReleases(ctx context.Context, endpoint string) ([]githubRelease, error) {
	response, err := l.get(ctx, l.follow, endpoint, "")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, failure("network", "HTTP status %s", statusText(response))
	}
	return decodeReleases(response.Body)
}

func decodeReleases(body io.Reader) ([]githubRelease, error) {
	data, err := readLimited(body, maxListBytes)
	if err != nil {
		return nil, failure("manifest", "%v", err)
	}
	var releases []githubRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, failure("manifest", "%v", err)
	}
	return releases, nil
}

// readLimited reads at most limit bytes, failing on a longer body.
func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return data, nil
}
