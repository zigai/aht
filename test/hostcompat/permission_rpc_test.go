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

	"github.com/zigai/aht/v2/pkg/registry"
)

func runRPCPermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, oracle, allow)
			switch contract.ID {
			case registry.Harness("pi"):
				runPiPermission(t, host, allow)
			case registry.Harness("omp"):
				runOmpPermission(t, host, allow)
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

func runPiPermission(t *testing.T, host *isolatedHost, allow bool) {
	t.Helper()
	configured, setup := host.lifecycleCommand(t)
	if len(setup) != 0 {
		t.Fatal("Pi permission driver does not expect setup processes")
	}
	gate := filepath.Join(host.root, "permission-gate.ts")
	host.writeFile(t, gate, piPermissionGate)
	cmd := host.command(t, configured.Env, "--mode", "rpc", "--provider", "aht-compat", "--model", "compat", "--extension", gate)
	input, next := permissionJSONPipes(t, cmd)
	process := startPermissionProcess(t, host, cmd)
	sendPermissionJSON(t, input, map[string]any{"id": "permission-prompt", "type": "prompt", "message": compatibilityPrompt})
	request := awaitRPCApprovalRequest(t, next, "Pi", "confirm")
	if request["title"] != "AHT compatibility permission" {
		t.Fatalf("unexpected Pi confirmation: %v", request)
	}
	id, ok := request["id"].(string)
	if !ok || id == "" {
		t.Fatalf("Pi confirmation lacks request ID: %v", request)
	}
	waiting := assertPermissionWaiting(t, host)
	sendPermissionJSON(t, input, map[string]any{"type": "extension_ui_response", "id": id, "confirmed": allow})
	awaitRPCRunEnd(t, next, "Pi", "confirm", func(frame map[string]any) bool { return frame["willRetry"] != true })
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
	frames := &jsonlFrames{output: output, scanner: scanner, remaining: 16 << 20, deadline: time.Now().Add(45 * time.Second)}
	return input, func() map[string]any { return frames.next(t) }
}

type jsonlFrames struct {
	output    *os.File
	scanner   *bufio.Scanner
	remaining int
	deadline  time.Time
}

func (frames *jsonlFrames) next(t *testing.T) map[string]any {
	t.Helper()
	if err := frames.output.SetReadDeadline(frames.deadline); err != nil {
		t.Fatal(err)
	}
	if !frames.scanner.Scan() {
		err := frames.scanner.Err()
		if err == nil {
			err = io.EOF
		}
		t.Fatalf("reading native permission JSONL: %v", err)
	}
	frames.remaining -= len(frames.scanner.Bytes())
	if frames.remaining < 0 {
		t.Fatal("native permission stream exceeded 16 MiB")
	}
	var frame map[string]any
	if err := json.Unmarshal(frames.scanner.Bytes(), &frame); err != nil {
		t.Fatalf("invalid native permission JSONL: %v", err)
	}
	if frame["type"] == "extension_error" || (frame["type"] == "response" && frame["success"] == false) || frame["error"] != nil {
		t.Fatalf("native permission protocol failed: %v", frame)
	}
	return frame
}

// OMP's native approval wrapper calls the RPC-backed select UI and emits
// tool_approval_requested/resolved around the decision. No policy extension is
// needed: always-ask gates the built-in shell tool itself.
func runOmpPermission(t *testing.T, host *isolatedHost, allow bool) {
	t.Helper()
	configured, setup := host.lifecycleCommand(t)
	if len(setup) != 0 {
		t.Fatal("OMP permission driver does not expect setup processes")
	}
	// Keep the existing fixture's explicit loading of the managed extension;
	// do not copy its print-mode or auto-approval flags.
	hooks := hookArguments(configured.Args)
	if len(hooks) == 0 {
		t.Fatal("OMP lifecycle command did not specify its managed hook")
	}
	args := append([]string{"--mode", "rpc", "--model", "aht-compat/compat", "--approval-mode", "always-ask"}, hooks...)
	cmd := host.command(t, configured.Env, args...)
	input, next := permissionJSONPipes(t, cmd)
	process := startPermissionProcess(t, host, cmd)
	sendPermissionJSON(t, input, map[string]any{"id": "permission-prompt", "type": "prompt", "message": compatibilityPrompt})
	request := awaitRPCApprovalRequest(t, next, "OMP", "select")
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
	awaitRPCRunEnd(t, next, "OMP", "select", func(frame map[string]any) bool { return frame["isTerminal"] != false })
	assertPermissionOutcome(t, host, waiting, allow)
	// OMP documents stdin EOF as a graceful session-disposal boundary.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func hookArguments(args []string) []string {
	var hooks []string
	for index := 1; index+1 < len(args); index++ {
		if args[index] == "--hook" {
			hooks = append(hooks, "--hook", args[index+1])
		}
	}
	return hooks
}

func awaitRPCApprovalRequest(t *testing.T, next func() map[string]any, host string, method string) map[string]any {
	t.Helper()
	for {
		frame := next()
		if frame["type"] == "agent_end" {
			t.Fatalf("%s completed without requesting permission", host)
		}
		if frame["type"] == "extension_ui_request" && frame["method"] == method {
			return frame
		}
	}
}

func awaitRPCRunEnd(t *testing.T, next func() map[string]any, host string, method string, final func(map[string]any) bool) {
	t.Helper()
	for {
		frame := next()
		if frame["type"] == "extension_ui_request" && frame["method"] == method {
			t.Fatalf("%s requested a second permission for a single tool call", host)
		}
		if frame["type"] == "agent_end" && final(frame) {
			return
		}
	}
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
