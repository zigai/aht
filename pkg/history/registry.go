package history

import (
	"cmp"
	"path/filepath"
	"slices"

	"github.com/zigai/aht/v2/pkg/registry"
)

type registryEntry struct {
	state     RegistryState
	path      string
	canonical string
	order     int
}

type registryLookup struct {
	ids        map[string][]registryEntry
	paths      map[string][]registryEntry
	cleanPaths map[string][]registryEntry
	pathCount  int
}

func (s *search) prepareRegistry() {
	s.registry = map[registry.Harness]*registryLookup{}
	for order, session := range s.query.Registry {
		lookup := s.registry[session.Harness]
		if lookup == nil {
			lookup = &registryLookup{ids: map[string][]registryEntry{}, paths: map[string][]registryEntry{}, cleanPaths: map[string][]registryEntry{}, pathCount: 0}
			s.registry[session.Harness] = lookup
		}
		entry := registryEntry{state: RegistryState{RegistryID: session.ID, Presence: session.Presence(), UpdatedAt: session.UpdatedAt}, path: "", canonical: "", order: order}
		if session.SessionPath != "" {
			entry.path = filepath.Clean(session.SessionPath)
			entry.canonical = registry.CanonicalPath(session.SessionPath)
		}
		if session.SessionID != "" {
			lookup.ids[session.SessionID] = append(lookup.ids[session.SessionID], entry)
		} else if entry.path != "" {
			lookup.paths[entry.canonical] = append(lookup.paths[entry.canonical], entry)
			lookup.cleanPaths[entry.path] = append(lookup.cleanPaths[entry.path], entry)
			lookup.pathCount++
		}
	}
}

func (s *search) registryStates(c Conversation) []RegistryState {
	result := []RegistryState{}
	lookup := s.registry[c.Harness]
	if lookup == nil {
		return result
	}
	var canonicalPath string
	canonicalized := false
	canonical := func() string {
		if !canonicalized {
			canonicalPath = registry.CanonicalPath(c.Path)
			canonicalized = true
		}
		return canonicalPath
	}
	var matched []registryEntry
	database := isDatabase(c.Path)
	for _, entry := range lookup.ids[c.SessionID] {
		if entry.path == "" || (!database && (entry.path == c.Path || entry.canonical == canonical())) {
			matched = append(matched, entry)
		}
	}
	if !database && lookup.pathCount > 0 {
		paths := lookup.cleanPaths[c.Path]
		if len(paths) != lookup.pathCount {
			paths = lookup.paths[canonical()]
		}
		matched = append(matched, paths...)
	}
	slices.SortFunc(matched, func(first, second registryEntry) int { return cmp.Compare(first.order, second.order) })
	for _, entry := range matched {
		result = append(result, entry.state)
	}
	return result
}
