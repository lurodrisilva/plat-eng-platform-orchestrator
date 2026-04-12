package github

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// ParsedTag associates a raw tag name with its canonical semver form.
type ParsedTag struct {
	Raw       string // original tag name, e.g. "v1.2.3"
	Canonical string // semver canonical form, e.g. "v1.2.3"
}

// SelectHighestSemVerTag selects the highest SemVer tag from a list, optionally filtered by a constraint.
// Returns the raw tag name and the parsed version string.
func SelectHighestSemVerTag(tags []Tag, constraint string, allowPrerelease bool) (string, string, error) {
	var parsed []ParsedTag

	for _, t := range tags {
		name := t.Name
		// Normalize: ensure "v" prefix for semver package compatibility
		canonical := name
		if !strings.HasPrefix(canonical, "v") {
			canonical = "v" + canonical
		}

		if !semver.IsValid(canonical) {
			continue
		}

		// Skip pre-release unless explicitly allowed
		if !allowPrerelease && semver.Prerelease(canonical) != "" {
			continue
		}

		parsed = append(parsed, ParsedTag{Raw: name, Canonical: canonical})
	}

	if len(parsed) == 0 {
		return "", "", fmt.Errorf("no valid SemVer tags found")
	}

	// Apply constraint if provided
	if constraint != "" {
		parsed = filterByConstraint(parsed, constraint)
		if len(parsed) == 0 {
			return "", "", fmt.Errorf("no tags match constraint %q", constraint)
		}
	}

	// Sort descending by SemVer — deterministic because SemVer defines total ordering
	sort.Slice(parsed, func(i, j int) bool {
		return semver.Compare(parsed[i].Canonical, parsed[j].Canonical) > 0
	})

	highest := parsed[0]
	// Return version without "v" prefix for chart versioning
	version := strings.TrimPrefix(highest.Canonical, "v")
	return highest.Raw, version, nil
}

// filterByConstraint filters tags based on a SemVer constraint.
// Supports:
//   - "~1.5.0" — compatible with 1.5.x (>=1.5.0, <1.6.0)
//   - "^2.0.0" — compatible with 2.x.x (>=2.0.0, <3.0.0)
//   - ">=1.0.0" — greater than or equal
//   - exact match "1.2.3"
func filterByConstraint(tags []ParsedTag, constraint string) []ParsedTag {
	var result []ParsedTag

	switch {
	case strings.HasPrefix(constraint, "~"):
		// Tilde: same major.minor
		base := "v" + strings.TrimPrefix(constraint, "~")
		if !semver.IsValid(base) {
			return tags // invalid constraint, return all
		}
		majorMinor := semver.MajorMinor(base)
		for _, t := range tags {
			if semver.MajorMinor(t.Canonical) == majorMinor &&
				semver.Compare(t.Canonical, base) >= 0 {
				result = append(result, t)
			}
		}

	case strings.HasPrefix(constraint, "^"):
		// Caret: same major
		base := "v" + strings.TrimPrefix(constraint, "^")
		if !semver.IsValid(base) {
			return tags
		}
		major := semver.Major(base)
		for _, t := range tags {
			if semver.Major(t.Canonical) == major &&
				semver.Compare(t.Canonical, base) >= 0 {
				result = append(result, t)
			}
		}

	case strings.HasPrefix(constraint, ">="):
		base := "v" + strings.TrimPrefix(constraint, ">=")
		if !semver.IsValid(base) {
			return tags
		}
		for _, t := range tags {
			if semver.Compare(t.Canonical, base) >= 0 {
				result = append(result, t)
			}
		}

	default:
		// Exact match
		exact := constraint
		if !strings.HasPrefix(exact, "v") {
			exact = "v" + exact
		}
		for _, t := range tags {
			if t.Canonical == exact {
				result = append(result, t)
			}
		}
	}

	return result
}
