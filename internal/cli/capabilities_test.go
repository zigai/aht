package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/harness"
)

func TestManageCapabilitiesTable(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"manage", "capabilities"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Harness", "Authority", "Start", "End", "Run/Idle", "Wait", "Process", "Catalog", "TTY/MUX", "Install", "Resume", "Screen", "pi", "codex", "claude"} {
		if !strings.Contains(output, expected) {
			t.Errorf("output missing expected token %q:\n%s", expected, output)
		}
	}
}

func TestManageCapabilitiesJSON(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := executeCLI(context.Background(), []string{"--json", "manage", "capabilities"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	var caps []harness.Capabilities
	if err := json.Unmarshal(stdout.Bytes(), &caps); err != nil {
		t.Fatalf("failed to decode JSON capabilities: %v\noutput: %s", err, stdout.String())
	}
	if len(caps) == 0 {
		t.Fatal("empty capabilities slice returned")
	}
	foundPi := false
	for _, c := range caps {
		if c.Harness == "pi" {
			foundPi = true
			if c.Authority != "hook" {
				t.Fatalf("Pi authority = %q, want hook", c.Authority)
			}
		}
	}
	if !foundPi {
		t.Fatal("pi capability missing from list")
	}
}

func TestManageCapabilitiesSingleHarness(t *testing.T) {
	t.Parallel()
	t.Run("human details", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "capabilities", "--harness", "pi"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
		}
		output := stdout.String()
		for _, expected := range []string{"Harness:", "pi", "Authority:", "hook", "Installable:", "yes", "Resumable:", "yes"} {
			if !strings.Contains(output, expected) {
				t.Errorf("single harness output missing %q:\n%s", expected, output)
			}
		}
	})

	t.Run("json object", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"--json", "manage", "capabilities", "--harness", "codex"}, strings.NewReader(""), &stdout, &stderr)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr = %s", code, stderr.String())
		}
		var c harness.Capabilities
		if err := json.Unmarshal(stdout.Bytes(), &c); err != nil {
			t.Fatalf("failed to decode single capability JSON: %v\noutput: %s", err, stdout.String())
		}
		if c.Harness != "codex" || c.Authority != "screen" || !c.ScreenSupport {
			t.Fatalf("unexpected codex capabilities: %+v", c)
		}
	})

	t.Run("invalid harness exits usage code 2", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		code := executeCLI(context.Background(), []string{"manage", "capabilities", "--harness", "invalid-harness"}, strings.NewReader(""), &stdout, &stderr)
		if code != exitCodeUsage {
			t.Fatalf("exit code = %d, want %d", code, exitCodeUsage)
		}
		if !strings.Contains(stderr.String(), "unknown harness") {
			t.Fatalf("stderr = %q, want unknown harness error", stderr.String())
		}
	})
}
