package opencode

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (opencodeHarness) Locations(projectDir string) []harness.Location {
	dir := opencodeUserConfigDir()

	builder := harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "opencode.json"))
	if value := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); value != "" {
		builder.Global(harness.LocationKindConfig, value)
	}
	if value := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); value != "" {
		builder.Global(harness.LocationKindConfig, value)
	}

	return builder.
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, "opencode.json").
		Project(harness.LocationKindInstructions, "AGENTS.md", "CLAUDE.md").
		Project(harness.LocationKindSkills, filepath.Join(".opencode", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".opencode", "commands")).
		Locations()
}
