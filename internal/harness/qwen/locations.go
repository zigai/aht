package qwen

import (
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (qwenHarness) Locations(projectDir string) []harness.Location {
	dir := configDirectory(harness.HomeDir())

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindMCP, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "QWEN.md"), filepath.Join(dir, "rules")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, filepath.Join(".qwen", "settings.json")).
		Project(harness.LocationKindMCP, filepath.Join(".qwen", "settings.json")).
		Project(harness.LocationKindInstructions, "QWEN.md", filepath.Join(".qwen", "QWEN.local.md"), "AGENTS.md", filepath.Join(".qwen", "rules")).
		Project(harness.LocationKindSkills, filepath.Join(".qwen", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".qwen", "commands")).
		Locations()
}
