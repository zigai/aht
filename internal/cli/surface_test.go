package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRootHelpShowsCompactCanonicalSurface(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	for _, command := range []string{"list", "watch", "info", "stop", "manage"} {
		if !strings.Contains(help, command) {
			t.Errorf("root help does not show %q:\n%s", command, help)
		}
	}
	for _, command := range []string{"admin", "setup", "integrations", "monitor", "registry", "doctor", "detection", "detect", "show", "explain", "install-hooks", "observe", "service", "report", "wire", "get", "gc", "queue", "drain", "path", "agy-hook"} {
		if strings.Contains(help, "\n   "+command+" ") || strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("root help exposes internal, nested, or removed command %q:\n%s", command, help)
		}
	}
}

func TestManageHelpShowsCanonicalSurface(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"manage", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := stdout.String()
	for _, command := range []string{"setup", "upgrade", "integrations", "tracker", "state", "doctor", "config", "detection"} {
		if !strings.Contains(help, command) {
			t.Errorf("manage help does not show %q:\n%s", command, help)
		}
	}
	for _, command := range []string{"monitor", "registry"} {
		if strings.Contains(help, "\n   "+command+" ") || strings.Contains(help, "\n  "+command+" ") {
			t.Errorf("manage help exposes removed command %q:\n%s", command, help)
		}
	}
}

func TestMachineFacingCommandsAndDestructiveResetAreExplicit(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"hook", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Integration protocol endpoint") {
		t.Fatalf("hook help does not identify the hook protocol endpoint:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "tracker", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Service entry point") {
		t.Fatalf("tracker help does not identify the service entry point:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := runTestCLI(context.Background(), []string{"manage", "state", "reset", "--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--force") || !strings.Contains(strings.ToLower(stdout.String()), "confirm destructive state reset") {
		t.Fatalf("state reset help omits confirmation requirement:\n%s", stdout.String())
	}
}

func TestEveryHiddenInternalCommandHasCallableHelp(t *testing.T) {
	commands := []string{"report", "hook"}
	for _, command := range commands {
		var stdout bytes.Buffer
		if err := runTestCLI(context.Background(), []string{command, "--help"}, &stdout, &bytes.Buffer{}); err != nil {
			t.Errorf("%s --help failed: %v", command, err)
			continue
		}
		if !strings.Contains(stdout.String(), "USAGE:") && !strings.Contains(stdout.String(), "Usage:") {
			t.Errorf("%s internal help missing usage: %q", command, stdout.String())
		}
	}
}

func TestJSONInvocationFailureLeavesStdoutEmpty(t *testing.T) {
	for _, args := range [][]string{{"--json", "info"}, {"--json", "list", "--not-a-flag"}, {"--json", "unknown"}, {"--json", "report", "--harness", "codex", "--presence", "live"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := executeCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code == 0 {
			t.Fatalf("invalid invocation succeeded: %v", args)
		}
		if stdout.Len() != 0 {
			t.Fatalf("%v wrote stdout %q", args, stdout.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("%v omitted stderr error", args)
		}
	}
}

func TestSubcommandFlagsAreScoped(t *testing.T) {
	tests := [][]string{
		{"manage", "integrations", "remove", "codex", "--force"},
		{"manage", "integrations", "status", "codex", "--dry-run"},
		{"manage", "tracker", "status", "--dry-run"},
		{"manage", "tracker", "disable", "--grace-period", "1s"},
		{"watch", "--summary"},
	}
	for _, args := range tests {
		err := runTestCLI(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || (!strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "flag provided but not defined")) {
			t.Errorf("%v error = %v, want unknown flag", args, err)
		}
	}
}

func TestVersionHonorsJSONFlag(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--json", "--version"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON version: %v; output=%q", err, stdout.String())
	}
	if result["version"] == "" {
		t.Fatalf("missing version in %#v", result)
	}
}

func TestVersionDefaultsToHumanOutput(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	if err := runTestCLI(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") || !strings.HasPrefix(stdout.String(), "aht ") {
		t.Fatalf("version default output = %q", stdout.String())
	}
}
