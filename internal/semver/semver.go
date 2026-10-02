// Package semver checks and orders the version strings of task versions.
// Versions are written without a leading "v" and without build metadata,
// for example 1.2.0 or 1.2.0-rc.1.
package semver

import (
	"fmt"
	"strconv"
	"strings"

	xsemver "golang.org/x/mod/semver"
)

// Step is the part of a version that a new version increases.
type Step string

const (
	Patch Step = "patch"
	Minor Step = "minor"
	Major Step = "major"
)

// Valid reports whether v is a full semantic version such as 1.2.0.
func Valid(v string) bool {
	return v != "" && v[0] != 'v' && xsemver.Canonical("v"+v) == "v"+v
}

// Compare returns -1, 0 or +1 depending on whether a is lower than, equal
// to, or higher than b. Both must be valid.
func Compare(a, b string) int {
	return xsemver.Compare("v"+a, "v"+b)
}

// Bump returns the version that follows v for the given step. A pre-release
// suffix is dropped before increasing, so 1.3.0-rc.1 bumps to 1.3.1.
func Bump(v string, step Step) (string, error) {
	if !Valid(v) {
		return "", fmt.Errorf("invalid version %q", v)
	}
	core, _, _ := strings.Cut(v, "-")
	var n [3]int
	for i, part := range strings.Split(core, ".") {
		n[i], _ = strconv.Atoi(part)
	}
	switch step {
	case Major:
		n = [3]int{n[0] + 1, 0, 0}
	case Minor:
		n = [3]int{n[0], n[1] + 1, 0}
	case Patch:
		n[2]++
	default:
		return "", fmt.Errorf("unknown step %q, want patch, minor or major", step)
	}
	return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]), nil
}
