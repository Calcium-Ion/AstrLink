package privacyworker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

const (
	toolFieldPathInputContract  = "tool-field-path-v1"
	maxInputContractConfigBytes = 4 << 20
	maxContextPrefixBytes       = 4096
)

// installedInputContract is called only after the installation was verified.
// Recheck the small config bytes against the bound manifest before using its
// metadata. The resulting contract is cached with that installation identity
// and attached to its worker process, never a global detector switch.
func installedInputContract(installation InstalledModel) (string, error) {
	manifestBytes, err := readBoundedModelFile(filepath.Join(installation.Directory, installationManifestName), maxInstallationManifestBytes)
	if err != nil || !manifestMatchesBinding(manifestBytes, installation.ManifestSHA256) {
		return "", privacy.ErrDetectorUnavailable
	}
	var manifest installationManifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || !safeModelPath(manifest.ConfigPath) {
		return "", privacy.ErrDetectorUnavailable
	}
	config, err := readBoundedModelFile(filepath.Join(installation.Directory, filepath.FromSlash(manifest.ConfigPath)), maxInputContractConfigBytes)
	if err != nil {
		return "", privacy.ErrDetectorUnavailable
	}
	digest := sha256.Sum256(config)
	matched := false
	for _, file := range manifest.Files {
		if file.Path == manifest.ConfigPath && file.Size == int64(len(config)) && file.SHA256 == hex.EncodeToString(digest[:]) {
			matched = true
			break
		}
	}
	if !matched {
		return "", privacy.ErrDetectorUnavailable
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(config, &metadata) != nil || metadata == nil {
		return "", privacy.ErrDetectorUnavailable
	}
	encodedContract, declared := metadata["astrlink_guard_input_contract"]
	if !declared {
		return "", nil
	}
	var inputContract string
	if string(encodedContract) == "null" || json.Unmarshal(encodedContract, &inputContract) != nil {
		return "", privacy.ErrDetectorUnavailable
	}
	if inputContract == "" {
		return "", nil
	}
	if inputContract != toolFieldPathInputContract || manifest.Adapter != contract.PrivacyModelAdapterHFToken {
		return "", privacy.ErrDetectorUnavailable
	}
	return inputContract, nil
}

func readBoundedModelFile(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxBytes {
		return nil, privacy.ErrDetectorUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, privacy.ErrDetectorUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, privacy.ErrDetectorUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes || int64(len(data)) != info.Size() {
		return nil, privacy.ErrDetectorUnavailable
	}
	return data, nil
}

func contextualWorkerRequest(id uint64, segments []privacy.Segment, inputContract string) (workerRequest, []privacy.Segment, []int, error) {
	if inputContract != "" && inputContract != toolFieldPathInputContract {
		return workerRequest{}, nil, nil, privacy.ErrDetectorUnavailable
	}
	request := workerRequest{Version: protocolVersion, ID: id, Texts: make([]workerText, len(segments))}
	modelSegments := make([]privacy.Segment, len(segments))
	prefixLengths := make([]int, len(segments))
	totalBytes := 0
	for index, segment := range segments {
		prefix := ""
		if inputContract == toolFieldPathInputContract {
			prefix = segment.ContextPrefix
		}
		if len(prefix) > maxContextPrefixBytes || !utf8.ValidString(prefix) || !utf8.ValidString(segment.Value) {
			return workerRequest{}, nil, nil, privacy.ErrDetectorLimit
		}
		// Count before allocating combined strings. JSON framing may consume
		// more bytes; Detect checks the actual encoded frame as well.
		if len(prefix) > maxFrameBytes-totalBytes || len(segment.Value) > maxFrameBytes-totalBytes-len(prefix) {
			return workerRequest{}, nil, nil, privacy.ErrDetectorLimit
		}
		totalBytes += len(prefix) + len(segment.Value)
		value := prefix + segment.Value
		request.Texts[index] = workerText{ID: uint32(index), Text: value}
		modelSegments[index] = privacy.Segment{Value: value}
		prefixLengths[index] = len(prefix)
	}
	return request, modelSegments, prefixLengths, nil
}

// responseFindings validates UTF-8 boundaries and limits against exactly the
// text sent to the model before this projection runs. Never clip or silently
// discard a prediction spanning the context/value boundary: it may include a
// real secret. An unprojectable value prediction must fail closed.
func projectContextFindings(findings []privacy.Finding, prefixLengths []int) ([]privacy.Finding, error) {
	projected := make([]privacy.Finding, 0, len(findings))
	for _, finding := range findings {
		prefix := prefixLengths[finding.Segment]
		if finding.End <= prefix {
			continue
		}
		if finding.Start < prefix {
			return nil, privacy.ErrDetectorUnavailable
		}
		finding.Start -= prefix
		finding.End -= prefix
		projected = append(projected, finding)
	}
	return projected, nil
}
