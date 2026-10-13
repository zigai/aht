package prime

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (primeHarness) Locations(projectDir string) []harness.Location {
	dir := primeAgentDir()
	builder := harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindMCP, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindInstructions,
			filepath.Join(dir, "AGENTS.md"),
			filepath.Join(dir, "CLAUDE.md"),
			filepath.Join(dir, "SYSTEM.md"),
			filepath.Join(dir, "APPEND_SYSTEM.md"),
		).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "prompts"))

	return builder.
		Project(harness.LocationKindConfig, filepath.Join(".prime", "agent", "settings.json")).
		Project(harness.LocationKindMCP, filepath.Join(".prime", "agent", "settings.json")).
		Project(harness.LocationKindInstructions,
			"AGENTS.md",
			"CLAUDE.md",
			filepath.Join(".prime", "agent", "SYSTEM.md"),
			filepath.Join(".prime", "agent", "APPEND_SYSTEM.md"),
		).
		Project(harness.LocationKindSkills,
			filepath.Join(".prime", "agent", "skills"),
			filepath.Join(".agents", "skills"),
		).
		Project(harness.LocationKindCommands, filepath.Join(".prime", "agent", "prompts")).
		Locations()
}
