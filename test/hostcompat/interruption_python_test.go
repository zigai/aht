//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

// ACP cancellation is a notification; the original prompt returns cancelled:
// https://agentclientprotocol.com/protocol/v1/prompt-turn#cancellation
func runPythonInterruption(t *testing.T, host isolatedHost, env []string) {
	t.Helper()
	if host.contract.ID != registry.HarnessHermes {
		t.Fatalf("unsupported Python interruption host %s", host.contract.ID)
	}
	command := host.command(env, "acp", "--accept-hooks")
	wire := newPythonPermissionWire(t, command)
	process := startPermissionProcess(t, host, command)
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "initialize", "method": "initialize", "params": map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "aht-compat", "version": "1"}, "clientCapabilities": map[string]any{}}})
	wire.response(t, "initialize")
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "new", "method": "session/new", "params": map[string]any{"cwd": host.work, "mcpServers": []any{}}})
	created := wire.response(t, "new")
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(created.Result, &session); err != nil || session.SessionID == "" {
		t.Fatalf("Hermes ACP returned invalid session: %s (%v)", created.Result, err)
	}
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": "session/prompt", "params": map[string]any{"sessionId": session.SessionID, "prompt": []any{map[string]any{"type": "text", "text": compatibilityPrompt}}}})
	select {
	case step := <-host.provider.checkpoints:
		if step != 0 {
			t.Fatalf("first active Python provider request step = %d", step)
		}
	case <-process.done:
		t.Fatalf("Python host exited before active request: %v", process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatal("Python host did not reach active provider request")
	}
	host.waitForActiveSession(t)

	wire.send(t, map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": session.SessionID}})
	// The provider response stays held: neither a tool call nor process exit
	// can stand in for the native prompt's cancellation response.
	completed := wire.response(t, "prompt")
	var result struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(completed.Result, &result); err != nil || result.StopReason != "cancelled" {
		t.Fatalf("native prompt did not report cancellation: %s (%v)", completed.Result, err)
	}
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}
