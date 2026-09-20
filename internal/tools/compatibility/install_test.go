package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstallationsPinTheSelectedRelease(t *testing.T) {
	for _, spec := range defaultCatalog {
		if spec.Source != "weekly" {
			t.Run(spec.ID, func(t *testing.T) { assertVersionPin(t, spec) })
		}
	}
}

func assertVersionPin(t *testing.T, spec harnessSpec) {
	t.Helper()
	version := "1.2.3"
	if spec.ID == "goose" {
		version = "v1.2.3"
	}
	work, bin := t.TempDir(), t.TempDir()
	commands, err := installationCommands(spec, version, work, bin)
	if err != nil {
		t.Fatalf("unexpected error generating commands for %s: %v", spec.ID, err)
	}
	if len(commands) == 0 {
		t.Fatalf("no commands generated for %s", spec.ID)
	}

	assertNoLatestReferences(t, spec, commands)

	switch spec.Source {
	case "npm":
		assertNPMPin(t, spec, version, commands)
	case "pypi":
		assertPyPIPin(t, spec, version, commands)
	case "github":
		assertGitHubPin(t, spec, version, work, commands)
	case "channel":
		assertChannelPin(t, spec, version, work, bin, commands)
	}
}

func assertNoLatestReferences(t *testing.T, spec harnessSpec, commands []installCommand) {
	t.Helper()
	for _, cmd := range commands {
		for _, arg := range cmd.Args {
			if strings.Contains(arg, "latest") {
				t.Errorf("%s argument %q resolves latest during install", spec.ID, arg)
			}
		}
		for _, env := range cmd.Env {
			if strings.Contains(env, "latest") {
				t.Errorf("%s env %q resolves latest during install", spec.ID, env)
			}
		}
	}
}

func assertNPMPin(t *testing.T, spec harnessSpec, version string, commands []installCommand) {
	t.Helper()
	if len(commands) != 1 {
		t.Fatalf("%s expected 1 command, got %d", spec.ID, len(commands))
	}
	cmd := commands[0]
	if cmd.Name != "npm" {
		t.Errorf("%s executable = %q, want npm", spec.ID, cmd.Name)
	}
	wantArgs := []string{"install", "--global", spec.Package + "@" + version}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("%s args = %#v, want %#v", spec.ID, cmd.Args, wantArgs)
	}
}

func assertPyPIPin(t *testing.T, spec harnessSpec, version string, commands []installCommand) {
	t.Helper()
	if len(commands) != 1 {
		t.Fatalf("%s expected 1 command, got %d", spec.ID, len(commands))
	}
	cmd := commands[0]
	if cmd.Name != "uv" {
		t.Errorf("%s executable = %q, want uv", spec.ID, cmd.Name)
	}
	expectedPkg := spec.Package + "==" + version
	if spec.ID == "hermes" {
		expectedPkg = spec.Package + "[acp]==" + version
	}
	wantArgs := []string{"tool", "install", expectedPkg}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("%s args = %#v, want %#v", spec.ID, cmd.Args, wantArgs)
	}
}

func assertGitHubPin(t *testing.T, spec harnessSpec, version, work string, commands []installCommand) {
	t.Helper()
	if len(commands) < 2 {
		t.Fatalf("%s expected at least 2 commands for github source, got %d", spec.ID, len(commands))
	}
	dl := commands[0]
	if dl.Name != "curl" {
		t.Errorf("%s download executable = %q, want curl", spec.ID, dl.Name)
	}
	wantURL := "https://github.com/" + spec.Repo + "/releases/download/" + version + "/" + spec.Asset
	wantAssetPath := filepath.Join(work, spec.Asset)
	if len(dl.Args) < 2 || dl.Args[len(dl.Args)-1] != wantURL || dl.Args[len(dl.Args)-2] != wantAssetPath {
		t.Errorf("%s download args = %#v, want URL %q and target %q", spec.ID, dl.Args, wantURL, wantAssetPath)
	}
}

func assertChannelPin(t *testing.T, spec harnessSpec, version, work, bin string, commands []installCommand) {
	t.Helper()
	if len(commands) != 2 {
		t.Fatalf("%s expected 2 commands for channel source, got %d", spec.ID, len(commands))
	}
	dl := commands[0]
	if dl.Name != "curl" {
		t.Errorf("%s download executable = %q, want curl", spec.ID, dl.Name)
	}
	script := filepath.Join(work, "install-grok.sh")
	run := commands[1]
	if run.Name != "bash" || !slices.Equal(run.Args, []string{script, version}) {
		t.Errorf("%s run command = %#v, want bash %s %s", spec.ID, run, script, version)
	}
	if !slices.Equal(run.Env, []string{"GROK_BIN_DIR=" + bin}) {
		t.Errorf("%s run env = %#v, want GROK_BIN_DIR=%s", spec.ID, run.Env, bin)
	}
}

func TestNativeReleaseInstallations(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()

	// Goose
	gooseCommands, err := installationCommands(testHarness(t, "goose"), "v1.2.3", work, bin)
	if err != nil {
		t.Fatalf("goose commands error: %v", err)
	}
	if len(gooseCommands) != 2 {
		t.Fatalf("goose commands count = %d, want 2", len(gooseCommands))
	}
	gooseRun := gooseCommands[1]
	if gooseRun.Name != "bash" || !slices.Equal(gooseRun.Args, []string{filepath.Join(work, "download_cli.sh")}) {
		t.Errorf("goose execution command = %#v, want bash with asset", gooseRun)
	}
	wantEnv := []string{"GOOSE_VERSION=v1.2.3", "GOOSE_BIN_DIR=" + bin, "CONFIGURE=false"}
	if !slices.Equal(gooseRun.Env, wantEnv) {
		t.Errorf("goose env = %#v, want %#v", gooseRun.Env, wantEnv)
	}

	// Antigravity (agy)
	agyCommands, err := installationCommands(testHarness(t, "agy"), "1.2.3", work, bin)
	if err != nil {
		t.Fatalf("agy commands error: %v", err)
	}
	if len(agyCommands) != 3 {
		t.Fatalf("agy commands count = %d, want 3", len(agyCommands))
	}
	tarCmd := agyCommands[1]
	if tarCmd.Name != "tar" || !slices.Equal(tarCmd.Args, []string{"-xzf", filepath.Join(work, "agy_cli_linux_x64.tar.gz"), "-C", work, "antigravity"}) {
		t.Errorf("agy extraction = %#v", tarCmd)
	}
	installCmd := agyCommands[2]
	if installCmd.Name != "install" || !slices.Equal(installCmd.Args, []string{"-m", "0755", filepath.Join(work, "antigravity"), filepath.Join(bin, "agy")}) {
		t.Errorf("agy install binary command = %#v", installCmd)
	}
}

func TestInvalidInstallVersionRunsNothing(t *testing.T) {
	for _, id := range []string{"droid", "grok", "cursor"} {
		spec := testHarness(t, id)
		for _, version := range []string{"latest", "1.2.3; echo wrong"} {
			if commands, err := installationCommands(spec, version, t.TempDir(), t.TempDir()); err == nil || len(commands) != 0 {
				t.Fatalf("accepted %s %q", id, version)
			}
		}
	}
}
