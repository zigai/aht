package codex

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (codexHarness) Locations(projectDir string) []harness.Location {
	home := codexHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.toml")).
		Global(harness.LocationKindInstructions, filepath.Join(home, "AGENTS.override.md"), filepath.Join(home, "AGENTS.md")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Project(harness.LocationKindConfig, filepath.Join(".codex", "config.toml")).
		Project(harness.LocationKindInstructions, "AGENTS.override.md", "AGENTS.md").
		Project(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Locations()
}
