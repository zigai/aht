package cursor

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

func (cursorHarness) Locations(projectDir string) []harness.Location {
	return harness.NewLocationBuilder(projectDir).
		Global(harness.LocationKindConfig, cursorCLIConfigPaths()...).
		Global(harness.LocationKindMCP, filepath.Join(cursorHome(), "mcp.json")).
		Global(harness.LocationKindSkills, filepath.Join(cursorHome(), "skills")).
		Home(harness.LocationKindSkills, filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".codex", "skills")).
		Project(harness.LocationKindConfig, filepath.Join(".cursor", "cli.json")).
		Project(harness.LocationKindInstructions, "AGENTS.md", "CLAUDE.md", filepath.Join(".cursor", "rules")).
		Project(harness.LocationKindMCP, filepath.Join(".cursor", "mcp.json")).
		Project(harness.LocationKindSkills, filepath.Join(".cursor", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".codex", "skills")).
		Locations()
}

func cursorCLIConfigPaths() []string {
	if value := strings.TrimSpace(os.Getenv("CURSOR_CONFIG_DIR")); value != "" {
		return []string{filepath.Join(value, "cli-config.json")}
	}
	paths := []string{filepath.Join(cursorHome(), "cli-config.json")}
	if slices.Contains([]string{"linux", "freebsd", "openbsd", "netbsd"}, runtime.GOOS) {
		if value := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); value != "" {
			paths = append(paths, filepath.Join(value, "cursor", "cli-config.json"))
		}
	}

	return paths
}
