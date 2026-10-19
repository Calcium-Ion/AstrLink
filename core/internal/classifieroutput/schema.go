package classifieroutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ErrInvalidSchema marks output that decoded as strict JSON but does not
// satisfy the task-type schema: unknown keys, missing label, labels outside
// the calling catalog, or non-finite scores.
var ErrInvalidSchema = errors.New("classifier output fails the task-type schema")

// Candidate is one ranked label from the classifier output.
type Candidate struct {
	Label string
	Score float64
}

// TaskType is the validated classification answer: the adopted top label,
// its upstream-reported score, and the full ranked candidate sequence after
// normalization.
type TaskType struct {
	Label      string
	Score      float64
	Candidates []Candidate
}

// ParseTaskType runs layers 2 and 3 over one assistant text. labels is the
// immutable catalog snapshot of the group being routed; anything outside it
// is rejected, never coerced. A missing score stays zero rather than being
// invented; a missing candidates list stays empty. Candidates are sorted by
// score descending with catalog order breaking ties, deduplicated by label,
// and capped at the catalog size.
func ParseTaskType(text string, labels []string) (TaskType, error) {
	catalog := make(map[string]int, len(labels))
	for index, label := range labels {
		catalog[label] = index
	}
	object, err := ParseObject(text)
	if err != nil {
		return TaskType{}, err
	}
	for key := range object {
		switch key {
		case "label", "score", "candidates":
		default:
			return TaskType{}, fmt.Errorf("%w: unknown key %q", ErrInvalidSchema, key)
		}
	}
	var result TaskType
	if raw, ok := object["label"]; ok {
		if err := json.Unmarshal(raw, &result.Label); err != nil {
			return TaskType{}, fmt.Errorf("%w: label must be a string", ErrInvalidSchema)
		}
		if _, ok := catalog[result.Label]; !ok {
			return TaskType{}, fmt.Errorf("%w: label %q is outside the catalog", ErrInvalidSchema, result.Label)
		}
	}
	if raw, ok := object["score"]; ok {
		if err := json.Unmarshal(raw, &result.Score); err != nil {
			return TaskType{}, fmt.Errorf("%w: score must be a number", ErrInvalidSchema)
		}
		if !validScore(result.Score) {
			return TaskType{}, fmt.Errorf("%w: score must be finite and within [0,1]", ErrInvalidSchema)
		}
	}
	if raw, ok := object["candidates"]; ok {
		var entries []struct {
			Label string  `json:"label"`
			Score float64 `json:"score"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return TaskType{}, fmt.Errorf("%w: candidates must be an array of objects", ErrInvalidSchema)
		}
		if len(entries) > MaxCandidates {
			return TaskType{}, fmt.Errorf("%w: candidates exceed %d entries", ErrInvalidSchema, MaxCandidates)
		}
		for _, entry := range entries {
			if _, ok := catalog[entry.Label]; !ok {
				return TaskType{}, fmt.Errorf("%w: candidate %q is outside the catalog", ErrInvalidSchema, entry.Label)
			}
			if !validScore(entry.Score) {
				return TaskType{}, fmt.Errorf("%w: candidate %q score must be finite and within [0,1]", ErrInvalidSchema, entry.Label)
			}
			result.Candidates = append(result.Candidates, Candidate{Label: entry.Label, Score: entry.Score})
		}
	}
	if result.Label == "" && len(result.Candidates) == 0 {
		return TaskType{}, fmt.Errorf("%w: neither label nor candidates present", ErrInvalidSchema)
	}
	result.Candidates = normalizeCandidates(result.Candidates, catalog, len(labels))
	if result.Label == "" {
		// A candidates-only answer adopts its normalized top entry.
		result.Label = result.Candidates[0].Label
		result.Score = result.Candidates[0].Score
	}
	return result, nil
}

func validScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

// DimensionLabel is one dimension's parsed answer: the label the provider
// chose, its reported confidence, and the ranked candidate sequence it sent.
// ParseDimensions returns one of these per addressed dimension, keyed by
// dimension name.
//
// Candidates matters for task_type and only for task_type: the evaluator's
// scored-fallback substitution reads the task_type element's sequence, so a
// multi-dimension reply that dropped it would silently disable substitution
// for every group that added one custom dimension. Each dimension carries its
// own sequence regardless, because that is the shape ParseTaskType validates
// per dimension.
type DimensionLabel struct {
	Label      string
	Score      float64
	Candidates []Candidate
}

// DimensionKeys returns the JSON keys one classification reply is expected to
// carry, in the order the dimensions were declared. A single-dimension
// configuration (no custom dimensions) yields exactly {"task_type"}, which is
// the shape every existing reply and every existing test already uses — the
// multi-dimension branch is never taken, so behaviour there is unchanged.
//
// task_type is always first: it is built in and always addressed, so the
// emitted schema keeps a stable opening key regardless of how many custom
// dimensions follow.
func DimensionKeys(dimensions []contract.ClassifierDimension) []string {
	keys := make([]string, 0, len(dimensions)+1)
	keys = append(keys, contract.BuiltInTaskTypeDimension)
	for _, dimension := range dimensions {
		if dimension.Name == contract.BuiltInTaskTypeDimension {
			// Refused by ClassifierSettings.Validate on the save path; skipping
			// it here keeps the key list unique for a document that reached the
			// request path anyway.
			continue
		}
		keys = append(keys, dimension.Name)
	}
	return keys
}

// ParseDimensions runs layers 2 and 3 over one multi-dimension reply. Each
// addressed dimension carries the same {label, score, candidates} object
// task_type does, so every dimension reuses ParseTaskType's validation
// verbatim rather than re-implementing it.
//
// A dimension the provider omitted is simply absent from the result: that is
// the "not produced" case the fact builder turns into an unknown dimension.
// A dimension the provider answered badly — label outside its own option list,
// non-finite score, unknown inner key — fails the whole reply with
// ErrInvalidSchema, matching task_type's existing contract: the reply is one
// document and a malformed member makes it unusable rather than partially
// usable.
//
// labels is the effective task_type label domain for this call; dimensions
// carries the custom dimensions only (never task_type).
func ParseDimensions(text string, labels []string, dimensions []contract.ClassifierDimension) (map[string]DimensionLabel, error) {
	object, err := ParseObject(text)
	if err != nil {
		return nil, err
	}
	keys := DimensionKeys(dimensions)
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	// Options per dimension, in declared order, used both to reject a label
	// outside the dimension's own list and to break candidate ties. Order has
	// to be the declared one and not map iteration order: ParseTaskType builds
	// its tie-breaking catalog from this slice, so an unordered list would make
	// equal-scored candidates sort differently from run to run.
	optionLists := make(map[string][]string, len(keys))
	optionSets := make(map[string]map[string]struct{}, len(keys))
	optionLists[contract.BuiltInTaskTypeDimension] = labels
	optionSets[contract.BuiltInTaskTypeDimension] = labelSet(labels)
	for _, dimension := range dimensions {
		if dimension.Name == contract.BuiltInTaskTypeDimension {
			continue
		}
		names := make([]string, 0, len(dimension.Options))
		for _, option := range dimension.Options {
			names = append(names, option.Name)
		}
		optionLists[dimension.Name] = names
		optionSets[dimension.Name] = labelSet(names)
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("%w: unknown key %q", ErrInvalidSchema, key)
		}
	}
	result := make(map[string]DimensionLabel, len(keys))
	for _, key := range keys {
		raw, ok := object[key]
		if !ok {
			continue
		}
		parsed, err := ParseTaskType(string(raw), optionLists[key])
		if err != nil {
			return nil, fmt.Errorf("dimension %q: %w", key, err)
		}
		result[key] = DimensionLabel{Label: parsed.Label, Score: parsed.Score, Candidates: parsed.Candidates}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: no addressed dimension present", ErrInvalidSchema)
	}
	return result, nil
}

func labelSet(labels []string) map[string]struct{} {
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[label] = struct{}{}
	}
	return set
}

// normalizeCandidates sorts by score descending with catalog order breaking
// ties, drops duplicate labels keeping the highest score, and caps the list
// at the catalog size.
func normalizeCandidates(candidates []Candidate, catalog map[string]int, size int) []Candidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return catalog[candidates[i].Label] < catalog[candidates[j].Label]
	})
	normalized := make([]Candidate, 0, min(len(candidates), size))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.Label]; ok {
			continue
		}
		seen[candidate.Label] = struct{}{}
		normalized = append(normalized, candidate)
		if len(normalized) == size {
			break
		}
	}
	return normalized
}
