package harness_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestLocationsForReportsExistingPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "AGENTS.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	locations, ok := harness.LocationsFor(registry.Harness("codex"), projectDir)
	if !ok {
		t.Fatal("LocationsFor(codex) reported an unknown harness")
	}
	existing := make(map[string]bool)
	for _, location := range locations {
		if location.Harness != registry.Harness("codex") {
			t.Errorf("location %+v is labeled with the wrong harness", location)
		}
		if location.Exists {
			existing[location.Path] = true
		}
	}
	want := map[string]bool{
		filepath.Join(home, ".codex", "config.toml"): true,
		filepath.Join(projectDir, "AGENTS.md"):       true,
	}
	for path := range want {
		if !existing[path] {
			t.Errorf("%s not reported as existing; existing = %v", path, existing)
		}
	}
}

type locationCase struct {
	name       string
	harness    registry.Harness
	path       string
	global     bool
	kind       harness.LocationKind
	isDir      bool
	override   string
	overrideAt string
}

func TestLocationsForDocumentedNativePaths(t *testing.T) {
	cases := []locationCase{
		{name: "claude user mcp", harness: "claude", path: ".claude.json", global: true, kind: harness.LocationKindMCP},
		{name: "omp yaml settings", harness: "omp", path: ".omp/agent/config.yaml", global: true, kind: harness.LocationKindConfig},
		{name: "omp project settings", harness: "omp", path: ".omp/settings.json", kind: harness.LocationKindConfig},
		{name: "omp project mcp alternative", harness: "omp", path: ".omp/.mcp.json", kind: harness.LocationKindMCP},
		{name: "pi user override", harness: "pi", path: ".pi/agent/AGENTS.override.md", global: true, kind: harness.LocationKindInstructions},
		{name: "pi project override", harness: "pi", path: "AGENTS.override.md", kind: harness.LocationKindInstructions},
		{name: "opencode jsonc", harness: "opencode", path: "opencode.jsonc", kind: harness.LocationKindConfig},
		{name: "opencode custom commands", harness: "opencode", path: "custom-open/commands", global: true, kind: harness.LocationKindCommands, isDir: true, override: "OPENCODE_CONFIG_DIR", overrideAt: "custom-open"},
		{name: "cline custom settings", harness: "cline", path: "custom-cline/settings.json", global: true, kind: harness.LocationKindConfig, override: "CLINE_GLOBAL_SETTINGS_PATH", overrideAt: "custom-cline/settings.json"},
		{name: "cline custom mcp", harness: "cline", path: "custom-cline/mcp.json", global: true, kind: harness.LocationKindMCP, override: "CLINE_MCP_SETTINGS_PATH", overrideAt: "custom-cline/mcp.json"},
		{name: "amp claude skills", harness: "amp", path: ".claude/skills", kind: harness.LocationKindSkills, isDir: true},
		{name: "goose legacy skills", harness: "goose", path: ".goose/skills", kind: harness.LocationKindSkills, isDir: true},
		{name: "copilot claude instructions", harness: "copilot", path: ".claude/CLAUDE.md", kind: harness.LocationKindInstructions},
		{name: "openclaw profile workspace", harness: "openclaw", path: ".openclaw-work/workspace/AGENTS.md", global: true, kind: harness.LocationKindInstructions, override: "OPENCLAW_PROFILE", overrideAt: "work"},
		{name: "openclaw workspace persona", harness: "openclaw", path: ".openclaw/workspace/SOUL.md", global: true, kind: harness.LocationKindInstructions},
		{name: "openclaw custom workspace skills", harness: "openclaw", path: "custom-work/skills", global: true, kind: harness.LocationKindSkills, isDir: true, override: "OPENCLAW_WORKSPACE_DIR", overrideAt: "custom-work"},
		{name: "antigravity rule manifest", harness: "agy", path: ".agents/rules.json", kind: harness.LocationKindConfig},
		{name: "antigravity legacy rules", harness: "agy", path: ".agent/rules", kind: harness.LocationKindInstructions, isDir: true},
		{name: "cursor codex skills", harness: "cursor", path: ".codex/skills", kind: harness.LocationKindSkills, isDir: true},
		{name: "droid personal skills", harness: "droid", path: ".agent/skills", global: true, kind: harness.LocationKindSkills, isDir: true},
		{name: "droid project instructions", harness: "droid", path: ".agent/AGENTS.md", kind: harness.LocationKindInstructions},
		{name: "grok shared commands", harness: "grok", path: ".agents/commands", global: true, kind: harness.LocationKindCommands, isDir: true},
		{name: "grok claude rules", harness: "grok", path: ".claude/rules", kind: harness.LocationKindInstructions, isDir: true},
		{name: "hermes override instructions", harness: "hermes", path: "AGENTS.override.md", kind: harness.LocationKindInstructions},
		{name: "kilo project context", harness: "kilo", path: "CONTEXT.md", kind: harness.LocationKindInstructions},
		{name: "kimi user skills", harness: "kimi-code", path: ".kimi-code/skills", global: true, kind: harness.LocationKindSkills, isDir: true},
		{name: "kimi project skills", harness: "kimi-code", path: ".kimi-code/skills", kind: harness.LocationKindSkills, isDir: true},
		{name: "kimi relocated instructions", harness: "kimi-code", path: "custom-agent/AGENTS.md", global: true, kind: harness.LocationKindInstructions, override: "KIMI_CODE_HOME", overrideAt: "custom-agent"},
		{name: "kimi project mcp", harness: "kimi-code", path: ".kimi-code/mcp.json", kind: harness.LocationKindMCP},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			checkDocumentedLocation(t, tt)
		})
	}
}

func checkDocumentedLocation(t *testing.T, tt locationCase) {
	t.Helper()
	path, project := createLocationFixture(t, tt)
	locations, ok := harness.LocationsFor(tt.harness, project)
	if !ok {
		t.Fatalf("%s is unknown", tt.harness)
	}
	scope := harness.LocationScopeProject
	if tt.global {
		scope = harness.LocationScopeGlobal
	}
	for _, location := range locations {
		if location.Path == path && location.Kind == tt.kind && location.Scope == scope && location.Exists {
			return
		}
	}
	t.Errorf("%s did not report existing %s location %q: %v", tt.harness, tt.kind, path, locations)
}

func createLocationFixture(t *testing.T, tt locationCase) (string, string) {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	for _, key := range []string{"PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "OMP_PROFILE", "PI_PROFILE", "OPENCODE_CONFIG_DIR", "OPENCLAW_STATE_DIR", "OPENCLAW_HOME", "OPENCLAW_PROFILE", "OPENCLAW_WORKSPACE_DIR", "CLAUDE_CONFIG_DIR", "CLINE_DATA_DIR", "CLINE_GLOBAL_SETTINGS_PATH", "CLINE_MCP_SETTINGS_PATH", "KIMI_CODE_HOME"} {
		t.Setenv(key, "")
	}
	base := project
	if tt.global {
		base = home
	}
	path := filepath.Join(base, tt.path)
	if tt.override != "" {
		value := tt.overrideAt
		if tt.override != "OPENCLAW_PROFILE" {
			value = filepath.Join(base, value)
		}
		t.Setenv(tt.override, value)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if tt.isDir {
		if err := os.Mkdir(path, 0o750); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, project
}

func TestCodexReportsSystemLocations(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Codex system paths are Unix-only")
	}
	locations, ok := harness.LocationsFor("codex", t.TempDir())
	if !ok {
		t.Fatal("codex is unknown")
	}
	for path, kind := range map[string]harness.LocationKind{
		"/etc/codex/config.toml": harness.LocationKindConfig,
		"/etc/codex/skills":      harness.LocationKindSkills,
	} {
		found := false
		for _, location := range locations {
			if location.Path == path && location.Kind == kind && location.Scope == harness.LocationScopeGlobal {
				found = true
			}
		}
		if !found {
			t.Errorf("missing Codex system path %s", path)
		}
	}
}

func TestOpenClawDoesNotClaimUnrelatedProjectWorkspace(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCLAW_STATE_DIR", "")
	t.Setenv("OPENCLAW_WORKSPACE_DIR", "")
	path := filepath.Join(project, "AGENTS.md")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	locations, ok := harness.LocationsFor("openclaw", project)
	if !ok {
		t.Fatal("openclaw is unknown")
	}
	for _, location := range locations {
		if location.Path == path {
			t.Errorf("unrelated project instructions reported as OpenClaw workspace: %+v", location)
		}
	}
}
