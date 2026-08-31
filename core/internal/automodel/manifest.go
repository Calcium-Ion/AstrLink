package automodel

import (
	"fmt"
	"path"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autotaxonomy"
)

type installationManifest struct {
	Version           uint32             `json:"version"`
	InstallationID    string             `json:"installation_id"`
	Identity          string             `json:"identity"`
	TaxonomyID        string             `json:"taxonomy_id"`
	TaxonomySHA256    string             `json:"taxonomy_sha256"`
	Preprocessing     preprocessing      `json:"preprocessing"`
	ArtifactTier      string             `json:"artifact_tier"`
	ReleaseMode       string             `json:"release_mode,omitempty"`
	ModelPath         string             `json:"model_path"`
	TokenizerPath     string             `json:"tokenizer_path"`
	ConfigPath        string             `json:"config_path"`
	MaxSequenceTokens int                `json:"max_sequence_tokens"`
	ContentBudget     int                `json:"content_budget"`
	HeadTokens        int                `json:"head_tokens"`
	TailTokens        int                `json:"tail_tokens"`
	PadTokenID        int                `json:"pad_token_id"`
	PadMultiple       int                `json:"pad_multiple"`
	AddSpecialTokens  bool               `json:"add_special_tokens"`
	InputNames        inputNames         `json:"input_names"`
	OutputName        string             `json:"output_name"`
	ID2Label          map[string]string  `json:"id2label"`
	Files             []installationFile `json:"files"`
}

type preprocessing struct {
	Text   string `json:"text"`
	Tokens string `json:"tokens"`
}

type inputNames struct {
	InputIDs      string `json:"input_ids"`
	AttentionMask string `json:"attention_mask"`
}

type installationFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func (manifest installationManifest) validate() error {
	if manifest.Version != 1 ||
		contract.AutoClassifierID(manifest.InstallationID).Validate() != nil ||
		manifest.Identity == "" ||
		manifest.TaxonomyID != autotaxonomy.ID ||
		manifest.TaxonomySHA256 != autotaxonomy.SHA256 ||
		manifest.Preprocessing.Text != autotaxonomy.TextPreprocessing ||
		manifest.Preprocessing.Tokens != autotaxonomy.TokenPreprocessing ||
		manifest.MaxSequenceTokens != 512 ||
		manifest.ContentBudget != 510 ||
		manifest.HeadTokens != 255 ||
		manifest.TailTokens != 255 ||
		manifest.PadTokenID != 0 ||
		manifest.PadMultiple != 8 ||
		manifest.AddSpecialTokens ||
		manifest.OutputName != "logits" ||
		manifest.InputNames.InputIDs == "" ||
		manifest.InputNames.AttentionMask == "" ||
		len(manifest.Files) == 0 ||
		len(manifest.Files) > 128 {
		return fmt.Errorf("invalid classifier manifest")
	}
	if !contract.AutoClassifierArtifactTier(manifest.ArtifactTier).Valid() {
		return fmt.Errorf("invalid classifier artifact tier")
	}
	if err := autotaxonomy.EqualID2Label(manifest.ID2Label); err != nil {
		return err
	}
	if !safeAssetPath(manifest.ModelPath) ||
		!safeAssetPath(manifest.TokenizerPath) ||
		!safeAssetPath(manifest.ConfigPath) {
		return fmt.Errorf("invalid classifier asset path")
	}
	for _, file := range manifest.Files {
		if !safeAssetPath(file.Path) || file.Size <= 0 || len(file.SHA256) != 64 {
			return fmt.Errorf("invalid classifier file declaration")
		}
	}
	return nil
}

func safeAssetPath(candidate string) bool {
	if candidate == "" || strings.Contains(candidate, "\\") || path.IsAbs(candidate) {
		return false
	}
	cleaned := path.Clean(candidate)
	if cleaned != candidate || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return false
	}
	for _, segment := range strings.Split(candidate, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsRune(segment, 0) {
			return false
		}
	}
	return true
}
