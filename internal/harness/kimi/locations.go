package kimi

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (kimiCodeHarness) Locations(projectDir string) []harness.Location {
	home := kimiCodeHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.toml"), filepath.Join(home, "tui.toml")).
		Global(harness.LocationKindMCP, filepath.Join(home, "mcp.json")).
		Global(harness.LocationKindInstructions, filepath.Join(home, "AGENTS.md"), filepath.Join(home, "SYSTEM.md")).
		Global(harness.LocationKindSkills, filepath.Join(home, "skills")).
		Home(harness.LocationKindInstructions, filepath.Join(".agents", "AGENTS.md")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Project(harness.LocationKindInstructions, "AGENTS.md", filepath.Join(".kimi-code", "AGENTS.md")).
		Project(harness.LocationKindSkills, filepath.Join(".kimi-code", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindMCP, filepath.Join(".kimi-code", "mcp.json")).
		Locations()
}
