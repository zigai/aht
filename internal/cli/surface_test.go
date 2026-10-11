package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

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
	if strings.TrimSpace(stdout.String()) == "" || strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("version default output = %q", stdout.String())
	}
}
