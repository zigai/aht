package droid

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (droidHarness) Locations(projectDir string) []harness.Location {
	dir := droidConfigDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json"), filepath.Join(dir, "settings.local.json")).
		Global(harness.LocationKindMCP, filepath.Join(dir, "mcp.json")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md")).
		Home(harness.LocationKindInstructions, filepath.Join(".agents", "AGENTS.md"), filepath.Join(".agent", "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".agent", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, filepath.Join(".factory", "settings.json"), filepath.Join(".factory", "settings.local.json")).
		Project(harness.LocationKindMCP, filepath.Join(".factory", "mcp.json")).
		Project(harness.LocationKindInstructions, "AGENTS.md", "CLAUDE.md", filepath.Join(".factory", "AGENTS.md"), filepath.Join(".agents", "AGENTS.md"), filepath.Join(".agent", "AGENTS.md")).
		Project(harness.LocationKindSkills, filepath.Join(".factory", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".agent", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".factory", "commands")).
		Locations()
}
