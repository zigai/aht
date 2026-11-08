package goose

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (gooseHarness) Locations(projectDir string) []harness.Location {
	dir := configDir(harness.HomeDir())

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "config.yaml")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, ".goosehints")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills")).
		Project(harness.LocationKindInstructions, "AGENTS.md", ".goosehints").
		Project(harness.LocationKindSkills, filepath.Join(
			".agents",
			"skills",
		), filepath.Join(
			".goose",
			"skills",
		), filepath.Join(
			".claude",
			"skills",
		)).
		Locations()
}
