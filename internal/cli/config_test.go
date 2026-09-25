package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/strata"

	"github.com/zigai/aht/v2/internal/config"
	catalog "github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

//nolint:cyclop // integration test verifying full flag precedence matrix
func TestCLIConfigFlagPrecedenceAndDefaults(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	configPath := filepath.Join(tempDir, "config.toml")

	store := registry.NewJournal(storePath, catalog.Rules{})
	ctx := context.Background()

	// Seed 3 sessions with different harnesses, presence, and created timestamps
	now := time.Now().UTC()
	presentTrue := true
	presentFalse := false

	// Session 1: created earlier, live, claude
	_, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("claude"), At: now.Add(-10 * time.Minute), Subject: registry.ObservationIdentity{SessionID: "s1"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 101, StartIdentity: "pid101"}, Present: presentTrue}})
	if err != nil {
		t.Fatal(err)
	}

	// Session 2: created later, live, codex
	_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("codex"), At: now.Add(-5 * time.Minute), Subject: registry.ObservationIdentity{SessionID: "s2"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 102, StartIdentity: "pid102"}, Present: presentTrue}})
	if err != nil {
		t.Fatal(err)
	}

	// Session 3: gone, pi
	_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("pi"), At: now.Add(-2 * time.Minute), Subject: registry.ObservationIdentity{SessionID: "s3"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 103, StartIdentity: "pid103"}, Present: presentFalse}})
	if err != nil {
		t.Fatal(err)
	}

	// Config: UI default presence = live, sort = created, sort_desc = true
	cfgContent := `
[ui]
default_presence = "live"
sort = "created"
sort_desc = true
`
	if err := os.WriteFile(configPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Run aht list without presence/sort flags -> should show only live, sorted by created desc (s2 then s1)
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("list failed: %v", err)
	}

	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 live sessions, got %d", len(sessions))
	}
	if sessions[0].SessionID != "s2" || sessions[1].SessionID != "s1" {
		t.Fatalf("expected order [s2, s1], got [%s, %s]", sessions[0].SessionID, sessions[1].SessionID)
	}
	for _, s := range sessions {
		if s.Presence() != registry.PresenceLive {
			t.Fatalf("expected live session, got %s", s.Presence())
		}
	}

	// 2. Explicit flags override config defaults: --presence all --sort created --desc=false
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "list", "--presence", "all", "--sort", "created", "--desc=false"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("list override failed: %v", err)
	}

	sessions = nil
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected all 3 sessions, got %d", len(sessions))
	}
	if sessions[0].SessionID != "s1" || sessions[1].SessionID != "s2" || sessions[2].SessionID != "s3" {
		t.Fatalf("expected ascending order [s1, s2, s3], got [%s, %s, %s]", sessions[0].SessionID, sessions[1].SessionID, sessions[2].SessionID)
	}
}

//nolint:cyclop // integration test verifying session filtering and unhiding
func TestCLIConfigFiltering(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	configPath := filepath.Join(tempDir, "config.toml")

	store := registry.NewJournal(storePath, catalog.Rules{})
	ctx := context.Background()
	presentTrue := true

	// Session 1: copilot harness (to be ignored)
	_, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("copilot"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "copilot-sess"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 201, StartIdentity: "pid201", CWD: "/home/user/project"}, Present: presentTrue}})
	if err != nil {
		t.Fatal(err)
	}

	// Session 2: ignored path (/tmp/scratch)
	_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "scratch-sess"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 202, StartIdentity: "pid202", CWD: "/tmp/scratch"}, Present: presentTrue}})
	if err != nil {
		t.Fatal(err)
	}

	// Session 3: normal session
	_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("claude"), At: time.Now().UTC(), Subject: registry.ObservationIdentity{SessionID: "claude-proj"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 203, StartIdentity: "pid203", CWD: "/home/user/project"}, Present: presentTrue}})
	if err != nil {
		t.Fatal(err)
	}

	// Config: ignore copilot and /tmp/scratch
	cfgContent := `
[filter]
ignore_harnesses = ["copilot"]
ignore_paths = ["/tmp/scratch"]
`
	if err := os.WriteFile(configPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Without --agent: copilot is filtered out; /tmp/scratch is filtered out. Only claude remains.
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("list failed: %v", err)
	}

	var sessions []registry.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}

	if len(sessions) != 1 || sessions[0].SessionID != "claude-proj" {
		t.Fatalf("expected only claude-proj, got %+v", sessions)
	}

	// 2. With explicit --agent copilot: copilot is unhidden despite config ignore
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "list", "--agent", "copilot"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("list with --agent failed: %v", err)
	}

	sessions = nil
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal list output: %v", err)
	}

	if len(sessions) != 1 || sessions[0].SessionID != "copilot-sess" {
		t.Fatalf("expected copilot-sess to be unhidden, got %+v", sessions)
	}
}

func TestCLIConfigRetentionAutoCleanFallback(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	configPath := filepath.Join(tempDir, "config.toml")

	store := registry.NewJournal(storePath, catalog.Rules{})
	ctx := context.Background()
	presentGone := false

	now := time.Now().UTC()
	// Record 1: Gone 48h ago
	_, err := store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: now.Add(-48 * time.Hour), Subject: registry.ObservationIdentity{SessionID: "old-gone"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 301, StartIdentity: "pid301"}, Present: presentGone}})
	if err != nil {
		t.Fatal(err)
	}

	// Record 2: Gone 2h ago
	_, err = store.Observe(ctx, registry.Observation{Harness: registry.Harness("goose"), At: now.Add(-2 * time.Hour), Subject: registry.ObservationIdentity{SessionID: "recent-gone"}, Evidence: &registry.Sighting{Process: registry.ProcessIdentity{PID: 302, StartIdentity: "pid302"}, Present: presentGone}})
	if err != nil {
		t.Fatal(err)
	}

	// Config: max_gone_age = "24h"
	cfgContent := `
[retention]
max_gone_age = "24h"
`
	if err := os.WriteFile(configPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Run clean without --all or --older-than -> should use 24h from config
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "manage", "state", "clean"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage state clean failed: %v", err)
	}

	var res registry.GCResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal clean result: %v", err)
	}

	if res.Deleted != 1 || res.Remaining != 1 {
		t.Fatalf("expected deleted=1, remaining=1, got %+v", res)
	}

	remaining, err := store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].SessionID != "recent-gone" {
		t.Fatalf("expected only recent-gone to remain, got %+v", remaining)
	}
}

//nolint:cyclop // integration test verifying doctor checks and config commands
func TestCLIConfigDoctorAndManageConfig(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	configPath := filepath.Join(tempDir, "config.toml")

	cfgContent := `
[ui]
sort = "created"
time_format = "iso8601"

[tracker]
interval = "5s"
`
	if err := os.WriteFile(configPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. aht manage config path
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "path"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("config path failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != configPath {
		t.Fatalf("config path = %q, want %q", strings.TrimSpace(stdout.String()), configPath)
	}

	// 2. aht manage config path --json
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--json", "manage", "config", "path"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("config path --json failed: %v", err)
	}
	var pathMap map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &pathMap); err != nil || pathMap["path"] != configPath {
		t.Fatalf("config path JSON = %v, want %q", pathMap, configPath)
	}

	// 3. aht manage config show --json
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--json", "manage", "config", "show"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("config show --json failed: %v", err)
	}
	var loadedCfg config.Config
	if err := json.Unmarshal(stdout.Bytes(), &loadedCfg); err != nil {
		t.Fatalf("unmarshal config show JSON: %v", err)
	}
	if loadedCfg.UI.Sort != "created" || loadedCfg.UI.TimeFormat != "iso8601" || loadedCfg.Tracker.Interval != "5s" {
		t.Fatalf("loaded config mismatch: %+v", loadedCfg)
	}

	// 4. aht manage config show (TOML)
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "show"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("config show TOML failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "sort = 'created'") && !strings.Contains(stdout.String(), `sort = "created"`) {
		t.Fatalf("expected toml output to contain sort = created: %q", stdout.String())
	}

	// 5. aht manage doctor with valid config
	stdout.Reset()
	_ = runTestCLI(ctx, []string{"--config", configPath, "--store", storePath, "--json", "manage", "doctor"}, &stdout, &bytes.Buffer{})
	var docRes doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &docRes); err != nil {
		t.Fatalf("unmarshal doctor result: %v", err)
	}
	var configCheck *doctorCheck
	for i := range docRes.Checks {
		if docRes.Checks[i].Name == "config.file" {
			configCheck = &docRes.Checks[i]
			break
		}
	}
	if configCheck == nil {
		t.Fatal("doctor result missing config.file check")
	}
	if configCheck.Status != doctorOK {
		t.Fatalf("config.file status = %s, want ok; message: %s", configCheck.Status, configCheck.Message)
	}
	if !strings.Contains(configCheck.Message, "config file is valid") {
		t.Fatalf("config.file message = %q, want 'config file is valid'", configCheck.Message)
	}
}

func TestCLIConfigDoctorWithNoConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	readOnlyDir := filepath.Join(tempDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o755)
	})
	t.Setenv(config.ConfigEnv, filepath.Join(readOnlyDir, "does-not-exist.toml"))

	var stdout bytes.Buffer
	_ = runTestCLI(context.Background(), []string{"--store", storePath, "--json", "manage", "doctor"}, &stdout, &bytes.Buffer{})

	var docRes doctorResult
	if err := json.Unmarshal(stdout.Bytes(), &docRes); err != nil {
		t.Fatalf("unmarshal doctor result: %v", err)
	}
	var configCheck *doctorCheck
	for i := range docRes.Checks {
		if docRes.Checks[i].Name == "config.file" {
			configCheck = &docRes.Checks[i]
			break
		}
	}
	if configCheck == nil {
		t.Fatal("doctor result missing config.file check")
	}
	if configCheck.Status != doctorOK {
		t.Fatalf("config.file status = %s, want ok", configCheck.Status)
	}
	if !strings.Contains(configCheck.Message, "no config file present") {
		t.Fatalf("config.file message = %q, want no config file present", configCheck.Message)
	}
}

func TestProtocolCommandIsolationWithBrokenConfig(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	brokenConfigPath := filepath.Join(tempDir, "broken-config.toml")

	// Create intentionally malformed TOML
	if err := os.WriteFile(brokenConfigPath, []byte("invalid [[ toml syntax @@"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Set AHT_CONFIG environment variable pointing to broken config
	t.Setenv(config.ConfigEnv, brokenConfigPath)

	ctx := context.Background()

	// 1. Verify that 'aht report' executes successfully and records the observation
	var stderr bytes.Buffer
	if err := runTestCLI(ctx, []string{
		"--store", storePath,
		"report", "codex",
		"--session-id", "broken-cfg-test",
		"--event", "start",
		"--quiet",
	}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("aht report failed with broken config: %v; stderr=%s", err, stderr.String())
	}

	// Verify the session was written to store
	store := registry.NewJournal(storePath, catalog.Rules{})
	sessions, err := store.List(ctx, registry.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sessions {
		if s.SessionID == "broken-cfg-test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("session reported by isolated command was not found in store")
	}

	// 2. Verify that 'aht hook codex' succeeds even with broken config
	var hookStdout bytes.Buffer
	err = runTestCLI(ctx, []string{
		"--store", storePath,
		"--json",
		"hook", "codex",
	}, &hookStdout, &bytes.Buffer{})
	// Hook without stdin payload may error on missing payload, but MUST NOT fail on config loading
	if err != nil && strings.Contains(err.Error(), "broken-config.toml") {
		t.Fatalf("aht hook failed due to broken config: %v", err)
	}
}

func TestCLIFirstRunDoesNotCreateConfig(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	targetConfigPath := filepath.Join(tempDir, "config", "config.toml")
	t.Setenv(config.ConfigEnv, targetConfigPath)
	var stdout bytes.Buffer
	if err := runTestCLI(t.Context(), []string{"--store", storePath, "list"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("first run aht list failed: %v", err)
	}
	if _, err := os.Stat(targetConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config file unexpectedly created: %v", err)
	}
}

func TestCLIExplicitConfigNonCreation(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "store.json")
	nonExistentPath := filepath.Join(tempDir, "missing", "explicit-config.toml")

	var stdout, stderr bytes.Buffer
	err := runTestCLI(context.Background(), []string{"--config", nonExistentPath, "--store", storePath, "list"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error with nonexistent explicit --config, got nil")
	}
	if !strings.Contains(err.Error(), "config file not found") {
		t.Fatalf("expected 'config file not found' error, got: %v", err)
	}

	// Verify the file was NOT created
	if _, err := os.Stat(nonExistentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected explicit missing config to NOT be created, but stat returned: %v", err)
	}
}

//nolint:cyclop // integration test verifying manage config init lifecycle
func TestCLIManageConfigInit(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "init_test", "config.toml")
	ctx := context.Background()

	// 1. aht manage config init --json in clean directory
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", targetPath, "--json", "manage", "config", "init"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config init --json failed: %v", err)
	}

	var initRes map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &initRes); err != nil {
		t.Fatalf("unmarshal init json response: %v", err)
	}
	if initRes["created"] != true || initRes["path"] != targetPath {
		t.Fatalf("unexpected init response: %+v", initRes)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("reading created config: %v", err)
	}
	if string(content) != config.DefaultConfigTemplate() {
		t.Fatal("created config content does not match template")
	}

	// 2. Run again without --force (human mode): should inform user on stderr that file exists (F10)
	var stderr bytes.Buffer
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", targetPath, "manage", "config", "init"}, &stdout, &stderr); err != nil {
		t.Fatalf("manage config init without force failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "already exists") || !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("expected stderr to mention file already exists and --force, got: %q", stderr.String())
	}

	// 3. Run again without --force (--json mode)
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", targetPath, "--json", "manage", "config", "init"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config init --json without force failed: %v", err)
	}
	initRes = nil
	if err := json.Unmarshal(stdout.Bytes(), &initRes); err != nil {
		t.Fatalf("unmarshal init json response: %v", err)
	}
	if initRes["created"] != false {
		t.Fatalf("expected created=false for existing file, got %+v", initRes)
	}

	// 4. Overwrite file with custom content, then run with --force
	if err := os.WriteFile(targetPath, []byte("# custom"), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", targetPath, "manage", "config", "init", "--force"}, &stdout, &stderr); err != nil {
		t.Fatalf("manage config init --force failed: %v", err)
	}
	if !strings.Contains(stderr.String(), "created") {
		t.Fatalf("expected stderr to mention created, got: %q", stderr.String())
	}
	reRead, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(reRead) != config.DefaultConfigTemplate() {
		t.Fatal("file was not overwritten with default template on --force")
	}
}

func TestAutoCleanCLIFlagHonorsConfiguredMaxGoneAge(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Retention: config.RetentionConfig{
			AutoClean:  new(false),
			MaxGoneAge: "7d",
		},
	}
	var opts observeOptions
	applyTrackerAutoClean(&opts, cfg)
	opts.autoClean = true
	if opts.maxGoneAge != 7*24*time.Hour {
		t.Fatalf("enabling --auto-clean after loading default config uses %v retention, want 7d", opts.maxGoneAge)
	}
}

//nolint:cyclop // integration test verifying manage config set lifecycle
func TestCLIManageConfigSet(t *testing.T) {
	tempDir := t.TempDir()
	targetPath := filepath.Join(tempDir, "config.toml")
	ctx := context.Background()

	// 1. Set key in new config file -> creates template, sets key in-place, preserves comments
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", targetPath, "manage", "config", "set", "ui.sort", "created"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config set failed: %v", err)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target config: %v", err)
	}
	if !strings.Contains(string(content), "# AHT Configuration") {
		t.Fatal("manage config set dropped template comments")
	}

	// 2. Verify setting was applied
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", targetPath, "--json", "manage", "config", "show"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config show failed: %v", err)
	}
	var loaded config.Config
	if err := json.Unmarshal(stdout.Bytes(), &loaded); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if loaded.UI.Sort != "created" {
		t.Fatalf("expected ui.sort=created, got %q", loaded.UI.Sort)
	}

	// 3. Set another key via --json
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", targetPath, "--json", "manage", "config", "set", "ui.default_presence", "live"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config set --json failed: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal set result: %v", err)
	}
	if res["set"] != true || res["key"] != "ui.default_presence" || res["value"] != "live" {
		t.Fatalf("unexpected set result: %+v", res)
	}

	// 4. Setting invalid value fails validation and leaves file uncorrupted
	if err := runTestCLI(ctx, []string{"--config", targetPath, "manage", "config", "set", "ui.sort", "invalid_sort_key"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected invalid sort key to fail, got nil")
	}
	// File should still have ui.sort = "created"
	reRead, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reRead), "invalid_sort_key") {
		t.Fatal("file was corrupted by invalid setting")
	}

	// 5. --no-config disallows set
	if err := runTestCLI(ctx, []string{"--no-config", "manage", "config", "set", "ui.sort", "created"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected --no-config to disallow set, got nil")
	}
}

func TestCLIManageConfigRejectsOtherFormats(t *testing.T) {
	for _, ext := range []string{".yaml", ".json"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config"+ext)
			for _, args := range [][]string{
				{"--config", path, "manage", "config", "init"},
				{"--config", path, "manage", "config", "set", "ui.sort", "created"},
				{"--config", path, "manage", "config", "show"},
			} {
				if err := runTestCLI(t.Context(), args, &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, strata.ErrUnsupportedFormat) {
					t.Fatalf("%v: expected unsupported format, got %v", args, err)
				}
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported config file was created: %v", err)
			}
		})
	}
}

func TestCLIManageConfigIgnoresExistingUserYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aht", "config.yaml")
	t.Setenv(config.ConfigEnv, "")
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ui:\n  sort: updated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runTestCLI(t.Context(), []string{"manage", "config", "set", "ui.sort", "created"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "ui:\n  sort: updated\n" {
		t.Fatalf("user YAML changed: %q, error = %v", data, err)
	}
	tomlPath := filepath.Join(dir, "aht", "config.toml")
	cfg, resolved, err := config.Load("")
	if err != nil || cfg.UI.Sort != "created" || resolved != tomlPath {
		t.Fatalf("TOML config: sort = %q, path = %q, error = %v", cfg.UI.Sort, resolved, err)
	}
}

func TestCLIManageConfigShowProvenance(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	ctx := context.Background()

	if err := os.WriteFile(configPath, []byte("[ui]\nsort = \"created\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Text mode with --provenance
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "show", "--provenance"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config show --provenance failed: %v", err)
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "# Active configuration files") || !strings.Contains(outStr, "ui.sort") {
		t.Fatalf("expected provenance output to mention active files and ui.sort, got:\n%s", outStr)
	}

	// 2. JSON mode with --provenance
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--json", "manage", "config", "show", "--provenance"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config show --provenance --json failed: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal provenance json: %v", err)
	}
	if _, ok := res["active_files"]; !ok {
		t.Fatalf("missing active_files in provenance json: %+v", res)
	}
	if _, ok := res["origins"]; !ok {
		t.Fatalf("missing origins in provenance json: %+v", res)
	}
}

//nolint:cyclop // integration test verifying manage config get lifecycle
func TestCLIManageConfigGet(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	ctx := context.Background()

	if err := os.WriteFile(configPath, []byte("[ui]\nsort = \"created\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Plain text get
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "get", "ui.sort"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config get failed: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "created" {
		t.Fatalf("expected 'created', got %q", stdout.String())
	}

	// 2. Get with --provenance
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "get", "ui.sort", "--provenance"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config get with provenance failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "created") || !strings.Contains(stdout.String(), configPath) {
		t.Fatalf("expected value and config path in provenance get, got: %q", stdout.String())
	}

	// 3. Get with --json
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--json", "manage", "config", "get", "ui.sort"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config get --json failed: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json get: %v", err)
	}
	if res["key"] != "ui.sort" || res["value"] != "created" {
		t.Fatalf("unexpected json get result: %+v", res)
	}

	// 4. Get with --json and --provenance
	stdout.Reset()
	if err := runTestCLI(ctx, []string{"--config", configPath, "--json", "manage", "config", "get", "ui.sort", "--provenance"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config get --json --provenance failed: %v", err)
	}
	res = nil
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json get with provenance: %v", err)
	}
	if _, ok := res["origin"]; !ok {
		t.Fatalf("expected origin in result: %+v", res)
	}

	// 5. Unknown key fails
	if err := runTestCLI(ctx, []string{"--config", configPath, "manage", "config", "get", "nonexistent.key"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestCLIManageConfigSchema(t *testing.T) {
	ctx := context.Background()
	var stdout bytes.Buffer
	if err := runTestCLI(ctx, []string{"manage", "config", "schema"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("manage config schema failed: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &schema); err != nil {
		t.Fatalf("schema output is not valid json: %v", err)
	}
	if schema["title"] != "AHT Configuration" {
		t.Fatalf("unexpected schema title: %v", schema["title"])
	}
	if _, ok := schema["properties"]; !ok {
		t.Fatal("schema missing properties")
	}
}
