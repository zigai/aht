//go:build integration

package install

import (
	"context"
	"embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/node testdata/python testdata/sh
var runtimeFixtures embed.FS

func runtimeScript(t *testing.T, name string) string {
	t.Helper()
	data, err := runtimeFixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read runtime fixture %s: %v", name, err)
	}
	return string(data)
}

func runNodeRuntime(t *testing.T, moduleName string, module string, driver string, extraFiles map[string]string) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "package.json"), `{"type":"module"}`, 0o600)
	writeTestFile(t, filepath.Join(dir, moduleName), module, 0o600)
	for path, content := range extraFiles {
		writeTestFile(t, filepath.Join(dir, path), content, 0o600)
	}
	driverPath := filepath.Join(dir, "driver.mjs")
	writeTestFile(t, driverPath, driver, 0o600)
	args := []string{driverPath}
	if strings.HasSuffix(moduleName, ".ts") {
		args = []string{"--experimental-strip-types", driverPath}
	}
	runGeneratedCommand(t, exec.Command("node", args...), "")
}

func writeRuntimeArtifact(t *testing.T, name string, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	writeTestFile(t, path, content, 0o600)
	return path
}

func writeTestFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create generated runtime directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write generated runtime file: %v", err)
	}
}

func runGeneratedCommand(t *testing.T, command *exec.Cmd, stdin string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command = exec.CommandContext(ctx, command.Path, command.Args[1:]...)
	command.Env = os.Environ()
	command.Dir = filepath.Dir(command.Path)
	command.Stdin = strings.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", strings.Join(command.Args, " "), err, output)
	}
}

func requireRuntimeTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("Phase 3 generated-artifact validation requires %s: %v", name, err)
	}
	return path
}

func TestMatchInvocationRequiresSameFrameAndAdjacentFlags(t *testing.T) {
	t.Parallel()

	invocations := [][]string{
		{"report", "claude", "--activity", "running"},
		{"report", "openclaw", "--activity", "failed"},
	}
	matched := false
	for _, inv := range invocations {
		if matchInvocation(inv, []string{"report", "openclaw", "--activity", "running"}) {
			matched = true
			break
		}
	}
	if matched {
		t.Fatal("matchInvocation accepted cross-invocation arguments")
	}

	nonAdjacent := []string{"report", "claude", "--activity", "extra-token", "running"}
	if matchInvocation(nonAdjacent, []string{"report", "claude", "--activity", "running"}) {
		t.Fatal("matchInvocation accepted non-adjacent flag and value")
	}

	valid := []string{"report", "claude", "--activity", "running", "--event", "test"}
	if !matchInvocation(valid, []string{"report", "claude", "--activity", "running"}) {
		t.Fatal("matchInvocation rejected valid same-frame adjacent invocation")
	}
}

type captureExecutable struct {
	command string
	path    string
}

func captureBinary(t *testing.T) captureExecutable {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "arguments")
	command := filepath.Join(dir, "aht-capture")
	script := runtimeScript(t, "sh/capture-report.sh")
	writeTestFile(t, command, script, 0o700)
	return captureExecutable{command: command, path: capture}
}

func captureBinaryCommand(t *testing.T) string {
	t.Helper()
	return captureBinary(t).command
}

func parseCapturedInvocations(content string) [][]string {
	var invocations [][]string
	for _, chunk := range strings.Split(content, "---") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		var args []string
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				args = append(args, line)
			}
		}
		if len(args) > 0 {
			invocations = append(invocations, args)
		}
	}
	return invocations
}

func matchInvocation(args []string, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	if len(args) < len(expected) {
		return false
	}
	idx := 0
	for idx < len(expected) && !strings.HasPrefix(expected[idx], "-") {
		if idx >= len(args) || args[idx] != expected[idx] {
			return false
		}
		idx++
	}
	for i := idx; i < len(expected); i += 2 {
		flag := expected[i]
		if i+1 >= len(expected) {
			found := false
			for _, a := range args {
				if a == flag {
					found = true
					break
				}
			}
			if !found {
				return false
			}
			break
		}
		val := expected[i+1]
		found := false
		for j := 0; j+1 < len(args); j++ {
			if args[j] == flag && args[j+1] == val {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func requireCapturedArguments(t *testing.T, path string, expected ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			content := string(data)
			if strings.Contains(content, generatedRuntimeSensitiveSentinel) {
				t.Fatalf("generated runtime leaked sensitive payload into report arguments:\n%s", content)
			}
			invocations := parseCapturedInvocations(content)
			for _, inv := range invocations {
				if matchInvocation(inv, expected) {
					return
				}
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("read generated runtime capture: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("generated runtime arguments in %s missing framed %q:\n%s", path, expected, data)
}
