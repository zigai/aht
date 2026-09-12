//go:build compatibility

package hostcompat

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

// Both native RPC implementations await session.abort before acknowledging abort.
// Pi documents agent_settled as the no-continuation boundary; OMP marks its final
// agent_end isTerminal=true. The canceled assistant has stopReason="aborted".
// Current Pi and OMP RPC stdin-EOF handlers dispose the real session, emitting
// session_shutdown; no synthetic callback or signal supplies cancellation.
// Sources: Pi docs/rpc.md and dist/modes/rpc/rpc-mode.js; OMP
// src/modes/rpc/rpc-mode.ts and src/session/agent-session.ts (installed packages).
func runRPCInterruption(t *testing.T, host isolatedHost, env []string) {
	t.Helper()
	var args []string
	switch host.contract.ID {
	case registry.HarnessPi:
		args = []string{"--mode", "rpc", "--provider", "aht-compat", "--model", "compat"}
	case registry.HarnessOmp:
		// lifecycleCommand has configured the isolated model and installed hook.
		// Preserve its explicit managed extension loading without print-mode flags.
		hooks, err := filepath.Glob(filepath.Join(host.root, "pi-agent", "extensions", "*"))
		if err != nil || len(hooks) != 1 {
			t.Fatalf("locating installed OMP interruption hook: paths=%v error=%v", hooks, err)
		}
		args = []string{"--mode", "rpc", "--model", "aht-compat/compat", "--hook", hooks[0]}
	default:
		t.Fatalf("unsupported native RPC interruption host %s", host.contract.ID)
	}
	cmd := host.command(env, args...)
	input, next := permissionJSONPipes(t, cmd)
	process := startPermissionProcess(t, host, cmd)
	sendPermissionJSON(t, input, map[string]any{"id": "interruption-prompt", "type": "prompt", "message": compatibilityPrompt})
	select {
	case step := <-host.provider.checkpoints:
		if step != 0 {
			t.Fatalf("first active RPC request step = %d", step)
		}
	case <-process.done:
		t.Fatalf("RPC host exited before active provider request: %v", process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatal("RPC host did not reach active provider request")
	}
	host.waitForActiveSession(t)
	// Deliberately never release the held model response: no tool call has yet
	// been delivered, and this command must cancel actual in-flight model IO.
	sendPermissionJSON(t, input, map[string]any{"id": "interruption-abort", "type": "abort"})
	acknowledged, ended, settled, started := false, false, false, false
	for !acknowledged || !ended || !settled {
		frame := next()
		switch frame["type"] {
		case "response":
			if frame["id"] == "interruption-abort" {
				if frame["command"] != "abort" || frame["success"] != true {
					t.Fatalf("native RPC abort was not acknowledged: %v", frame)
				}
				acknowledged = true
			}
		case "agent_start":
			if started {
				t.Fatal("RPC host continued the canceled agent run")
			}
			started = true
		case "tool_execution_start", "tool_execution_end", "auto_retry_start", "auto_compaction_start":
			t.Fatalf("RPC host executed a tool or continued after held request cancellation: %v", frame)
		case "agent_end":
			if ended || frame["willRetry"] == true || frame["isTerminal"] == false {
				t.Fatalf("RPC cancellation did not end a single terminal run: %v", frame)
			}
			messages, ok := frame["messages"].([]any)
			if !ok || len(messages) == 0 {
				t.Fatalf("RPC canceled completion lacks messages: %v", frame)
			}
			last, ok := messages[len(messages)-1].(map[string]any)
			if !ok || last["role"] != "assistant" || last["stopReason"] != "aborted" {
				t.Fatalf("RPC completion was not a canceled assistant response: %v", frame)
			}
			ended = true
			if host.contract.ID == registry.HarnessOmp {
				if frame["isTerminal"] != true {
					t.Fatalf("OMP cancellation lacks terminal agent_end: %v", frame)
				}
				settled = true
			}
		case "agent_settled":
			settled = true
		}
	}
	if !started {
		t.Fatal("RPC cancellation lacked native agent_start")
	}
	// EOF is an orderly disposal boundary only after native abort completed.
	// permissionJSONPipes owns deadline-bearing pipes, not reader goroutines;
	// startPermissionProcess owns/reaps the entire process group on failures.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}
