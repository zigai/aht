//go:build integration

package install

import (
	"os"
	"strconv"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

func TestOmpReportsRootSessionsWithAndWithoutUI(t *testing.T) {
	tests := []struct {
		name     string
		hasUI    bool
		subagent bool
	}{
		{name: "interactive", hasUI: true},
		{name: "headless"},
		{name: "subagent", subagent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture := captureBinary(t)
			t.Setenv("AHT_CAPTURE", capture.path)
			module := generatedArtifactContent(t, registry.HarnessOmp, "aht-state.ts")
			runNodeRuntime(t, "extension.ts", module, `
import extension from "./extension.ts";
const hooks = new Map();
extension({on: (name, callback) => hooks.set(name, callback)});
const ctx = {
  hasUI: `+strconv.FormatBool(test.hasUI)+`,
  cwd: "/tmp/project",
  sessionManager: {
    getSessionId: () => "omp-session",
    getSessionFile: () => "/tmp/omp.jsonl",
    getBranch: () => `+strconv.FormatBool(test.subagent)+` ? [{type: "session_init"}] : [],
  },
};
await hooks.get("session_start")({type: "session_start"}, ctx);
await hooks.get("agent_error")({type: "agent_error", error: "fatal"}, ctx);
await hooks.get("session_shutdown")({type: "session_shutdown"}, ctx);
`, nil)
			if test.subagent {
				data, err := os.ReadFile(capture.path)
				if err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if len(data) != 0 {
					t.Fatalf("subagent emitted root session reports: %s", data)
				}
				return
			}
			requireCapturedArguments(t, capture.path, "--lifecycle", "start", "--session-id", "omp-session", "--session-path", "/tmp/omp.jsonl", "--cwd", "/tmp/project")
			requireCapturedArguments(t, capture.path, "--activity", "failed")
			requireCapturedArguments(t, capture.path, "--lifecycle", "end", "--presence", "gone")
		})
	}
}
