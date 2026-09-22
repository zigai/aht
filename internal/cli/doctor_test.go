package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/internal/agentstate"
	"github.com/zigai/aht/internal/install"
	"github.com/zigai/aht/pkg/manage"
	"github.com/zigai/aht/pkg/registry"
)

func TestDoctorIsConciseUnlessVerbose(t *testing.T) {
	assertDoctorSurface(t)
}

func TestDoctorFixtureIgnoresInheritedOmpProfile(t *testing.T) {
	foreignProfile := filepath.Join(t.TempDir(), ".omp", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", foreignProfile)
	installed, err := install.RunContext(t.Context(), install.Options{Harness: registry.HarnessOmp, Binary: defaultInstallBinary()})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(installed.Path)
	if err != nil {
		t.Fatal(err)
	}

	path := prepareDoctorEnvironment(t)
	output := executeDoctorSurface(t, "--store", path, "--json", "manage", "doctor")
	var result doctorResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode isolated doctor result: %v\noutput: %s", err, output)
	}
	if !result.OK {
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

func prepareDoctorEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Like the upgrade fixture, redirect every harness override rather than
	// letting an inherited profile escape the temporary home directory.
	for _, key := range []string{"XDG_CONFIG_HOME", "AHT_CONFIG", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "COPILOT_HOME", "CLINE_DIR", "CLINE_HOOKS_DIR", "KIMI_SHARE_DIR", "GROK_HOME", "PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "AGY_CONFIG_HOME", "HERMES_HOME", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG", "KILO_CONFIG_DIR", registry.StateDirEnv} {
		t.Setenv(key, filepath.Join(home, key))
	}
	t.Setenv("OMP_PROFILE", "default")
	t.Setenv("PI_PROFILE", "default")
	fakeBin := filepath.Join(home, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	return filepath.Join(home, "sessions.json")
}

func assertDoctorSurface(t *testing.T) {
	t.Helper()
	path := prepareDoctorEnvironment(t)
	concise := executeDoctorSurface(t, "--store", path, "manage", "doctor")
	if strings.Contains(concise, "integration.codex") || strings.Contains(concise, "integration.pi") {
		t.Fatalf("concise doctor includes uninstalled integrations:\n%s", concise)
	}
	executeDoctorSurface(t, "manage", "integrations", "install", "codex", "--binary", defaultInstallBinary())
	concise = executeDoctorSurface(t, "--store", path, "manage", "doctor")
	if !strings.Contains(concise, "integration.codex") || strings.Contains(concise, "integration.pi") {
		t.Fatalf("concise doctor omitted installed integration or included uninstalled integrations:\n%s", concise)
	}

	verbose := executeDoctorSurface(t, "--store", path, "manage", "doctor", "--verbose")
	if !strings.Contains(verbose, "integration.pi") || !strings.Contains(verbose, "integration.codex") {
		t.Fatalf("verbose doctor omitted integration details:\n%s", verbose)
	}
	for _, mode := range []struct {
		name         string
		flags        []string
		capabilities bool
	}{
		{name: "concise", flags: nil, capabilities: false},
		{name: "verbose", flags: []string{"--verbose"}, capabilities: true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			args := append([]string{"--store", path, "--json", "manage", "doctor"}, mode.flags...)
			output := executeDoctorSurface(t, args...)
			var result doctorResult
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("decode doctor JSON: %v\nstdout:\n%s", err, output)
			}
			if !result.OK || (len(result.Capabilities) != 0) != mode.capabilities {
				t.Fatalf("doctor health or capability visibility mismatch:\n%s", output)
			}
		})
	}
}

func executeDoctorSurface(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := runTestCLI(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("aht %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func TestDoctorHandlesInvalidDetectionManifests(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     doctorStatus
	}{
		{name: "bundled override warns and falls back", manifest: "pi.toml", want: doctorWarning},
		{name: "local-only manifest fails", manifest: "agy.toml", want: doctorError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			detectionDir := agentstate.DefaultConfigDir()
			if detectionDir == "" {
				t.Fatal("empty detection dir")
			}
			if err := os.MkdirAll(detectionDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(detectionDir, test.manifest), []byte("invalid ["), 0o600); err != nil {
				t.Fatal(err)
			}
			result := doctorResult{OK: true, Checks: nil, Capabilities: nil}
			result.addDetectionManifestCheck()
			if len(result.Checks) != 1 || result.Checks[0].Name != "detection.manifests" || result.Checks[0].Status != test.want {
				t.Fatalf("detection doctor check = %#v, want status %q", result.Checks, test.want)
			}
		})
	}
}

func (result *doctorResult) addDetectionManifestCheck() {
	m := manage.New(manage.Config{
		Binary: defaultInstallBinary(),
	})
	m.CheckManifests(func(name string, status manage.DoctorStatus, message string) {
		result.Checks = append(result.Checks, doctorCheck{
			Name:    name,
			Status:  status,
			Message: message,
		})
	})
}
