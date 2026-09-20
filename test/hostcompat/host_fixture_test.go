//go:build compatibility

package hostcompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/broker"
)

type isolatedHost struct {
	root      string
	home      string
	work      string
	store     string
	aht       string
	hostPath  string
	env       []string
	contract  hostContract
	provider  *scriptedProvider
	interrupt bool
}

func newIsolatedHost(t *testing.T, contract hostContract, oracle string) isolatedHost {
	t.Helper()

	hostPath, err := exec.LookPath(contract.Executable)
	if err != nil {
		t.Fatalf("current %s executable %q is not installed: %v", contract.ID, contract.Executable, err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	for _, directory := range []string{home, work, filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	aht := filepath.Join(root, "bin", "aht-compat-oracle")
	if err := os.MkdirAll(filepath.Dir(aht), 0o700); err != nil {
		t.Fatal(err)
	}
	stageCompatibilityOracle(t, oracle, aht+".real")

	writeObservationLauncher(t, aht, filepath.Join(root, "native-events"))
	env := isolatedEnvironment()
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"TMPDIR="+filepath.Join(root, "tmp"),
		"AHT_STATE_DIR="+filepath.Join(root, "aht-state"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"),
		"CODEX_HOME="+filepath.Join(root, "codex"),
		"COPILOT_HOME="+filepath.Join(root, "copilot"),
		"AHT_STORE="+filepath.Join(root, "sessions.json"),
		"CLINE_DIR="+filepath.Join(root, "cline"),
		"CLINE_DATA_DIR="+filepath.Join(root, "cline-data"),
		"KIMI_SHARE_DIR="+filepath.Join(root, "kimi"),
		"GROK_HOME="+filepath.Join(root, "grok"),
		"PI_CODING_AGENT_DIR="+filepath.Join(root, "pi-agent"),
		"OPENCODE_CONFIG_DIR="+filepath.Join(root, "opencode"),
		"KILO_CONFIG_DIR="+filepath.Join(root, "kilo"),
		"AGY_CONFIG_HOME="+filepath.Join(root, "agy"),
		"FACTORY_CONFIG_DIR="+filepath.Join(home, ".factory"),
		"FACTORY_DROID_AUTO_UPDATE_ENABLED=false",
		"HERMES_HOME="+filepath.Join(root, "hermes"),
	)

	return isolatedHost{
		root: root, home: home, work: work, store: filepath.Join(root, "sessions.json"),
		aht: aht, hostPath: hostPath, env: env, contract: contract, provider: nil,
	}
}

func sourceDirectory() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("locating compatibility source directory")
	}
	return filepath.Dir(file)
}

func (host isolatedHost) installIntegration(t *testing.T) {
	t.Helper()
	host.mustRunAHT(t, "manage", "integrations", "install", string(host.contract.ID), "--binary", host.aht)
}

func (host isolatedHost) assertIntegrationCurrent(t *testing.T) {
	t.Helper()
	output := host.mustRunAHT(t, "--json", "manage", "integrations", "status", string(host.contract.ID), "--binary", host.aht)
	var statuses []struct {
		Status string   `json:"status"`
		Paths  []string `json:"paths"`
	}
	if err := json.Unmarshal(output, &statuses); err != nil {
		t.Fatalf("decoding integration status: %v\n%s", err, output)
	}
	if len(statuses) != 1 || statuses[0].Status != "current" {
		t.Fatalf("installed integration is not current:\n%s", output)
	}
	for _, path := range statuses[0].Paths {
		found := false
		_ = filepath.Walk(path, func(candidate string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return nil
			}
			content, readErr := os.ReadFile(candidate)
			if readErr == nil && bytes.Contains(content, []byte(host.aht)) {
				found = true
			}
			return nil
		})
		if found {
			return
		}
	}
	t.Fatalf("current integration does not reference compatibility oracle %s:\n%s", host.aht, output)
}

func (host isolatedHost) assertVersion(t *testing.T) {
	t.Helper()
	command := exec.Command(host.hostPath, host.contract.VersionArgs...)
	command.Env = host.env
	command.Dir = host.work
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := runCompatibilityCommand(ctx, command)
	output := stdout.Bytes()
	if err != nil {
		t.Fatalf("%s version probe failed: %v\n%s\n%s", host.contract.ID, err, output, stderr.Bytes())
	}
	if len(bytes.TrimSpace(output)) == 0 {
		t.Fatalf("%s version probe returned no version", host.contract.ID)
	}
	t.Logf("current %s: %s", host.contract.ID, bytes.TrimSpace(output))
}

func (host isolatedHost) startTracker(t *testing.T) {
	t.Helper()

	logFile, err := os.Create(filepath.Join(host.root, "tracker.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(host.aht, "--store", host.store, "manage", "tracker", "run", "--quiet")
	command.Env = host.env
	command.Dir = host.work
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
		_ = logFile.Close()
	})

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	socketPath := broker.SocketPath(host.store)
	for {
		select {
		case <-deadline.C:
			t.Fatalf("tracker did not create broker socket %s", socketPath)
		case <-ticker.C:
			info, statErr := os.Stat(socketPath)
			if statErr == nil && info.Mode()&os.ModeSocket != 0 {
				return
			}
		}
	}
}

func (host isolatedHost) writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (host isolatedHost) mustRunAHT(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := host.runAHT(ctx, args...)
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	return output
}

func (host isolatedHost) runAHT(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.Command(host.aht, append([]string{"--store", host.store}, args...)...)
	command.Env = host.env
	command.Dir = host.work
	output, err := compatibilityOutput(ctx, command)
	if err != nil {
		return output, fmt.Errorf("aht %s failed: %w", strings.Join(args, " "), err)
	}
	return output, nil
}
