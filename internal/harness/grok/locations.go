package grok

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (grokHarness) Locations(projectDir string) []harness.Location {
	home := grokHome()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(home, "config.toml")).
		Global(harness.LocationKindInstructions, filepath.Join(home, "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(home, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Project(harness.LocationKindConfig, filepath.Join(".grok", "config.toml")).
		Project(harness.LocationKindInstructions, "AGENTS.md", "CLAUDE.md", filepath.Join(".grok", "rules")).
		Project(harness.LocationKindSkills, filepath.Join(".grok", "skills")).
		Locations()
}
