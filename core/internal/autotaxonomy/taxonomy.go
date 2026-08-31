// Package autotaxonomy freezes the astrlink-text-v1 classifier labels.
//
// Route contracts only check category_id shape. The four labels and their
// index mapping live here so probe, install, and the worker cannot drift.
package autotaxonomy

import (
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
)

const (
	ID     = contract.AstrLinkTextClassificationV1
	SHA256 = "83990d9d763df0feac9b06aef36ef14417a662494f53004e68c3f18248fd5a2f"

	TextPreprocessing  = "current-user-text-v3"
	TokenPreprocessing = "tokenize_head_tail-v1"
)

var Labels = []string{"general", "research", "coding", "architect"}

func LabelAt(index int) (string, error) {
	if index < 0 || index >= len(Labels) {
		return "", fmt.Errorf("taxonomy index %d is out of range", index)
	}
	return Labels[index], nil
}

func IndexOf(label string) (int, error) {
	for index, candidate := range Labels {
		if candidate == label {
			return index, nil
		}
	}
	return -1, fmt.Errorf("unknown taxonomy label %q", label)
}

func EqualID2Label(id2label map[string]string) error {
	if len(id2label) != len(Labels) {
		return fmt.Errorf("id2label must contain exactly %d labels", len(Labels))
	}
	for index, expected := range Labels {
		actual, exists := id2label[fmt.Sprintf("%d", index)]
		if !exists || actual != expected {
			return fmt.Errorf("id2label[%d] must be %q", index, expected)
		}
	}
	return nil
}
