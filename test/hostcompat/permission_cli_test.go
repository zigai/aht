//go:build compatibility

package hostcompat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func runCLIPermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, oracle, allow)
			configured, setup := host.lifecycleCommand(t)
			if len(setup) != 0 {
				t.Fatal("unexpected CLI provider setup commands")
			}
			switch contract.ID {
			case registry.Harness("claude"):
				runClaudePermission(t, host, configured.Env, allow)
			case registry.Harness("codex"):
				runCodexPermission(t, host, configured.Env, allow)
			case registry.Harness("copilot"):
				env := make([]string, 0, len(configured.Env))
				for _, value := range configured.Env {
					if !strings.HasPrefix(value, "COPILOT_ALLOW_ALL=") {
						env = append(env, value)
					}
				}
				runCopilotPermission(t, host, env, allow)
			default:
				t.Fatalf("no CLI permission driver for %s", contract.ID)
			}
		})
	}
}

// The hosts expose two native stdio framings: JSONL and SDK Content-Length.
// Explicit pipes permit deadlines without reader goroutines surviving a failure.
type cliPermissionWire struct {
	input    *os.File
	output   *os.File
	reader   *bufio.Reader
	framed   bool
	deadline time.Time
}

func startCLIPermissionWire(t *testing.T, host *isolatedHost, command *exec.Cmd, framed bool) (*cliPermissionWire, *permissionProcess) {
	t.Helper()
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, ok := input.(*os.File)
	if !ok {
		t.Fatal("native stdin is not an OS pipe")
	}
	out, ok := output.(*os.File)
	if !ok {
		t.Fatal("native stdout is not an OS pipe")
	}
	wire := &cliPermissionWire{input: in, output: out, reader: bufio.NewReader(out), framed: framed, deadline: time.Now().Add(90 * time.Second)}
	process := startPermissionProcess(t, host, command)
	t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
	return wire, process
}

func (wire *cliPermissionWire) send(t *testing.T, message any) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.input.SetWriteDeadline(wire.deadline); err != nil {
		t.Fatal(err)
	}
	if wire.framed {
		data = append(fmt.Appendf(nil, "Content-Length: %d\r\n\r\n", len(data)), data...)
	} else {
		data = append(data, '\n')
	}
	if _, err := wire.input.Write(data); err != nil {
		t.Fatalf("writing native permission protocol: %v", err)
	}
}

func (wire *cliPermissionWire) receive(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	if err := wire.output.SetReadDeadline(wire.deadline); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if wire.framed {
		data = wire.readFramedMessage(t)
	} else {
		data = wire.readLineMessage(t)
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("invalid native protocol message: %v\n%s", err, data)
	}
	if value := message["error"]; len(value) > 0 && string(value) != "null" {
		t.Fatalf("native RPC error: %s", data)
	}
	return message
}

func (wire *cliPermissionWire) readFramedMessage(t *testing.T) []byte {
	t.Helper()
	length := -1
	for count := 0; ; count++ {
		if count == 16 {
			t.Fatal("too many native RPC headers")
		}
		line, err := wire.reader.ReadSlice('\n')
		if err != nil {
			t.Fatalf("reading native RPC header: %v", err)
		}
		text := strings.TrimSpace(string(line))
		if text == "" {
			break
		}
		if value, ok := strings.CutPrefix(text, "Content-Length:"); ok {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if length < 0 || length > 4<<20 {
		t.Fatalf("invalid native RPC body length %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(wire.reader, data); err != nil {
		t.Fatal(err)
	}
	return data
}

func (wire *cliPermissionWire) readLineMessage(t *testing.T) []byte {
	t.Helper()
	var data []byte
	for {
		part, err := wire.reader.ReadSlice('\n')
		data = append(data, part...)
		if len(data) > 4<<20 {
			t.Fatal("native JSONL message exceeds 4 MiB")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			t.Fatalf("reading native JSONL: %v", err)
		}
		return data
	}
}

func cliField(t *testing.T, data json.RawMessage, key string) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decoding native %s: %v: %s", key, err, data)
	}
	value, ok := object[key]
	if !ok {
		t.Fatalf("native message missing %s: %s", key, data)
	}
	return value
}

func cliString(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func (wire *cliPermissionWire) call(t *testing.T, id int, method string, params any) json.RawMessage {
	t.Helper()
	message := map[string]any{"id": id, "method": method, "params": params}
	if wire.framed {
		message["jsonrpc"] = "2.0"
	}
	wire.send(t, message)
	for {
		response := wire.receive(t)
		if string(response["id"]) == strconv.Itoa(id) {
			return response["result"]
		}
		if len(response["id"]) > 0 && len(response["method"]) > 0 {
			t.Fatalf("unexpected native request while calling %s: %v", method, response)
		}
	}
}

func runClaudePermission(t *testing.T, host *isolatedHost, env []string, allow bool) {
	t.Helper()
	command := host.command(t, env, "-p", "--model", "compat", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-prompt-tool", "stdio", "--permission-mode", "default", "--settings", `{"permissions":{"ask":["Bash"]}}`)
	wire, process := startCLIPermissionWire(t, host, command, false)
	wire.send(t, map[string]any{"type": "control_request", "request_id": "aht-init", "request": map[string]any{"subtype": "initialize"}})
	awaitClaudeInitialized(t, wire)
	wire.send(t, map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": compatibilityPrompt}, "parent_tool_use_id": nil})
	var waiting registry.Session
	approved := false
	for {
		message := wire.receive(t)
		switch string(message["type"]) {
		case `"control_request"`:
			if approved {
				t.Fatalf("unexpected second Claude control request: %s", message["request"])
			}
			waiting = answerClaudePermission(t, host, wire, message, allow)
			approved = true
		case `"result"`:
			if !approved || string(message["is_error"]) == "true" {
				t.Fatalf("Claude ended without successful permission continuation: %v", message)
			}
			if err := wire.input.Close(); err != nil {
				t.Fatal(err)
			}
			process.wait(t)
			assertPermissionOutcome(t, host, waiting, allow)
			return
		case `"assistant"`, `"system"`, `"progress"`, `"ping"`:
			continue
		}
	}
}

func awaitClaudeInitialized(t *testing.T, wire *cliPermissionWire) {
	t.Helper()
	for {
		message := wire.receive(t)
		if string(message["type"]) != `"control_response"` {
			continue
		}
		response := message["response"]
		if cliString(t, cliField(t, response, "subtype")) != "success" {
			t.Fatalf("Claude initialization failed: %s", response)
		}
		return
	}
}

func answerClaudePermission(t *testing.T, host *isolatedHost, wire *cliPermissionWire, message map[string]json.RawMessage, allow bool) registry.Session {
	t.Helper()
	request := message["request"]
	if cliString(t, cliField(t, request, "subtype")) != "can_use_tool" || cliString(t, cliField(t, request, "tool_name")) != "Bash" {
		t.Fatalf("unexpected Claude control request: %s", request)
	}
	waiting := assertPermissionWaiting(t, host)
	decision := map[string]any{"behavior": "deny", "message": "User denied this action"}
	if allow {
		decision = map[string]any{"behavior": "allow", "updatedInput": cliField(t, request, "input")}
	}
	wire.send(t, map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": message["request_id"], "response": decision}})
	return waiting
}

func runCodexPermission(t *testing.T, host *isolatedHost, env []string, allow bool) {
	t.Helper()
	wire, process := startCLIPermissionWire(t, host, host.command(t, env, "app-server"), false)
	wire.call(t, 1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "aht_compat", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}})
	wire.send(t, map[string]any{"method": "initialized", "params": map[string]any{}})
	id := trustCodexHooks(t, host, wire, 3)
	thread := wire.call(t, id, "thread/start", map[string]any{"model": "compat", "cwd": host.work, "approvalPolicy": "untrusted", "sandbox": "workspace-write"})
	threadID := cliString(t, cliField(t, cliField(t, thread, "thread"), "id"))
	wire.call(t, id+1, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": compatibilityPrompt}}})
	var waiting registry.Session
	approved := false
	for {
		message := wire.receive(t)
		switch string(message["method"]) {
		case `"item/commandExecution/requestApproval"`:
			if approved || cliString(t, cliField(t, message["params"], "threadId")) != threadID {
				t.Fatal("unexpected Codex approval request")
			}
			waiting = assertPermissionWaiting(t, host)
			wire.send(t, map[string]any{"id": message["id"], "result": map[string]any{"decision": codexDecision(allow)}})
			approved = true
		case `"turn/completed"`:
			if !approved || cliString(t, cliField(t, cliField(t, message["params"], "turn"), "status")) != "completed" {
				t.Fatalf("Codex turn did not complete after approval: %v", message)
			}
			assertPermissionOutcome(t, host, waiting, allow)
			if err := wire.input.Close(); err != nil {
				t.Fatal(err)
			}
			process.wait(t)
			return
		default:
			if len(message["id"]) > 0 {
				t.Fatalf("unexpected Codex server request: %v", message)
			}
		}
	}
}

func trustCodexHooks(t *testing.T, host *isolatedHost, wire *cliPermissionWire, firstID int) int {
	t.Helper()
	inventory := wire.call(t, 2, "hooks/list", map[string]any{"cwds": []string{host.work}})
	var entries []struct {
		Hooks []struct {
			Key     string `json:"key"`
			Hash    string `json:"currentHash"` //nolint:tagliatelle // Codex app-server wire field.
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(cliField(t, inventory, "data"), &entries); err != nil {
		t.Fatal(err)
	}
	id := firstID
	for _, entry := range entries {
		for _, hook := range entry.Hooks {
			if !strings.Contains(hook.Command, host.aht) || hook.Key == "" || hook.Hash == "" {
				t.Fatalf("refusing to trust unexpected hook: %+v", hook)
			}
			wire.call(t, id, "config/value/write", map[string]any{"keyPath": "hooks.state." + strconv.Quote(hook.Key) + ".trusted_hash", "value": hook.Hash, "mergeStrategy": "replace"})
			id++
		}
	}
	if id == firstID {
		t.Fatal("Codex did not discover installed managed AHT hooks")
	}
	return id
}

func codexDecision(allow bool) string {
	if allow {
		return "accept"
	}
	return "decline"
}

func runCopilotPermission(t *testing.T, host *isolatedHost, env []string, allow bool) {
	t.Helper()
	wire, process := startCLIPermissionWire(t, host, host.command(t, env, "--headless", "--stdio", "--no-auto-update", "--no-auto-login"), true)
	created := wire.call(t, 2, "session.create", map[string]any{"model": "compat", "workingDirectory": host.work, "requestPermission": true, "provider": map[string]any{"type": "openai", "baseUrl": host.provider.URL() + "/v1", "apiKey": "compat"}})
	sessionID := cliString(t, cliField(t, created, "sessionId"))
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session.send", "params": map[string]any{"sessionId": sessionID, "prompt": compatibilityPrompt}})
	turn := &copilotPermissionTurn{host: host, wire: wire, sessionID: sessionID, allow: allow}
	for !turn.finished() {
		message := wire.receive(t)
		if string(message["method"]) == `"session.event"` {
			turn.observeEvent(t, message["params"])
		} else {
			turn.observeResponse(t, message)
		}
	}
	assertPermissionOutcome(t, host, turn.waiting, allow)
	// The shipped SDK disconnects with session.destroy, preserving disk
	// history; session.delete is the separate destructive operation.
	wire.call(t, 5, "session.destroy", map[string]any{"sessionId": sessionID})
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

type copilotPermissionTurn struct {
	host                *isolatedHost
	wire                *cliPermissionWire
	sessionID           string
	allow               bool
	waiting             registry.Session
	permissionRequested bool
	permissionHandled   bool
	turnIdle            bool
	sendAcknowledged    bool
	toolCompleted       bool
}

func (turn *copilotPermissionTurn) finished() bool {
	return turn.turnIdle && turn.sendAcknowledged && turn.permissionHandled && turn.toolCompleted
}

func (turn *copilotPermissionTurn) observeResponse(t *testing.T, message map[string]json.RawMessage) {
	t.Helper()
	switch string(message["id"]) {
	case "3":
		if cliString(t, cliField(t, message["result"], "messageId")) == "" {
			t.Fatal("Copilot did not acknowledge the submitted prompt")
		}
		turn.sendAcknowledged = true
	case "4":
		if !turn.permissionRequested || string(cliField(t, message["result"], "success")) != "true" {
			t.Fatalf("Copilot did not handle the pending permission: %s", message["result"])
		}
		turn.permissionHandled = true
	}
	if len(message["method"]) > 0 && len(message["id"]) > 0 {
		t.Fatalf("unexpected Copilot request: %v", message)
	}
}

func (turn *copilotPermissionTurn) observeEvent(t *testing.T, params json.RawMessage) {
	t.Helper()
	if cliString(t, cliField(t, params, "sessionId")) != turn.sessionID {
		t.Fatal("Copilot event belongs to another session")
	}
	event := cliField(t, params, "event")
	switch cliString(t, cliField(t, event, "type")) {
	case "permission.requested":
		turn.answerPermission(t, cliField(t, event, "data"))
	case "tool.execution_complete":
		turn.observeToolCompletion(t, cliField(t, event, "data"))
	case "session.error":
		t.Fatalf("Copilot native session failed: %s", event)
	case "session.idle":
		if !turn.permissionRequested {
			t.Fatal("Copilot became idle without requesting permission")
		}
		turn.turnIdle = true
	}
}

func (turn *copilotPermissionTurn) answerPermission(t *testing.T, data json.RawMessage) {
	t.Helper()
	if turn.permissionRequested {
		t.Fatal("Copilot requested multiple permissions for single marker operation")
	}
	var permission struct {
		RequestID      string `json:"requestId"`      //nolint:tagliatelle // Copilot session event field.
		ResolvedByHook bool   `json:"resolvedByHook"` //nolint:tagliatelle // Copilot session event field.
	}
	if err := json.Unmarshal(data, &permission); err != nil {
		t.Fatal(err)
	}
	if permission.RequestID == "" || permission.ResolvedByHook {
		t.Fatalf("Copilot permission is not pending for an explicit decision: %s", data)
	}
	turn.waiting = assertPermissionWaiting(t, turn.host)
	if turn.waiting.SessionID != turn.sessionID {
		t.Fatalf("Copilot waiting session %q does not match RPC session %q", turn.waiting.SessionID, turn.sessionID)
	}
	allowKind, rejectKind := copilotDecisionKinds(t, turn.host)
	decision := map[string]any{"kind": rejectKind, "feedback": "User denied this action"}
	if turn.allow {
		decision = map[string]any{"kind": allowKind}
	}
	turn.wire.send(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session.permissions.handlePendingPermissionRequest", "params": map[string]any{"sessionId": turn.sessionID, "requestId": permission.RequestID, "result": decision}})
	turn.permissionRequested = true
}

func copilotDecisionKinds(t *testing.T, host *isolatedHost) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	out, versionErr := compatibilityOutput(ctx, host.command(t, host.env, "--version"))
	cancel()
	if versionErr != nil {
		t.Fatalf("Copilot permission version probe: %v\n%s", versionErr, out)
	}
	if strings.Contains(string(out), "1.0.2") {
		return "approved", "denied-interactively-by-user"
	}
	return "approve-once", "reject"
}

func (turn *copilotPermissionTurn) observeToolCompletion(t *testing.T, data json.RawMessage) {
	t.Helper()
	if cliString(t, cliField(t, data, "toolCallId")) != turn.host.provider.callID {
		t.Fatalf("Copilot completed an unrelated tool: %s", data)
	}
	success := string(cliField(t, data, "success")) == "true"
	if success != turn.allow || (!turn.allow && cliString(t, cliField(t, cliField(t, data, "error"), "code")) != "denied") {
		t.Fatalf("Copilot tool outcome does not match the native permission decision: %s", data)
	}
	turn.toolCompleted = true
}
