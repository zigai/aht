package goose

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/zigai/aht/v2/internal/harness"
)

func (gooseHarness) Locations(projectDir string) []harness.Location {
	dir := gooseConfigDir()

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, filepath.Join(dir, "config.yaml")).
		Global(harness.LocationKindInstructions, filepath.Join(dir, "AGENTS.md"), filepath.Join(dir, ".goosehints")).
		Global(harness.LocationKindSkills, filepath.Join(dir, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills")).
		Project(harness.LocationKindInstructions, "AGENTS.md", ".goosehints").
		Project(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".goose", "skills"), filepath.Join(".claude", "skills")).
		Locations()
}

func gooseConfigDir() string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(root) {
		return filepath.Join(root, "config")
	}
	home := harness.HomeDir()
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}

		return filepath.Join(appData, "Block", "goose", "config")
	}

	return filepath.Join(home, ".config", "goose")
}
