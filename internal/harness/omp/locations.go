package omp

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (ompHarness) Locations(projectDir string) []harness.Location {
	dir := ompAgentDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "config.yml"), filepath.Join(dir, "config.yaml")).
		Global(harness.LocationKindMCP, filepath.Join(dir, "mcp.json"), filepath.Join(dir, ".mcp.json")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, filepath.Join(".omp", "config.yml"), filepath.Join(".omp", "settings.json")).
		Project(harness.LocationKindMCP, filepath.Join(".omp", "mcp.json"), filepath.Join(".omp", ".mcp.json")).
		Project(harness.LocationKindInstructions, "AGENTS.md", filepath.Join(".omp", "AGENTS.md")).
		Project(harness.LocationKindSkills, filepath.Join(".omp", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".omp", "commands")).
		Locations()
}
