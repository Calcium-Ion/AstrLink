package updates

import (
	"strconv"
	"strings"
)

// semVersion is a version as Semantic Versioning 2.0.0 spells it: the
// numbers, the pre-release identifiers, and build metadata, which ordering
// ignores.
type semVersion struct {
	major, minor, patch uint64
	pre                 []string
}

// parseVersion reads a strict semantic version, such as 1.2.3-beta.1+build.
func parseVersion(text string) (semVersion, bool) {
	text, build, hasBuild := strings.Cut(text, "+")
	if hasBuild && !identifiers(build, false) {
		return semVersion{}, false
	}
	core, pre, hasPre := strings.Cut(text, "-")
	if hasPre && !identifiers(pre, true) {
		return semVersion{}, false
	}
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return semVersion{}, false
	}
	var numbers [3]uint64
	for i, field := range fields {
		if !numeric(field) {
			return semVersion{}, false
		}
		n, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return semVersion{}, false
		}
		numbers[i] = n
	}
	v := semVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// tagVersion reads a release tag, with or without its leading v.
func tagVersion(tag string) (semVersion, bool) {
	return parseVersion(strings.TrimPrefix(tag, "v"))
}

// numeric reports whether s is a number without leading zeros.
func numeric(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// identifiers checks dot-separated identifiers of [0-9A-Za-z-]; those of a
// pre-release that are numbers cannot have leading zeros.
func identifiers(s string, pre bool) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		digits := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				digits = false
			default:
				return false
			}
		}
		if pre && digits && !numeric(id) {
			return false
		}
	}
	return true
}

// compare orders versions by precedence: -1, 0 or 1.
func (v semVersion) compare(w semVersion) int {
	for _, pair := range [][2]uint64{{v.major, w.major}, {v.minor, w.minor}, {v.patch, w.patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(v.pre) == 0 && len(w.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(w.pre) == 0:
		return -1
	}
	for i := 0; i < len(v.pre) && i < len(w.pre); i++ {
		if c := compareIdentifier(v.pre[i], w.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) < len(w.pre):
		return -1
	case len(v.pre) > len(w.pre):
		return 1
	}
	return 0
}

// compareIdentifier orders pre-release identifiers: numbers numerically and
// before words, words in ASCII order.
func compareIdentifier(a, b string) int {
	an, bn := numeric(a), numeric(b)
	switch {
	case an && bn:
		if len(a) != len(b) {
			if len(a) < len(b) {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// String spells the version without build metadata.
func (v semVersion) String() string {
	s := strconv.FormatUint(v.major, 10) + "." + strconv.FormatUint(v.minor, 10) + "." + strconv.FormatUint(v.patch, 10)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

func (v semVersion) prerelease() bool { return len(v.pre) > 0 }
