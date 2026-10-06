package kimi

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (kimiCodeHarness) Locations(projectDir string) []harness.Location {
	home := kimiCodeHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.toml")).
		Global(harness.LocationKindMCP, filepath.Join(home, "mcp.json")).
		Home(harness.LocationKindSkills, filepath.Join(".kimi", "skills"), filepath.Join(".config", "agents", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindInstructions, "AGENTS.md", filepath.Join(".kimi", "AGENTS.md")).
		Project(harness.LocationKindSkills, filepath.Join(".kimi", "skills"), filepath.Join(".agents", "skills")).
		Locations()
}
