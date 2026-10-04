package toolchain

import (
	"fmt"
	"strconv"
	"strings"
)

// Range is an inclusive version interval written as "low~high". Empty bounds are
// open, so "14.3~" and "~14.5" both work, and an empty spec matches everything.
// Ranges keep trestle.toml portable: they express which installed version a
// project needs instead of the absolute path of one machine's toolchain.
//
// A bound compares numerically over the components it spells out, so spell out
// the digits you mean: "14.30~14.50" accepts the toolset 14.44.35207, and
// "12.0~12.9" accepts 12.9.131 while still rejecting 12.10.
type Range struct {
	Min string
	Max string
}

// Any reports whether the range accepts every version.
func (r Range) Any() bool { return r.Min == "" && r.Max == "" }

// Constrained reports whether the range actually narrows the choice.
func (r Range) Constrained() bool { return !r.Any() }

// String renders the range in the canonical trestle.toml spelling.
func (r Range) String() string {
	switch {
	case r.Any():
		return ""
	case r.Min == "":
		return "~" + r.Max
	case r.Max == "":
		return r.Min + "~"
	default:
		return r.Min + "~" + r.Max
	}
}

// ParseRange accepts "", "auto", "any", "12.4", "12.4~12.9", "~12.9" and "12.0~".
func ParseRange(spec string) (Range, error) {
	trimmed := strings.TrimSpace(spec)
	if trimmed == "" || strings.EqualFold(trimmed, "auto") || strings.EqualFold(trimmed, "any") {
		return Range{}, nil
	}
	result := Range{}
	if low, high, found := strings.Cut(trimmed, "~"); found {
		result.Min = strings.TrimSpace(low)
		result.Max = strings.TrimSpace(high)
	} else {
		result.Min = trimmed
		result.Max = trimmed
	}
	for _, bound := range []string{result.Min, result.Max} {
		if bound == "" {
			continue
		}
		if err := validateVersion(bound); err != nil {
			return Range{}, fmt.Errorf("E_CONFIG_INVALID_VERSION: %q is not a valid version in %q; use dotted numbers such as 14.3~14.5", bound, spec)
		}
	}
	return result, nil
}

func validateVersion(value string) error {
	parts := strings.Split(value, ".")
	if len(parts) == 0 {
		return fmt.Errorf("empty version")
	}
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("empty component")
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return fmt.Errorf("non-numeric component %q", part)
			}
		}
	}
	return nil
}

// Contains reports whether version falls inside the range. Unparsable versions
// never match a constrained range, so a stale cache entry cannot be selected.
func (r Range) Contains(version string) bool {
	candidate, ok := NormalizeVersion(version)
	if !ok {
		return r.Any()
	}
	if r.Min != "" && compareAtPrecision(candidate, r.Min) < 0 {
		return false
	}
	if r.Max != "" && compareAtPrecision(candidate, r.Max) > 0 {
		return false
	}
	return true
}

// compareAtPrecision orders candidate and bound over the components both spell
// out, so "14.4" accepts 14.44.35207 and "12.9" rejects 12.10.
func compareAtPrecision(candidate, bound string) int {
	candidateParts := strings.Split(candidate, ".")
	boundParts := strings.Split(bound, ".")
	limit := len(candidateParts)
	if len(boundParts) < limit {
		limit = len(boundParts)
	}
	for index := 0; index < limit; index++ {
		left := versionComponent(candidateParts, index)
		right := versionComponent(boundParts, index)
		if left != right {
			if left < right {
				return -1
			}
			return 1
		}
	}
	return 0
}

// NormalizeVersion strips a leading "v" and trailing pre-release or build labels
// so directory names, banners, and configuration all compare equal.
func NormalizeVersion(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) > 0 && (value[0] == 'v' || value[0] == 'V') {
		value = value[1:]
	}
	if index := strings.IndexAny(value, "-+ "); index >= 0 {
		value = value[:index]
	}
	if value == "" || validateVersion(value) != nil {
		return "", false
	}
	return value, true
}

// CompareVersions orders dotted numeric versions, treating missing trailing
// components as zero so "14.44" and "14.44.0" compare equal.
func CompareVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := 0; index < len(leftParts) || index < len(rightParts); index++ {
		leftValue := versionComponent(leftParts, index)
		rightValue := versionComponent(rightParts, index)
		if leftValue != rightValue {
			if leftValue < rightValue {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionComponent(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	value, err := strconv.Atoi(parts[index])
	if err != nil {
		return 0
	}
	return value
}

// ExtractVersion pulls the first dotted number out of free-form tool output such
// as "clang version 18.1.8" or "release 12.4, V12.4.131".
func ExtractVersion(text string) (string, bool) {
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			continue
		}
		end := index
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		components := 0
		cursor := end
		for cursor+1 < len(text) && text[cursor] == '.' && text[cursor+1] >= '0' && text[cursor+1] <= '9' {
			cursor++
			for cursor < len(text) && text[cursor] >= '0' && text[cursor] <= '9' {
				cursor++
			}
			components++
		}
		if components > 0 {
			return NormalizeVersion(text[index:cursor])
		}
		index = end - 1
	}
	return "", false
}

// LooksLikeVersion reports whether a spec is meant to be a version constraint
// rather than an executable name. An explicit range separator always marks one;
// otherwise every character must belong to a dotted number. This keeps
// "clang-cl" from being rejected as a malformed constraint while still catching
// a typo such as "12.x~12.9".
func LooksLikeVersion(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, "~") {
		return true
	}
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9', character == '.', character == 'v', character == 'V':
		default:
			return false
		}
	}
	return true
}

// HighestVersion returns the numerically greatest version in candidates.
func HighestVersion(candidates []string) (string, bool) {
	best := ""
	found := false
	for _, candidate := range candidates {
		normalized, ok := NormalizeVersion(candidate)
		if !ok {
			continue
		}
		if !found || CompareVersions(normalized, best) > 0 {
			best, found = normalized, true
		}
	}
	return best, found
}
