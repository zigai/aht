package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

func shortRegistryID(id string) string {
	separator := strings.LastIndexByte(id, '-')
	if separator < 0 || len(id)-separator-1 <= registryIDShortLength {
		return id
	}

	return id[:separator+1] + id[separator+1:separator+1+registryIDShortLength]
}

func abbreviatedRegistryIDs(sessions []registry.Session) map[string]string {
	if len(sessions) == 0 {
		return nil
	}

	type item struct {
		id     string
		suffix string
	}

	byPrefix := make(map[string][]item)

	for _, session := range sessions {
		p, s := splitRegistryID(session.ID)
		byPrefix[p] = append(byPrefix[p], item{id: session.ID, suffix: s})
	}

	result := make(map[string]string, len(sessions))

	for prefix, items := range byPrefix {
		slices.SortFunc(items, func(a, b item) int {
			return strings.Compare(a.suffix, b.suffix)
		})

		for i, it := range items {
			previous, next := "", ""
			if i > 0 {
				previous = items[i-1].suffix
			}

			if i+1 < len(items) {
				next = items[i+1].suffix
			}

			result[it.id] = abbreviatedRegistryID(it.id, prefix, it.suffix, previous, next)
		}
	}

	return result
}

func abbreviatedRegistryID(
	id string,
	prefix string,
	suffix string,
	previous string,
	next string,
) string {
	if len(suffix) <= registryIDShortLength {
		return id
	}

	length := registryIDShortLength

	for _, neighbor := range []string{previous, next} {
		if common := commonPrefixLength(suffix, neighbor); common >= length {
			length = min(common+1, len(suffix))
		}
	}

	return prefix + suffix[:length]
}

func splitRegistryID(id string) (string, string) {
	separator := strings.LastIndexByte(id, '-')
	if separator < 0 {
		return "", id
	}

	return id[:separator+1], id[separator+1:]
}

func commonPrefixLength(left string, right string) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] != right[index] {
			return index
		}
	}

	return limit
}

func sessionDisplayLabel(session registry.Session) string {
	if session.SessionID != "" {
		return session.SessionID
	}

	if session.SessionPath != "" {
		return formatSessionPathLabel(session.SessionPath)
	}

	if session.Location.PaneID != "" {
		return session.Location.PaneID
	}

	if session.Process != nil && session.Process.PID > 0 {
		return fmt.Sprintf("pid:%d", session.Process.PID)
	}

	return shortRegistryID(session.ID)
}

func formatSessionPathLabel(path string) string {
	base := filepath.Base(path)

	trimmed := base
	for _, ext := range []string{".jsonl", ".json"} {
		if cut, ok := strings.CutSuffix(trimmed, ext); ok {
			trimmed = cut
			break
		}
	}

	if separator := strings.LastIndexByte(
		trimmed,
		'_',
	); separator >= 0 &&
		separator+1 < len(trimmed) {
		suffix := trimmed[separator+1:]
		if len(suffix) >= 8 || strings.Contains(suffix, "-") {
			return suffix
		}
	}

	return base
}

func sessionLabel(name string, id string) string {
	if name != "" {
		return name
	}

	if id != "" {
		return id
	}

	return "-"
}

func tmuxWindowLabel(location registry.Location) string {
	if location.WindowIndex != "" && location.WindowName != "" {
		return location.WindowIndex + ":" + location.WindowName
	}

	if location.WindowName != "" {
		return location.WindowName
	}

	if location.WindowIndex != "" {
		return location.WindowIndex
	}

	return "-"
}

func multiplexerSessionLabel(location registry.Location) string {
	return sessionLabel(location.SessionName, location.SessionID)
}

func multiplexerContainerLabel(location registry.Location) string {
	if location.Kind == registry.MultiplexerTmux {
		return tmuxWindowLabel(location)
	}

	var parts []string
	if location.WorkspaceName != "" {
		parts = append(parts, location.WorkspaceName)
	} else if location.WorkspaceID != "" {
		parts = append(parts, location.WorkspaceID)
	}

	tab := location.TabName
	if tab == "" {
		tab = location.TabID
	}

	if tab == "" {
		tab = location.TabIndex
	}

	if tab != "" {
		parts = append(parts, tab)
	}

	if len(parts) == 0 {
		return "-"
	}

	return strings.Join(parts, "/")
}

func multiplexerSummaryKind(summary registry.Summary) string {
	if summary.MultiplexerKind != "" {
		return string(summary.MultiplexerKind)
	}

	return "unknown"
}
