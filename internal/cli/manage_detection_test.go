package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/pkg/registry"
)

func TestManageDetectionTestHumanOutput(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	screenPath := filepath.Join(fixtureDir, "codex_screen.txt")
	screenContent := "Would you like to run the following command?\n[y/N]"
	if err := os.WriteFile(screenPath, []byte(screenContent), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("default human output without screen echo", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", screenPath}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected clean stderr, got: %s", stderr.String())
		}
		out := stdout.String()
		for _, required := range []string{
			"Harness:             codex",
			"Manifest source:     bundled:codex",
			"Effective activity:  waiting",
			"Reason:              manifest_rule",
			"Winning rule:        permission_prompt",
			"Rule", "State", "Priority", "Region", "Match", "Reason",
		} {
			if !strings.Contains(out, required) {
				t.Fatalf("stdout missing %q:\n%s", required, out)
			}
		}
		// Confirm raw screen is not echoed by default
		if strings.Contains(out, "Screen:") || strings.Contains(out, "[y/N]") {
			t.Fatalf("screen text unexpectedly leaked in default output:\n%s", out)
		}
	})

	t.Run("human output with --show-screen", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", screenPath, "--show-screen"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Screen:") || !strings.Contains(out, "[y/N]") {
			t.Fatalf("expected screen echo with --show-screen, got:\n%s", out)
		}
	})
}

//nolint:cyclop,gocognit // test scenarios cover JSON inspection shape and screen separation
func TestManageDetectionTestJSONOutput(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	screenPath := filepath.Join(fixtureDir, "claude_screen.txt")
	screenContent := "Thinking… esc to interrupt\n"
	if err := os.WriteFile(screenPath, []byte(screenContent), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("json output without screen echo", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"--json", "manage", "detection", "test", "claude", "--screen", screenPath}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("expected clean stderr, got: %s", stderr.String())
		}

		raw := stdout.String()
		if !strings.HasPrefix(raw, "{\n  \"harness\": \"claude\",\n") {
			t.Fatalf("JSON output not formatted with 2-space indentation:\n%s", raw)
		}

		var inspection agentstate.Inspection
		if err := json.Unmarshal(stdout.Bytes(), &inspection); err != nil {
			t.Fatalf("failed to unmarshal JSON: %v\nOutput: %s", err, raw)
		}

		if inspection.Harness != registry.HarnessClaude {
			t.Fatalf("harness = %s, want claude", inspection.Harness)
		}
		if inspection.WinningRule != "working_interruptible" {
			t.Fatalf("winning rule = %s, want working_interruptible", inspection.WinningRule)
		}
		if inspection.Decision.Activity != registry.ActivityRunning {
			t.Fatalf("decision activity = %s, want running", inspection.Decision.Activity)
		}
		if len(inspection.Candidates) == 0 {
			t.Fatal("candidates list is empty")
		}
		if inspection.Screen != "" {
			t.Fatalf("screen unexpectedly populated without --show-screen: %q", inspection.Screen)
		}

		// Ensure matchers are populated
		foundWinner := false
		for _, c := range inspection.Candidates {
			if c.ID == "working_interruptible" {
				foundWinner = true
				if !c.Matched || !c.Winner || len(c.Matchers) == 0 {
					t.Fatalf("candidate %s = %#v", c.ID, c)
				}
			}
		}
		if !foundWinner {
			t.Fatal("winner rule candidate not found in candidates list")
		}
	})

	t.Run("json output with --show-screen", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"--json", "manage", "detection", "test", "claude", "--screen", screenPath, "--show-screen"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr.String())
		}
		var inspection agentstate.Inspection
		if err := json.Unmarshal(stdout.Bytes(), &inspection); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if inspection.Screen != screenContent {
			t.Fatalf("inspection.Screen = %q, want %q", inspection.Screen, screenContent)
		}
	})
}

func TestManageDetectionTestStdin(t *testing.T) {
	t.Parallel()

	stdin := strings.NewReader("Permission required: allow / deny\n")
	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--json", "manage", "detection", "test", "opencode", "--screen", "-"}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr: %s", code, stderr.String())
	}

	var inspection agentstate.Inspection
	if err := json.Unmarshal(stdout.Bytes(), &inspection); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if inspection.WinningRule != "permission_prompt" || inspection.Decision.Activity != registry.ActivityWaiting {
		t.Fatalf("stdin detection inspection = %#v", inspection)
	}
}

func TestManageDetectionTestNoMatch(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	screenPath := filepath.Join(fixtureDir, "nomatch_screen.txt")
	if err := os.WriteFile(screenPath, []byte("random unmatched terminal text\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--json", "manage", "detection", "test", "codex", "--screen", screenPath}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("normal no-match exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var inspection agentstate.Inspection
	if err := json.Unmarshal(stdout.Bytes(), &inspection); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if inspection.WinningRule != "" {
		t.Fatalf("winning rule = %q, want empty", inspection.WinningRule)
	}
	if inspection.Decision.Activity != registry.ActivityUnknown {
		t.Fatalf("activity = %s, want unknown", inspection.Decision.Activity)
	}
	if inspection.Decision.Reason != "no_rule_matched" {
		t.Fatalf("reason = %s, want no_rule_matched", inspection.Decision.Reason)
	}
	for _, c := range inspection.Candidates {
		if c.Matched || c.Winner {
			t.Fatalf("candidate %s matched = %v, winner = %v", c.ID, c.Matched, c.Winner)
		}
	}
}

func TestManageDetectionTestValidationErrors(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	screenPath := filepath.Join(fixtureDir, "screen.txt")
	if err := os.WriteFile(screenPath, []byte("test screen\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		args        []string
		want        int
		errContains string
	}{
		{
			name:        "missing harness arg",
			args:        []string{"manage", "detection", "test"},
			want:        exitCodeUsage,
			errContains: "harness argument is required",
		},
		{
			name:        "missing --screen flag",
			args:        []string{"manage", "detection", "test", "codex"},
			want:        exitCodeUsage,
			errContains: "--screen is required",
		},
		{
			name:        "conflicting --manifest and --config-dir",
			args:        []string{"manage", "detection", "test", "codex", "--screen", screenPath, "--manifest", "m.toml", "--config-dir", "d"},
			want:        exitCodeUsage,
			errContains: "cannot be used together",
		},
		{
			name:        "unknown harness with ambient loading",
			args:        []string{"manage", "detection", "test", "totally_unknown_agent_123", "--screen", screenPath},
			want:        exitCodeUsage,
			errContains: "unknown harness",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := executeCLI(context.Background(), tc.args, nil, &stdout, &stderr)
			if code != tc.want {
				t.Fatalf("code = %d, want %d (stderr: %s)", code, tc.want, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.errContains) {
				t.Fatalf("stderr = %q, want containing %q", stderr.String(), tc.errContains)
			}
		})
	}
}

//nolint:cyclop,gocognit // test scenarios verify each explicit vs ambient error case
func TestManageDetectionTestExplicitVsAmbientErrors(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	screenPath := filepath.Join(fixtureDir, "screen.txt")
	if err := os.WriteFile(screenPath, []byte("sample line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit malformed manifest fails with exit code 1", func(t *testing.T) {
		t.Parallel()
		brokenManifestPath := filepath.Join(fixtureDir, "broken.toml")
		if err := os.WriteFile(brokenManifestPath, []byte("broken TOML [["), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", screenPath, "--manifest", brokenManifestPath}, nil, &stdout, &stderr)
		if code != exitCodeGeneral {
			t.Fatalf("exit code = %d, want %d (general error)", code, exitCodeGeneral)
		}
		if !strings.Contains(stderr.String(), "parsing detection manifest") {
			t.Fatalf("stderr = %q, want parse error", stderr.String())
		}
	})

	t.Run("ambient malformed override falls back to bundled with warning and exit code 0", func(t *testing.T) {
		t.Parallel()
		configDir := t.TempDir()
		overridePath := filepath.Join(configDir, "codex.toml")
		if err := os.WriteFile(overridePath, []byte("broken TOML [["), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", screenPath, "--config-dir", configDir}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (bundled fallback), stderr: %s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Warning:") || !strings.Contains(out, "ignoring invalid local override") {
			t.Fatalf("expected warning in output, got:\n%s", out)
		}
		if !strings.Contains(out, "bundled:codex") {
			t.Fatalf("expected bundled:codex source, got:\n%s", out)
		}
	})

	t.Run("nonexistent screen fixture fails with exit code 1", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", filepath.Join(fixtureDir, "missing.txt")}, nil, &stdout, &stderr)
		if code != exitCodeGeneral {
			t.Fatalf("exit code = %d, want %d", code, exitCodeGeneral)
		}
		if !strings.Contains(stderr.String(), "open screen fixture") {
			t.Fatalf("stderr = %q, want open screen fixture error", stderr.String())
		}
	})

	t.Run("oversized screen fixture fails with exit code 1", func(t *testing.T) {
		t.Parallel()
		hugeScreenPath := filepath.Join(fixtureDir, "huge_screen.txt")
		hugeData := make([]byte, agentstate.MaxScreenBytes+10)
		if err := os.WriteFile(hugeScreenPath, hugeData, 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "detection", "test", "codex", "--screen", hugeScreenPath}, nil, &stdout, &stderr)
		if code != exitCodeGeneral {
			t.Fatalf("exit code = %d, want %d", code, exitCodeGeneral)
		}
		if !strings.Contains(stderr.String(), "screen fixture exceeds 2 MiB") {
			t.Fatalf("stderr = %q, want screen fixture exceeds 2 MiB", stderr.String())
		}
	})
}

func TestDetectionUsesConfiguredManifestDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "aht.toml")
	manifest := `version = 1
agent = "codex"
[[rules]]
id = "configured"
state = "idle"
all = ["fixture"]
`
	if err := os.WriteFile(filepath.Join(dir, "codex.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[detection]\nmanifests_dir = '"+dir+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := executeCLI(t.Context(), []string{"--config", cfg, "--json", "manage", "detection", "test", "codex", "--screen", "-"}, strings.NewReader("fixture"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got agentstate.Inspection
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WinningRule != "configured" {
		t.Fatalf("ignored configured manifest: %+v", got)
	}
}
