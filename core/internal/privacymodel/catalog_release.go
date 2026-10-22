package privacymodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	catalogReleaseTTL       = time.Hour
	maxCatalogReleaseTags   = 8
	maxReleaseChecksumBytes = 64 << 10
	releaseChecksumsName    = "CHECKSUMS.json"
)

var (
	releaseTagPattern  = regexp.MustCompile(`^v(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
	errReleaseMismatch = errors.New("privacy model release does not match")
	// errReleaseFilesDisagree marks release files whose checksums, Hub blob
	// metadata and contents disagree, unlike a Hub that cannot be reached.
	// It is still ErrRemoteMetadata to every caller.
	errReleaseFilesDisagree = fmt.Errorf("%w: release files disagree", ErrRemoteMetadata)
)

type catalogRelease struct {
	item  contract.PrivacyModelCatalogItem
	plans map[string]variantPlan
}

// catalogReleaseCache keeps update checks from refetching every tag and lets
// installs reuse the checksums resolved for the release a client just saw.
type catalogReleaseCache struct {
	mu         sync.Mutex
	latest     *catalogRelease
	checkedAt  time.Time
	byRevision map[string]catalogRelease
}

type releaseVersion [3]int

type hfRefs struct {
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
}

type releaseChecksum struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// CatalogReleases reports the newest compatible release of each catalog model
// published through versioned tags, so clients can offer an update. Refresh
// skips the hour-long cache for an operator who asked to check now.
func (registry *Registry) CatalogReleases(
	ctx context.Context,
	refresh bool,
) (contract.PrivacyModelCatalogResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := registry.latestAstrLinkGuardRelease(ctx, refresh)
	if err != nil {
		return contract.PrivacyModelCatalogResponse{}, err
	}
	return contract.PrivacyModelCatalogResponse{
		Items: []contract.PrivacyModelCatalogItem{copyCatalogItem(release.item)},
	}, nil
}

func (registry *Registry) probeAstrLinkGuard(
	ctx context.Context,
	request contract.PrivacyModelProbeRequest,
) (contract.PrivacyModelProbeResponse, error) {
	if err := contract.ValidateRequestedPrivacyModelRevision(request.Revision); err != nil {
		return contract.PrivacyModelProbeResponse{}, ErrInvalidConfig
	}
	release, err := registry.astrLinkGuardRelease(ctx, request.Revision)
	if err != nil {
		return contract.PrivacyModelProbeResponse{}, err
	}
	labels := make([]contract.PrivacyModelLabel, 0, len(astrLinkGuardKinds))
	for _, kind := range astrLinkGuardKinds {
		suggested := kind
		labels = append(labels, contract.PrivacyModelLabel{
			Label: string(kind), SuggestedKind: &suggested,
		})
	}
	sort.Slice(labels, func(left, right int) bool {
		return labels[left].Label < labels[right].Label
	})
	item := copyCatalogItem(release.item)
	response := contract.PrivacyModelProbeResponse{
		RepoID: item.RepoID, RequestedRevision: request.Revision,
		Revision: item.Revision, Name: item.Name, License: &item.License,
		Languages: item.Languages, Adapter: item.Adapter,
		Variants: item.Variants, Labels: labels,
	}
	if contract.ValidatePrivacyModelProbeResponse(response) != nil {
		return contract.PrivacyModelProbeResponse{}, ErrRemoteMetadata
	}
	return response, nil
}

// astrLinkGuardRelease resolves the pinned revision, a release tag or commit,
// or "main" (the newest compatible release) into download plans.
func (registry *Registry) astrLinkGuardRelease(
	ctx context.Context,
	revision string,
) (catalogRelease, error) {
	pinned := pinnedAstrLinkGuardRelease()
	switch {
	case revision == pinned.item.Revision || revision == "v"+astrLinkGuardVersion:
		return pinned, nil
	case revision == "main":
		return registry.latestAstrLinkGuardRelease(ctx, false)
	case !releaseTagPattern.MatchString(revision) &&
		contract.ValidatePrivacyModelRevision(revision) != nil:
		return catalogRelease{}, ErrUnsupportedModel
	}
	registry.releases.mu.Lock()
	cached, exists := registry.releases.byRevision[revision]
	registry.releases.mu.Unlock()
	if exists {
		return cached, nil
	}
	release, err := registry.resolveAstrLinkGuardRelease(ctx, revision)
	if err != nil {
		return catalogRelease{}, err
	}
	registry.releases.mu.Lock()
	registry.releases.remember(release)
	registry.releases.mu.Unlock()
	return release, nil
}

func (registry *Registry) latestAstrLinkGuardRelease(
	ctx context.Context,
	refresh bool,
) (catalogRelease, error) {
	cache := &registry.releases
	cache.mu.Lock()
	if !refresh && cache.latest != nil && time.Since(cache.checkedAt) < catalogReleaseTTL {
		latest := *cache.latest
		cache.mu.Unlock()
		return latest, nil
	}
	cache.mu.Unlock()
	release, err := registry.resolveAstrLinkGuardRelease(ctx, "")
	if err != nil {
		return catalogRelease{}, err
	}
	cache.mu.Lock()
	cache.latest = &release
	cache.checkedAt = time.Now()
	cache.remember(release)
	cache.mu.Unlock()
	return release, nil
}

func (cache *catalogReleaseCache) remember(release catalogRelease) {
	if release.item.Revision == astrLinkGuardRevision {
		return
	}
	if cache.byRevision == nil {
		cache.byRevision = make(map[string]catalogRelease)
	}
	cache.byRevision[release.item.Revision] = release
}

// resolveAstrLinkGuardRelease scans release tags newer than the pinned one,
// newest first. An empty wanted revision selects the newest compatible
// release; otherwise only a tag named or pointing at wanted is accepted.
func (registry *Registry) resolveAstrLinkGuardRelease(
	ctx context.Context,
	wanted string,
) (catalogRelease, error) {
	pinned := pinnedAstrLinkGuardRelease()
	tags, err := registry.astrLinkGuardTags(ctx)
	if err != nil {
		return catalogRelease{}, err
	}
	wantedTag := releaseTagPattern.MatchString(wanted)
	for _, tag := range tags {
		if wantedTag && tag != wanted {
			continue
		}
		wantedSHA := ""
		if !wantedTag {
			wantedSHA = wanted
		}
		release, err := registry.fetchAstrLinkGuardRelease(ctx, tag, wantedSHA)
		switch {
		case err == nil:
			return release, nil
		case errors.Is(err, errReleaseMismatch),
			wanted == "" && errors.Is(err, ErrUnsupportedModel):
			continue
		default:
			return catalogRelease{}, err
		}
	}
	if wanted == "" {
		return pinned, nil
	}
	return catalogRelease{}, ErrUnsupportedModel
}

func (registry *Registry) astrLinkGuardTags(ctx context.Context) ([]string, error) {
	probe := registry.probe
	document, err := probe.getJSON(ctx, probe.buildEndpoint(
		endpointPart{value: "api"},
		endpointPart{value: "models"},
		endpointPart{value: astrLinkGuardRepoID, slashSeparated: true},
		endpointPart{value: "refs"},
	), maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	var refs hfRefs
	if json.Unmarshal(document, &refs) != nil {
		return nil, ErrRemoteMetadata
	}
	current, _ := parseReleaseVersion("v" + astrLinkGuardVersion)
	type candidate struct {
		name    string
		version releaseVersion
	}
	candidates := make([]candidate, 0, len(refs.Tags))
	for _, tag := range refs.Tags {
		if version, ok := parseReleaseVersion(tag.Name); ok && version.after(current) {
			candidates = append(candidates, candidate{name: tag.Name, version: version})
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].version.after(candidates[right].version)
	})
	if len(candidates) > maxCatalogReleaseTags {
		candidates = candidates[:maxCatalogReleaseTags]
	}
	names := make([]string, len(candidates))
	for index, candidate := range candidates {
		names[index] = candidate.name
	}
	return names, nil
}

// fetchAstrLinkGuardRelease pins every file from the per-folder checksums,
// cross-checked against the Hub's blob metadata, and rejects releases whose
// labels or decoding contracts this build cannot run.
//
// The int8/ and fp32/ folders are required. The fp16/ folder is optional:
// releases before 0.1.3 have none, and one that fails any check (checksums,
// Hub metadata, config) drops only the accelerated variants, so a broken
// FP16 upload never takes the CPU variants down with it. A Hub that cannot
// be reached still fails the whole check, which is retried later, instead
// of caching a release without its FP16 build.
func (registry *Registry) fetchAstrLinkGuardRelease(
	ctx context.Context,
	tag string,
	wantedSHA string,
) (catalogRelease, error) {
	probe := registry.probe
	document, err := probe.getJSON(
		ctx,
		probe.metadataEndpoint(astrLinkGuardRepoID, tag)+"?blobs=true",
		maxMetadataBytes,
	)
	if err != nil {
		return catalogRelease{}, err
	}
	var metadata hfModelMetadata
	if json.Unmarshal(document, &metadata) != nil ||
		contract.ValidatePrivacyModelRevision(metadata.SHA) != nil {
		return catalogRelease{}, ErrRemoteMetadata
	}
	repository := metadata.ModelID
	if repository == "" {
		repository = metadata.ID
	}
	if repository != astrLinkGuardRepoID {
		return catalogRelease{}, ErrRemoteMetadata
	}
	if wantedSHA != "" && metadata.SHA != wantedSHA {
		return catalogRelease{}, errReleaseMismatch
	}
	licenseDocument, err := registry.fetchReleaseFile(
		ctx, &metadata, astrLinkGuardLicense, maxConfigBytes,
	)
	if err != nil {
		return catalogRelease{}, err
	}
	license := Asset{
		Path: astrLinkGuardLicense, Size: int64(len(licenseDocument)),
		SHA256: sha256Hex(licenseDocument),
	}
	item := astrLinkGuardEntry(metadata.SHA, tag[1:], true)
	offered := offeredAstrLinkGuardVariants(item.Variants)
	item.Variants = make([]contract.PrivacyModelVariant, 0, len(offered))
	folders := make(map[string]releaseFolder, 3)
	plans := make(map[string]variantPlan, len(offered))
	for _, variant := range offered {
		name, _, _ := astrLinkGuardLayout(variant.ID)
		folder, fetched := folders[name]
		if !fetched {
			folder = registry.fetchAstrLinkGuardFolder(ctx, &metadata, variant.ID)
			folders[name] = folder
		}
		if folder.err != nil {
			if name == astrLinkGuardFP16Folder &&
				(errors.Is(folder.err, ErrUnsupportedModel) ||
					errors.Is(folder.err, errReleaseFilesDisagree)) {
				continue
			}
			return catalogRelease{}, folder.err
		}
		assets := append(copyAssets(folder.assets), license)
		var total int64
		for _, asset := range assets {
			if total > contract.MaxPrivacyModelByteCount-asset.Size {
				return catalogRelease{}, ErrUnsupportedModel
			}
			total += asset.Size
		}
		variant.BytesTotal = total
		item.Variants = append(item.Variants, variant)
		if !variant.Supported {
			continue
		}
		runtime := astrLinkGuardRuntime(variant.ID)
		if runtime.execution != nil {
			runtime.execution.PadTokenID = folder.padTokenID
		}
		plans[variant.ID] = variantPlan{
			variant: variant, assets: assets, runtime: runtime,
		}
	}
	for id, plan := range plans {
		plan.item = item
		plans[id] = plan
	}
	return catalogRelease{item: item, plans: plans}, nil
}

// releaseFolder is one checked variant folder of a release.
type releaseFolder struct {
	assets []Asset
	// padTokenID is read from the tokenizer for the FP16 build.
	padTokenID int
	err        error
}

func (registry *Registry) fetchAstrLinkGuardFolder(
	ctx context.Context,
	metadata *hfModelMetadata,
	variant string,
) releaseFolder {
	assets, checksums, err := registry.fetchAstrLinkGuardAssets(ctx, metadata, variant)
	if err != nil {
		return releaseFolder{err: err}
	}
	folder := releaseFolder{assets: assets}
	if _, accelerated := astrLinkGuardAccelerated(variant); accelerated {
		folder.padTokenID, folder.err = registry.fetchAstrLinkGuardPadTokenID(
			ctx, metadata, checksums["tokenizer.json"],
		)
	}
	return folder
}

// fetchAstrLinkGuardPadTokenID knows the pad id of the tokenizer every
// release so far ships, and reads any other tokenizer from the release.
func (registry *Registry) fetchAstrLinkGuardPadTokenID(
	ctx context.Context,
	metadata *hfModelMetadata,
	checksum releaseChecksum,
) (int, error) {
	if checksum.SHA256 == astrLinkGuardTokenizerSHA256 {
		return astrLinkGuardPadTokenID, nil
	}
	if checksum.Size > maxProbeJSONAssetBytes {
		return 0, ErrUnsupportedModel
	}
	document, err := registry.fetchReleaseFile(
		ctx, metadata, astrLinkGuardFP16Folder+"/tokenizer.json", checksum.Size,
	)
	if err != nil {
		return 0, err
	}
	if sha256Hex(document) != checksum.SHA256 {
		return 0, errReleaseFilesDisagree
	}
	padTokenID, ok := tokenizerPadTokenID(document)
	if !ok {
		return 0, ErrUnsupportedModel
	}
	return padTokenID, nil
}

func (registry *Registry) fetchAstrLinkGuardAssets(
	ctx context.Context,
	metadata *hfModelMetadata,
	variant string,
) ([]Asset, map[string]releaseChecksum, error) {
	folder, _, ok := astrLinkGuardLayout(variant)
	if !ok {
		return nil, nil, ErrUnsupportedModel
	}
	document, err := registry.fetchReleaseFile(
		ctx, metadata, folder+"/"+releaseChecksumsName, maxReleaseChecksumBytes,
	)
	if err != nil {
		return nil, nil, err
	}
	var checksums map[string]releaseChecksum
	if json.Unmarshal(document, &checksums) != nil {
		return nil, nil, ErrUnsupportedModel
	}
	files := astrLinkGuardFolderFiles(variant)
	assets := make([]Asset, 0, len(files)+1)
	for _, name := range files {
		checksum, exists := checksums[name]
		asset := Asset{
			Path: folder + "/" + name, Size: checksum.Size,
			SHA256: checksum.SHA256,
		}
		if !exists || !validPinnedAsset(asset) {
			return nil, nil, ErrUnsupportedModel
		}
		if !siblingMatchesAsset(metadata.Siblings, asset) {
			return nil, nil, errReleaseFilesDisagree
		}
		assets = append(assets, asset)
	}
	config, err := registry.fetchReleaseFile(
		ctx, metadata, folder+"/config.json", maxConfigBytes,
	)
	if err != nil {
		return nil, nil, err
	}
	if sha256Hex(config) != checksums["config.json"].SHA256 {
		return nil, nil, errReleaseFilesDisagree
	}
	if err := validateAstrLinkGuardConfig(config); err != nil {
		return nil, nil, err
	}
	return assets, checksums, nil
}

func (registry *Registry) fetchReleaseFile(
	ctx context.Context,
	metadata *hfModelMetadata,
	filename string,
	limit int64,
) ([]byte, error) {
	if !siblingExists(metadata.Siblings, filename) {
		return nil, ErrUnsupportedModel
	}
	document, err := registry.probe.getJSON(
		ctx,
		registry.probe.endpoint(astrLinkGuardRepoID, "resolve", metadata.SHA, filename),
		limit,
	)
	if err != nil {
		return nil, err
	}
	if !pinSibling(metadata, filename, document) {
		return nil, errReleaseFilesDisagree
	}
	return document, nil
}

func siblingMatchesAsset(siblings []hfSibling, asset Asset) bool {
	for _, sibling := range siblings {
		if sibling.Filename != asset.Path {
			continue
		}
		size := sibling.Size
		if sibling.LFS != nil {
			if sibling.LFS.Size > 0 {
				size = sibling.LFS.Size
			}
			if sibling.LFS.SHA256 != "" && sibling.LFS.SHA256 != asset.SHA256 {
				return false
			}
		}
		return size == asset.Size
	}
	return false
}

func pinnedAstrLinkGuardRelease() catalogRelease {
	item := astrLinkGuardCatalogEntry()
	plans := make(map[string]variantPlan, len(item.Variants))
	for _, variant := range item.Variants {
		if !variant.Supported {
			continue
		}
		if plan, exists := builtinVariantPlan(item.RepoID, item.Revision, variant.ID); exists {
			plan.item = copyCatalogItem(item)
			plans[variant.ID] = plan
		}
	}
	return catalogRelease{item: item, plans: plans}
}

// astrLinkGuardReleaseTemplate describes any other Guard release offline:
// releases keep the pinned runtime and file layout, while sizes and hashes
// vary. It knows the FP16 variants even while the pin has none, because a
// newer release can offer them.
func astrLinkGuardReleaseTemplate(
	repoID, revision, variantID string,
) (variantPlan, bool) {
	if repoID != astrLinkGuardRepoID || revision == astrLinkGuardRevision ||
		contract.ValidatePrivacyModelRevision(revision) != nil {
		return variantPlan{}, false
	}
	item := astrLinkGuardEntry(astrLinkGuardRevision, astrLinkGuardVersion, true)
	for _, variant := range item.Variants {
		if variant.ID == variantID {
			return variantPlan{
				item: item, variant: variant,
				assets:  astrLinkGuardAssets(variantID),
				runtime: astrLinkGuardRuntime(variantID),
			}, true
		}
	}
	return variantPlan{}, false
}

func parseReleaseVersion(tag string) (releaseVersion, bool) {
	match := releaseTagPattern.FindStringSubmatch(tag)
	if match == nil {
		return releaseVersion{}, false
	}
	var version releaseVersion
	for index := range version {
		version[index], _ = strconv.Atoi(match[index+1])
	}
	return version, true
}

func (version releaseVersion) after(other releaseVersion) bool {
	for index := range version {
		if version[index] != other[index] {
			return version[index] > other[index]
		}
	}
	return false
}
