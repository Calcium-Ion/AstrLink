package plugininstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// A plugin marketplace is new-api's index format: an index.json that lists
// each plugin's versions, the file of each relative to the index, and its
// sha256. A marketplace link names a plugin of an index, and a version:
// https://…/index.json#key@version, the newest without one.

// Bounds of what a marketplace install reads.
const (
	maxIndexBytes = 8 << 20
	// marketCacheFor is how long a marketplace's listing is reused.
	marketCacheFor = 5 * time.Minute
	// iconFetches is how many icons a listing fetches at a time.
	iconFetches = 4
	// iconRetry is how long an icon that could not be fetched, and the
	// icon package, wait before another try.
	iconRetry = 2 * time.Minute
)

// marketCodePattern is a plugin's code on the New API site.
var marketCodePattern = regexp.MustCompile(`^[0-9]{8}$`)

// The waits of reading a marketplace, variables so that tests can shorten
// them.
var (
	// indexAttempt bounds one address of an index while another may
	// answer: a GitHub that cannot be reached often hangs rather than
	// refuses, and its mirror should get its turn.
	indexAttempt = 8 * time.Second
	// iconBudget bounds how long a listing waits for the icons it has not
	// got: they are decoration, and a listing without a few beats a late
	// one.
	iconBudget = 8 * time.Second
)

type marketIndex struct {
	IndexVersion int            `json:"indexVersion"`
	Name         string         `json:"name"`
	Plugins      []marketPlugin `json:"plugins"`
	// Revoked are the files the marketplace took down, by sha256.
	Revoked []marketRevoked `json:"revoked"`
}

type marketRevoked struct {
	SHA256 string               `json:"sha256"`
	Key    string               `json:"key"`
	Reason plugin.LocalizedText `json:"reason"`
}

type marketPlugin struct {
	Key          string                     `json:"key"`
	Name         plugin.LocalizedText       `json:"name"`
	Icon         string                     `json:"icon"`
	IconFile     *marketFile                `json:"iconFile"`
	Description  plugin.LocalizedText       `json:"description"`
	Website      string                     `json:"website"`
	SortPriority int32                      `json:"sortPriority"`
	Models       []string                   `json:"models"`
	Latest       string                     `json:"latest"`
	Versions     []marketVersion            `json:"versions"`
	Routes       []plugin.TaskRoute         `json:"routes"`
	Protocols    []plugin.TaskProtocolClaim `json:"protocols"`
	// Provider and Transform say the newest version has a provider part
	// or a request processing part, which new-api's index lists beside
	// the task part.
	Provider  bool `json:"provider"`
	Transform bool `json:"transform"`
	// Code, Recommended, Author, Repo and Installs30d are what the New API
	// site adds: the plugin's 8-digit code, whether it recommends the
	// plugin, who published a community plugin and from where, and how many
	// gateways installed it in 30 days.
	Code        string        `json:"code"`
	Recommended bool          `json:"recommended"`
	Author      *marketAuthor `json:"author"`
	Repo        string        `json:"repo"`
	Installs30d *int          `json:"installs30d"`
}

// runnableParts are the kinds of new-api plugin versions this gateway
// runs.
var runnableParts = []string{"task", contract.PluginPartProvider, contract.PluginPartTransform}

type marketAuthor struct {
	Login string `json:"login"`
	URL   string `json:"url"`
}

type marketFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// marketDoc is a version's usage guide or release notes: a file, its
// sha256 when the index lists one, a page to read it on, and its
// translations by language.
type marketDoc struct {
	Path         string                   `json:"path"`
	SHA256       string                   `json:"sha256"`
	SourceURL    string                   `json:"sourceUrl"`
	Translations map[string]marketDocFile `json:"translations"`
}

type marketDocFile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SourceURL string `json:"sourceUrl"`
}

type marketVersion struct {
	Version       string          `json:"version"`
	Path          string          `json:"path"`
	SHA256        string          `json:"sha256"`
	MinAPIVersion int             `json:"minApiVersion"`
	Kind          string          `json:"kind"`
	AllowedHosts  []string        `json:"allowedHosts"`
	BaseURL       string          `json:"baseUrl"`
	Auth          plugin.TaskAuth `json:"auth"`
	Readme        *marketDoc      `json:"readme"`
	Changelog     *marketDoc      `json:"changelog"`
}

func parseIndex(data []byte) (*marketIndex, error) {
	var index marketIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.New("the marketplace's index.json is not an index")
	}
	if index.IndexVersion != 1 {
		return nil, fmt.Errorf("the marketplace's index is version %d, not 1", index.IndexVersion)
	}
	return &index, nil
}

// find returns a plugin of the index and one of its versions: version, or
// the newest for "".
func (index *marketIndex) find(key, version string) (*marketPlugin, *marketVersion) {
	for at := range index.Plugins {
		entry := &index.Plugins[at]
		if entry.Key != key {
			continue
		}
		if version == "" {
			version = entry.Latest
		}
		for v := range entry.Versions {
			if entry.Versions[v].Version == version {
				return entry, &entry.Versions[v]
			}
		}
		return entry, nil
	}
	return nil, nil
}

// MarketLink is the link that installs version of key from index.
func MarketLink(index, key, version string) string {
	link := strings.SplitN(index, "#", 2)[0] + "#" + key
	if version != "" {
		link += "@" + version
	}
	return link
}

// marketRef is what a marketplace link names.
type marketRef struct {
	index   string
	key     string
	version string
}

// parseMarketLink reads a marketplace link: an http or https address of
// an index.json, or of a marketplace's lookup of a plugin by its code,
// which answers in the index's shape, and the plugin's key and version
// after #.
func parseMarketLink(text string) (marketRef, bool) {
	parsed, err := url.Parse(strings.TrimSpace(text))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return marketRef{}, false
	}
	lookup := path.Base(parsed.Path) == "resolve" && marketCodePattern.MatchString(parsed.Query().Get("code"))
	if !strings.HasSuffix(parsed.Path, ".json") && !lookup {
		return marketRef{}, false
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackHost(parsed.Hostname())) {
		return marketRef{}, false
	}
	key, version, _ := strings.Cut(parsed.Fragment, "@")
	if !plugin.ValidPluginKey(key) {
		return marketRef{}, false
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	return marketRef{index: parsed.String(), key: key, version: version}, true
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// marketGet reads one file of a marketplace, up to limit.
func (installer *Installer) marketGet(ctx context.Context, address string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := installer.options.Client.Do(request)
	if err != nil {
		return nil, fail(contract.PluginInstallUnavailable, "marketplace: "+networkError(err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, fail(contract.PluginInstallNoPlugin, address)
	case response.StatusCode != http.StatusOK:
		return nil, fail(contract.PluginInstallUnavailable, fmt.Sprintf("marketplace: %s answered %d", request.URL.Host, response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fail(contract.PluginInstallUnavailable, "marketplace: "+networkError(err))
	}
	if int64(len(data)) > limit {
		return nil, fail(contract.PluginInstallTooLarge, path.Base(request.URL.Path))
	}
	return data, nil
}

// fetchIndex reads a marketplace's index, from a mirror when its address
// cannot be reached, and returns the address it came from, which the files
// it lists are relative to. The address that answered last goes first, so
// a network that cannot reach the first waits for it once.
func (installer *Installer) fetchIndex(ctx context.Context, address string) (*marketIndex, string, error) {
	addresses := append([]string{address}, contract.PluginMarketMirrors(address)...)
	installer.market.mutex.Lock()
	answered := installer.market.answered[address]
	installer.market.mutex.Unlock()
	if at := slices.Index(addresses, answered); at > 0 {
		addresses[0], addresses[at] = addresses[at], addresses[0]
	}
	var last error
	for at, candidate := range addresses {
		attempt, cancel := ctx, context.CancelFunc(func() {})
		if at < len(addresses)-1 {
			attempt, cancel = context.WithTimeout(ctx, indexAttempt)
		}
		data, err := installer.marketGet(attempt, candidate, maxIndexBytes)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			last = err
			continue
		}
		index, err := parseIndex(data)
		if err != nil {
			return nil, "", &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
		}
		installer.market.mutex.Lock()
		if installer.market.answered == nil {
			installer.market.answered = map[string]string{}
		}
		installer.market.answered[address] = candidate
		installer.market.mutex.Unlock()
		return index, candidate, nil
	}
	return nil, "", last
}

// relative resolves a path of an index against the index's address,
// refusing one that leaves the marketplace's host.
func relative(base, name string) (string, error) {
	root, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	target, err := root.Parse(name)
	if err != nil || target.Host != root.Host || target.Scheme != root.Scheme {
		return "", errors.New("the index names a file outside the marketplace")
	}
	return target.String(), nil
}

// verified fetches a file of a marketplace and checks it against the
// sha256 the index lists.
func (installer *Installer) verified(ctx context.Context, base, name, sum string, limit int64) ([]byte, error) {
	address, err := relative(base, name)
	if err != nil {
		return nil, fail(contract.PluginInstallInvalidLink, name)
	}
	data, err := installer.marketGet(ctx, address, limit)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), sum) {
		return nil, fail(contract.PluginInstallIntegrity, name)
	}
	return data, nil
}

// previewMarket stages a plugin a marketplace link names.
func (installer *Installer) previewMarket(ctx context.Context, ref marketRef, link, work string, existing func(string) *Existing) (contract.PluginInstallReview, contract.PluginInstallRecord, error) {
	index, base, err := installer.fetchIndex(ctx, ref.index)
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	entry, version := index.find(ref.key, ref.version)
	switch {
	case entry == nil:
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallNoPlugin, ref.key)
	case version == nil:
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallRefNotFound, ref.key+"@"+ref.version)
	case version.Kind != "" && version.Kind != "task":
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallIncompatible, version.Kind)
	}
	text, err := installer.verified(ctx, base, version.Path, version.SHA256, plugin.MaxSourceBytes)
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	files := taskFiles{changelogs: map[string][]byte{}}
	if entry.IconFile != nil {
		if icon, err := installer.verified(ctx, base, entry.IconFile.Path, entry.IconFile.SHA256, plugin.MaxIconBytes); err == nil {
			files.icon, files.iconName = icon, "icon.svg"
			if strings.HasSuffix(strings.ToLower(entry.IconFile.Path), ".png") {
				files.iconName = "icon.png"
			}
		}
	}
	installer.marketChangelogs(ctx, base, version, files.changelogs)
	staged, err := installer.stageTask(ctx, string(text), files, filepath.Join(work, "out"))
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	if staged.checked.Task.Key != entry.Key {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{"the plugin's key is not the one the index lists"}}}
	}
	name := index.Name
	if name == "" {
		name = ref.index
	}
	record := contract.PluginInstallRecord{
		Source: contract.PluginInstallSource{
			Kind: contract.PluginSourceMarket, Repository: name, URL: link, Index: ref.index, Key: entry.Key,
			Ref: version.Version, RefKind: contract.PluginRefVersion, Commit: strings.ToLower(version.SHA256), Subdir: version.Path,
		},
		Version: staged.checked.Task.Version, Outputs: staged.outputs,
	}
	review := installer.taskReview(staged, len(text), record, files)
	if existing != nil {
		compare(&review, record, existing(entry.Key))
	}
	return review, record, nil
}

// marketChangelogs fetches a version's release notes and their
// translations: the files the index lists, checked against their sha256,
// else the CHANGELOG.md beside the version's file and the translations its
// front matter names.
func (installer *Installer) marketChangelogs(ctx context.Context, base string, version *marketVersion, into map[string][]byte) {
	if listed := version.Changelog; listed != nil && listed.Path != "" {
		if data, err := installer.docFile(ctx, base, marketDocFile{Path: listed.Path, SHA256: listed.SHA256}); err == nil {
			into["CHANGELOG.md"] = data
		}
		for language, file := range listed.Translations {
			if !changelogPattern.MatchString("CHANGELOG." + language + ".md") {
				continue
			}
			if data, err := installer.docFile(ctx, base, file); err == nil {
				into["CHANGELOG."+language+".md"] = data
			}
		}
		return
	}
	dir := path.Dir(version.Path)
	canonical, err := installer.relativeGet(ctx, base, path.Join(dir, "CHANGELOG.md"))
	if err != nil {
		return
	}
	into["CHANGELOG.md"] = canonical
	for _, translation := range changelogTranslations(string(canonical)) {
		if data, err := installer.relativeGet(ctx, base, path.Join(dir, translation)); err == nil {
			into[translation] = data
		}
	}
}

// docFile fetches a usage guide or release notes the index lists, checked
// against its sha256 when the index gives one.
func (installer *Installer) docFile(ctx context.Context, base string, file marketDocFile) ([]byte, error) {
	if file.SHA256 != "" {
		return installer.verified(ctx, base, file.Path, file.SHA256, maxDocBytes)
	}
	address, err := relative(base, file.Path)
	if err != nil {
		return nil, fail(contract.PluginInstallInvalidLink, file.Path)
	}
	return installer.marketGet(ctx, address, maxDocBytes)
}

func (installer *Installer) relativeGet(ctx context.Context, base, name string) ([]byte, error) {
	address, err := relative(base, name)
	if err != nil {
		return nil, err
	}
	return installer.marketGet(ctx, address, maxChangelogBytes)
}

// changelogTranslations reads the translations map of a changelog's front
// matter: the sibling files of other languages.
func changelogTranslations(text string) []string {
	if !strings.HasPrefix(text, "---") {
		return nil
	}
	var names []string
	inTranslations := false
	for _, line := range strings.Split(text, "\n")[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		switch {
		case strings.HasPrefix(line, "translations:"):
			inTranslations = true
		case inTranslations && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			_, value, found := strings.Cut(trimmed, ":")
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if found && changelogPattern.MatchString(value) && value != "CHANGELOG.md" {
				names = append(names, value)
			}
		default:
			inTranslations = false
		}
	}
	return names
}

// checkMarketUpdate looks for a newer version of a plugin installed from a
// marketplace: the newest its index lists.
func (installer *Installer) checkMarketUpdate(ctx context.Context, record contract.PluginInstallRecord) (contract.PluginUpdateCheck, error) {
	source := record.Source
	index, base, err := installer.fetchIndex(ctx, source.Index)
	if err != nil {
		return contract.PluginUpdateCheck{}, installer.failure(ctx, err)
	}
	entry, latest := index.find(source.Key, "")
	check := contract.PluginUpdateCheck{Ref: record.Version, RefKind: contract.PluginRefVersion}
	if entry == nil || latest == nil {
		return check, nil
	}
	current, okCurrent := pluginbuild.ParseVersion(record.Version)
	newest, okNewest := pluginbuild.ParseVersion(latest.Version)
	if !okCurrent || !okNewest || current.Compare(newest) >= 0 {
		return check, nil
	}
	check.Available, check.Version, check.Ref = true, latest.Version, latest.Version
	check.Commit = strings.ToLower(latest.SHA256)
	check.URL = MarketLink(source.Index, source.Key, latest.Version)
	changelogs := map[string][]byte{}
	installer.marketChangelogs(ctx, base, latest, changelogs)
	check.Notes = changelogIn(changelogs, installer.language())
	return check, nil
}

// marketListing is a marketplace's listing as the installer last read it:
// the plugins it lists by key, and the address the files they name are
// relative to.
type marketListing struct {
	at      time.Time
	market  contract.PluginMarket
	entries map[string]marketPlugin
	base    string
	// revoked are the reasons of the files the marketplace took down, by
	// sha256.
	revoked map[string]string
}

type marketCache struct {
	mutex    sync.Mutex
	listings map[string]marketListing
	icons    map[string]marketIcon
	// answered is the address each index last came from.
	answered map[string]string
}

// marketIcon is an icon as fetched: a data URL, "" for none. failed is when
// it could not be fetched; a listing after iconRetry tries again.
type marketIcon struct {
	url    string
	failed time.Time
}

// Installed is what a marketplace listing needs of a plugin installed
// now: its version and, when it came from a marketplace, the sha256 of its
// file.
type Installed struct {
	Version string
	SHA256  string
}

// Market lists what a marketplace offers, the official one for "":
// newest version, models, protocols, and an icon to show. installed tells
// what of a key is installed now.
func (installer *Installer) Market(ctx context.Context, index string, installed func(key string) Installed) (contract.PluginMarket, error) {
	if index == "" {
		index = contract.OfficialPluginMarket
	}
	if _, ok := parseMarketLink(MarketLink(index, "probe", "")); !ok {
		return contract.PluginMarket{}, fail(contract.PluginInstallInvalidLink, index)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cached, err := installer.listing(ctx, index)
	if err != nil {
		return contract.PluginMarket{}, installer.failure(ctx, err)
	}
	return installer.present(ctx, cached, installed), nil
}

// listing is a marketplace's listing in the plugin page's language, read
// again when the one kept is older than marketCacheFor.
func (installer *Installer) listing(ctx context.Context, index string) (marketListing, error) {
	kept := index + "\x00" + installer.language()
	installer.market.mutex.Lock()
	cached, ok := installer.market.listings[kept]
	installer.market.mutex.Unlock()
	if ok && installer.options.Now().Sub(cached.at) <= marketCacheFor {
		return cached, nil
	}
	listing, err := installer.readMarket(ctx, index)
	if err != nil {
		return marketListing{}, err
	}
	installer.market.mutex.Lock()
	if installer.market.listings == nil {
		installer.market.listings = map[string]marketListing{}
	}
	installer.market.listings[kept] = listing
	installer.market.mutex.Unlock()
	return listing, nil
}

// present is a listing as the plugin page shows it: with icons, what is
// installed now, and whether the marketplace took that version down.
func (installer *Installer) present(ctx context.Context, cached marketListing, installed func(key string) Installed) contract.PluginMarket {
	market := cached.market
	market.Plugins = slices.Clone(market.Plugins)
	installer.fillIcons(ctx, cached, market.Plugins)
	for at := range market.Plugins {
		if installed == nil {
			continue
		}
		now := installed(market.Plugins[at].Key)
		market.Plugins[at].Installed = now.Version
		if reason, ok := cached.revoked[strings.ToLower(now.SHA256)]; ok && now.SHA256 != "" {
			market.Plugins[at].Revoked = &contract.PluginMarketRevoked{Reason: reason}
		}
	}
	return market
}

// FindByCode finds a plugin by its 8-digit code: in the official
// marketplace first, then through the community marketplace's lookup, which
// also finds a plugin its listing leaves out. The answer lists the one
// plugin, its Index the address its links install from.
func (installer *Installer) FindByCode(ctx context.Context, code string, installed func(key string) Installed) (contract.PluginMarket, error) {
	code = strings.TrimSpace(code)
	if !marketCodePattern.MatchString(code) {
		return contract.PluginMarket{}, fail(contract.PluginInstallInvalidLink, code)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if official, err := installer.listing(ctx, contract.OfficialPluginMarket); err == nil {
		market := installer.present(ctx, official, installed)
		for _, entry := range market.Plugins {
			if entry.Code == code {
				market.Plugins = []contract.PluginMarketEntry{entry}
				return market, nil
			}
		}
	}
	lookup := contract.CommunityPluginResolve + "?code=" + code
	listing, err := installer.readMarket(ctx, lookup)
	if err != nil {
		return contract.PluginMarket{}, installer.failure(ctx, err)
	}
	market := installer.present(ctx, listing, installed)
	if len(market.Plugins) == 0 {
		return contract.PluginMarket{}, fail(contract.PluginInstallNoPlugin, code)
	}
	market.Plugins = market.Plugins[:1]
	return market, nil
}

// MarketDoc is a version's usage guide or release notes from a
// marketplace, in the user's language when it has a translation, else in
// English. Release notes the index does not list are read from the
// CHANGELOG.md beside the version's file.
func (installer *Installer) MarketDoc(ctx context.Context, index, key, version, doc string) (contract.PluginMarketDoc, error) {
	if index == "" {
		index = contract.OfficialPluginMarket
	}
	if _, ok := parseMarketLink(MarketLink(index, "probe", "")); !ok {
		return contract.PluginMarketDoc{}, fail(contract.PluginInstallInvalidLink, index)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	listing, err := installer.listing(ctx, index)
	if err != nil {
		return contract.PluginMarketDoc{}, installer.failure(ctx, err)
	}
	entry, ok := listing.entries[key]
	if !ok {
		return contract.PluginMarketDoc{}, fail(contract.PluginInstallNoPlugin, key)
	}
	if version == "" {
		version = entry.Latest
	}
	var found *marketVersion
	for at := range entry.Versions {
		if entry.Versions[at].Version == version {
			found = &entry.Versions[at]
		}
	}
	if found == nil {
		return contract.PluginMarketDoc{}, fail(contract.PluginInstallRefNotFound, key+"@"+version)
	}
	described := found.Readme
	if doc == contract.PluginMarketChangelog {
		described = found.Changelog
	} else if doc != contract.PluginMarketReadme {
		return contract.PluginMarketDoc{}, fail(contract.PluginInstallInvalidLink, doc)
	}
	if described == nil || described.Path == "" {
		if doc == contract.PluginMarketChangelog {
			changelogs := map[string][]byte{}
			installer.marketChangelogs(ctx, listing.base, found, changelogs)
			if notes := changelogIn(changelogs, installer.language()); notes != "" {
				return contract.PluginMarketDoc{Markdown: notes}, nil
			}
		}
		return contract.PluginMarketDoc{}, fail(contract.PluginInstallNoPlugin, key+"@"+version+" "+doc)
	}
	language, file := docIn(described, installer.language())
	data, err := installer.docFile(ctx, listing.base, file)
	if err != nil {
		return contract.PluginMarketDoc{}, installer.failure(ctx, err)
	}
	result := contract.PluginMarketDoc{Markdown: stripFrontMatter(string(data)), Language: language}
	if httpsURL(file.SourceURL) {
		result.SourceURL = file.SourceURL
	}
	return result, nil
}

// maxDocBytes bounds a usage guide.
const maxDocBytes = 256 << 10

// docIn picks a document's file in language, its primary language, or the
// English file.
func docIn(doc *marketDoc, language string) (string, marketDocFile) {
	candidates := []string{language}
	if primary, _, found := strings.Cut(language, "-"); found {
		candidates = append(candidates, primary)
	}
	if strings.HasPrefix(language, "zh") {
		candidates = append(candidates, "zh-CN")
	}
	for _, candidate := range candidates {
		if file, ok := doc.Translations[candidate]; ok && file.Path != "" {
			return candidate, file
		}
	}
	return "en", marketDocFile{Path: doc.Path, SHA256: doc.SHA256, SourceURL: doc.SourceURL}
}

// fillIcons gives the plugins of a listing their icons: those fetched
// before at once, the others side by side for at most iconBudget.
func (installer *Installer) fillIcons(ctx context.Context, listing marketListing, plugins []contract.PluginMarketEntry) {
	ctx, cancel := context.WithTimeout(ctx, iconBudget)
	defer cancel()
	var group sync.WaitGroup
	slots := make(chan struct{}, iconFetches)
	for at := range plugins {
		entry, ok := listing.entries[plugins[at].Key]
		if !ok {
			continue
		}
		group.Add(1)
		go func() {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			plugins[at].IconURL = installer.marketIcon(ctx, listing.base, entry)
		}()
	}
	group.Wait()
}

// readMarket reads a marketplace's index; the icons of its plugins come with
// each listing.
func (installer *Installer) readMarket(ctx context.Context, address string) (marketListing, error) {
	index, base, err := installer.fetchIndex(ctx, address)
	if err != nil {
		return marketListing{}, err
	}
	language := installer.language()
	market := contract.PluginMarket{Index: address, Name: index.Name, Plugins: []contract.PluginMarketEntry{}}
	entries := map[string]marketPlugin{}
	for _, entry := range index.Plugins {
		if !plugin.ValidPluginKey(entry.Key) || len(entry.Versions) == 0 {
			continue
		}
		listed := contract.PluginMarketEntry{
			Key: entry.Key, Name: entry.Name.In(language), Description: entry.Description.In(language), Icon: entry.Icon, Website: entry.Website,
			SortPriority: entry.SortPriority, Latest: entry.Latest, Models: slices.Clone(entry.Models),
			Protocols: []contract.PluginTaskProtocol{}, Routes: []contract.PluginTaskRoute{}, Versions: []contract.PluginMarketVersion{},
			Link: MarketLink(address, entry.Key, entry.Latest), Recommended: entry.Recommended, Installs30d: entry.Installs30d,
		}
		if listed.Name == "" {
			listed.Name = entry.Key
		}
		if marketCodePattern.MatchString(entry.Code) {
			listed.Code = entry.Code
		}
		if author := entry.Author; author != nil && author.Login != "" && len(author.Login) <= 64 {
			listed.Author = &contract.PluginMarketAuthor{Login: author.Login}
			if httpsURL(author.URL) {
				listed.Author.URL = author.URL
			}
		}
		if httpsURL(entry.Repo) {
			listed.Repository = entry.Repo
		}
		if listed.Models == nil {
			listed.Models = []string{}
		}
		for _, claim := range entry.Protocols {
			listed.Protocols = append(listed.Protocols, contract.PluginTaskProtocol{Name: claim.Name, Supports: claim.Supports, Models: claim.Models})
		}
		for _, route := range entry.Routes {
			listed.Routes = append(listed.Routes, contract.PluginTaskRoute{Method: route.Method, Path: route.Path, Type: route.Type, Models: route.Models})
		}
		for _, version := range entry.Versions {
			// A version of only a part this gateway does not run is listed
			// so, and cannot be installed. new-api's task, provider and
			// request processing (transform) parts all run.
			if version.Version == entry.Latest && version.Kind != "" && !slices.Contains(runnableParts, version.Kind) &&
				!slices.Contains(listed.Unsupported, version.Kind) {
				listed.Unsupported = append(listed.Unsupported, version.Kind)
			}
			listed.Versions = append(listed.Versions, contract.PluginMarketVersion{
				Version: version.Version, MinAPIVersion: version.MinAPIVersion, BaseURL: version.BaseURL,
				Auth: string(version.Auth.Kind()), AllowedHosts: version.AllowedHosts, Link: MarketLink(address, entry.Key, version.Version),
				Readme: version.Readme != nil && version.Readme.Path != "", Changelog: version.Changelog != nil && version.Changelog.Path != "",
			})
		}
		if len(listed.Versions) == 0 {
			continue
		}
		entries[entry.Key] = entry
		market.Plugins = append(market.Plugins, listed)
	}
	// Each source sorts by descending priority, then key.
	sort.SliceStable(market.Plugins, func(i, j int) bool {
		if market.Plugins[i].SortPriority != market.Plugins[j].SortPriority {
			return market.Plugins[i].SortPriority > market.Plugins[j].SortPriority
		}
		return market.Plugins[i].Key < market.Plugins[j].Key
	})
	revoked := map[string]string{}
	for _, entry := range index.Revoked {
		if sum := strings.ToLower(strings.TrimSpace(entry.SHA256)); len(sum) == 64 {
			revoked[sum] = entry.Reason.In(language)
		}
	}
	return marketListing{at: installer.options.Now(), market: market, entries: entries, base: base, revoked: revoked}, nil
}

// httpsURL reports whether raw is an absolute https address, as the links a
// marketplace shows must be.
func httpsURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

// marketIcon is a marketplace plugin's icon as a data URL: the file the
// index lists, else its LobeHub icon; "" for a text avatar or an icon that
// could not be fetched, which a listing tries again after iconRetry.
func (installer *Installer) marketIcon(ctx context.Context, base string, entry marketPlugin) string {
	cacheKey := entry.Icon
	if entry.IconFile != nil {
		cacheKey = entry.IconFile.SHA256
	}
	now := installer.options.Now()
	installer.market.mutex.Lock()
	cached, ok := installer.market.icons[cacheKey]
	installer.market.mutex.Unlock()
	if ok && (cached.failed.IsZero() || now.Sub(cached.failed) < iconRetry) {
		return cached.url
	}
	var data []byte
	var err error
	mime := "image/svg+xml"
	switch {
	case entry.IconFile != nil:
		data, err = installer.verified(ctx, base, entry.IconFile.Path, entry.IconFile.SHA256, plugin.MaxIconBytes)
		if strings.HasSuffix(strings.ToLower(entry.IconFile.Path), ".png") {
			mime = "image/png"
		}
	case entry.Icon != "" && !strings.HasPrefix(entry.Icon, "text"):
		data, err = installer.lobeIcon(ctx, entry.Icon)
	}
	icon := marketIcon{}
	switch {
	case err != nil:
		icon.failed = now
	case mime == "image/png" && bytes.HasPrefix(data, []byte("\x89PNG")) || mime == "image/svg+xml" && bytes.Contains(data, []byte("<svg")):
		icon.url = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	installer.market.mutex.Lock()
	if installer.market.icons == nil {
		installer.market.icons = map[string]marketIcon{}
	}
	installer.market.icons[cacheKey] = icon
	installer.market.mutex.Unlock()
	return icon.url
}
