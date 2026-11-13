package plugininstall

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

// DefaultGitHub is where GitHub serves repositories over Git's HTTP
// protocol, and its release pages.
const DefaultGitHub = "https://github.com"

// maxRefBytes bounds the refs a repository lists.
const maxRefBytes = 8 << 20

// objectPattern is a full object id: SHA-1, or SHA-256 in a repository
// that uses it.
var objectPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// repoRefs are a repository's branches and tags, each with the commit it
// names, and the branch HEAD points at.
type repoRefs struct {
	head     string
	branches map[string]string
	tags     map[string]string
}

// tagList lists the tags for newestTag.
func (refs repoRefs) tagList() []tagInfo {
	tags := make([]tagInfo, 0, len(refs.tags))
	for name, commit := range refs.tags {
		tag := tagInfo{Name: name}
		tag.Commit.SHA = commit
		tags = append(tags, tag)
	}
	slices.SortFunc(tags, func(a, b tagInfo) int { return strings.Compare(a.Name, b.Name) })
	return tags
}

// pktLine frames a line of Git's protocol.
func pktLine(line string) []byte {
	return fmt.Appendf(nil, "%04x%s", len(line)+4, line)
}

// refs lists the repository's branches and tags as git ls-remote does,
// through Git's own HTTP protocol (version 2, ls-refs). GitHub does not
// count it against the hourly limit of its API, which an address many
// people share runs out of. An annotated tag names the commit it points to.
func (hub *gitHub) refs(ctx context.Context, owner, repo string) (repoRefs, error) {
	var body bytes.Buffer
	body.Write(pktLine("command=ls-refs\n"))
	body.WriteString("0001")
	for _, argument := range []string{"peel\n", "symrefs\n", "ref-prefix HEAD\n", "ref-prefix refs/heads/\n", "ref-prefix refs/tags/\n"} {
		body.Write(pktLine(argument))
	}
	body.WriteString("0000")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hub.web+"/"+owner+"/"+repo+".git/git-upload-pack", &body)
	if err != nil {
		return repoRefs{}, err
	}
	request.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	request.Header.Set("Accept", "application/x-git-upload-pack-result")
	request.Header.Set("Git-Protocol", "version=2")
	response, err := hub.client.Do(request)
	if err != nil {
		return repoRefs{}, fail(contract.PluginInstallUnavailable, "GitHub: "+networkError(err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusOK:
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusNotFound:
		// GitHub asks for a login for a private repository as for one that
		// does not exist.
		return repoRefs{}, fail(contract.PluginInstallRepositoryNotFound, owner+"/"+repo)
	case response.StatusCode == http.StatusTooManyRequests:
		return repoRefs{}, fail(contract.PluginInstallRateLimited, retryAfter(response))
	default:
		return repoRefs{}, fail(contract.PluginInstallUnavailable, "GitHub "+response.Status)
	}
	refs, err := parseRefs(response.Body)
	if err != nil {
		return repoRefs{}, fail(contract.PluginInstallUnavailable, "GitHub: "+err.Error())
	}
	return refs, nil
}

// parseRefs reads the answer of ls-refs: one ref a line, as
// "<object> <name>" with symref-target: and peeled: after it, then a flush.
func parseRefs(reader io.Reader) (repoRefs, error) {
	refs := repoRefs{branches: map[string]string{}, tags: map[string]string{}}
	counted := &limitReader{reader: reader, left: maxRefBytes}
	buffered := bufio.NewReader(counted)
	for {
		var size [4]byte
		if _, err := io.ReadFull(buffered, size[:]); err != nil {
			if counted.exceeded {
				return refs, errors.New("the repository lists too many refs")
			}
			return refs, errors.New("the list of refs ended early")
		}
		length, err := strconv.ParseUint(string(size[:]), 16, 16)
		if err != nil {
			return refs, errors.New("GitHub answered what is not Git's protocol")
		}
		switch {
		case length == 0:
			return refs, nil
		case length < 4:
			continue
		}
		payload := make([]byte, length-4)
		if _, err := io.ReadFull(buffered, payload); err != nil {
			return refs, errors.New("the list of refs ended early")
		}
		line := strings.TrimSuffix(string(payload), "\n")
		if message, failed := strings.CutPrefix(line, "ERR "); failed {
			return refs, errors.New(message)
		}
		fields := strings.Split(line, " ")
		if len(fields) < 2 || !objectPattern.MatchString(fields[0]) {
			return refs, errors.New("GitHub answered a ref without its object")
		}
		object, name := fields[0], fields[1]
		for _, attribute := range fields[2:] {
			if target, ok := strings.CutPrefix(attribute, "symref-target:"); ok && name == "HEAD" {
				refs.head = strings.TrimPrefix(target, "refs/heads/")
			}
			if peeled, ok := strings.CutPrefix(attribute, "peeled:"); ok && objectPattern.MatchString(peeled) {
				object = peeled
			}
		}
		if branch, ok := strings.CutPrefix(name, "refs/heads/"); ok {
			refs.branches[branch] = object
		} else if tag, ok := strings.CutPrefix(name, "refs/tags/"); ok {
			refs.tags[tag] = object
		}
	}
}

// latestRelease is the tag of the repository's latest release, read from
// where its release page sends a browser rather than from the API.
func (hub *gitHub) latestRelease(ctx context.Context, owner, repo string) (string, error) {
	page := hub.web + "/" + owner + "/" + repo + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, page, nil)
	if err != nil {
		return "", err
	}
	client := *hub.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return "", fail(contract.PluginInstallUnavailable, "GitHub: "+networkError(err))
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	switch {
	case response.StatusCode == http.StatusNotFound:
		return "", fail(contract.PluginInstallRepositoryNotFound, owner+"/"+repo)
	case response.StatusCode == http.StatusTooManyRequests:
		return "", fail(contract.PluginInstallRateLimited, retryAfter(response))
	case response.StatusCode < 300 || response.StatusCode >= 400:
		return "", fail(contract.PluginInstallUnavailable, "GitHub "+response.Status)
	}
	location, err := request.URL.Parse(response.Header.Get("Location"))
	if err != nil {
		return "", fail(contract.PluginInstallUnavailable, "GitHub sent the release page elsewhere")
	}
	// A repository without releases sends the page to the list of them.
	escaped, found := strings.CutPrefix(location.EscapedPath(), "/"+owner+"/"+repo+"/releases/tag/")
	if !found {
		return "", fail(contract.PluginInstallRefNotFound, "releases/latest")
	}
	tag, err := url.PathUnescape(escaped)
	if err != nil || tag == "" {
		return "", fail(contract.PluginInstallUnavailable, "GitHub sent the release page elsewhere")
	}
	return tag, nil
}

// retryAfter is when a rate limited request may try again, "" when the
// answer does not say.
func retryAfter(response *http.Response) string {
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339)
	}
	return ""
}
