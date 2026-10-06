package kilo

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (kiloHarness) Locations(projectDir string) []harness.Location {
	dir := kiloConfigDir()

	builder := harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "kilo.json"), filepath.Join(dir, "kilo.jsonc"))
	if value := strings.TrimSpace(os.Getenv("KILO_CONFIG")); value != "" {
		builder.Global(harness.LocationKindConfig, value)
	}

	return builder.
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md")).
		Home(harness.LocationKindSkills, filepath.Join(".kilo", "skills"), filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, "kilo.json", "kilo.jsonc", filepath.Join(".kilo", "kilo.json"), filepath.Join(".kilo", "kilo.jsonc")).
		Project(harness.LocationKindInstructions, "AGENTS.md", "CLAUDE.md", "CONTEXT.md", filepath.Join(".kilo", "rules")).
		Project(harness.LocationKindSkills, filepath.Join(".kilo", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".kilo", "commands")).
		Locations()
}
