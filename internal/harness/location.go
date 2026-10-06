package harness

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	LocationKindConfig       LocationKind = "config"
	LocationKindInstructions LocationKind = "instructions"
	LocationKindSkills       LocationKind = "skills"
	LocationKindCommands     LocationKind = "commands"
	LocationKindMCP          LocationKind = "mcp"

	LocationScopeGlobal  LocationScope = "global"
	LocationScopeProject LocationScope = "project"
)

type (
	LocationKind  string
	LocationScope string
)

// Location is a file or directory a harness documents as user-facing
// configuration. It names where the harness looks; the path may not exist.
type Location struct {
	Kind  LocationKind
	Scope LocationScope
	Path  string
}

// LocationProvider is an optional adapter capability that lists the documented
// user-facing configuration locations. Project locations are relative to
// projectDir and are omitted when it is empty.
type LocationProvider interface {
	Locations(projectDir string) []Location
}

// LocationBuilder collects the locations an adapter reports.
type LocationBuilder struct {
	projectDir string
	locations  []Location
}

func NewLocationBuilder(projectDir string) *LocationBuilder {
	return &LocationBuilder{projectDir: projectDir, locations: nil}
}

// HomeDir returns the user's home directory, or an empty string when none is known.
func HomeDir() string {
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(home)
}

// Global adds locations at absolute paths. It skips relative paths, which
// appear when a base directory cannot be resolved and would depend on the
// working directory.
func (builder *LocationBuilder) Global(kind LocationKind, paths ...string) *LocationBuilder {
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			continue
		}
		builder.locations = append(builder.locations, Location{Kind: kind, Scope: LocationScopeGlobal, Path: path})
	}
	return builder
}

// Home adds global locations under the user's home directory. It adds nothing
// when the home directory is unknown.
func (builder *LocationBuilder) Home(kind LocationKind, relative ...string) *LocationBuilder {
	home := HomeDir()
	if home == "" {
		return builder
	}
	for _, path := range relative {
		builder.Global(kind, filepath.Join(home, path))
	}
	return builder
}

// Project adds locations under the project directory. It adds nothing when the
// project directory is empty.
func (builder *LocationBuilder) Project(kind LocationKind, relative ...string) *LocationBuilder {
	if builder.projectDir == "" {
		return builder
	}
	for _, path := range relative {
		builder.locations = append(builder.locations, Location{Kind: kind, Scope: LocationScopeProject, Path: filepath.Join(builder.projectDir, path)})
	}
	return builder
}

func (builder *LocationBuilder) Locations() []Location {
	return builder.locations
}
