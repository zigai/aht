//go:build compatibility

package hostcompat

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func runRPCPermissionScenarios(t *testing.T, contract hostContract) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, allow)
			switch contract.ID {
			case registry.HarnessPi:
				runPiPermission(t, host, allow)
			case registry.HarnessOmp:
				runOMPPermission(t, host, allow)
			default:
				t.Fatalf("no RPC-family permission driver for %s", contract.ID)
			}
		})
	}
}

// Pi documents permission gates as tool_call extensions. Calling the real UI
// API causes Pi itself to emit ui_prompt_start/end; this fixture never reports
// AHT state or replaces the installed managed integration.
const piPermissionGate = `export default function (pi) {
  pi.on("tool_call", async (event, ctx) => {
    if (event.toolName !== "bash") return;
    if (!ctx.hasUI) throw new Error("Permission scenario requires native UI");
    const allowed = await ctx.ui.confirm("AHT compatibility permission", "Allow this shell tool?");
    if (!allowed) return { block: true, reason: "Tool call rejected by user" };
  });
  pi.registerCommand("aht-permission-exit", {
    description: "Close the isolated permission scenario",
    handler: async (_args, ctx) => { ctx.shutdown(); },
  });
}
`

func runPiPermission(t *testing.T, host isolatedHost, allow bool) {
	t.Helper()
	configured, setup := host.lifecycleCommand(t)
	if len(setup) != 0 {
		t.Fatal("Pi permission driver does not expect setup processes")
	}
	gate := filepath.Join(host.root, "permission-gate.ts")
	host.writeFile(t, gate, piPermissionGate)
	cmd := host.command(configured.Env, "--mode", "rpc", "--provider", "aht-compat", "--model", "compat", "--extension", gate)
	input, next := permissionJSONPipes(t, cmd)
	process := startPermissionProcess(t, host, cmd)
	sendPermissionJSON(t, input, map[string]any{"id": "permission-prompt", "type": "prompt", "message": compatibilityPrompt})
	var request map[string]any
	for {
		frame := next()
		if frame["type"] == "agent_end" {
			t.Fatal("Pi completed without requesting permission")
		}
		if frame["type"] == "extension_ui_request" && frame["method"] == "confirm" {
			if frame["title"] != "AHT compatibility permission" {
				t.Fatalf("unexpected Pi confirmation: %v", frame)
			}
			request = frame
			break
		}
	}
	id, ok := request["id"].(string)
	if !ok || id == "" {
		t.Fatalf("Pi confirmation lacks request ID: %v", request)
	}
	waiting := assertPermissionWaiting(t, host)
	sendPermissionJSON(t, input, map[string]any{"type": "extension_ui_response", "id": id, "confirmed": allow})
	for {
		frame := next()
		if frame["type"] == "extension_ui_request" && frame["method"] == "confirm" {
			t.Fatal("Pi requested a second permission for a single tool call")
		}
		if frame["type"] == "agent_end" && frame["willRetry"] != true {
			break
		}
	}
	assertPermissionOutcome(t, host, waiting, allow)
	sendPermissionJSON(t, input, map[string]any{"id": "permission-exit", "type": "prompt", "message": "/aht-permission-exit"})
	process.wait(t)
}

// Deadline-bearing OS pipes keep JSONL reads and writes bounded without a
// reader goroutine that can outlive the process. Scanner splits only on LF,
// preserves JSON's Unicode separators, and caps individual native frames.
func permissionJSONPipes(t *testing.T, cmd *exec.Cmd) (*os.File, func() map[string]any) {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdout.Close() })
	input, ok := stdin.(*os.File)
	if !ok {
		t.Fatal("native permission stdin is not an OS pipe")
	}
	output, ok := stdout.(*os.File)
	if !ok {
		t.Fatal("native permission stdout is not an OS pipe")
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	remaining := 16 << 20
	deadline := time.Now().Add(45 * time.Second)
	return input, func() map[string]any {
		t.Helper()
		if err := output.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			t.Fatalf("reading native permission JSONL: %v", err)
		}
		remaining -= len(scanner.Bytes())
		if remaining < 0 {
			t.Fatal("native permission stream exceeded 16 MiB")
		}
		var frame map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatalf("invalid native permission JSONL: %v", err)
		}
		if frame["type"] == "extension_error" || (frame["type"] == "response" && frame["success"] == false) || frame["error"] != nil {
			t.Fatalf("native permission protocol failed: %v", frame)
		}
		return frame
	}
}

// OMP's native approval wrapper calls the RPC-backed select UI and emits
// tool_approval_requested/resolved around the decision. No policy extension is
// needed: always-ask gates the built-in shell tool itself.
func runOMPPermission(t *testing.T, host isolatedHost, allow bool) {
	t.Helper()
	configured, setup := host.lifecycleCommand(t)
	if len(setup) != 0 {
		t.Fatal("OMP permission driver does not expect setup processes")
	}
	args := []string{"--mode", "rpc", "--model", "aht-compat/compat", "--approval-mode", "always-ask"}
	// Keep the existing fixture's explicit loading of the managed extension;
	// do not copy its print-mode or auto-approval flags.
	for index := 1; index+1 < len(configured.Args); index++ {
		if configured.Args[index] == "--hook" {
			args = append(args, "--hook", configured.Args[index+1])
		}
	}
	if len(args) == 6 {
		t.Fatal("OMP lifecycle command did not specify its managed hook")
	}
	cmd := host.command(configured.Env, args...)
	input, next := permissionJSONPipes(t, cmd)
	process := startPermissionProcess(t, host, cmd)
	sendPermissionJSON(t, input, map[string]any{"id": "permission-prompt", "type": "prompt", "message": compatibilityPrompt})
	var request map[string]any
	for {
		frame := next()
		if frame["type"] == "agent_end" {
			t.Fatal("OMP completed without requesting permission")
		}
		if frame["type"] == "extension_ui_request" && frame["method"] == "select" {
			request = frame
			break
		}
	}
	title, _ := request["title"].(string)
	options, _ := request["options"].([]any)
	id, _ := request["id"].(string)
	if id == "" || !strings.Contains(title, "Allow tool: bash") || len(options) != 2 || options[0] != "Approve" || options[1] != "Deny" {
		t.Fatalf("unexpected OMP native approval request: %v", request)
	}
	waiting := assertPermissionWaiting(t, host)
	choice := "Deny"
	if allow {
		choice = "Approve"
	}
	sendPermissionJSON(t, input, map[string]any{"type": "extension_ui_response", "id": id, "value": choice})
	for {
		frame := next()
		if frame["type"] == "extension_ui_request" && frame["method"] == "select" {
			t.Fatal("OMP requested a second permission for a single tool call")
		}
		if frame["type"] == "agent_end" && frame["isTerminal"] != false {
			break
		}
	}
	assertPermissionOutcome(t, host, waiting, allow)
	// OMP documents stdin EOF as a graceful session-disposal boundary.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func sendPermissionJSON(t *testing.T, input *os.File, frame any) {
	t.Helper()
	if err := input.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(input).Encode(frame); err != nil {
		t.Fatalf("writing native permission response: %v", err)
	}
}
