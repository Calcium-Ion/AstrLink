package contract

import "net/url"

// Plugins installed from source (plugin platform v2, §7): a GitHub link is
// resolved to a commit, its source built in Core with the dependencies its
// lock file pins, reviewed, and installed only once the user confirms.

// Where an installed plugin's source came from.
const (
	PluginRefTag    = "tag"
	PluginRefBranch = "branch"
	PluginRefCommit = "commit"
	// PluginRefVersion is a version of a plugin marketplace, which an
	// update follows to the newest version the index lists.
	PluginRefVersion = "version"
)

// Kinds of PluginInstallSource.
const (
	PluginSourceGitHub = "github"
	// PluginSourceMarket is a plugin marketplace in new-api's index
	// format: its index.json lists the plugins, their versions and the
	// sha256 of each file.
	PluginSourceMarket = "market"
)

// PluginInstallSource is where a plugin installed from source came from.
type PluginInstallSource struct {
	// Kind is github or market.
	Kind string `json:"kind"`
	// Repository is owner/name; for a marketplace, its name.
	Repository string `json:"repository"`
	// URL is the link the install started from.
	URL string `json:"url"`
	// Ref is the tag or branch installed, "" for a commit.
	Ref string `json:"ref,omitempty"`
	// RefKind is tag, branch or commit: what an update follows.
	RefKind string `json:"ref_kind"`
	// Commit is the commit built; for a marketplace, the sha256 of the
	// plugin's file.
	Commit      string `json:"commit"`
	CommittedAt string `json:"committed_at,omitempty"`
	// Subdir is the plugin's directory in the repository, "" for its root;
	// for a marketplace, the plugin's file relative to the index.
	Subdir string `json:"subdir,omitempty"`
	// Index is the marketplace index a plugin came from, and Key its key
	// there.
	Index string `json:"index,omitempty"`
	Key   string `json:"key,omitempty"`
}

// PluginInstallRecord is install.json beside an installed plugin: where its
// files came from and what went into them.
type PluginInstallRecord struct {
	Source  PluginInstallSource `json:"source"`
	Version string              `json:"version"`
	// Lockfile is the lock file the dependencies came from, "" for none,
	// and LockfileSHA256 its hash.
	Lockfile       string `json:"lockfile,omitempty"`
	LockfileSHA256 string `json:"lockfile_sha256,omitempty"`
	// Outputs are the sha256 of each file built.
	Outputs      map[string]string  `json:"outputs"`
	Dependencies []PluginDependency `json:"dependencies,omitempty"`
	InstalledAt  string             `json:"installed_at"`
}

// PluginDependency is a package a build took from the lock file.
type PluginDependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Bytes is its unpacked size.
	Bytes int64 `json:"bytes"`
	// Scripts is set for a package with install scripts, which never ran.
	Scripts bool `json:"scripts,omitempty"`
}

// PluginInstallPreviewRequest asks for a plugin to be fetched, built and
// reviewed.
type PluginInstallPreviewRequest struct {
	URL string `json:"url"`
}

// PluginInstallRequest installs what a review staged.
type PluginInstallRequest struct {
	Token string `json:"token"`
}

// PluginRollbackRequest puts back a version a later install replaced.
type PluginRollbackRequest struct {
	Version string `json:"version"`
}

// PluginInstallReview is what the user reviews before a plugin is
// installed: where it comes from, what it asks for, and what it replaces.
type PluginInstallReview struct {
	// Token names the build staged for the install, until ExpiresAt.
	Token     string              `json:"token"`
	ExpiresAt string              `json:"expires_at"`
	Source    PluginInstallSource `json:"source"`

	Key         string `json:"key"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	APIVersion  int    `json:"api_version"`
	// Kind is extension, or task for a task plugin, which Task describes.
	Kind string      `json:"kind,omitempty"`
	Task *PluginTask `json:"task,omitempty"`
	// Icon is the icon the plugin installs with, as a data URL.
	Icon string `json:"icon,omitempty"`
	// Changelog is what the version's changelog says, in the page's
	// language when the plugin has it.
	Changelog      string   `json:"changelog,omitempty"`
	Capabilities   []string `json:"capabilities"`
	NetworkPurpose string   `json:"network_purpose,omitempty"`
	Hooks          []string `json:"hooks"`
	// Client is set when the plugin brings interface code.
	Client      bool `json:"client,omitempty"`
	PluginBytes int  `json:"plugin_bytes"`
	ClientBytes int  `json:"client_bytes,omitempty"`

	Lockfile        string             `json:"lockfile,omitempty"`
	Dependencies    []PluginDependency `json:"dependencies"`
	DependencyBytes int64              `json:"dependency_bytes"`
	// Engines is the range of AstrLink versions the plugin declares.
	Engines  string                 `json:"engines,omitempty"`
	Warnings []PluginInstallWarning `json:"warnings,omitempty"`

	// Replaces is the plugin of the same key the install replaces, and
	// Changes what differs from it.
	Replaces *PluginInstallReplace `json:"replaces,omitempty"`
	Changes  *PluginInstallChanges `json:"changes,omitempty"`
}

// Warnings of a review.
const (
	// PluginWarningScripts: a package's install scripts did not run.
	PluginWarningScripts = "install_scripts_skipped"
	// PluginWarningVersion: package.json and meta.version disagree.
	PluginWarningVersion = "version_mismatch"
	// PluginWarningBuild: the bundler warned.
	PluginWarningBuild = "build_warning"
	// PluginWarningIcon: the plugin's icon could not be fetched.
	PluginWarningIcon = "icon_unavailable"
	// PluginWarningRoute is a route of a new-api plugin's provider part
	// AstrLink does not serve: one of a path the gateway does not answer.
	// Detail names its path.
	PluginWarningRoute = "route_unsupported"
)

// PluginInstallWarning is something the review points out that does not
// stop the install.
type PluginInstallWarning struct {
	Code    string `json:"code"`
	Package string `json:"package,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// What an install replaces.
const (
	PluginReplacesBuiltin     = "builtin"
	PluginReplacesSource      = "source"
	PluginReplacesDevelopment = "development"
	PluginReplacesDirectory   = "directory"
)

// PluginInstallReplace is the plugin an install replaces.
type PluginInstallReplace struct {
	// Origin is builtin, source, development or directory.
	Origin  string `json:"origin"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Repository is set for a plugin installed from another repository.
	Repository string `json:"repository,omitempty"`
}

// PluginInstallChanges is what an install changes of the plugin it
// replaces.
type PluginInstallChanges struct {
	FromVersion         string   `json:"from_version,omitempty"`
	AddedCapabilities   []string `json:"added_capabilities,omitempty"`
	RemovedCapabilities []string `json:"removed_capabilities,omitempty"`
	// Client is "added" or "removed" when the interface code comes or
	// goes.
	Client                string   `json:"client,omitempty"`
	NetworkPurposeChanged bool     `json:"network_purpose_changed,omitempty"`
	AddedDependencies     []string `json:"added_dependencies,omitempty"`
	RemovedDependencies   []string `json:"removed_dependencies,omitempty"`
	// ChangedDependencies read "name 1.0.0 → 1.1.0".
	ChangedDependencies []string `json:"changed_dependencies,omitempty"`
}

// Why an install could not be reviewed.
const (
	PluginInstallInvalidLink           = "invalid_link"
	PluginInstallRepositoryNotFound    = "repository_not_found"
	PluginInstallRefNotFound           = "ref_not_found"
	PluginInstallRateLimited           = "rate_limited"
	PluginInstallUnavailable           = "source_unavailable"
	PluginInstallTooLarge              = "too_large"
	PluginInstallNoPlugin              = "no_plugin"
	PluginInstallNoLockfile            = "no_lockfile"
	PluginInstallLockfileUnsupported   = "lockfile_unsupported"
	PluginInstallDependencyUnsupported = "dependency_unsupported"
	PluginInstallIntegrity             = "integrity_failed"
	PluginInstallRegistryUnavailable   = "registry_unavailable"
	PluginInstallNativeModule          = "native_module"
	PluginInstallBuildFailed           = "build_failed"
	PluginInstallInvalidPlugin         = "invalid_plugin"
	PluginInstallIncompatible          = "incompatible"
	PluginInstallExpired               = "expired"
	PluginInstallBusy                  = "busy"
)

// PluginInstallFailure says why a plugin could not be fetched, built or
// installed, for the review to show instead of installing.
type PluginInstallFailure struct {
	Reason string `json:"reason"`
	// Detail names what failed: a package, a version range, a ref.
	Detail string `json:"detail,omitempty"`
	// Messages are the bundler's or the check's, for build_failed and
	// invalid_plugin.
	Messages []string `json:"messages,omitempty"`
}

// PluginUpdateCheck is what checking an installed plugin's repository for
// a newer version found.
type PluginUpdateCheck struct {
	// Pinned is set for a plugin installed from a commit, which has no
	// newer version to follow.
	Pinned    bool `json:"pinned,omitempty"`
	Available bool `json:"available"`
	// Ref is the newer tag, or the branch followed; Version the tag's
	// version.
	Ref         string `json:"ref,omitempty"`
	RefKind     string `json:"ref_kind,omitempty"`
	Commit      string `json:"commit,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	Version     string `json:"version,omitempty"`
	// Notes are the release's notes, or the new commits' first lines.
	Notes string `json:"notes,omitempty"`
	// URL is the link to review the update with.
	URL string `json:"url,omitempty"`
}

// PluginInstalledVersion is a version a later install replaced, kept to go
// back to.
type PluginInstalledVersion struct {
	ID      string               `json:"id"`
	Version string               `json:"version,omitempty"`
	Source  *PluginInstallSource `json:"source,omitempty"`
	// ReplacedAt is when a later install replaced it.
	ReplacedAt string `json:"replaced_at"`
}

// The plugin marketplaces of new-api, on the New API site: the official
// one, which is also built from the new-api-plugins repository on GitHub,
// and the community one, where authors publish their own plugins.
const (
	OfficialPluginMarket  = "https://www.newapi.ai/api/v1/plugins/index.json"
	CommunityPluginMarket = "https://www.newapi.ai/api/v1/community/index.json"
	// GitHubPluginMarket is the official marketplace's repository, which
	// installs made before the New API site served it name.
	GitHubPluginMarket = "https://raw.githubusercontent.com/QuantumNous/new-api-plugins/main/index.json"
	// CommunityPluginResolve finds a community plugin by its code.
	CommunityPluginResolve = "https://www.newapi.ai/api/v1/community/resolve"

	newAPISite        = "www.newapi.ai"
	newAPIBackupSite  = "www.newapi.pro"
	gitHubMarketCache = "https://cdn.jsdelivr.net/gh/QuantumNous/new-api-plugins@main/index.json"
)

// PluginMarketMirrors are the addresses that serve the same index as
// index, to try in order when it does not answer: the New API site's
// backup site for its addresses, and for the official marketplace its
// repository on GitHub and jsDelivr's copy of it.
func PluginMarketMirrors(index string) []string {
	var mirrors []string
	if parsed, err := url.Parse(index); err == nil && parsed.Host == newAPISite {
		parsed.Host = newAPIBackupSite
		mirrors = append(mirrors, parsed.String())
	}
	switch index {
	case OfficialPluginMarket:
		mirrors = append(mirrors, GitHubPluginMarket, gitHubMarketCache)
	case GitHubPluginMarket:
		mirrors = append(mirrors, gitHubMarketCache)
	}
	return mirrors
}

// PluginMarket is what a plugin marketplace offers.
type PluginMarket struct {
	// Index is the address of its index.json; Name what it calls itself.
	Index   string              `json:"index"`
	Name    string              `json:"name"`
	Plugins []PluginMarketEntry `json:"plugins"`
}

// PluginMarketAuthor is who published a community plugin.
type PluginMarketAuthor struct {
	Login string `json:"login"`
	URL   string `json:"url,omitempty"`
}

// PluginMarketRevoked says the marketplace took down the version installed
// now, and why.
type PluginMarketRevoked struct {
	Reason string `json:"reason,omitempty"`
}

// PluginMarketDoc is a version's usage guide or release notes, in the
// user's language when the marketplace has it.
type PluginMarketDoc struct {
	Markdown string `json:"markdown"`
	// Language is the language of Markdown; SourceURL a page where the
	// file can be read.
	Language  string `json:"language,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

// The documents a marketplace version may have.
const (
	PluginMarketReadme    = "readme"
	PluginMarketChangelog = "changelog"
)

// PluginMarketEntry is a plugin a marketplace offers, as its index
// describes the newest version.
type PluginMarketEntry struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Icon is a LobeHub icon name or text / text:<label>; IconURL the
	// icon as a data URL, when the gateway could fetch it.
	Icon         string                `json:"icon,omitempty"`
	IconURL      string                `json:"icon_url,omitempty"`
	Website      string                `json:"website,omitempty"`
	SortPriority int32                 `json:"sort_priority,omitempty"`
	Latest       string                `json:"latest"`
	Versions     []PluginMarketVersion `json:"versions"`
	Models       []string              `json:"models"`
	Protocols    []PluginTaskProtocol  `json:"protocols"`
	Routes       []PluginTaskRoute     `json:"routes"`
	// Link installs the newest version.
	Link string `json:"link"`
	// Installed is the version installed now, "" for none.
	Installed string `json:"installed,omitempty"`
	// Unsupported are the parts of the newest version this gateway does
	// not run yet; such a plugin is listed but cannot be installed.
	Unsupported []string `json:"unsupported,omitempty"`
	// Code is the plugin's 8-digit code on the New API site.
	Code string `json:"code,omitempty"`
	// Recommended marks a plugin the marketplace recommends.
	Recommended bool `json:"recommended,omitempty"`
	// Author published a community plugin from Repository.
	Author     *PluginMarketAuthor `json:"author,omitempty"`
	Repository string              `json:"repository,omitempty"`
	// Installs30d is how many gateways installed it in 30 days, when the
	// marketplace counts.
	Installs30d *int `json:"installs_30d,omitempty"`
	// Revoked is set when the marketplace took down the version installed
	// now.
	Revoked *PluginMarketRevoked `json:"revoked,omitempty"`
}

// The parts of a new-api plugin besides its task part.
const (
	PluginPartProvider  = "provider"
	PluginPartTransform = "transform"
)

// PluginMarketVersion is a version a marketplace lists.
type PluginMarketVersion struct {
	Version       string   `json:"version"`
	MinAPIVersion int      `json:"min_api_version,omitempty"`
	BaseURL       string   `json:"base_url,omitempty"`
	Auth          string   `json:"auth,omitempty"`
	AllowedHosts  []string `json:"allowed_hosts,omitempty"`
	// Link installs this version.
	Link string `json:"link"`
	// Readme and Changelog say the version has a usage guide and release
	// notes to read.
	Readme    bool `json:"readme,omitempty"`
	Changelog bool `json:"changelog,omitempty"`
}
