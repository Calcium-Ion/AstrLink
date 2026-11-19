package plugininstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// Where GitHub serves its API and its source archives.
const (
	DefaultGitHubAPI = "https://api.github.com"
	DefaultCodeload  = "https://codeload.github.com"
)

// maxAPIBytes bounds an answer of GitHub's API.
const maxAPIBytes = 4 << 20

// gitHub reads public repositories: their refs through Git's protocol,
// their source from the archives GitHub serves, and what only the API
// knows, such as release notes, when the API answers.
type gitHub struct {
	client   *http.Client
	web      string
	api      string
	codeload string
}

// Resolved is a link pinned to a commit.
type Resolved struct {
	Owner, Repo string
	// Ref is the tag or branch, "" for a commit; RefKind says which.
	Ref         string
	RefKind     string
	Commit      string
	CommittedAt time.Time
	Subdir      string
}

// Source is what a resolved link records.
func (resolved Resolved) Source(link string) contract.PluginInstallSource {
	source := contract.PluginInstallSource{
		Kind: contract.PluginSourceGitHub, Repository: resolved.Owner + "/" + resolved.Repo, URL: link,
		Ref: resolved.Ref, RefKind: resolved.RefKind, Commit: resolved.Commit, Subdir: resolved.Subdir,
	}
	if !resolved.CommittedAt.IsZero() {
		source.CommittedAt = resolved.CommittedAt.UTC().Format(time.RFC3339)
	}
	return source
}

// errNotFound is GitHub's 404.
var errNotFound = errors.New("not found")

// get reads a JSON answer of GitHub's API into value.
func (hub *gitHub) get(ctx context.Context, path string, value any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hub.api+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := hub.client.Do(request)
	if err != nil {
		return fail(contract.PluginInstallUnavailable, "GitHub: "+networkError(err))
	}
	defer response.Body.Close()
	if failure := hub.statusFailure(response); failure != nil {
		return failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAPIBytes+1))
	if err != nil {
		return fail(contract.PluginInstallUnavailable, "GitHub: "+networkError(err))
	}
	if len(data) > maxAPIBytes {
		return fail(contract.PluginInstallUnavailable, "GitHub answered more than expected")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fail(contract.PluginInstallUnavailable, "GitHub answered what is not JSON")
	}
	return nil
}

// statusFailure turns a status other than 200 into why: errNotFound for
// 404, the rate limit, or GitHub being unavailable.
func (hub *gitHub) statusFailure(response *http.Response) error {
	switch {
	case response.StatusCode == http.StatusOK:
		return nil
	case response.StatusCode == http.StatusNotFound:
		return errNotFound
	case response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0"):
		detail := ""
		if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			detail = time.Unix(reset, 0).UTC().Format(time.RFC3339)
		}
		return fail(contract.PluginInstallRateLimited, detail)
	}
	return fail(contract.PluginInstallUnavailable, "GitHub "+response.Status)
}

type commitInfo struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// commit looks ref up: a branch, a tag or a commit. errNotFound when the
// repository has none of that name.
func (hub *gitHub) commit(ctx context.Context, owner, repo, ref string) (commitInfo, error) {
	var info commitInfo
	err := hub.get(ctx, "/repos/"+owner+"/"+repo+"/commits/"+url.PathEscape(ref), &info)
	if err == nil && !shaPattern.MatchString(info.SHA) {
		err = fail(contract.PluginInstallUnavailable, "GitHub answered a commit without its id")
	}
	return info, err
}

type tagInfo struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// tagPattern splits a version tag into what comes before its version and
// the version: v1.2.0, plugin@1.2.0, plugin-v1.2.0.
var tagPattern = regexp.MustCompile(`^(.*?)v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// tagVersion reads a version tag.
func tagVersion(name string) (prefix string, version pluginbuild.Version, ok bool) {
	match := tagPattern.FindStringSubmatch(name)
	if match == nil {
		return "", pluginbuild.Version{}, false
	}
	version, ok = pluginbuild.ParseVersion(match[2])
	return match[1], version, ok
}

// newestTag is the tag of the newest version among tags with prefix, newer
// than after when after is set. Prereleases count only when withPrerelease.
func newestTag(tags []tagInfo, prefix string, after *pluginbuild.Version, withPrerelease bool) (tagInfo, pluginbuild.Version, bool) {
	var best tagInfo
	var bestVersion pluginbuild.Version
	found := false
	for _, tag := range tags {
		tagPrefix, version, ok := tagVersion(tag.Name)
		if !ok || tagPrefix != prefix || (version.Prerelease != "" && !withPrerelease) || !shaPattern.MatchString(tag.Commit.SHA) {
			continue
		}
		if after != nil && version.Compare(*after) <= 0 {
			continue
		}
		if !found || version.Compare(bestVersion) > 0 {
			best, bestVersion, found = tag, version, true
		}
	}
	return best, bestVersion, found
}

// resolve pins a link to a commit from the repository's refs, which Git's
// protocol lists without the API. A link to a commit is pinned as it is:
// the archive tells its full id and when it was made.
func (hub *gitHub) resolve(ctx context.Context, link Link) (Resolved, error) {
	resolved := Resolved{Owner: link.Owner, Repo: link.Repo}
	if link.Ref != "" && !link.Tag {
		resolved.Commit, resolved.RefKind = strings.ToLower(link.Ref), contract.PluginRefCommit
		return resolved, nil
	}
	tag := link.Ref
	if link.LatestRelease {
		latest, err := hub.latestRelease(ctx, link.Owner, link.Repo)
		if err != nil {
			return resolved, err
		}
		tag = latest
	}
	refs, err := hub.refs(ctx, link.Owner, link.Repo)
	if err != nil {
		return resolved, err
	}
	switch {
	case tag != "":
		commit, ok := refs.tags[tag]
		if !ok {
			return resolved, fail(contract.PluginInstallRefNotFound, tag)
		}
		resolved.Ref, resolved.RefKind, resolved.Commit = tag, contract.PluginRefTag, commit
	case len(link.Tree) > 0:
		// A branch may hold slashes: the shortest ref the repository has
		// wins, and what follows is the plugin's directory.
		for length := 1; length <= len(link.Tree); length++ {
			ref := strings.Join(link.Tree[:length], "/")
			if commit, ok := refs.branches[ref]; ok {
				resolved.Ref, resolved.RefKind, resolved.Commit = ref, contract.PluginRefBranch, commit
			} else if commit, ok := refs.tags[ref]; ok {
				resolved.Ref, resolved.RefKind, resolved.Commit = ref, contract.PluginRefTag, commit
			} else if length == 1 && shaPattern.MatchString(ref) {
				resolved.RefKind, resolved.Commit = contract.PluginRefCommit, strings.ToLower(ref)
			} else {
				continue
			}
			resolved.Subdir = strings.Join(link.Tree[length:], "/")
			return resolved, nil
		}
		return resolved, fail(contract.PluginInstallRefNotFound, link.Tree[0])
	default:
		// The home page: the newest version tag, else the default branch.
		if tag, _, ok := newestTag(refs.tagList(), "", nil, false); ok {
			resolved.Ref, resolved.RefKind, resolved.Commit = tag.Name, contract.PluginRefTag, tag.Commit.SHA
		} else if commit, ok := refs.branches[refs.head]; ok {
			resolved.Ref, resolved.RefKind, resolved.Commit = refs.head, contract.PluginRefBranch, commit
		} else {
			return resolved, fail(contract.PluginInstallRefNotFound, "default branch")
		}
	}
	return resolved, nil
}

// Bounds of a repository archive.
const (
	// maxArchiveBytes bounds the compressed archive of a commit.
	maxArchiveBytes = 64 << 20
	// maxSourceBytes and maxSourceFiles bound the plugin's directory in it.
	maxSourceBytes = 64 << 20
	maxSourceFiles = 10000
)

// download writes the files of resolved's directory at its commit into
// dest, and returns the commit's full id and when it was made, which the
// archive records.
func (hub *gitHub) download(ctx context.Context, resolved Resolved, dest string) (archiveCommit, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, hub.codeload+"/"+resolved.Owner+"/"+resolved.Repo+"/tar.gz/"+resolved.Commit, nil)
	if err != nil {
		return archiveCommit{}, err
	}
	response, err := hub.client.Do(request)
	if err != nil {
		return archiveCommit{}, fail(contract.PluginInstallUnavailable, "GitHub: "+networkError(err))
	}
	defer response.Body.Close()
	if err := hub.statusFailure(response); err != nil {
		if errors.Is(err, errNotFound) {
			return archiveCommit{}, fail(contract.PluginInstallRefNotFound, resolved.Commit)
		}
		return archiveCommit{}, err
	}
	limited := &limitReader{reader: response.Body, left: maxArchiveBytes}
	found, commit, err := extractSource(limited, resolved.Subdir, dest, archiveLimits{files: maxSourceFiles, bytes: maxSourceBytes}, taskExtras(resolved.Subdir), filepath.Join(filepath.Dir(dest), extrasDir))
	if limited.exceeded {
		return archiveCommit{}, fail(contract.PluginInstallTooLarge, "repository")
	}
	if err != nil {
		var failure *Failure
		if errors.As(err, &failure) {
			return archiveCommit{}, failure
		}
		return archiveCommit{}, fail(contract.PluginInstallUnavailable, "GitHub: "+err.Error())
	}
	// An archive git made names its commit; one that names none is taken
	// for the commit asked for only when that was named in full.
	asked := strings.ToLower(resolved.Commit)
	switch {
	case commit.id == "" && objectPattern.MatchString(asked):
		commit.id = asked
	case !objectPattern.MatchString(commit.id) || !strings.HasPrefix(commit.id, asked):
		return archiveCommit{}, fail(contract.PluginInstallUnavailable, "GitHub sent the source of another commit")
	}
	if !found {
		return archiveCommit{}, fail(contract.PluginInstallNoPlugin, resolved.Subdir)
	}
	return commit, nil
}

// notes are a release's notes, "" when the tag has no release.
func (hub *gitHub) notes(ctx context.Context, owner, repo, tag string) string {
	var release struct {
		Body string `json:"body"`
	}
	if hub.get(ctx, "/repos/"+owner+"/"+repo+"/releases/tags/"+url.PathEscape(tag), &release) != nil {
		return ""
	}
	return clipNotes(release.Body)
}

// commitsSince are the first lines of the commits from base to head, the
// newest first.
func (hub *gitHub) commitsSince(ctx context.Context, owner, repo, base, head string) string {
	var compare struct {
		Commits []commitInfo `json:"commits"`
	}
	if hub.get(ctx, "/repos/"+owner+"/"+repo+"/compare/"+base+"..."+head, &compare) != nil {
		return ""
	}
	var lines []string
	for _, commit := range slices.Backward(compare.Commits) {
		line, _, _ := strings.Cut(strings.TrimSpace(commit.Commit.Message), "\n")
		if line != "" {
			lines = append(lines, "- "+line)
		}
		if len(lines) == 20 {
			break
		}
	}
	return clipNotes(strings.Join(lines, "\n"))
}

// maxNotesBytes bounds the notes of an update.
const maxNotesBytes = 8 << 10

func clipNotes(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if len(text) <= maxNotesBytes {
		return text
	}
	cut := maxNotesBytes
	for cut > 0 && !utf8RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// networkError is what a failed request says without the address it went
// to.
func networkError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return err.Error()
}

// limitReader reads at most left bytes and notes a source longer than that.
type limitReader struct {
	reader   io.Reader
	left     int64
	exceeded bool
}

func (limited *limitReader) Read(buffer []byte) (int, error) {
	if limited.left <= 0 {
		var probe [1]byte
		if n, _ := limited.reader.Read(probe[:]); n > 0 {
			limited.exceeded = true
			return 0, fmt.Errorf("larger than allowed")
		}
		return 0, io.EOF
	}
	if int64(len(buffer)) > limited.left {
		buffer = buffer[:limited.left]
	}
	n, err := limited.reader.Read(buffer)
	limited.left -= int64(n)
	return n, err
}
