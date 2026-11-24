package omp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func isolateDirEnvironment(t *testing.T) string {
	t.Helper()

	for _, name := range []string{
		"PI_CODING_AGENT_DIR",
		"PI_CONFIG_DIR",
		"OMP_PROFILE",
		"PI_PROFILE",
		"XDG_DATA_HOME",
	} {
		t.Setenv(name, "")

		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)

	return home
}

func TestAgentDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want func(home string) string
	}{
		{
			name: "default",
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
		{
			name: "config dir name replaces the default",
			env:  map[string]string{"PI_CONFIG_DIR": ".custom-omp"},
			want: func(home string) string { return filepath.Join(home, ".custom-omp", "agent") },
		},
		{
			name: "absolute config dir is joined under home",
			env:  map[string]string{"PI_CONFIG_DIR": "/srv/omp"},
			want: func(home string) string { return filepath.Join(home, "srv", "omp", "agent") },
		},
		{
			name: "agent dir override replaces the default profile agent dir",
			env: map[string]string{
				"PI_CODING_AGENT_DIR": "/srv/agent",
				"PI_CONFIG_DIR":       ".custom-omp",
			},
			want: func(string) string { return "/srv/agent" },
		},
		{
			name: "named profile",
			env:  map[string]string{"OMP_PROFILE": "work"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "named profile follows the config dir name",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_CONFIG_DIR": ".custom-omp"},
			want: func(home string) string { return filepath.Join(home, ".custom-omp", "profiles", "work", "agent") },
		},
		{
			name: "named profile ignores the agent dir override",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_CODING_AGENT_DIR": "/srv/agent"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "legacy profile variable is used when the canonical one is unset",
			env:  map[string]string{"PI_PROFILE": "legacy"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "legacy", "agent") },
		},
		{
			name: "canonical profile wins over the legacy one",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_PROFILE": "legacy"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "explicitly empty canonical profile selects the default despite the legacy one",
			env: map[string]string{
				"OMP_PROFILE":         "",
				"PI_PROFILE":          "legacy",
				"PI_CODING_AGENT_DIR": "/srv/agent",
			},
			want: func(string) string { return "/srv/agent" },
		},
		{
			name: "profile named default selects the default profile",
			env:  map[string]string{"OMP_PROFILE": "default"},
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
		{
			name: "unrelated variable is ignored",
			env:  map[string]string{"OMP_CONFIG_DIR": "/srv/ignored"},
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDirEnvironment(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}

			if got, want := ompAgentDir(), test.want(home); got != want {
				t.Fatalf("ompAgentDir() = %q, want %q", got, want)
			}
		})
	}
}

func TestSessionsDir(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		dataDirs []string
		want     func(home, data string) string
	}{
		{
			name: "default profile",
			want: func(home, _ string) string { return filepath.Join(home, ".omp", "agent", "sessions") },
		},
		{
			name: "config dir name",
			env:  map[string]string{"PI_CONFIG_DIR": ".custom-omp"},
			want: func(home, _ string) string { return filepath.Join(home, ".custom-omp", "agent", "sessions") },
		},
		{
			name: "named profile",
			env:  map[string]string{"OMP_PROFILE": "work"},
			want: func(home, _ string) string {
				return filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions")
			},
		},
		{
			name: "agent dir override",
			env:  map[string]string{"PI_CODING_AGENT_DIR": "/srv/agent"},
			want: func(string, string) string { return "/srv/agent/sessions" },
		},
		{
			name:     "existing data home root is used",
			dataDirs: []string{"omp"},
			want:     func(_, data string) string { return filepath.Join(data, "omp", "sessions") },
		},
		{
			name:     "data home without an omp root is ignored",
			dataDirs: []string{},
			want:     func(home, _ string) string { return filepath.Join(home, ".omp", "agent", "sessions") },
		},
		{
			name:     "named profile ignores a data home root that lacks its profile",
			env:      map[string]string{"OMP_PROFILE": "work"},
			dataDirs: []string{"omp"},
			want: func(home, _ string) string {
				return filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions")
			},
		},
		{
			name:     "named profile uses its own data home root",
			env:      map[string]string{"OMP_PROFILE": "work"},
			dataDirs: []string{filepath.Join("omp", "profiles", "work")},
			want:     func(_, data string) string { return filepath.Join(data, "omp", "profiles", "work", "sessions") },
		},
		{
			name:     "agent dir override disables the data home root",
			env:      map[string]string{"PI_CODING_AGENT_DIR": "/srv/agent"},
			dataDirs: []string{"omp"},
			want:     func(string, string) string { return "/srv/agent/sessions" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDirEnvironment(t)

			data := t.TempDir()
			if test.dataDirs != nil {
				t.Setenv("XDG_DATA_HOME", data)
			}

			for _, dir := range test.dataDirs {
				if err := os.MkdirAll(filepath.Join(data, dir), 0o750); err != nil {
					t.Fatal(err)
				}
			}

			for name, value := range test.env {
				t.Setenv(name, value)
			}

			if got, want := sessionsDirUnder(home), test.want(home, data); got != want {
				t.Fatalf("sessionsDirUnder() = %q, want %q", got, want)
			}
		})
	}
}

func TestDefinitionRetainsNativeActivity(t *testing.T) {
	def := New().Definition()
	if !def.RetainNativeActivity {
		t.Fatal("RetainNativeActivity = false, want true")
	}

	if def.IntegrationVersion != integrationVersion {
		t.Fatalf("IntegrationVersion = %d, want %d", def.IntegrationVersion, integrationVersion)
	}
}

type extensionReport struct {
	Activity string
	Detail   string
	Event    string
	Message  string
}

func runExtensionScenario(t *testing.T, script string) ([]extensionReport, int) {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to exercise the generated extension")
	}

	dir := t.TempDir()
	capture := filepath.Join(dir, "reports")
	reporter := filepath.Join(dir, "reporter")
	reporterScript := "#!" + node + "\nrequire('node:fs').appendFileSync(process.env.AHT_CAPTURE," +
		" JSON.stringify(process.argv.slice(2))+'\\n');\n"

	if err := os.WriteFile(reporter, []byte(reporterScript), 0o700); err != nil {
		t.Fatal(err)
	}

	module := harness.RenderScriptTemplate(ompExtensionTemplate, reporter, New().Definition())
	if err := os.WriteFile(filepath.Join(dir, "extension.ts"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}

	activeFile := filepath.Join(dir, "intervals")
	driver := `import { readFileSync, writeFileSync } from "node:fs";
let activeIntervals = new Set();
let nextId = 1;
globalThis.setInterval = () => {
  const id = nextId++;
  activeIntervals.add(id);
  return { id, unref() {} };
};
globalThis.clearInterval = (handle) => {
  if (handle && typeof handle === "object") activeIntervals.delete(handle.id);
};
const { default: extension } = await import("./extension.ts");
const handlers = new Map();
extension({ on: (name, handler) => handlers.set(name, handler) });
const ctx = {
  cwd: "/work",
  mode: "tui",
  sessionManager: { getSessionId: () => "omp-test-session", getBranch: () => [] },
};
const emit = (type, extra = {}) => handlers.get(type)({ type, ...extra }, ctx);
const waitReports = async (count) => {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {
      const lines = readFileSync(process.env.AHT_CAPTURE, "utf8").trim().split("\n").filter(Boolean);
      if (lines.length >= count) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  throw new Error("timed out waiting for " + count + " reports");
};
` + script + `
writeFileSync(process.env.AHT_INTERVALS, String(activeIntervals.size));
await emit("session_shutdown");
process.exit(0);
`

	driverPath := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), node, "--experimental-strip-types", driverPath)

	cmd.Env = append(os.Environ(), "AHT_CAPTURE="+capture, "AHT_INTERVALS="+activeFile)

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run extension scenario: %v\n%s", err, output)
	}

	intervalData, err := os.ReadFile(activeFile)
	if err != nil {
		t.Fatal(err)
	}

	activeCount, err := strconv.Atoi(strings.TrimSpace(string(intervalData)))
	if err != nil {
		t.Fatalf("parse active intervals: %v", err)
	}

	return parseExtensionReports(t, capture), activeCount
}

func parseExtensionReports(t *testing.T, path string) []extensionReport {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var reports []extensionReport

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}

		var args []string
		if err := json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}

		var rep extensionReport

		for index := 0; index+1 < len(args); index++ {
			switch args[index] {
			case "--activity":
				rep.Activity = args[index+1]
			case "--detail":
				rep.Detail = args[index+1]
			case "--event":
				rep.Event = args[index+1]
			case "--attribute":
				if value, ok := strings.CutPrefix(args[index+1], "agent_state_message="); ok {
					rep.Message = value
				}
			}
		}

		reports = append(reports, rep)
	}

	return reports
}

func reportActivities(reports []extensionReport) []string {
	activities := make([]string, 0, len(reports))
	for _, rep := range reports {
		if rep.Activity != "" {
			activities = append(activities, rep.Activity)
		}
	}

	return activities
}

func TestExtensionBlockedRecoveryAndHeartbeat(t *testing.T) {
	t.Run("tracked prompt restarts heartbeat on resolve", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
await waitReports(1);
emit("agent_start");
await waitReports(2);
emit("tool_approval_requested", { toolCallId: "t1", reason: "Run bash?" });
await waitReports(3);
emit("tool_approval_resolved", { toolCallId: "t1", approved: true });
await waitReports(4);
`)

		want := []string{"idle", "running", "waiting", "running"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if active != 1 {
			t.Fatalf("active heartbeats after unblock = %d, want 1", active)
		}
	})

	t.Run("untracked prompt clears blockedUncertain and restarts heartbeat", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
await waitReports(1);
emit("agent_start");
await waitReports(2);
emit("tool_approval_requested", { reason: "No toolCallId" });
await waitReports(3);
emit("tool_approval_resolved", { approved: true });
await waitReports(4);
`)

		want := []string{"idle", "running", "waiting", "running"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if reports[2].Detail != "clear" {
			t.Fatalf("untracked prompt detail = %q, want clear", reports[2].Detail)
		}

		if active != 1 {
			t.Fatalf("active heartbeats after untracked unblock = %d, want 1", active)
		}
	})

	t.Run("concurrent tracked and untracked prompts wait until both resolve", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
await waitReports(1);
emit("agent_start");
await waitReports(2);
emit("tool_approval_requested", { toolCallId: "t1", reason: "Tracked" });
await waitReports(3);
emit("tool_approval_requested", { reason: "Untracked" });
await waitReports(4);
emit("tool_approval_resolved", { toolCallId: "t1", approved: true });
await new Promise((resolve) => setTimeout(resolve, 50));
emit("tool_approval_resolved", { approved: true });
await waitReports(5);
`)

		want := []string{"idle", "running", "waiting", "waiting", "running"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if active != 1 {
			t.Fatalf("active heartbeats = %d, want 1", active)
		}
	})

	t.Run("terminal agent_end clears lingering untracked prompt", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
await waitReports(1);
emit("agent_start");
await waitReports(2);
emit("tool_approval_requested", { reason: "Abandoned prompt" });
await waitReports(3);
emit("agent_end", { messages: [] });
await waitReports(4);
`)

		want := []string{"idle", "running", "waiting", "idle"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if active != 0 {
			t.Fatalf("active heartbeats after idle = %d, want 0", active)
		}
	})
}

func TestExtensionRetryLifecycle(t *testing.T) {
	t.Run(
		"terminal agent_end with 503 without auto_retry_start reports failed",
		func(t *testing.T) {
			reports, active := runExtensionScenario(t, `
emit("session_start");
emit("agent_start");
emit("agent_end", {
  messages: [{ role: "assistant", stopReason: "error", errorMessage: "503 service unavailable" }],
});
`)

			want := []string{"idle", "running", "failed"}
			if got := reportActivities(reports); !slices.Equal(got, want) {
				t.Fatalf("activities = %v, want %v", got, want)
			}

			if reports[2].Message != "503 service unavailable" {
				t.Fatalf(
					"failure message = %q, want %q",
					reports[2].Message,
					"503 service unavailable",
				)
			}

			if active != 0 {
				t.Fatalf("active heartbeats after failure = %d, want 0", active)
			}
		},
	)

	t.Run("auto_retry_start holds running with heartbeat across willContinue", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
emit("agent_start");
emit("auto_retry_start", { attempt: 1, maxAttempts: 3, delayMs: 5000, errorMessage: "429 rate limit" });
emit("agent_end", { willContinue: true, messages: [] });
`)

		want := []string{"idle", "running"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if active != 1 {
			t.Fatalf("active heartbeats during retry hold = %d, want 1", active)
		}
	})

	t.Run("auto_retry_end failure reports failed and stops heartbeat", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
emit("agent_start");
emit("auto_retry_start", { attempt: 1, maxAttempts: 1, delayMs: 1000, errorMessage: "429 rate limit" });
emit("auto_retry_end", { success: false, attempt: 1, finalError: "429 rate limit exhausted" });
`)

		want := []string{"idle", "running", "failed"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if reports[2].Message != "429 rate limit exhausted" {
			t.Fatalf(
				"failure message = %q, want %q",
				reports[2].Message,
				"429 rate limit exhausted",
			)
		}

		if active != 0 {
			t.Fatalf("active heartbeats after retry failure = %d, want 0", active)
		}
	})

	t.Run("auto_retry_end success settles to idle on final agent_end", func(t *testing.T) {
		reports, active := runExtensionScenario(t, `
emit("session_start");
emit("agent_start");
emit("auto_retry_start", { attempt: 1, maxAttempts: 3, delayMs: 1000, errorMessage: "503" });
emit("agent_end", { willContinue: true, messages: [] });
emit("agent_start");
emit("auto_retry_end", { success: true, attempt: 1 });
emit("agent_end", { messages: [{ role: "assistant", stopReason: "stop" }] });
await new Promise((resolve) => setTimeout(resolve, 250));
`)

		want := []string{"idle", "running", "idle"}
		if got := reportActivities(reports); !slices.Equal(got, want) {
			t.Fatalf("activities = %v, want %v", got, want)
		}

		if active != 0 {
			t.Fatalf("active heartbeats after settled retry = %d, want 0", active)
		}
	})
}
