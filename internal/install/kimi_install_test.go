package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestInstallKimiCodeWritesHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kimi-code install to report changed")
	}
	if result.Path != filepath.Join(dir, "config.toml") {
		t.Fatalf("unexpected path %q", result.Path)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}
	text := string(data)
	for _, event := range []string{
		hookEventSessionStart,
		"UserPromptSubmit",
		"PreToolUse",
		"PostToolUse",
		"PostToolUseFailure",
		hookEventStop,
		"StopFailure",
		"SubagentStart",
		"SubagentStop",
		"PreCompact",
		"PostCompact",
		"SessionEnd",
	} {
		if !strings.Contains(text, `event = "`+event+`"`) {
			t.Fatalf("expected %s hook in snippet: %s", event, text)
		}
	}
	for _, want := range []string{
		`matcher = "startup|resume"`,
		`event = "SessionEnd"` + "\nmatcher = \"exit\"",
		"--raw-stdin",
		"--quiet",
		"aht_integration=kimi-code-hook",
		managedMarker,
		"--activity idle --event SessionStart",
		"--activity running --event UserPromptSubmit",
		"--activity running --event PreToolUse",
		"--activity failed --event StopFailure",
		"--presence gone --event SessionEnd",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in snippet: %s", want, text)
		}
	}
	for _, forbidden := range []string{"statusMessage", "hooks =", "type ="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unexpected unsupported Kimi hook field %q in snippet: %s", forbidden, text)
		}
	}
}

func TestInstallKimiCodeReplacesManagedBlockAndPreservesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating kimi-code dir: %v", err)
	}
	oldConfig := strings.Join([]string{
		`default_model = "kimi-code/kimi-for-coding"`,
		"",
		kimiCodeManagedStart,
		"[[hooks]]",
		`event = "` + hookEventSessionStart + `"`,
		`command = "old-aht report --harness kimi-code --source kimi-code-hook"`,
		kimiCodeManagedEnd,
		"",
		"[thinking]",
		`mode = "auto"`,
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("writing old config: %v", err)
	}

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kimi-code install to replace old managed block")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading installed hooks: %v", err)
	}
	text := string(data)
	for _, want := range []string{`default_model = "kimi-code/kimi-for-coding"`, "[thinking]", `mode = "auto"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected preserved config %q in snippet: %s", want, text)
		}
	}
	if strings.Contains(text, "old-aht") {
		t.Fatalf("expected old managed hook to be removed: %s", text)
	}

	second, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       false,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if second.Changed {
		t.Fatal("expected second kimi-code install to be idempotent")
	}
}

func TestInstallKimiCodeDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", dir)

	result, err := Run(Options{
		Harness:      registry.HarnessKimiCode,
		Binary:       testInstallBinary,
		TargetBinary: "",
		DryRun:       true,
		Force:        false,
		UseShim:      false,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected kimi-code dry-run to report changed")
	}
	if !strings.Contains(result.Snippet, `event = "`+hookEventSessionStart+`"`) {
		t.Fatalf("expected dry-run snippet to include Kimi hooks: %s", result.Snippet)
	}
	if _, err := os.Stat(result.Path); err == nil {
		t.Fatalf("expected dry-run not to write %s", result.Path)
	}
}
