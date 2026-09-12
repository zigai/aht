//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

// Cline --acp implements ACP's active-turn cancellation:
// https://agentclientprotocol.com/protocol/v1/prompt-turn#cancellation
// Cline: apps/cli/src/acp/acpAgent.ts, cancel() -> ClineCore.abort().
// Closing stdin happens only after the pending prompt reports "cancelled";
// process exit alone is not evidence that a native turn was interrupted.
func runCLIInterruption(t *testing.T, host isolatedHost, env []string) {
	t.Helper()
	var command *exec.Cmd
	switch host.contract.ID {
	case registry.HarnessCline:
		// ACP branches before the CLI's ordinary provider/sandbox argument
		// handling. Its documented environment supplies provider selection;
		// force the native local backend to avoid a detached hub daemon.
		command = clineACPCommand(host, env)
	default:
		t.Fatalf("no native CLI interruption driver for %s", host.contract.ID)
	}
	wire, process := startCLIPermissionWire(t, host, command, false)
	cliInterruptionCall(t, wire, "initialize", "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]string{"name": "aht-compat", "version": "1"},
		"clientCapabilities": map[string]any{},
	})
	created := cliInterruptionCall(t, wire, "new", "session/new", map[string]any{"cwd": host.work, "mcpServers": []any{}})
	sessionID := cliString(t, cliField(t, created, "sessionId"))
	if sessionID == "" {
		t.Fatal("native ACP session has no identity")
	}
	if host.contract.ID == registry.HarnessCline {
		// ACP model defaults are catalog-scoped; explicitly select our fake
		// model through the native model setter even if absent from a catalog.
		cliInterruptionCall(t, wire, "model", "session/set_model", map[string]any{"sessionId": sessionID, "modelId": "compat"})
	}
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": "session/prompt", "params": map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]string{{"type": "text", "text": compatibilityPrompt}},
	}})
	select {
	case step := <-host.provider.checkpoints:
		if step != 0 {
			t.Fatalf("first native CLI provider request step = %d, want 0", step)
		}
	case <-process.done:
		t.Fatalf("native CLI exited before active provider request: %v", process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatal("native CLI did not reach active provider request")
	}
	host.waitForActiveSession(t)
	wire.send(t, map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": sessionID}})
	result := cliInterruptionResponse(t, wire, "prompt")
	if reason := cliString(t, cliField(t, result, "stopReason")); reason != "cancelled" {
		t.Fatalf("native CLI interruption stop reason = %q, want cancelled", reason)
	}
	if count := len(host.provider.Requests()); count != 1 {
		t.Fatalf("interrupted native CLI sent %d model requests, want one held request", count)
	}
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func cliInterruptionCall(t *testing.T, wire *cliPermissionWire, id, method string, params any) json.RawMessage {
	t.Helper()
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return cliInterruptionResponse(t, wire, id)
}

func cliInterruptionResponse(t *testing.T, wire *cliPermissionWire, id string) json.RawMessage {
	t.Helper()
	for {
		message := wire.receive(t)
		if len(message["id"]) > 0 {
			if len(message["method"]) > 0 || cliString(t, message["id"]) != id {
				t.Fatalf("unexpected native ACP request/response while awaiting %s: %v", id, message)
			}
			return message["result"]
		}
		method := cliString(t, message["method"])
		// ACP extension notifications are one-way and may be ignored when
		// unrecognized: https://agentclientprotocol.com/protocol/v1/extensibility
		if strings.HasPrefix(method, "_") {
			continue
		}
		if method != "session/update" {
			t.Fatalf("unexpected native ACP notification: %v", message)
		}
		update := cliField(t, message["params"], "update")
		kind := cliString(t, cliField(t, update, "sessionUpdate"))
		if kind == "tool_call" || kind == "tool_call_update" {
			t.Fatalf("native ACP emitted a tool call before held provider request was released: %s", update)
		}
	}
}

func clineACPCommand(host isolatedHost, env []string) *exec.Cmd {
	env = append(env, "CLINE_PROVIDER=openai-compatible", "CLINE_API_KEY=compat", "CLINE_MODEL=compat", "CLINE_SESSION_BACKEND_MODE=local")
	return host.command(env, "--config", filepath.Join(host.root, "cline"), "--acp", "--auto-approve", "true")
}

// ACP loads the persisted core session under its original session ID. No
// exported transcript or test-side history conversion is involved.
func (host isolatedHost) runClineResume(t *testing.T, env []string, previous registry.Session) {
	t.Helper()
	if previous.SessionID == "" {
		t.Fatal("Cline resume requires the recorded native session ID")
	}
	wire, process := startCLIPermissionWire(t, host, clineACPCommand(host, env), false)
	cliInterruptionCall(t, wire, "initialize", "initialize", map[string]any{
		"protocolVersion":    1,
		"clientInfo":         map[string]string{"name": "aht-compat", "version": "1"},
		"clientCapabilities": map[string]any{},
	})
	// Loading replays historical tool calls, not requests to execute them.
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "load", "method": "session/load", "params": map[string]any{
		"sessionId": previous.SessionID, "cwd": host.work, "mcpServers": []any{},
	}})
	clineResumeResponse(t, wire, "load")
	cliInterruptionCall(t, wire, "model", "session/set_model", map[string]any{"sessionId": previous.SessionID, "modelId": "compat"})
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": "session/prompt", "params": map[string]any{
		"sessionId": previous.SessionID,
		"prompt":    []map[string]string{{"type": "text", "text": compatibilityPrompt}},
	}})
	for expected := range 2 {
		select {
		case step := <-host.provider.checkpoints:
			if step != expected {
				t.Fatalf("resumed Cline provider step = %d, want %d", step, expected)
			}
		case <-process.done:
			t.Fatalf("Cline exited before resumed request %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Cline did not reach resumed provider request %d", expected)
		}
		host.waitForActiveSession(t)
		select {
		case host.provider.release <- struct{}{}:
		case <-process.done:
			t.Fatalf("Cline exited before resumed response %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Cline did not consume resumed response %d", expected)
		}
	}
	result := clineResumeResponse(t, wire, "prompt")
	if reason := cliString(t, cliField(t, result, "stopReason")); reason != "end_turn" {
		t.Fatalf("resumed Cline stop reason = %q, want end_turn", reason)
	}
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func clineResumeResponse(t *testing.T, wire *cliPermissionWire, id string) json.RawMessage {
	t.Helper()
	for {
		message := wire.receive(t)
		if len(message["id"]) > 0 {
			if len(message["method"]) > 0 || cliString(t, message["id"]) != id {
				t.Fatalf("unexpected Cline ACP request/response while awaiting %s: %v", id, message)
			}
			return message["result"]
		}
		if string(message["method"]) != `"session/update"` {
			t.Fatalf("unexpected Cline ACP notification: %v", message)
		}
	}
}
