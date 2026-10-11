package pi

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (piHarness) Locations(projectDir string) []harness.Location {
	dir := piAgentDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindMCP, filepath.Join(dir, "mcp.json")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.override.md"), filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, "AGENTS.MD"), filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "CLAUDE.MD")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "prompts")).
		Project(harness.LocationKindConfig, filepath.Join(".pi", "settings.json")).
		Project(harness.LocationKindMCP, filepath.Join(".pi", "mcp.json")).
		Project(harness.LocationKindInstructions, "AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD").
		Project(harness.LocationKindSkills, filepath.Join(".pi", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".pi", "prompts")).
		Locations()
}
