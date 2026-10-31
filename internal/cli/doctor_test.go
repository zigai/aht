package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/install"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDoctorIsConciseUnlessVerbose(t *testing.T) {
	path := prepareDoctorEnvironment(t)

	concise := executeDoctorSurface(t, "--store", path, "--json", "doctor")
	if ids := doctorCheckIDs(
		t,
		concise,
	); slices.Contains(ids, "integrations.claude") ||
		slices.Contains(ids, "integrations.pi") {
		t.Fatalf("concise doctor includes uninstalled integrations: %v", ids)
	}

	executeDoctorSurface(
		t,
		"manage",
		"integrations",
		"install",
		"claude",
		"--binary",
		defaultInstallBinary(),
	)

	concise = executeDoctorSurface(t, "--store", path, "--json", "doctor")
	if ids := doctorCheckIDs(
		t,
		concise,
	); !slices.Contains(ids, "integrations.claude") ||
		slices.Contains(ids, "integrations.pi") {
		t.Fatalf(
			"concise doctor omitted installed integration or included uninstalled ones: %v",
			ids,
		)
	}

	verbose := executeDoctorSurface(t, "--store", path, "--json", "doctor", "--verbose")
	if ids := doctorCheckIDs(
		t,
		verbose,
	); !slices.Contains(ids, "integrations.pi") ||
		!slices.Contains(ids, "integrations.claude") {
		t.Fatalf("verbose doctor omitted integration rows: %v", ids)
	}
}

func TestDoctorFixtureIgnoresInheritedOmpProfile(t *testing.T) {
	foreignProfile := filepath.Join(t.TempDir(), ".omp", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", foreignProfile)

	installed, err := install.Run(
		t.Context(),
		install.Options{Harness: registry.Harness("omp"), Binary: defaultInstallBinary()},
	)
	if err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}

	path := prepareDoctorEnvironment(t)

	output := executeDoctorSurface(t, "--store", path, "--json", "doctor")
	if ids := doctorCheckIDs(t, output); slices.Contains(ids, "integrations.omp") {
		t.Fatalf("isolated doctor inspected inherited profile:\n%s", output)
	}

	after, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatalf("read foreign OMP integration after doctor: %v", err)
	}

	if !bytes.Equal(before, after) {
		t.Fatalf("doctor fixture modified foreign OMP integration %s", installed.Path)
	}
}

func TestDoctorHandlesInvalidDetectionManifests(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     manage.DoctorStatus
	}{
		{
			name:     "bundled override warns and falls back",
			manifest: "pi.toml",
			want:     manage.DoctorStatusWarn,
		},
		{name: "local-only manifest fails", manifest: "agy.toml", want: manage.DoctorStatusFail},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := prepareDoctorEnvironment(t)

			detectionDir := agentstate.DefaultConfigDir()
			if err := os.MkdirAll(detectionDir, 0o700); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(
				filepath.Join(detectionDir, test.manifest),
				[]byte("invalid ["),
				0o600,
			); err != nil {
				t.Fatal(err)
			}

			var stdout bytes.Buffer

			_ = runTestCLI(
				t.Context(),
				[]string{"--store", path, "--json", "doctor"},
				&stdout,
				&bytes.Buffer{},
			)

			report := decodeDoctorReport(t, stdout.String())
			for _, check := range report.Checks {
				if check.ID == "detection.manifests" && check.Status == test.want {
					return
				}
			}

			t.Fatalf("detection.manifests is not %s:\n%s", test.want, stdout.String())
		})
	}
}

func TestDoctorTextReportPipesCleanly(t *testing.T) {
	path := prepareDoctorEnvironment(t)

	home := filepath.Dir(path)
	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte(
			"[ui]\nsrot = \"updated\"\nmisspelled_key_with_a_long_name_that_would_wrap_on_a_narrow_terminal = true\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	code := executeCLI(t.Context(), []string{"--store", path, "doctor"}, nil, &stdout, &stderr)
	output := stdout.String()

	if code != exitCodeGeneral {
		t.Fatalf(
			"exit code = %d, want %d with a failing check; stderr=%s",
			code,
			exitCodeGeneral,
			stderr.String(),
		)
	}

	if strings.Contains(output, "\x1b[") {
		t.Fatalf("piped doctor output contains escape codes:\n%s", output)
	}

	if strings.Contains(output, home) {
		t.Fatalf("doctor output shows the home directory instead of ~:\n%s", output)
	}

	if !strings.HasPrefix(output, "aht ") {
		t.Fatalf("doctor output does not open with the identity line:\n%s", output)
	}

	if !strings.Contains(
		output,
		`"ui.misspelled_key_with_a_long_name_that_would_wrap_on_a_narrow_terminal" at ~/`,
	) {
		t.Fatalf("piped doctor output wrapped or dropped a finding:\n%s", output)
	}

	for _, line := range []string{"\nConfig\n", "\n  fail  ", "        fix: ", " fail, "} {
		if !strings.Contains(output, line) &&
			!strings.Contains(output, strings.TrimPrefix(line, "\n")) {
			t.Fatalf("doctor output lacks %q:\n%s", line, output)
		}
	}

	if !strings.Contains(output, "need attention.\n") &&
		!strings.Contains(output, "needs attention.\n") {
		t.Fatalf("doctor output lacks a verdict line:\n%s", output)
	}
}

func TestDoctorJSONHeaderNamesLocations(t *testing.T) {
	path := prepareDoctorEnvironment(t)
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(path), "config.toml"),
		[]byte("[ui]\nsort = \"updated\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	report := decodeDoctorReport(t, executeDoctorSurface(t, "--store", path, "--json", "doctor"))
	if report.Paths.Store.Source != "--store" || !strings.HasPrefix(report.Paths.Store.Path, "~/") {
		t.Fatalf("store path = %+v, want a ~ path chosen by --store", report.Paths.Store)
	}

	if report.Env["AHT_CONFIG"] != "~/config.toml" {
		t.Fatalf("env = %v, want AHT_CONFIG abbreviated", report.Env)
	}

	if len(report.Paths.Config) == 0 {
		t.Fatal("doctor header lists no config files")
	}

	last := report.Paths.Config[len(report.Paths.Config)-1]
	if last.Path != "~/config.toml" || last.Status != doctorConfigLoaded ||
		last.Source != "AHT_CONFIG" {
		t.Fatalf("config entry = %+v, want ~/config.toml loaded via AHT_CONFIG", last)
	}

	report = decodeDoctorReport(
		t,
		executeDoctorSurface(t, "--store", path, "--no-config", "--json", "doctor"),
	)
	for _, file := range report.Paths.Config {
		if file.Status != doctorConfigSkipped {
			t.Fatalf("config entry under --no-config = %+v, want skipped", file)
		}
	}
}

func TestDoctorHeaderDistinguishesAbsentConfigAfterFailedLoad(t *testing.T) {
	path := prepareDoctorEnvironment(t)
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(path), "config.toml"),
		[]byte("not toml ["),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer

	_ = runTestCLI(
		t.Context(),
		[]string{"--store", path, "--json", "doctor"},
		&stdout,
		&bytes.Buffer{},
	)

	statuses := make(map[string]string)
	for _, file := range decodeDoctorReport(t, stdout.String()).Paths.Config {
		statuses[file.Source] = file.Status
	}

	if statuses[""] != doctorConfigNotFound || statuses["AHT_CONFIG"] != doctorConfigNotLoaded {
		t.Fatalf(
			"config statuses after a failed load = %v, want the absent "+
				"user file not_found and the broken AHT_CONFIG file "+
				"not_loaded",
			statuses,
		)
	}
}

func TestDoctorFixRequiresConsent(t *testing.T) {
	path := prepareDoctorEnvironment(t)

	var stdout, stderr bytes.Buffer
	if code := executeCLI(
		t.Context(),
		[]string{"--store", path, "doctor", "--yes"},
		nil,
		&stdout,
		&stderr,
	); code != exitCodeUsage {
		t.Fatalf("doctor --yes without --fix exit code = %d, want %d", code, exitCodeUsage)
	}

	state := filepath.Dir(path)

	before, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	if code := executeCLI(
		t.Context(),
		[]string{"--store", path, "doctor", "--fix"},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
	); code != exitCodeUsage ||
		!strings.Contains(stderr.String(), "--yes") {
		t.Fatalf(
			"doctor --fix without a terminal = %d, stderr %q; want a usage error naming --yes",
			code,
			stderr.String(),
		)
	}

	after, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}

	if len(before) != len(after) {
		t.Fatalf("declined fixes changed %s: %v -> %v", state, before, after)
	}
}

func TestEnvironmentErrorsPointToDoctor(t *testing.T) {
	path := prepareDoctorEnvironment(t)
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(path), "config.toml"),
		[]byte("[ui]\nsrot = \"updated\"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	executeCLI(t.Context(), []string{"--store", path, "list"}, nil, &bytes.Buffer{}, &stderr)

	if !strings.HasSuffix(stderr.String(), "Run 'aht doctor' for details.\n") {
		t.Fatalf("config failure stderr = %q, want a doctor hint", stderr.String())
	}

	stderr.Reset()
	executeCLI(
		t.Context(),
		[]string{"--store", path, "--config", filepath.Join(t.TempDir(), "missing.toml"), "list"},
		nil,
		&bytes.Buffer{},
		&stderr,
	)

	if strings.Contains(stderr.String(), "aht doctor") {
		t.Fatalf("missing --config path stderr = %q, want no doctor hint", stderr.String())
	}
}

func TestDoctorIsSecondToLastCommand(t *testing.T) {
	var stdout bytes.Buffer
	if err := runTestCLI(t.Context(), []string{"--help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	var commands []string

	listing := false

	for line := range strings.Lines(stdout.String()) {
		switch {
		case strings.HasPrefix(line, "Available Commands:"):
			listing = true
		case listing && strings.TrimSpace(line) == "":
			listing = false
		case listing:
			commands = append(commands, strings.Fields(line)[0])
		}
	}

	if len(commands) < 2 || commands[len(commands)-2] != "doctor" ||
		commands[len(commands)-1] != "help" {
		t.Fatalf("root commands = %v, want doctor before help at the end", commands)
	}
}

func prepareDoctorEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Like the upgrade fixture, redirect every harness override rather than
	// letting an inherited profile escape the temporary home directory.
	for _, key := range []string{
		"XDG_CONFIG_HOME",
		"XDG_STATE_HOME",
		"AHT_CONFIG",
		"CLAUDE_CONFIG_DIR",
		"CODEX_HOME",
		"COPILOT_HOME",
		"CLINE_DIR",
		"CLINE_HOOKS_DIR",
		"KIMI_CODE_HOME",
		"GROK_HOME",
		"PI_CODING_AGENT_DIR",
		"PI_CONFIG_DIR",
		"HERMES_HOME",
		"OPENCODE_CONFIG_DIR",
		"OPENCODE_CONFIG",
		"KILO_CONFIG_DIR",
		registry.StateDirEnv,
	} {
		t.Setenv(key, filepath.Join(home, key))
	}

	t.Setenv("AHT_CONFIG", filepath.Join(home, "config.toml"))
	t.Setenv(registry.StorePathEnv, "")
	t.Setenv("OMP_PROFILE", "default")
	t.Setenv("PI_PROFILE", "default")

	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(
		"PATH",
		fakeBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
	)

	return filepath.Join(home, "sessions.json")
}

func executeDoctorSurface(t *testing.T, args ...string) string {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf(
			"aht %v: %v\nstdout:\n%s\nstderr:\n%s",
			args,
			err,
			stdout.String(),
			stderr.String(),
		)
	}

	return stdout.String()
}

func decodeDoctorReport(t *testing.T, output string) doctorReport {
	t.Helper()

	var report doctorReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("decode doctor JSON: %v\nstdout:\n%s", err, output)
	}

	return report
}

func doctorCheckIDs(t *testing.T, output string) []string {
	t.Helper()
	report := decodeDoctorReport(t, output)

	ids := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		ids = append(ids, check.ID)
	}

	return ids
}
