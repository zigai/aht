package openclaw

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (openclawHarness) Locations(projectDir string) []harness.Location {
	state := openclawStateDir()
	config := filepath.Join(state, "openclaw.json")
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG_PATH")); value != "" {
		config = value
	}
	workspace := filepath.Join(state, "workspace")
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_WORKSPACE_DIR")); value != "" {
		workspace = value
	}

	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, config).
		Global(harness.LocationKindInstructions, filepath.Join(workspace, "AGENTS.md")).
		Global(harness.LocationKindSkills, filepath.Join(state, "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
		Project(harness.LocationKindInstructions, "AGENTS.md").
		Project(harness.LocationKindSkills, "skills", filepath.Join(".agents", "skills")).
		Locations()
}

func openclawStateDir() string {
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_STATE_DIR")); value != "" {
		return value
	}
	home := strings.TrimSpace(os.Getenv("OPENCLAW_HOME"))
	if home == "" {
		home = harness.HomeDir()
	}

	return filepath.Join(home, ".openclaw")
}
