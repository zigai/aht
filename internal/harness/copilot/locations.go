package copilot

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (copilotHarness) Locations(projectDir string) []harness.Location {
	home := copilotHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.json"), filepath.Join(home, "settings.json")).
		Global(harness.LocationKindInstructions, filepath.Join(home, "copilot-instructions.md"), filepath.Join(home, "instructions")).
		Global(harness.LocationKindMCP, filepath.Join(home, "mcp-config.json")).
		Global(harness.LocationKindSkills, filepath.Join(home, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Project(harness.LocationKindInstructions, filepath.Join(".github", "copilot-instructions.md"), filepath.Join(".github", "instructions"), "AGENTS.md", "CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "GEMINI.md").
		Project(harness.LocationKindSkills, filepath.Join(".github", "skills"), filepath.Join(".agents", "skills")).
		Locations()
}
