// Package plugininstall installs plugins from source (plugin platform v2,
// §7): a GitHub link is pinned to a commit, the commit's source fetched
// through the gateway's outbound proxy, the packages its lock file pins
// fetched from npm and checked against the lock file's hashes, and the
// project built in Core without running any script. What the build makes
// waits for the user's review; only a confirmed install replaces what the
// plugin directory holds, and the version it replaces is kept to go back
// to.
package plugininstall

import (
	"net/url"
	"regexp"
	"strings"
)

// Link is a GitHub link to a plugin's source.
type Link struct {
	Owner string
	Repo  string
	// Ref is the tag or commit the link names. A link to the repository
	// alone names none: the install takes the newest version tag, else the
	// default branch.
	Ref string
	// Tag is set when the link names Ref as a tag, as a release does.
	Tag bool
	// Tree is what follows tree/ in the link: a ref and the plugin's
	// directory, which only the repository can tell apart.
	Tree []string
	// LatestRelease asks for the newest release.
	LatestRelease bool
}

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	shaPattern   = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	// segmentPattern bounds a segment of a ref or a directory.
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
)

// maxTreeDepth bounds the segments of a ref and directory together.
const maxTreeDepth = 16

// ParseLink reads a link to a GitHub repository: its home page, a branch,
// tag or directory under tree/, a release, or a commit. owner/name alone
// counts as the home page.
func ParseLink(text string) (Link, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2048 {
		return Link{}, false
	}
	if !strings.Contains(text, "://") {
		if strings.HasPrefix(text, "github.com/") || strings.HasPrefix(text, "www.github.com/") {
			text = "https://" + text
		} else if strings.Count(text, "/") == 1 {
			text = "https://github.com/" + text
		}
	}
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return Link{}, false
	}
	if host := strings.ToLower(parsed.Hostname()); host != "github.com" && host != "www.github.com" {
		return Link{}, false
	}
	if parsed.Port() != "" {
		return Link{}, false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 2 {
		return Link{}, false
	}
	link := Link{Owner: segments[0], Repo: strings.TrimSuffix(segments[1], ".git")}
	if !ownerPattern.MatchString(link.Owner) || !repoPattern.MatchString(link.Repo) || link.Repo == "." || link.Repo == ".." {
		return Link{}, false
	}
	rest := segments[2:]
	if len(rest) == 0 {
		return link, true
	}
	for _, segment := range rest {
		if !segmentPattern.MatchString(segment) || segment == "." || segment == ".." {
			return Link{}, false
		}
	}
	switch rest[0] {
	case "tree", "blob":
		tree := rest[1:]
		if rest[0] == "blob" {
			// A link to a file of the plugin, such as its package.json,
			// names the directory the file is in.
			if len(tree) < 2 {
				return Link{}, false
			}
			tree = tree[:len(tree)-1]
		}
		if len(tree) == 0 || len(tree) > maxTreeDepth {
			return Link{}, false
		}
		link.Tree = tree
	case "releases":
		switch {
		case len(rest) == 1:
		case len(rest) == 2 && rest[1] == "latest":
			link.LatestRelease = true
		case len(rest) >= 3 && rest[1] == "tag" && len(rest)-2 <= maxTreeDepth:
			link.Ref, link.Tag = strings.Join(rest[2:], "/"), true
		default:
			return Link{}, false
		}
	case "tags":
		if len(rest) != 1 {
			return Link{}, false
		}
	case "commit":
		if len(rest) != 2 || !shaPattern.MatchString(rest[1]) {
			return Link{}, false
		}
		link.Ref = strings.ToLower(rest[1])
	default:
		return Link{}, false
	}
	return link, true
}

// Repository is owner/name.
func (link Link) Repository() string { return link.Owner + "/" + link.Repo }

// TreeURL is the link to dir at ref, for an update to review.
func TreeURL(repository, ref, dir string) string {
	text := "https://github.com/" + repository + "/tree/" + ref
	if dir != "" {
		text += "/" + dir
	}
	return text
}

// CommitURL is the link to a commit.
func CommitURL(repository, commit string) string {
	return "https://github.com/" + repository + "/commit/" + commit
}
