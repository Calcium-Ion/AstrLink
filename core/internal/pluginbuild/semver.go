package pluginbuild

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version: major.minor.patch with an optional
// prerelease.
type Version struct {
	Major, Minor, Patch int
	Prerelease          string
}

// ParseVersion reads a semantic version, a leading v allowed and build
// metadata ignored.
func ParseVersion(text string) (Version, bool) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "v")
	text, _, _ = strings.Cut(text, "+")
	core, prerelease, _ := strings.Cut(text, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var numbers [3]int
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 || (len(part) > 1 && part[0] == '0') {
			return Version{}, false
		}
		numbers[index] = number
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2], Prerelease: prerelease}, true
}

// Compare orders versions as semantic versioning does: a prerelease comes
// before its release.
func (version Version) Compare(other Version) int {
	for _, pair := range [][2]int{{version.Major, other.Major}, {version.Minor, other.Minor}, {version.Patch, other.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case version.Prerelease == other.Prerelease:
		return 0
	case version.Prerelease == "":
		return 1
	case other.Prerelease == "":
		return -1
	}
	return comparePrerelease(version.Prerelease, other.Prerelease)
}

func comparePrerelease(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for index := 0; index < len(left) && index < len(right); index++ {
		x, xErr := strconv.Atoi(left[index])
		y, yErr := strconv.Atoi(right[index])
		switch {
		case xErr == nil && yErr == nil:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case xErr == nil:
			return -1
		case yErr == nil:
			return 1
		case left[index] != right[index]:
			if left[index] < right[index] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	}
	return 0
}

func (version Version) String() string {
	text := fmt.Sprintf("%d.%d.%d", version.Major, version.Minor, version.Patch)
	if version.Prerelease != "" {
		text += "-" + version.Prerelease
	}
	return text
}

// comparator is one condition of a range: version op bound.
type comparator struct {
	op    string
	bound Version
}

func (condition comparator) matches(version Version) bool {
	order := version.Compare(condition.bound)
	switch condition.op {
	case ">":
		return order > 0
	case ">=":
		return order >= 0
	case "<":
		return order < 0
	case "<=":
		return order <= 0
	}
	return order == 0
}

// Satisfies reports whether version is in the range text: comparators such
// as >=1.2.0, ^1.2, ~1.2.3, 1.x and * joined by spaces (all must hold) and
// || (any set may). A prerelease version satisfies a range only through a
// comparator with a prerelease of the same major.minor.patch, as npm does.
func Satisfies(version Version, text string) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return true, nil
	}
	for _, alternative := range strings.Split(text, "||") {
		conditions, err := parseConditions(strings.TrimSpace(alternative))
		if err != nil {
			return false, err
		}
		if conditionsMatch(conditions, version) {
			return true, nil
		}
	}
	return false, nil
}

func conditionsMatch(conditions []comparator, version Version) bool {
	for _, condition := range conditions {
		if !condition.matches(version) {
			return false
		}
	}
	if version.Prerelease == "" {
		return true
	}
	for _, condition := range conditions {
		bound := condition.bound
		if bound.Prerelease != "" && bound.Major == version.Major && bound.Minor == version.Minor && bound.Patch == version.Patch {
			return true
		}
	}
	return false
}

func parseConditions(text string) ([]comparator, error) {
	if text == "" || text == "*" || text == "x" || text == "X" {
		return nil, nil
	}
	fields := strings.Fields(text)
	if len(fields) == 3 && fields[1] == "-" {
		lower, _, err := partialVersion(fields[0])
		if err != nil {
			return nil, err
		}
		upper, precision, err := partialVersion(fields[2])
		if err != nil {
			return nil, err
		}
		conditions := []comparator{{">=", lower}}
		if precision == 3 {
			return append(conditions, comparator{"<=", upper}), nil
		}
		return append(conditions, comparator{"<", bumped(upper, precision)}), nil
	}
	var conditions []comparator
	for _, field := range fields {
		parsed, err := parseComparator(field)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, parsed...)
	}
	return conditions, nil
}

// partialVersion reads 1, 1.2 or 1.2.3, x or * standing for a missing part,
// and returns how many parts it had.
func partialVersion(text string) (Version, int, error) {
	text = strings.TrimPrefix(text, "v")
	text, _, _ = strings.Cut(text, "+")
	core, prerelease, _ := strings.Cut(text, "-")
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return Version{}, 0, fmt.Errorf("%q is not a version", text)
	}
	var numbers [3]int
	precision := 0
	for index, part := range parts {
		if part == "x" || part == "X" || part == "*" {
			break
		}
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Version{}, 0, fmt.Errorf("%q is not a version", text)
		}
		numbers[index] = number
		precision = index + 1
	}
	version := Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}
	if precision == 3 {
		version.Prerelease = prerelease
	}
	return version, precision, nil
}

// bumped is the first version past the partial version of precision parts:
// 1.2 becomes 1.3.0-0, 1 becomes 2.0.0-0.
func bumped(version Version, precision int) Version {
	switch precision {
	case 1:
		return Version{Major: version.Major + 1, Prerelease: "0"}
	case 2:
		return Version{Major: version.Major, Minor: version.Minor + 1, Prerelease: "0"}
	}
	return version
}

func parseComparator(text string) ([]comparator, error) {
	op := ""
	for _, prefix := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(text, prefix) {
			op, text = prefix, strings.TrimPrefix(text, prefix)
			break
		}
	}
	if text == "*" || text == "x" || text == "X" {
		return nil, nil
	}
	version, precision, err := partialVersion(text)
	if err != nil {
		return nil, err
	}
	if precision == 0 {
		return nil, nil
	}
	switch op {
	case "^":
		upper := Version{Major: version.Major + 1, Prerelease: "0"}
		switch {
		case version.Major == 0 && precision >= 2 && version.Minor == 0 && precision == 3:
			upper = Version{Minor: 0, Patch: version.Patch + 1, Prerelease: "0"}
		case version.Major == 0 && precision >= 2:
			upper = Version{Minor: version.Minor + 1, Prerelease: "0"}
		}
		return []comparator{{">=", version}, {"<", upper}}, nil
	case "~":
		if precision == 1 {
			return []comparator{{">=", version}, {"<", bumped(version, 1)}}, nil
		}
		return []comparator{{">=", version}, {"<", Version{Major: version.Major, Minor: version.Minor + 1, Prerelease: "0"}}}, nil
	case "", "=":
		if precision == 3 {
			return []comparator{{"=", version}}, nil
		}
		return []comparator{{">=", version}, {"<", bumped(version, precision)}}, nil
	case ">":
		if precision == 3 {
			return []comparator{{">", version}}, nil
		}
		return []comparator{{">=", bumped(version, precision)}}, nil
	case "<=":
		if precision == 3 {
			return []comparator{{"<=", version}}, nil
		}
		return []comparator{{"<", bumped(version, precision)}}, nil
	}
	return []comparator{{op, version}}, nil
}
