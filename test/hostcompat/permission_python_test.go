//go:build compatibility

package hostcompat

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

// Both protocols expose the host's real approval gate over stdio; neither print
// mode nor a test-generated lifecycle hook is involved.
func runPythonPermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			runPythonPermission(t, contract, oracle, allow)
		})
	}
}

func runPythonPermission(t *testing.T, contract hostContract, oracle string, allow bool) {
	t.Helper()
	host := newPermissionHost(t, contract, oracle, allow)
	configured, setup := host.lifecycleCommand(t)
	if len(setup) != 0 {
		t.Fatal("unexpected Python host setup commands")
	}
	command := pythonPermissionCommand(t, host, configured.Env)
	wire := newPythonPermissionWire(t, command)
	process := startPermissionProcess(t, host, command)
	sessionID, promptMethod, promptParams := openPythonPermissionSession(t, host, wire)
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": promptMethod, "params": promptParams})
	message := wire.request(t)
	var result any
	if contract.ID == registry.Harness("kimi-code") {
		result = kimiApprovalResult(t, message, allow)
	} else {
		result = hermesApprovalResult(t, message, sessionID, allow)
	}
	if len(message.ID) == 0 || string(message.ID) == "null" {
		t.Fatal("approval request has no response ID")
	}
	waiting := assertPermissionWaiting(t, host)
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
	requirePythonTurnFinished(t, contract.ID, wire.response(t, "prompt"))
	assertPermissionOutcome(t, host, waiting, allow)
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func pythonPermissionCommand(t *testing.T, host *isolatedHost, env []string) *exec.Cmd {
	t.Helper()
	switch host.contract.ID {
	case registry.Harness("kimi-code"):
		return host.kimiWireCommand(t, env, []string{"--no-thinking", "--model", "aht-compat", "--max-steps-per-turn", "2"})
	case registry.Harness("hermes"):
		return host.command(t, env, "acp", "--accept-hooks")
	default:
		t.Fatalf("unsupported Python permission host %s", host.contract.ID)
		return nil
	}
}

func requirePythonTurnFinished(t *testing.T, id registry.Harness, completed pythonPermissionMessage) {
	t.Helper()
	var finish struct {
		Status     string `json:"status"`
		StopReason string `json:"stopReason"` //nolint:tagliatelle // ACP wire field.
	}
	if err := json.Unmarshal(completed.Result, &finish); err != nil {
		t.Fatal(err)
	}
	if id == registry.Harness("kimi-code") && finish.Status != "finished" || id == registry.Harness("hermes") && finish.StopReason != "end_turn" {
		t.Fatalf("native permission turn did not finish: %s", completed.Result)
	}
}

func openPythonPermissionSession(t *testing.T, host *isolatedHost, wire *pythonPermissionWire) (string, string, map[string]any) {
	t.Helper()
	if host.contract.ID == registry.Harness("kimi-code") {
		// Kimi's root approval hub suppresses requests until initialize.
		// No external tools or hook subscriptions are needed.
		wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "initialize", "method": "initialize", "params": map[string]any{"protocol_version": "1.10", "client": map[string]any{"name": "aht-compat", "version": "1"}}})
		wire.response(t, "initialize")
		return "", "prompt", map[string]any{"user_input": compatibilityPrompt}
	}
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "initialize", "method": "initialize", "params": map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "aht-compat", "version": "1"}, "clientCapabilities": map[string]any{}}})
	wire.response(t, "initialize")
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "new", "method": "session/new", "params": map[string]any{"cwd": host.work, "mcpServers": []any{}}})
	created := wire.response(t, "new")
	var session struct {
		SessionID string `json:"sessionId"` //nolint:tagliatelle // ACP wire field.
	}
	if err := json.Unmarshal(created.Result, &session); err != nil || session.SessionID == "" {
		t.Fatalf("Hermes ACP returned invalid session: %s (%v)", created.Result, err)
	}
	return session.SessionID, "session/prompt", map[string]any{"sessionId": session.SessionID, "prompt": []any{map[string]any{"type": "text", "text": compatibilityPrompt}}}
}

func kimiApprovalResult(t *testing.T, message pythonPermissionMessage, allow bool) map[string]any {
	t.Helper()
	var request struct {
		Type    string `json:"type"`
		Payload struct {
			ID         string `json:"id"`
			Sender     string `json:"sender"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(message.Params, &request); err != nil || message.Method != "request" || request.Type != "ApprovalRequest" || request.Payload.ID == "" || request.Payload.Sender != "Shell" || request.Payload.ToolCallID == "" {
		t.Fatalf("expected native Kimi Shell approval, got %+v (%v)", message, err)
	}
	choice := "reject"
	if allow {
		choice = "approve"
	}
	return map[string]any{"request_id": request.Payload.ID, "response": choice}
}

func hermesApprovalResult(t *testing.T, message pythonPermissionMessage, sessionID string, allow bool) map[string]any {
	t.Helper()
	var request struct {
		SessionID string `json:"sessionId"` //nolint:tagliatelle // ACP wire field.
		Options   []struct {
			OptionID string `json:"optionId"` //nolint:tagliatelle // ACP wire field.
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(message.Params, &request); err != nil || message.Method != "session/request_permission" || request.SessionID != sessionID {
		t.Fatalf("expected native Hermes session permission, got %+v (%v)", message, err)
	}
	kind := "reject_once"
	if allow {
		kind = "allow_once"
	}
	for _, option := range request.Options {
		if option.Kind != kind {
			continue
		}
		if option.OptionID == "" {
			break
		}
		return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option.OptionID}}
	}
	t.Fatalf("Hermes did not offer %s", kind)
	return nil
}

type pythonPermissionMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type pythonPermissionWire struct {
	input   *os.File
	scanner *bufio.Scanner
}

func newPythonPermissionWire(t *testing.T, command *exec.Cmd) *pythonPermissionWire {
	t.Helper()
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	inputFile, ok := input.(*os.File)
	if !ok {
		t.Fatal("native permission stdin is not a deadline-capable OS pipe")
	}
	outputFile, ok := output.(*os.File)
	if !ok {
		t.Fatal("native permission stdout is not a deadline-capable OS pipe")
	}
	deadline := time.Now().Add(90 * time.Second)
	if err := inputFile.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := outputFile.SetReadDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	return &pythonPermissionWire{input: inputFile, scanner: scanner}
}

func (wire *pythonPermissionWire) send(t *testing.T, message any) {
	t.Helper()
	if err := json.NewEncoder(wire.input).Encode(message); err != nil {
		t.Fatalf("writing native permission RPC: %v", err)
	}
}

func (wire *pythonPermissionWire) read(t *testing.T) pythonPermissionMessage {
	t.Helper()
	if !wire.scanner.Scan() {
		t.Fatalf("native permission RPC ended before expected response: %v", wire.scanner.Err())
	}
	var message pythonPermissionMessage
	if err := json.Unmarshal(wire.scanner.Bytes(), &message); err != nil {
		t.Fatalf("invalid native permission RPC: %v: %s", err, wire.scanner.Bytes())
	}
	return message
}

func (wire *pythonPermissionWire) request(t *testing.T) pythonPermissionMessage {
	t.Helper()
	for {
		message := wire.read(t)
		if message.Method == "event" || message.Method == "session/update" {
			continue
		}
		if len(message.Error) != 0 {
			t.Fatalf("native permission RPC error: %s", message.Error)
		}
		return message
	}
}

func (wire *pythonPermissionWire) response(t *testing.T, id string) pythonPermissionMessage {
	t.Helper()
	message := wire.request(t)
	var responseID string
	if err := json.Unmarshal(message.ID, &responseID); err != nil || responseID != id || message.Method != "" {
		t.Fatalf("expected native RPC response %q, got %+v", id, message)
	}
	return message
}
