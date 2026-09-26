package contract

import (
	"regexp"
	"strconv"
	"strings"
)

// MinCodexClientVersion is the oldest Codex client the inference backend
// accepts; older versions are rejected upstream.
const MinCodexClientVersion = "0.144.0"

const maxClientVersionLength = 64

var clientVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type clientVersion struct {
	release    [3]uint64
	prerelease []string
}

func parseClientVersion(value string) (clientVersion, bool) {
	if len(value) > maxClientVersionLength || !clientVersionPattern.MatchString(value) {
		return clientVersion{}, false
	}
	value, _, _ = strings.Cut(value, "+")
	release, prerelease, hasPrerelease := strings.Cut(value, "-")
	var parsed clientVersion
	for index, part := range strings.Split(release, ".") {
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return clientVersion{}, false
		}
		parsed.release[index] = number
	}
	if hasPrerelease {
		parsed.prerelease = strings.Split(prerelease, ".")
	}
	return parsed, true
}

// ValidClientVersion reports whether value is a bounded semantic version,
// the form subscription clients declare in their User-Agent.
func ValidClientVersion(value string) bool {
	_, ok := parseClientVersion(value)
	return ok
}

// ValidCodexClientVersion also requires MinCodexClientVersion or newer.
func ValidCodexClientVersion(value string) bool {
	order, ok := CompareClientVersions(value, MinCodexClientVersion)
	return ok && order >= 0
}

// CompareClientVersions orders two client versions by semantic version
// precedence: a pre-release sorts before its release and build metadata is
// ignored. ok is false when either version is invalid.
func CompareClientVersions(left, right string) (order int, ok bool) {
	a, okA := parseClientVersion(left)
	b, okB := parseClientVersion(right)
	if !okA || !okB {
		return 0, false
	}
	for index := range a.release {
		if a.release[index] != b.release[index] {
			if a.release[index] < b.release[index] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case len(a.prerelease) == 0 && len(b.prerelease) == 0:
		return 0, true
	case len(a.prerelease) == 0:
		return 1, true
	case len(b.prerelease) == 0:
		return -1, true
	}
	for index := 0; index < len(a.prerelease) && index < len(b.prerelease); index++ {
		if order := comparePrereleaseIdentifier(a.prerelease[index], b.prerelease[index]); order != 0 {
			return order, true
		}
	}
	switch {
	case len(a.prerelease) < len(b.prerelease):
		return -1, true
	case len(a.prerelease) > len(b.prerelease):
		return 1, true
	}
	return 0, true
}

// ClientVersionMajor returns the major component of a valid client version.
func ClientVersionMajor(value string) (uint64, bool) {
	parsed, ok := parseClientVersion(value)
	return parsed.release[0], ok
}

func comparePrereleaseIdentifier(left, right string) int {
	a, errA := strconv.ParseUint(left, 10, 64)
	b, errB := strconv.ParseUint(right, 10, 64)
	switch {
	case errA == nil && errB == nil:
		if a == b {
			return 0
		}
		if a < b {
			return -1
		}
		return 1
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(left, right)
}
