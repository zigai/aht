package crush

import (
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
)

func (crushHarness) Locations(projectDir string) []harness.Location {
	configDir := crushConfigDir()
	globalShellConfig := crushGlobalShellConfigPath()
	globalConfig := ""
	if globalDir := crushGlobalConfigDir(); globalDir != "" {
		globalConfig = filepath.Join(globalDir, "crush.json")
	}
	dataConfig := ""
	if dataHome := crushDataHome(harness.HomeDir()); dataHome != "" {
		dataConfig = filepath.Join(dataHome, "crush.json")
	}
	projectConfigs := []string{".crushrc", "crushrc", ".crush.json", "crush.json", filepath.Join(".crush", "crush.json")}

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, globalShellConfig, globalConfig, "/etc/crush/crush.json", dataConfig).
		Global(harness.LocationKindMCP, globalShellConfig, globalConfig).
		Global(harness.LocationKindSkills, globalSkillDirs(configDir)...).
		Global(harness.LocationKindCommands, filepath.Join(configDir, "commands")).
		Home(harness.LocationKindCommands, filepath.Join(".crush", "commands")).
		Project(harness.LocationKindConfig, projectConfigs...).
		Project(harness.LocationKindMCP, projectConfigs...).
		Project(harness.LocationKindInstructions, "AGENTS.md", "agents.md", "Agents.md", "CRUSH.md", "CRUSH.local.md", "crush.md", "crush.local.md", "Crush.md", "Crush.local.md", "CLAUDE.md", "CLAUDE.local.md", "GEMINI.md", "gemini.md", ".cursorrules", filepath.Join(".cursor", "rules"), filepath.Join(".github", "copilot-instructions.md")).
		Project(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".crush", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".cursor", "skills")).
		Project(harness.LocationKindCommands, filepath.Join(".crush", "commands")).
		Locations()
}

func globalSkillDirs(configDir string) []string {
	if dir := os.Getenv("CRUSH_SKILLS_DIR"); dir != "" {
		return []string{dir}
	}
	var dirs []string
	if configDir != "" {
		dirs = append(dirs, filepath.Join(configDir, "skills"), filepath.Join(filepath.Dir(configDir), "agents", "skills"))
	}
	if home := harness.HomeDir(); home != "" {
		dirs = append(dirs, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"))
	}

	return dirs
}
