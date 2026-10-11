package openclaw

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (openclawHarness) Locations(_ string) []harness.Location {
	state := openclawStateDir()
	config := filepath.Join(state, "openclaw.json")
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_CONFIG_PATH")); value != "" {
		config = value
	}
	workspace := filepath.Join(state, "workspace")
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_WORKSPACE_DIR")); value != "" {
		workspace = value
	}

	return harness.NewLocationBuilder("").
		Global(harness.LocationKindConfig, config).
		Global(harness.LocationKindInstructions,
			filepath.Join(workspace, "AGENTS.md"),
			filepath.Join(workspace, "SOUL.md"),
			filepath.Join(workspace, "USER.md"),
			filepath.Join(workspace, "IDENTITY.md"),
			filepath.Join(workspace, "BOOT.md"),
			filepath.Join(workspace, "BOOTSTRAP.md"),
		).
		Global(harness.LocationKindSkills, filepath.Join(state, "skills"), filepath.Join(workspace, "skills"), filepath.Join(workspace, ".agents", "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills")).
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
	if home == "" {
		return ""
	}

	profile := strings.TrimSpace(os.Getenv("OPENCLAW_PROFILE"))
	if profile != "" && profile != "default" {
		return filepath.Join(home, ".openclaw-"+profile)
	}
	return filepath.Join(home, ".openclaw")
}
