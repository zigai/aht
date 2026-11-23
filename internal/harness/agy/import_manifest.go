package agy

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

const importsKey = "imports"

type importManifest struct {
	Imports []importEntry
	extra   map[string]json.RawMessage
}

type importEntry struct {
	fields map[string]json.RawMessage
}

func (manifest *importManifest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decoding import manifest object: %w", err)
	}

	if raw, ok := fields[importsKey]; ok {
		if err := json.Unmarshal(raw, &manifest.Imports); err != nil {
			return fmt.Errorf("decoding import manifest entries: %w", err)
		}

		delete(fields, importsKey)
	}

	manifest.extra = fields

	return nil
}

func (manifest importManifest) MarshalJSON() ([]byte, error) {
	imports := manifest.Imports
	if imports == nil {
		imports = []importEntry{}
	}

	rawImports, err := json.Marshal(imports)
	if err != nil {
		return nil, fmt.Errorf("encoding import manifest entries: %w", err)
	}

	fields := make(map[string]json.RawMessage, len(manifest.extra)+1)
	maps.Copy(fields, manifest.extra)
	fields[importsKey] = rawImports

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding import manifest object: %w", err)
	}

	return data, nil
}

func (entry *importEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &entry.fields); err != nil {
		return fmt.Errorf("decoding import manifest entry: %w", err)
	}

	return nil
}

func (entry importEntry) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(entry.fields)
	if err != nil {
		return nil, fmt.Errorf("encoding import manifest entry: %w", err)
	}

	return data, nil
}

func newImportEntry(plan importRegistration, now time.Time) importEntry {
	entry := importEntry{fields: map[string]json.RawMessage{}}
	entry.set("name", plan.Name)
	entry.set("source", plan.Source)
	entry.set("importedAt", now.Format(time.RFC3339))
	entry.set("components", append([]string(nil), plan.Components...))

	return entry
}

func (entry importEntry) withPlan(
	plan importRegistration,
	now time.Time,
) (importEntry, bool) {
	next := importEntry{fields: make(map[string]json.RawMessage, len(entry.fields)+1)}
	maps.Copy(next.fields, entry.fields)

	changed := false

	if entry.source() != plan.Source {
		next.set("source", plan.Source)

		changed = true
	}

	if entry.importedAt() == "" {
		next.set("importedAt", now.Format(time.RFC3339))

		changed = true
	}

	components := slices.Clone(entry.components())
	for _, component := range plan.Components {
		if !slices.Contains(components, component) {
			components = append(components, component)
			changed = true
		}
	}

	if changed {
		next.set("components", components)
	}

	return next, changed
}

func (entry importEntry) name() string       { return entry.stringField("name") }
func (entry importEntry) source() string     { return entry.stringField("source") }
func (entry importEntry) importedAt() string { return entry.stringField("importedAt") }

func (entry importEntry) components() []string {
	var components []string
	if err := json.Unmarshal(entry.fields["components"], &components); err != nil {
		return nil
	}

	return components
}

func (entry importEntry) stringField(key string) string {
	var value string
	if err := json.Unmarshal(entry.fields[key], &value); err != nil {
		return ""
	}

	return value
}

func (entry importEntry) set(key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encoding import manifest field %s: %v", key, err))
	}

	entry.fields[key] = data
}

func readImportManifest(path string) (importManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return importManifest{
				Imports: nil,
				extra:   nil,
			}, nil
		}

		return importManifest{}, fmt.Errorf("reading import manifest: %w", err)
	}

	var manifest importManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return importManifest{}, fmt.Errorf("parsing import manifest: %w", err)
	}

	return manifest, nil
}

func upsertImport(
	manifest importManifest,
	plan importRegistration,
	now time.Time,
) (importManifest, bool) {
	for index, item := range manifest.Imports {
		if item.name() != plan.Name {
			continue
		}

		next, changed := item.withPlan(plan, now)
		if !changed {
			return manifest, false
		}

		manifest.Imports = slices.Clone(manifest.Imports)
		manifest.Imports[index] = next

		return manifest, true
	}

	manifest.Imports = append(slices.Clone(manifest.Imports), newImportEntry(plan, now))

	return manifest, true
}

func writeImportManifest(path string, manifest importManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding import manifest: %w", err)
	}

	data = append(data, '\n')

	return replaceImportManifest(path, data)
}

func removeImport(manifest importManifest, name string) (importManifest, bool) {
	next := make([]importEntry, 0, len(manifest.Imports))
	removed := false

	for _, item := range manifest.Imports {
		if item.name() == name {
			removed = true
			continue
		}

		next = append(next, item)
	}

	manifest.Imports = next

	return manifest, removed
}

func replaceImportManifest(path string, data []byte) error {
	previous, err := os.ReadFile(path)
	existed := err == nil

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading import manifest rollback state: %w", err)
	}

	const createError = "creating config directory"
	if err := harness.WriteFileAtomicMode(
		path,
		data,
		0o600,
		createError,
		"writing import manifest",
	); err != nil {
		return errors.Join(err, restoreImportManifest(path, previous, existed))
	}

	return nil
}

func restoreImportManifest(path string, previous []byte, existed bool) error {
	if existed {
		return harness.WriteFileAtomicMode(
			path,
			previous,
			0o600,
			"creating config directory",
			"restoring import manifest",
		)
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("removing newly created import manifest: %w", err)
	}

	return harness.SyncDir(filepath.Dir(path))
}
