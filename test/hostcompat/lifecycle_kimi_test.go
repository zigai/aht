//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

var errKimiPermission = errors.New("invalid Kimi ACP permission request")

func (host *isolatedHost) runKimiACP(t *testing.T, command *exec.Cmd, previous *registry.Session, interrupt bool) {
	t.Helper()
	wire, process := startCLIPermissionWire(t, host, command, false)
	sessionID := host.openKimiSession(t, wire, previous, "yolo")
	host.promptKimi(t, wire, sessionID)
	host.driveKimiCheckpoints(t, wire, process, sessionID, interrupt)
	want := "end_turn"
	if interrupt {
		want = acpCancelledStopReason
	}
	requireKimiStopReason(t, kimiACPResponse(t, wire, "prompt", sessionID), want)
	if interrupt {
		host.waitForKimiEvent(t, sessionID, "Interrupt", registry.ActivityInterrupted)
	} else {
		host.waitForKimiEvent(t, sessionID, "Stop", registry.ActivityIdle)
	}
	host.closeKimiSession(t, wire, process, sessionID)
}

func (host *isolatedHost) openKimiSession(t *testing.T, wire *cliPermissionWire, previous *registry.Session, mode string) string {
	t.Helper()
	initialized := kimiACPCall(t, wire, "initialize", "initialize", "", map[string]any{
		"protocolVersion": 1, "clientInfo": map[string]string{"name": "aht-compat", "version": "1"}, "clientCapabilities": map[string]any{},
	})
	capabilities := cliField(t, initialized, "agentCapabilities")
	if string(cliField(t, capabilities, "loadSession")) != "true" {
		t.Fatal("Kimi ACP does not advertise native session loading")
	}
	method := "session/new"
	params := map[string]any{"cwd": host.work, "mcpServers": []any{}}
	sessionID := ""
	if previous != nil {
		sessionID = previous.SessionID
		if sessionID == "" {
			t.Fatal("Kimi resume requires the recorded native session ID")
		}
		method = "session/load"
		params["sessionId"] = sessionID
	}
	created := kimiACPCall(t, wire, "open", method, sessionID, params)
	if previous == nil {
		sessionID = cliString(t, cliField(t, created, "sessionId"))
		if sessionID == "" {
			t.Fatal("Kimi ACP session has no identity")
		}
	}
	host.waitForKimiEvent(t, sessionID, "SessionStart", registry.ActivityIdle)
	kimiACPCall(t, wire, "mode", "session/set_mode", sessionID, map[string]string{"sessionId": sessionID, "modeId": mode})
	return sessionID
}

func (host *isolatedHost) promptKimi(t *testing.T, wire *cliPermissionWire, sessionID string) {
	t.Helper()
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": "session/prompt", "params": map[string]any{
		"sessionId": sessionID, "prompt": []map[string]string{{"type": "text", "text": compatibilityPrompt}},
	}})
}

func (host *isolatedHost) driveKimiCheckpoints(t *testing.T, wire *cliPermissionWire, process *permissionProcess, sessionID string, interrupt bool) {
	t.Helper()
	for expected := range 2 {
		awaitKimiCheckpoint(t, host, process, expected)
		if expected == 0 {
			host.waitForKimiEvent(t, sessionID, "TurnStarted", registry.ActivityRunning)
		}
		host.waitForObservation(t, "same foreground Kimi session running", func(session registry.Session) bool {
			return kimiNativeSession(session, sessionID) && nativeActivityRunning(session) &&
				session.Presence() == registry.PresenceLive && effectiveActivityMatches(session, registry.ActivityRunning)
		})
		if interrupt {
			wire.send(t, map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]string{"sessionId": sessionID}})
			return
		}
		select {
		case host.provider.release <- struct{}{}:
		case <-process.done:
			t.Fatalf("Kimi ACP exited before response %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Kimi did not consume provider response %d", expected)
		}
	}
}

func awaitKimiCheckpoint(t *testing.T, host *isolatedHost, process *permissionProcess, expected int) {
	t.Helper()
	select {
	case step := <-host.provider.checkpoints:
		if step != expected {
			t.Fatalf("Kimi provider checkpoint = %d, want %d", step, expected)
		}
	case <-process.done:
		t.Fatalf("Kimi ACP exited before provider checkpoint %d: %v", expected, process.waitErr())
	case <-time.After(30 * time.Second):
		t.Fatalf("Kimi did not reach provider checkpoint %d", expected)
	}
}

func kimiNativeSession(session registry.Session, sessionID string) bool {
	native := session.Observations.Native
	return native != nil && native.SessionID == sessionID && session.SessionID == sessionID &&
		native.Reporter.Integration == "kimi-code-hook" && native.Reporter.Version == 15 && native.Reporter.MultiSession &&
		native.Attributes["kimi_code_client_type"] == "kimi_code_cli" &&
		(native.Attributes["kimi_code_agent_id"] == "" || native.Attributes["kimi_code_agent_id"] == "main")
}

func (host *isolatedHost) waitForKimiEvent(t *testing.T, sessionID, event string, activity registry.Activity) registry.Session {
	t.Helper()
	return host.waitForObservation(t, "foreground Kimi "+event+" hook", func(session registry.Session) bool {
		native := session.Observations.Native
		return kimiNativeSession(session, sessionID) && native.Event == event && native.Activity != nil &&
			*native.Activity == activity && effectiveActivityMatches(session, activity) && session.Presence() == registry.PresenceLive
	})
}

func (host *isolatedHost) closeKimiSession(t *testing.T, wire *cliPermissionWire, process *permissionProcess, sessionID string) {
	t.Helper()
	kimiACPCall(t, wire, "close", "session/close", sessionID, map[string]string{"sessionId": sessionID})
	host.waitForObservation(t, "native Kimi SessionEnd before ACP process exit", func(session registry.Session) bool {
		return kimiNativeSession(session, sessionID) && terminalSession(host.contract.ID, session)
	})
	select {
	case <-process.done:
		t.Fatal("Kimi ACP exited instead of closing only its native session")
	default:
	}
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func kimiACPCall(t *testing.T, wire *cliPermissionWire, id, method, sessionID string, params any) json.RawMessage {
	t.Helper()
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return kimiACPResponse(t, wire, id, sessionID)
}

func kimiACPResponse(t *testing.T, wire *cliPermissionWire, id, sessionID string) json.RawMessage {
	t.Helper()
	for {
		message := wire.receive(t)
		if len(message["id"]) > 0 {
			if len(message["method"]) > 0 || cliString(t, message["id"]) != id {
				t.Fatalf("unexpected Kimi ACP request/response while awaiting %s: %v", id, message)
			}
			return message["result"]
		}
		requireKimiNotification(t, message, sessionID)
	}
}

func requireKimiNotification(t *testing.T, message map[string]json.RawMessage, sessionID string) {
	t.Helper()
	method := cliString(t, message["method"])
	if strings.HasPrefix(method, "_") {
		return
	}
	if method != "session/update" {
		t.Fatalf("unexpected Kimi ACP notification: %v", message)
	}
	actual := cliString(t, cliField(t, message["params"], "sessionId"))
	if sessionID != "" && actual != sessionID {
		t.Fatalf("Kimi ACP update session = %q, want foreground %q", actual, sessionID)
	}
}

func requireKimiStopReason(t *testing.T, result json.RawMessage, want string) {
	t.Helper()
	if reason := cliString(t, cliField(t, result, "stopReason")); reason != want {
		t.Fatalf("Kimi ACP stop reason = %q, want %q", reason, want)
	}
}

func runKimiPermissionScenarios(t *testing.T, contract hostContract, oracle string) {
	t.Helper()
	for _, allow := range []bool{true, false} {
		name := "deny"
		if allow {
			name = "allow"
		}
		t.Run(name, func(t *testing.T) {
			host := newPermissionHost(t, contract, oracle, allow)
			command, _ := host.lifecycleCommand(t)
			wire, process := startCLIPermissionWire(t, host, command, false)
			sessionID := host.openKimiSession(t, wire, nil, "default")
			host.promptKimi(t, wire, sessionID)
			var request map[string]json.RawMessage
			for {
				request = wire.receive(t)
				if len(request["id"]) > 0 {
					break
				}
				requireKimiNotification(t, request, sessionID)
			}
			result, err := kimiPermissionResult(request, sessionID, host.provider.callID, allow)
			if err != nil {
				t.Fatal(err)
			}
			waiting := host.waitForKimiEvent(t, sessionID, "PermissionRequest", registry.ActivityWaiting)
			attrs := waiting.Observations.Native.Attributes
			if attrs["kimi_code_agent_id"] != "main" || attrs["kimi_code_tool_name"] != "Bash" || attrs["kimi_code_tool_call_id"] != host.provider.callID {
				t.Fatalf("permission hook does not identify foreground Bash call: %v", attrs)
			}
			assertPermissionMarker(t, host, false)
			wire.send(t, map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
			requireKimiStopReason(t, kimiACPResponse(t, wire, "prompt", sessionID), "end_turn")
			host.waitForKimiEvent(t, sessionID, "Stop", registry.ActivityIdle)
			host.assertKimiCallback(t, "PermissionResult", registry.ActivityRunning)
			assertPermissionOutcome(t, host, waiting, allow)
			host.closeKimiSession(t, wire, process, sessionID)
			host.assertNativeEvents(t)
		})
	}
}

func (host *isolatedHost) assertKimiCallback(t *testing.T, event string, activity registry.Activity) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(host.root, "native-events"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		hasEvent, hasActivity := false, false
		for _, field := range fields {
			hasEvent = hasEvent || field == "--event="+event
			hasActivity = hasActivity || field == "--activity="+string(activity)
		}
		if hasEvent && hasActivity {
			return
		}
	}
	t.Fatalf("missing Kimi %s native callback with activity %s: %s", event, activity, data)
}

func kimiPermissionResult(message map[string]json.RawMessage, sessionID, callID string, allow bool) (map[string]any, error) {
	var method, actualSession, title, actualCall string
	var request, toolCall map[string]json.RawMessage
	var options []map[string]string
	if err := errors.Join(json.Unmarshal(message["method"], &method), json.Unmarshal(message["params"], &request)); err != nil {
		return nil, fmt.Errorf("%w: %w", errKimiPermission, err)
	}
	if err := errors.Join(json.Unmarshal(request["sessionId"], &actualSession), json.Unmarshal(request["toolCall"], &toolCall), json.Unmarshal(request["options"], &options)); err != nil {
		return nil, fmt.Errorf("%w: %w", errKimiPermission, err)
	}
	if err := errors.Join(json.Unmarshal(toolCall["title"], &title), json.Unmarshal(toolCall["toolCallId"], &actualCall)); err != nil {
		return nil, fmt.Errorf("%w: %w", errKimiPermission, err)
	}
	if !kimiPermissionMatches(message, method, actualSession, title, actualCall, sessionID, callID) {
		return nil, errKimiPermission
	}
	kind := "reject_once"
	if allow {
		kind = "allow_once"
	}
	for _, option := range options {
		if option["kind"] == kind && option["optionId"] != "" {
			return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": option["optionId"]}}, nil
		}
	}
	return nil, errKimiPermission
}

func kimiPermissionMatches(message map[string]json.RawMessage, method, actualSession, title, actualCall, sessionID, callID string) bool {
	_, rawID, prefixed := strings.Cut(actualCall, ":")
	return method == "session/request_permission" && len(message["id"]) > 0 && string(message["id"]) != "null" &&
		actualSession == sessionID && title == "Bash" && prefixed && rawID == callID
}

func TestKimiPermissionUsesNativeOptionsAndForegroundIdentity(t *testing.T) {
	t.Parallel()
	for _, allow := range []bool{true, false} {
		message := map[string]json.RawMessage{
			"id": json.RawMessage(`7`), "method": json.RawMessage(`"session/request_permission"`),
			"params": json.RawMessage(`{"sessionId":"foreground","toolCall":{"title":"Bash","toolCallId":"0:call_compat"},"options":[{"kind":"allow_once","optionId":"native-allow"},{"kind":"reject_once","optionId":"native-deny"}]}`),
		}
		result, err := kimiPermissionResult(message, "foreground", "call_compat", allow)
		if err != nil {
			t.Fatal(err)
		}
		want := "native-deny"
		if allow {
			want = "native-allow"
		}
		if outcome, ok := result["outcome"].(map[string]string); !ok || outcome["outcome"] != "selected" || outcome["optionId"] != want {
			t.Fatalf("native permission result = %v, want %s", result, want)
		}
		for _, mutation := range []struct{ old, new string }{
			{`foreground`, `sibling`}, {`Bash`, `Shell`}, {`0:call_compat`, `0:other`}, {`0:call_compat`, `call_compat`}, {`allow_once`, `allow_always`},
		} {
			if mutation.old == "allow_once" && !allow {
				continue
			}
			invalid := map[string]json.RawMessage{"id": message["id"], "method": message["method"], "params": json.RawMessage(strings.ReplaceAll(string(message["params"]), mutation.old, mutation.new))}
			if _, err := kimiPermissionResult(invalid, "foreground", "call_compat", allow); !errors.Is(err, errKimiPermission) {
				t.Fatalf("accepted invalid permission mutation %+v: %v", mutation, err)
			}
		}
	}
}

func TestKimiNativeOracleRejectsUnrelatedAndSubagentEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*registry.NativeObservation)
	}{
		{name: "unrelated reporter", change: func(n *registry.NativeObservation) { n.Reporter.Integration = "other-hook" }},
		{name: "old generation", change: func(n *registry.NativeObservation) { n.Reporter.Version = 14 }},
		{name: "exclusive process", change: func(n *registry.NativeObservation) { n.Reporter.MultiSession = false }},
		{name: "child agent", change: func(n *registry.NativeObservation) { n.Attributes["kimi_code_agent_id"] = "agent-0" }},
		{name: "other session", change: func(n *registry.NativeObservation) { n.SessionID = "sibling" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := registry.Session{SessionID: "foreground", Observations: registry.Observations{Native: &registry.NativeObservation{
				SessionID: "foreground", Reporter: registry.Reporter{Integration: "kimi-code-hook", Version: 15, MultiSession: true},
				Attributes: map[string]string{"kimi_code_client_type": "kimi_code_cli", "kimi_code_agent_id": "main"},
			}}}
			if !kimiNativeSession(session, "foreground") {
				t.Fatal("rejected valid foreground native hooks")
			}
			test.change(session.Observations.Native)
			if kimiNativeSession(session, "foreground") {
				t.Fatal("accepted unrelated native evidence")
			}
		})
	}
}
