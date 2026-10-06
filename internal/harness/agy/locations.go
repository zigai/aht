package agy

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (agyHarness) Locations(projectDir string) []harness.Location {
	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(agyConfigDir(), "settings.json")).
		Global(harness.LocationKindSkills, filepath.Join(agyConfigDir(), "skills")).
		Home(harness.LocationKindMCP, filepath.Join(".gemini", "config", "mcp_config.json")).
		Home(
			harness.LocationKindInstructions,
			filepath.Join(".gemini", "GEMINI.md"),
			filepath.Join(".gemini", "AGENTS.md"),
			filepath.Join(".gemini", "config", "GEMINI.md"),
			filepath.Join(".gemini", "config", "AGENTS.md"),
			filepath.Join(".gemini", "config", "rules"),
		).
		Project(harness.LocationKindMCP, filepath.Join(".agents", "mcp_config.json")).
		Project(
			harness.LocationKindInstructions,
			"AGENTS.md",
			"GEMINI.md",
			filepath.Join(".agents", "AGENTS.md"),
			filepath.Join(".agents", "GEMINI.md"),
			filepath.Join(".agents", "rules"),
		).
		Project(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Locations()
}
