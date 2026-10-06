package cline

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (clineHarness) Locations(projectDir string) []harness.Location {
	config := clineConfigDir()
	settings := filepath.Join(clineDataDir(), "settings")

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, clineSettingsPath(settings, "CLINE_GLOBAL_SETTINGS_PATH", "global-settings.json")).
		Global(harness.LocationKindMCP, clineSettingsPath(settings, "CLINE_MCP_SETTINGS_PATH", "cline_mcp_settings.json")).
		Global(harness.LocationKindInstructions, filepath.Join(config, "rules")).
		Home(harness.LocationKindInstructions, filepath.Join(".agents", "AGENTS.md"), filepath.Join("Cline", "Rules"), filepath.Join("Documents", "Cline", "Rules")).
		Global(harness.LocationKindSkills, filepath.Join(config, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Global(harness.LocationKindCommands, filepath.Join(config, "workflows")).
		Home(harness.LocationKindCommands, filepath.Join("Documents", "Cline", "Workflows")).
		Project(harness.LocationKindInstructions, "AGENTS.md", ".clinerules", filepath.Join(".cline", "rules")).
		Project(harness.LocationKindSkills, filepath.Join(".clinerules", "skills"), filepath.Join(".cline", "skills"), filepath.Join(".agents", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".clinerules", "workflows"), filepath.Join(".cline", "workflows")).
		Locations()
}

func clineSettingsPath(dir, variable, name string) string {
	if value := strings.TrimSpace(os.Getenv(variable)); value != "" {
		return value
	}
	return filepath.Join(dir, name)
}

func clineDataDir() string {
	if value := strings.TrimSpace(os.Getenv("CLINE_DATA_DIR")); value != "" {
		return value
	}

	return filepath.Join(clineConfigDir(), "data")
}
