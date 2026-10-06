package hermes

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (hermesHarness) Locations(projectDir string) []harness.Location {
	home := hermesHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.yaml")).
		Global(harness.LocationKindInstructions, filepath.Join(home, "SOUL.md")).
		Global(harness.LocationKindSkills, filepath.Join(home, "skills")).
		Project(harness.LocationKindInstructions, ".hermes.md", "HERMES.md", "AGENTS.md", "CLAUDE.md").
		Project(harness.LocationKindSkills, filepath.Join(".hermes", "skills"), filepath.Join(".agents", "skills")).
		Locations()
}
