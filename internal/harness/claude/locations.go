package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (claudeHarness) Locations(projectDir string) []harness.Location {
	dir := claudeConfigDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "settings.json")).
		Global(harness.LocationKindMCP, claudeMCPConfigPath()).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "rules")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Global(harness.LocationKindCommands, filepath.Join(dir, "commands")).
		Project(harness.LocationKindConfig, filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json")).
		Project(harness.LocationKindInstructions, "CLAUDE.md", filepath.Join(".claude", "CLAUDE.md"), "CLAUDE.local.md", "AGENTS.md", filepath.Join(".claude", "AGENTS.md"), filepath.Join(".claude", "rules")).
		Project(harness.LocationKindSkills, filepath.Join(".claude", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".claude", "commands")).
		Project(harness.LocationKindMCP, ".mcp.json").
		Locations()
}

func claudeMCPConfigPath() string {
	if value := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); value != "" {
		return filepath.Join(value, ".claude.json")
	}
	return filepath.Join(harness.HomeDir(), ".claude.json")
}
