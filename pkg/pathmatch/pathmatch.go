// Package pathmatch implements the shared AHT path-filter semantics.
package pathmatch

import (
	"path/filepath"
	"slices"
	"strings"
)

const doubleStar = "**"

// Match reports whether path or one of its ancestor directories matches
// pattern. Pattern segments are [filepath.Match] globs, "**" matches zero or
// more segments, and a trailing "/*" also matches the directory itself.
func Match(path string, pattern string) bool {
	if pattern == "" || path == "" {
		return false
	}

	pathSegments := segments(path)

	// reachable[j] holds whether the pattern consumed so far can end right
	// before path segment j.
	reachable := make([]bool, len(pathSegments)+1)
	reachable[0] = true

	for _, patternSegment := range trimTrailingStar(segments(pattern)) {
		reachable = advance(reachable, patternSegment, pathSegments)
	}

	return slices.Contains(reachable, true)
}

func MatchAny(patterns []string, paths ...string) bool {
	for _, pattern := range patterns {
		for _, path := range paths {
			if Match(path, pattern) {
				return true
			}
		}
	}

	return false
}

func segments(path string) []string {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "/" {
		return []string{""}
	}

	return strings.Split(cleaned, "/")
}

func trimTrailingStar(patternSegments []string) []string {
	if last := len(patternSegments) - 1; last > 0 && patternSegments[last] == "*" {
		return patternSegments[:last]
	}

	return patternSegments
}

func advance(reachable []bool, patternSegment string, pathSegments []string) []bool {
	next := make([]bool, len(reachable))

	for j, ok := range reachable {
		switch {
		case !ok:
		case patternSegment == doubleStar:
			for k := j; k < len(next) && !next[k]; k++ {
				next[k] = true
			}
		case j < len(pathSegments) && matchSegment(patternSegment, pathSegments[j]):
			next[j+1] = true
		}
	}

	return next
}

func matchSegment(pattern string, segment string) bool {
	if pattern == segment {
		return true
	}

	// The empty root segment of an absolute path must not let a relative
	// pattern such as "*/foo" match "/foo".
	if segment == "" {
		return false
	}

	matched, err := filepath.Match(pattern, segment)

	return err == nil && matched
}
