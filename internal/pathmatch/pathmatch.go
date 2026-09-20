// Package pathmatch implements the shared AHT path-filter semantics.
package pathmatch

import (
	"path/filepath"
	"strings"
)

// Match reports whether a path matches an AHT ignore path, subtree, or glob.
func Match(path, pattern string) bool {
	if pattern == "" || path == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanPattern := filepath.Clean(pattern)
	if cleanPath == cleanPattern {
		return true
	}
	if matchGlobPattern(cleanPath, cleanPattern, pattern) {
		return true
	}
	return matchPrefixOrWildcard(cleanPath, pattern)
}

func matchGlobPattern(cleanPath, cleanPattern, pattern string) bool {
	if matched, err := filepath.Match(pattern, cleanPath); err == nil && matched {
		return true
	}
	if matched, err := filepath.Match(cleanPattern, cleanPath); err == nil && matched {
		return true
	}
	return false
}

func matchPrefixOrWildcard(cleanPath, pattern string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(pattern, "/*"), "/")
	if cleanPath == trimmed || strings.HasPrefix(cleanPath, trimmed+string(filepath.Separator)) {
		return true
	}
	if strings.Contains(pattern, "**") {
		sub := strings.Trim(strings.Trim(pattern, "*"), string(filepath.Separator))
		if sub != "" && (strings.Contains(cleanPath, string(filepath.Separator)+sub+string(filepath.Separator)) ||
			strings.HasSuffix(cleanPath, string(filepath.Separator)+sub) ||
			strings.HasPrefix(cleanPath, sub+string(filepath.Separator)) ||
			cleanPath == sub) {
			return true
		}
	}
	return false
}
