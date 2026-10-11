package harness

import (
	"os"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	// LocationKindConfig is a settings file or directory.
	LocationKindConfig LocationKind = LocationKind(harness.LocationKindConfig)
	// LocationKindInstructions is a global or project instruction file, or a directory of them.
	LocationKindInstructions LocationKind = LocationKind(harness.LocationKindInstructions)
	// LocationKindSkills is a directory of skills.
	LocationKindSkills LocationKind = LocationKind(harness.LocationKindSkills)
	// LocationKindCommands is a directory of custom commands or prompt templates.
	LocationKindCommands LocationKind = LocationKind(harness.LocationKindCommands)
	// LocationKindMCP is a file holding MCP server definitions.
	LocationKindMCP LocationKind = LocationKind(harness.LocationKindMCP)

	// LocationScopeGlobal applies to every project of the current user.
	LocationScopeGlobal LocationScope = LocationScope(harness.LocationScopeGlobal)
	// LocationScopeProject applies to one project directory.
	LocationScopeProject LocationScope = LocationScope(harness.LocationScopeProject)
)

type (
	// LocationKind says what a [Location] holds.
	LocationKind string

	// LocationScope says whom a [Location] applies to.
	LocationScope string

	// Location is a path where a harness documents reading user-facing
	// configuration. A harness reads some kinds from several alternative paths,
	// so most entries do not exist on a given machine.
	Location struct {
		Harness registry.Harness `json:"harness"`
		Kind    LocationKind     `json:"kind"`
		Scope   LocationScope    `json:"scope"`
		Path    string           `json:"path"`
		Exists  bool             `json:"exists"`
	}
)

// LocationsFor returns the documented configuration locations of harnessID,
// resolving environment overrides such as the harness's home directory
// variable at call time. Project locations are joined under projectDir and
// omitted when it is empty. It returns false for an unknown harness.
func LocationsFor(harnessID registry.Harness, projectDir string) ([]Location, bool) {
	found, ok := catalog.LocationsFor(harnessID, projectDir)
	if !ok {
		return nil, false
	}
	locations := make([]Location, 0, len(found))
	for _, location := range found {
		_, err := os.Stat(location.Path)
		locations = append(locations, Location{
			Harness: harnessID,
			Kind:    LocationKind(location.Kind),
			Scope:   LocationScope(location.Scope),
			Path:    location.Path,
			Exists:  err == nil,
		})
	}
	return locations, true
}

// AllLocations returns the locations of every supported harness in canonical order.
func AllLocations(projectDir string) []Location {
	var locations []Location
	for _, harnessID := range Supported() {
		found, ok := LocationsFor(harnessID, projectDir)
		if ok {
			locations = append(locations, found...)
		}
	}
	return locations
}
