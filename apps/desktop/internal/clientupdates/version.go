package clientupdates

import (
	"strconv"
	"strings"
)

// semver is a SemVer 2.0 version, read and ordered as the semver crate
// does.
type semver struct {
	major, minor, patch uint64
	pre, build          []string
}

func parseSemver(text string) (semver, bool) {
	rest, build, hasBuild := strings.Cut(text, "+")
	numbers, pre, hasPre := strings.Cut(rest, "-")
	parts := strings.Split(numbers, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, target := range []*uint64{&v.major, &v.minor, &v.patch} {
		n, ok := numeric(parts[i])
		if !ok {
			return semver{}, false
		}
		*target = n
	}
	if hasPre {
		if v.pre = identifiers(pre, true); v.pre == nil {
			return semver{}, false
		}
	}
	if hasBuild {
		if v.build = identifiers(build, false); v.build == nil {
			return semver{}, false
		}
	}
	return v, true
}

// numeric reads a version number: digits without a leading zero.
func numeric(text string) (uint64, bool) {
	if text == "" || !digits(text) || len(text) > 1 && text[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(text, 10, 64)
	return n, err == nil
}

func digits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// identifiers splits a pre-release or build part; nil when one is empty or
// holds another character. Numeric pre-release identifiers take no leading
// zero.
func identifiers(text string, pre bool) []string {
	parts := strings.Split(text, ".")
	for _, part := range parts {
		if part == "" {
			return nil
		}
		for i := 0; i < len(part); i++ {
			if c := part[i]; !isAlnum(c) && c != '-' {
				return nil
			}
		}
		if pre && digits(part) && len(part) > 1 && part[0] == '0' {
			return nil
		}
	}
	return parts
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func (v semver) String() string {
	text := strconv.FormatUint(v.major, 10) + "." + strconv.FormatUint(v.minor, 10) + "." + strconv.FormatUint(v.patch, 10)
	if len(v.pre) > 0 {
		text += "-" + strings.Join(v.pre, ".")
	}
	if len(v.build) > 0 {
		text += "+" + strings.Join(v.build, ".")
	}
	return text
}

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compare orders by precedence, a release after its pre-releases, then by
// build metadata as the semver crate does.
func (v semver) compare(o semver) int {
	for _, c := range []int{compareUint(v.major, o.major), compareUint(v.minor, o.minor), compareUint(v.patch, o.patch)} {
		if c != 0 {
			return c
		}
	}
	switch {
	case len(v.pre) == 0 && len(o.pre) > 0:
		return 1
	case len(v.pre) > 0 && len(o.pre) == 0:
		return -1
	}
	if c := compareIdentifiers(v.pre, o.pre); c != 0 {
		return c
	}
	return compareIdentifiers(v.build, o.build)
}

// compareIdentifiers puts numeric identifiers, by value, before
// alphanumeric ones, by ASCII; a list that runs out first is lower.
func compareIdentifiers(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		xNumeric, yNumeric := digits(x), digits(y)
		var c int
		switch {
		case xNumeric && yNumeric:
			xs, ys := strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if c = compareUint(uint64(len(xs)), uint64(len(ys))); c == 0 {
				if c = strings.Compare(xs, ys); c == 0 {
					c = compareUint(uint64(len(x)), uint64(len(y)))
				}
			}
		case xNumeric:
			c = -1
		case yNumeric:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return compareUint(uint64(len(a)), uint64(len(b)))
}

// clientVersion is a version as the client prints it, with the key that
// orders releases.
type clientVersion struct {
	order semver
	text  string
}

func (v clientVersion) String() string { return v.text }

func parseClientVersion(id ClientID, raw string) (clientVersion, bool) {
	raw = strings.TrimLeft(strings.Trim(raw, "()"), "v")
	if id != ClientCursor {
		order, ok := parseSemver(raw)
		if !ok {
			return clientVersion{}, false
		}
		return clientVersion{order: order, text: order.String()}, true
	}
	// Cursor builds are dated: YYYY.MM.DD-<commit>. Only the date orders
	// them.
	date, commit, _ := strings.Cut(raw, "-")
	if len(date) != 10 || date[4] != '.' || date[7] != '.' {
		return clientVersion{}, false
	}
	for i := 0; i < len(date); i++ {
		if i != 4 && i != 7 && (date[i] < '0' || date[i] > '9') {
			return clientVersion{}, false
		}
	}
	for i := 0; i < len(commit); i++ {
		if !isAlnum(commit[i]) {
			return clientVersion{}, false
		}
	}
	year, _ := strconv.ParseUint(date[:4], 10, 64)
	month, _ := strconv.ParseUint(date[5:7], 10, 64)
	day, _ := strconv.ParseUint(date[8:], 10, 64)
	return clientVersion{order: semver{major: year, minor: month, patch: day}, text: raw}, true
}

// newerThan counts a different build from the same day as newer, as
// Cursor's updater does.
func (v clientVersion) newerThan(o clientVersion) bool {
	switch c := v.order.compare(o.order); {
	case c > 0:
		return true
	case c == 0:
		return v.text != o.text
	}
	return false
}

func parseVersion(id ClientID, raw string) (clientVersion, *clientError) {
	for _, token := range strings.Fields(raw) {
		if v, ok := parseClientVersion(id, token); ok {
			return v, nil
		}
	}
	return clientVersion{}, newError("version", "Could not read a version")
}
