package plugininstall

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/taskplugin"
)

// A task plugin, written for new-api's task plugin API, is one plugin.js
// that imports nothing: it installs as it is, never built, so that its
// sha256 stays what its marketplace lists. Its icon is the sidecar file
// beside it or its versions, else the LobeHub icon its manifest names.

// extrasDir holds what an install of a task plugin reads from outside
// the plugin's directory: the repository's marketplace index and the icon
// beside the plugin's versions.
const extrasDir = "extras"

// maxChangelogBytes bounds a changelog an install keeps.
const maxChangelogBytes = 64 << 10

// changelogPattern is a changelog's file name: CHANGELOG.md, or a
// translation CHANGELOG.<locale>.md.
var changelogPattern = regexp.MustCompile(`^CHANGELOG(\.[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*)?\.md$`)

// taskFiles are what a task plugin installs with beside its source.
type taskFiles struct {
	// icon is the sidecar icon, and iconName its file name.
	icon     []byte
	iconName string
	// changelogs are its changelogs by file name.
	changelogs map[string][]byte
}

// sidecarIcon finds the icon in the first of dirs that has one.
func sidecarIcon(dirs ...string) ([]byte, string) {
	for _, dir := range dirs {
		for _, name := range iconFiles {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && len(data) <= plugin.MaxIconBytes {
				return data, name
			}
		}
	}
	return nil, ""
}

// localChangelogs reads the changelogs in dir.
func localChangelogs(dir string) map[string][]byte {
	changelogs := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return changelogs
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && changelogPattern.MatchString(entry.Name()) {
			if data, err := os.ReadFile(filepath.Join(dir, entry.Name())); err == nil && len(data) <= maxChangelogBytes {
				changelogs[entry.Name()] = data
			}
		}
	}
	return changelogs
}

// stagedTask is a task plugin written out to be installed.
type stagedTask struct {
	checked  *plugin.Checked
	outputs  map[string]string
	icon     string
	warnings []contract.PluginInstallWarning
}

// stageTask writes a task plugin as it is into out, with its icon and
// changelogs, and checks it there.
func (installer *Installer) stageTask(ctx context.Context, text string, files taskFiles, out string) (*stagedTask, error) {
	checked, err := plugin.Check(plugin.Source{File: "plugin.js", Text: text})
	if err != nil {
		return nil, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
	}
	if checked.Task == nil {
		return nil, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{"plugin.js is not a new-api plugin"}}}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	staged := &stagedTask{checked: checked, outputs: map[string]string{}}
	write := func(name string, data []byte) error {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		staged.outputs[name] = hex.EncodeToString(sum[:])
		return nil
	}
	if err := write("plugin.js", []byte(text)); err != nil {
		return nil, err
	}
	icon, iconName := files.icon, files.iconName
	if icon == nil && checked.Task.Icon != "" && !strings.HasPrefix(checked.Task.Icon, "text") {
		fetched, err := installer.lobeIcon(ctx, checked.Task.Icon)
		if err != nil {
			staged.warnings = append(staged.warnings, contract.PluginInstallWarning{Code: contract.PluginWarningIcon, Detail: checked.Task.Icon})
		} else {
			icon, iconName = fetched, "icon.svg"
		}
	}
	if icon != nil {
		if err := write(iconName, icon); err != nil {
			return nil, err
		}
		mime := "image/svg+xml"
		if iconName == "icon.png" {
			mime = "image/png"
		}
		staged.icon = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(icon)
	}
	for name, data := range files.changelogs {
		if err := write(name, data); err != nil {
			return nil, err
		}
	}
	// Checked where it is installed from, as the registry loads it.
	if checked, err = plugin.Check(plugin.Source{File: "plugin.js", Text: text, Dir: out}); err != nil {
		return nil, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
	}
	staged.checked = checked
	return staged, nil
}

// taskReview is the review of a staged task plugin.
func (installer *Installer) taskReview(staged *stagedTask, pluginBytes int, record contract.PluginInstallRecord, files taskFiles) contract.PluginInstallReview {
	review := newReview(staged.checked, pluginBytes, 0, record)
	meta := staged.checked.Task
	language := installer.language()
	switch {
	case meta.HasTaskPart():
		review.Kind, review.Task = contract.PluginKindTask, taskplugin.Summary(meta, language)
	case len(meta.Relay) == 0 && meta.HasRequestPart():
		review.Kind = contract.PluginKindRequest
	default:
		review.Kind = contract.PluginKindProvider
	}
	review.Description, review.Author = meta.Description.In(language), meta.Author.Name
	for _, route := range meta.Relay {
		if plugin.RelayProtocol(route.IncomingPath) == "" {
			review.Warnings = append(review.Warnings, contract.PluginInstallWarning{Code: contract.PluginWarningRoute, Detail: route.IncomingPath})
		}
	}
	review.Icon = staged.icon
	review.Changelog = changelogIn(files.changelogs, language)
	review.Warnings = append(review.Warnings, staged.warnings...)
	return review
}

func (installer *Installer) language() string {
	if installer.options.Language != nil {
		return installer.options.Language()
	}
	return "en"
}

// changelogIn is the changelog in language, its primary language or
// English, without its front matter.
func changelogIn(changelogs map[string][]byte, language string) string {
	candidates := []string{"CHANGELOG." + language + ".md"}
	if primary, _, found := strings.Cut(language, "-"); found {
		candidates = append(candidates, "CHANGELOG."+primary+".md")
	}
	if strings.HasPrefix(language, "zh") {
		candidates = append(candidates, "CHANGELOG.zh-CN.md")
	}
	candidates = append(candidates, "CHANGELOG.md")
	for _, name := range candidates {
		if data, ok := changelogs[name]; ok {
			return clipNotes(stripFrontMatter(string(data)))
		}
	}
	return ""
}

// stripFrontMatter drops a Markdown file's YAML front matter.
func stripFrontMatter(text string) string {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return strings.TrimSpace(text)
	}
	lines := strings.Split(text, "\n")
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.TrimSpace(strings.Join(lines[index+1:], "\n"))
		}
	}
	return strings.TrimSpace(text)
}

// buildTask stages a task plugin a GitHub link names. A plugin of a
// repository that is a marketplace, whose index lists this very file,
// counts as installed from the marketplace, which its updates follow.
func (installer *Installer) buildTask(ctx context.Context, resolved Resolved, link, work, source, text string, existing func(string) *Existing) (contract.PluginInstallReview, contract.PluginInstallRecord, error) {
	extras := filepath.Join(work, extrasDir)
	icon, iconName := sidecarIcon(source, extras)
	files := taskFiles{icon: icon, iconName: iconName, changelogs: localChangelogs(source)}
	out := filepath.Join(work, "out")
	staged, err := installer.stageTask(ctx, text, files, out)
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	sum := sha256.Sum256([]byte(text))
	record := contract.PluginInstallRecord{Source: resolved.Source(link), Version: staged.checked.Task.Version, Outputs: staged.outputs}
	if index, err := os.ReadFile(filepath.Join(extras, "index.json")); err == nil {
		file := path.Join(resolved.Subdir, "plugin.js")
		if market, err := parseIndex(index); err == nil {
			if entry, version := market.find(staged.checked.Task.Key, staged.checked.Task.Version); version != nil && version.Path == file && strings.EqualFold(version.SHA256, hex.EncodeToString(sum[:])) {
				record.Source = contract.PluginInstallSource{
					Kind: contract.PluginSourceMarket, Repository: market.Name, URL: link, Index: repositoryIndex(resolved),
					Key: entry.Key, Ref: version.Version, RefKind: contract.PluginRefVersion, Commit: version.SHA256,
					CommittedAt: resolved.Source(link).CommittedAt, Subdir: version.Path,
				}
			}
		}
	}
	_ = os.RemoveAll(source)
	review := installer.taskReview(staged, len(text), record, files)
	if existing != nil {
		compare(&review, record, existing(staged.checked.Meta.Key))
	}
	return review, record, nil
}

// repositoryIndex is the address of a repository's index.json at the ref
// a link named: the official marketplace by its own address.
func repositoryIndex(resolved Resolved) string {
	ref := resolved.Ref
	if ref == "" {
		ref = resolved.Commit
	}
	if strings.EqualFold(resolved.Owner+"/"+resolved.Repo, "QuantumNous/new-api-plugins") && ref == "main" {
		return contract.GitHubPluginMarket
	}
	return "https://raw.githubusercontent.com/" + resolved.Owner + "/" + resolved.Repo + "/" + ref + "/index.json"
}
