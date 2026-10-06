package amp

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (ampHarness) Locations(projectDir string) []harness.Location {
	dir := ampConfigDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json"), filepath.Join(dir, "settings.jsonc")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md")).
		Home(harness.LocationKindInstructions, filepath.Join(".config", "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".config", "agents", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindConfig, filepath.Join(".amp", "settings.json"), filepath.Join(".amp", "settings.jsonc")).
		Project(harness.LocationKindInstructions, "AGENTS.md", "AGENT.md", "CLAUDE.md").
		Project(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Locations()
}
