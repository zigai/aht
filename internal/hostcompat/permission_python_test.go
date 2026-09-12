//go:build compatibility

package hostcompat

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

// Both protocols expose the host's real approval gate over stdio; neither print
// mode nor a test-generated lifecycle hook is involved.
func runPythonPermissionScenarios(t *testing.T, contract hostContract) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, allow)
			configured, setup := host.lifecycleCommand(t)
			if len(setup) != 0 {
				t.Fatal("unexpected Python host setup commands")
			}
			var command *exec.Cmd
			switch contract.ID {
			case registry.HarnessKimiCode:
				command = host.kimiWireCommand(t, configured.Env, []string{"--no-thinking", "--model", "aht-compat", "--max-steps-per-turn", "2"})
			case registry.HarnessHermes:
				command = host.command(configured.Env, "acp", "--accept-hooks")
			default:
				t.Fatalf("unsupported Python permission host %s", contract.ID)
			}
			wire := newPythonPermissionWire(t, command)
			process := startPermissionProcess(t, host, command)
			promptMethod := "prompt"
			promptParams := map[string]any{"user_input": compatibilityPrompt}
			sessionID := ""
			if contract.ID == registry.HarnessKimiCode {
				// Kimi's root approval hub suppresses requests until initialize.
				// No external tools or hook subscriptions are needed.
				wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "initialize", "method": "initialize", "params": map[string]any{"protocol_version": "1.10", "client": map[string]any{"name": "aht-compat", "version": "1"}}})
				wire.response(t, "initialize")
			}
			if contract.ID == registry.HarnessHermes {
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
				sessionID = session.SessionID
				promptMethod = "session/prompt"
				promptParams = map[string]any{"sessionId": sessionID, "prompt": []any{map[string]any{"type": "text", "text": compatibilityPrompt}}}
			}
			wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": promptMethod, "params": promptParams})
			for {
				message := wire.read(t)
				if message.Method == "event" || message.Method == "session/update" {
					continue
				}
				if len(message.Error) != 0 {
					t.Fatalf("native permission RPC error: %s", message.Error)
				}
				var result any
				if contract.ID == registry.HarnessKimiCode {
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
					result = map[string]any{"request_id": request.Payload.ID, "response": choice}
				} else {
					var request struct {
						SessionID string `json:"sessionId"`
						Options   []struct {
							OptionID string `json:"optionId"`
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
					optionID := ""
					for _, option := range request.Options {
						if option.Kind == kind {
							optionID = option.OptionID
							break
						}
					}
					if optionID == "" {
						t.Fatalf("Hermes did not offer %s", kind)
					}
					result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
				}
				if len(message.ID) == 0 || string(message.ID) == "null" {
					t.Fatal("approval request has no response ID")
				}
				waiting := assertPermissionWaiting(t, host)
				wire.send(t, map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
				completed := wire.response(t, "prompt")
				var finish struct {
					Status     string `json:"status"`
					StopReason string `json:"stopReason"`
				}
				if err := json.Unmarshal(completed.Result, &finish); err != nil {
					t.Fatal(err)
				}
				if contract.ID == registry.HarnessKimiCode && finish.Status != "finished" || contract.ID == registry.HarnessHermes && finish.StopReason != "end_turn" {
					t.Fatalf("native permission turn did not finish: %s", completed.Result)
				}
				assertPermissionOutcome(t, host, waiting, allow)
				if err := wire.input.Close(); err != nil {
					t.Fatal(err)
				}
				process.wait(t)
				break
			}
		})
	}
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
	output  *os.File
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
	return &pythonPermissionWire{input: inputFile, output: outputFile, scanner: scanner}
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

func (wire *pythonPermissionWire) response(t *testing.T, id string) pythonPermissionMessage {
	t.Helper()
	for {
		message := wire.read(t)
		if message.Method == "event" || message.Method == "session/update" {
			continue
		}
		if len(message.Error) != 0 {
			t.Fatalf("native permission RPC error: %s", message.Error)
		}
		var responseID string
		if err := json.Unmarshal(message.ID, &responseID); err != nil || responseID != id || message.Method != "" {
			t.Fatalf("expected native RPC response %q, got %+v", id, message)
		}
		return message
	}
}
