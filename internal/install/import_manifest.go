package install

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
)

const importsKey = "imports"

// importManifest is agy's shared import manifest. Fields aht does not own are
// kept as raw JSON so rewriting the file never drops them.
type importManifest struct {
	Imports []importEntry
	extra   map[string]json.RawMessage
}

// importEntry is one entry of the manifest. Only the fields aht owns are
// interpreted; every other field of the entry is carried through untouched.
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

func newImportEntry(plan harnesspkg.ImportManifestInstallPlan, now time.Time) importEntry {
	entry := importEntry{fields: map[string]json.RawMessage{}}
	entry.set("name", plan.Name)
	entry.set("source", plan.Source)
	entry.set("importedAt", now.Format(time.RFC3339))
	entry.set("components", append([]string(nil), plan.Components...))

	return entry
}

// withPlan returns the entry with the fields aht owns brought in line with the
// plan, and whether that changed anything. Fields it does not own are kept.
func (entry importEntry) withPlan(plan harnesspkg.ImportManifestInstallPlan, now time.Time) (importEntry, bool) {
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

// stringField treats a missing or non-string value as empty.
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
