package manage_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/manage"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDoctorReportsEveryCheckWhenConfigIsInvalid(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	writeDoctorFile(t, filepath.Join(home, ".config", "aht", "config.toml"), "[ui]\nsrot = \"updated\"\n")

	result := newDoctorManager(t, home, "").Doctor(t.Context(), manage.DoctorOptions{})

	assertDoctorInvariants(t, result)
	configCheck := findDoctorCheck(t, result, "config.file")
	if configCheck.Status != manage.DoctorStatusFail || !strings.Contains(configCheck.Message, "ui.srot") {
		t.Fatalf("config.file = %+v, want a failure naming ui.srot", configCheck)
	}
	for _, id := range []string{"tracker.service", "detection.manifests"} {
		if check := findDoctorCheck(t, result, id); check.Status != manage.DoctorStatusSkip || check.Needs != "config.file" {
			t.Fatalf("%s = %+v, want skip needing config.file", id, check)
		}
	}
	for _, id := range []string{"install.path", "observer.platform", "observer.process-enumeration", "store.read", "tracker.health"} {
		findDoctorCheck(t, result, id)
	}
	if !result.Failed() {
		t.Fatal("Failed() = false with a failing config check")
	}
}

func TestDoctorNoConfigIgnoresConfigFiles(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	writeDoctorFile(t, filepath.Join(home, ".config", "aht", "config.toml"), "not toml [")

	result := newDoctorManager(t, home, "").Doctor(t.Context(), manage.DoctorOptions{NoConfig: true})

	assertDoctorInvariants(t, result)
	if check := findDoctorCheck(t, result, "config.file"); check.Status != manage.DoctorStatusSkip {
		t.Fatalf("config.file = %+v, want skip under NoConfig", check)
	}
}

func TestDoctorLeavesStateDirectoryUnchanged(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(stateDir, "state.json")
	manager := newDoctorManager(t, home, storePath)

	manager.Doctor(t.Context(), manage.DoctorOptions{})
	if entries := listDirectory(t, stateDir); len(entries) != 0 {
		t.Fatalf("doctor created %v in an empty state directory", entries)
	}

	presence := registry.PresenceLive
	store := registry.NewJournal(storePath, catalog.Rules{})
	if _, err := store.Observe(t.Context(), registry.Observation{Harness: registry.Harness("codex"), Subject: registry.ObservationIdentity{SessionID: "one"}, Evidence: &registry.Report{Claim: &presence}}); err != nil {
		t.Fatal(err)
	}
	before := listDirectory(t, stateDir)
	result := manager.Doctor(t.Context(), manage.DoctorOptions{})
	if after := listDirectory(t, stateDir); !slices.Equal(before, after) {
		t.Fatalf("state directory changed from %v to %v", before, after)
	}
	if check := findDoctorCheck(t, result, "store.read"); check.Status != manage.DoctorStatusOK || !strings.HasPrefix(check.Message, "1 session,") {
		t.Fatalf("store.read = %+v, want one stored session", check)
	}
}

func TestDoctorStoreFailuresOfferReset(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{name: "corrupt", content: "garbage", message: "Corrupt registry store"},
		{name: "old schema", content: `{"schema_version":2,"sessions":{}}`, message: "version 2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDoctorEnvironment(t)
			storePath := filepath.Join(home, "custom", "state.json")
			writeDoctorFile(t, storePath, test.content)

			result := newDoctorManager(t, home, storePath).Doctor(t.Context(), manage.DoctorOptions{})

			assertDoctorInvariants(t, result)
			check := findDoctorCheck(t, result, "store.read")
			if check.Status != manage.DoctorStatusFail || !strings.Contains(check.Message, test.message) {
				t.Fatalf("store.read = %+v, want failure containing %q", check, test.message)
			}
			if want := "aht --store " + storePath + " manage state reset --force"; check.Fix.Command != want {
				t.Fatalf("store.read fix = %q, want %q", check.Fix.Command, want)
			}
		})
	}
}

func TestDoctorChecksConfiguredManifestDirectory(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	manifests := filepath.Join(home, "manifests")
	writeDoctorFile(t, filepath.Join(manifests, "agy.toml"), "invalid [")
	writeDoctorFile(t, filepath.Join(home, ".config", "aht", "config.toml"), "[detection]\nmanifests_dir = \""+manifests+"\"\n")

	result := newDoctorManager(t, home, "").Doctor(t.Context(), manage.DoctorOptions{})

	assertDoctorInvariants(t, result)
	if check := findDoctorCheck(t, result, "detection.manifests"); check.Status != manage.DoctorStatusFail {
		t.Fatalf("detection.manifests = %+v, want failure from the configured directory", check)
	}
}

func TestDoctorListsEachExecutableOnPathOnce(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	first := writeExecutable(t, filepath.Join(home, "first", "aht"))
	linked := filepath.Join(home, "linked", "aht")
	if err := os.MkdirAll(filepath.Dir(linked), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, linked); err != nil {
		t.Fatal(err)
	}
	second := writeExecutable(t, filepath.Join(home, "second", "aht"))

	tests := []struct {
		name    string
		path    []string
		status  manage.DoctorStatus
		details []string
	}{
		{name: "absent", path: []string{filepath.Join(home, "empty")}, status: manage.DoctorStatusInfo, details: nil},
		{name: "one copy through a symlink", path: []string{filepath.Dir(first), filepath.Dir(linked)}, status: manage.DoctorStatusOK, details: nil},
		{name: "two copies", path: []string{filepath.Dir(linked), filepath.Dir(first), filepath.Dir(second)}, status: manage.DoctorStatusWarn, details: []string{linked, second + " (this executable)"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PATH", strings.Join(test.path, string(os.PathListSeparator)))
			manager := manage.New(manage.Config{Binary: second, StorePath: filepath.Join(home, "state.json")})

			result := manager.Doctor(t.Context(), manage.DoctorOptions{})

			assertDoctorInvariants(t, result)
			check := findDoctorCheck(t, result, "install.path")
			if check.Status != test.status || !slices.Equal(check.Details, test.details) {
				t.Fatalf("install.path = %+v, want %s with details %v", check, test.status, test.details)
			}
		})
	}
}

func TestDoctorIntegrationInstalledByAnotherExecutable(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	installer := writeExecutable(t, filepath.Join(home, "installer", "aht"))
	current := writeExecutable(t, filepath.Join(home, "current", "aht"))
	harness := registry.Harness("claude")
	if _, err := manage.New(manage.Config{Binary: installer}).InstallIntegration(t.Context(), harness, manage.IntegrationOptions{}); err != nil {
		t.Fatal(err)
	}
	manager := manage.New(manage.Config{Binary: current, StorePath: filepath.Join(home, "state.json")})

	t.Setenv("PATH", filepath.Dir(installer))
	result := manager.Doctor(t.Context(), manage.DoctorOptions{})
	assertDoctorInvariants(t, result)
	if check := findDoctorCheck(t, result, "integrations.claude"); check.Status != manage.DoctorStatusInfo || !strings.Contains(check.Message, installer) {
		t.Fatalf("integration current for the PATH copy = %+v, want info naming %s", check, installer)
	}

	t.Setenv("PATH", filepath.Dir(current))
	result = manager.Doctor(t.Context(), manage.DoctorOptions{})
	assertDoctorInvariants(t, result)
	stale := findDoctorCheck(t, result, "integrations.claude")
	if stale.Status != manage.DoctorStatusWarn || !stale.Fix.Automatic {
		t.Fatalf("integration stale for this executable = %+v, want warn with an automatic fix", stale)
	}
	if _, err := stale.Repair(t.Context()); err != nil {
		t.Fatal(err)
	}
	result = manager.Doctor(t.Context(), manage.DoctorOptions{})
	if check := findDoctorCheck(t, result, "integrations.claude"); check.Status != manage.DoctorStatusOK {
		t.Fatalf("integration after repair = %+v, want ok", check)
	}
}

func TestDoctorRepairWithoutAutomaticFix(t *testing.T) {
	home := isolateDoctorEnvironment(t)
	result := newDoctorManager(t, home, "").Doctor(t.Context(), manage.DoctorOptions{})
	check := findDoctorCheck(t, result, "observer.platform")
	if _, err := check.Repair(t.Context()); !errors.Is(err, manage.ErrNoRepair) {
		t.Fatalf("Repair() error = %v, want %v", err, manage.ErrNoRepair)
	}
}

func isolateDoctorEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("PATH", filepath.Join(home, "bin"))
	for _, key := range []string{"AHT_CONFIG", registry.StorePathEnv, registry.StateDirEnv} {
		t.Setenv(key, "")
	}
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_CODE_HOME", "GROK_HOME", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "KILO_CONFIG_DIR"} {
		t.Setenv(key, filepath.Join(home, key))
	}
	return home
}

func newDoctorManager(t *testing.T, home, storePath string) *manage.Manager {
	t.Helper()
	if storePath == "" {
		storePath = filepath.Join(home, "state.json")
	}
	return manage.New(manage.Config{Binary: filepath.Join(home, "bin", "aht"), StorePath: storePath})
}

func assertDoctorInvariants(t *testing.T, result manage.DoctorResult) {
	t.Helper()
	var counts manage.DoctorSummary
	seen := make(map[string]bool)
	for _, check := range result.Checks {
		if want := strings.ToLower(check.Group) + "." + check.Name(); check.ID != want {
			t.Errorf("check id %q does not match group %q and name %q", check.ID, check.Group, check.Name())
		}
		countDoctorStatus(t, &counts, check)
		if (check.Status == manage.DoctorStatusWarn || check.Status == manage.DoctorStatusFail) && check.Fix == nil {
			t.Errorf("%s is %s without a fix", check.ID, check.Status)
		}
		if check.Needs != "" && !seen[check.Needs] {
			t.Errorf("%s needs %q, which did not run before it", check.ID, check.Needs)
		}
		seen[check.ID] = true
	}
	if counts != result.Summary {
		t.Errorf("summary = %+v, counted %+v", result.Summary, counts)
	}
	if result.Failed() != (counts.Fail > 0) {
		t.Errorf("Failed() = %t with %d failures", result.Failed(), counts.Fail)
	}
}

func countDoctorStatus(t *testing.T, counts *manage.DoctorSummary, check manage.DoctorCheck) {
	t.Helper()
	switch check.Status {
	case manage.DoctorStatusOK:
		counts.OK++
	case manage.DoctorStatusInfo:
		counts.Info++
	case manage.DoctorStatusWarn:
		counts.Warn++
	case manage.DoctorStatusFail:
		counts.Fail++
	case manage.DoctorStatusSkip:
		counts.Skip++
	default:
		t.Errorf("%s has unknown status %q", check.ID, check.Status)
	}
}

func findDoctorCheck(t *testing.T, result manage.DoctorResult, id string) manage.DoctorCheck {
	t.Helper()
	for _, check := range result.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("doctor report has no %s check", id)
	return manage.DoctorCheck{}
}

func writeDoctorFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	writeDoctorFile(t, path, "#!/bin/sh\n")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func listDirectory(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	listing := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		listing = append(listing, entry.Name()+" "+info.ModTime().String()+" "+info.Mode().String())
	}
	return listing
}
