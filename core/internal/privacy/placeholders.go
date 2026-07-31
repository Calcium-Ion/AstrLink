package privacy

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
)

const (
	placeholderRandomBytes      = 8
	placeholderAllocationTrials = 8
)

// placeholderAllocator creates opaque, request-scoped placeholders. It is
// instantiated for each inspection so suffixes are never shared across
// upstream attempts.
type placeholderAllocator struct {
	entropy io.Reader
	used    map[string]struct{}
}

func newPlaceholderAllocator(entropy io.Reader) *placeholderAllocator {
	if entropy == nil {
		entropy = rand.Reader
	}
	return &placeholderAllocator{
		entropy: entropy,
		used:    make(map[string]struct{}),
	}
}

func (allocator *placeholderAllocator) allocate(kind Kind) (string, error) {
	if allocator == nil || allocator.entropy == nil {
		return "", ErrUnsafeRewrite
	}
	for range placeholderAllocationTrials {
		var random [placeholderRandomBytes]byte
		if _, err := io.ReadFull(allocator.entropy, random[:]); err != nil {
			return "", ErrUnsafeRewrite
		}
		placeholder := withRandomPlaceholderSuffix(
			replacementFor(kind),
			hex.EncodeToString(random[:]),
		)
		if _, exists := allocator.used[placeholder]; exists {
			continue
		}
		allocator.used[placeholder] = struct{}{}
		return placeholder, nil
	}
	return "", ErrUnsafeRewrite
}

func withRandomPlaceholderSuffix(base, suffix string) string {
	if strings.HasPrefix(base, "<") && strings.HasSuffix(base, ">") {
		return base[:len(base)-1] + "_" + suffix + ">"
	}
	return base + "_" + suffix
}
