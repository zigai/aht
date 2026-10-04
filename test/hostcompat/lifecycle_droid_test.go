//go:build compatibility

package hostcompat

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func droidRPCArguments(work string) []string {
	return []string{"exec", "--input-format", "stream-jsonrpc", "--output-format", "stream-jsonrpc", "--auto", "high", "--cwd", work}
}

// The public SDK's JsonRpcEnvelopeSchema requires type and factoryApiVersion;
// factoryProtocolVersion is optional, not a client-selected compatibility pin.
// Sources: docs.factory.ai/droid-exec/overview and @factory/droid-sdk 0.9.1
// JsonRpcEnvelopeSchema, LoadSessionRequestParamsSchema, SessionNotificationSchema,
// AgentTurnCompletedNotificationSchema, and InterruptSessionResponseSchema.
// RPC initialization must also select native auto-high: the CLI flag alone does
// not set the initialized session's autonomy level on current Droid.
func (host *isolatedHost) runDroidRPC(t *testing.T, env []string, previous *registry.Session, interrupt bool) []byte {
	t.Helper()
	command := host.command(t, env, droidRPCArguments(host.work)...)
	input, next := permissionJSONPipes(t, command)
	process := startPermissionProcess(t, host, command)
	session := &droidRPCSession{input: input, next: next, pending: make(map[string]bool), interrupt: interrupt}
	session.open(t, host.work, previous)
	session.send(t, "compat-turn", "droid.add_user_message", map[string]any{"text": compatibilityPrompt})
	host.driveDroidCheckpoints(t, process, session.sessionID, previous, interrupt)
	if interrupt {
		// Keep gate zero held: cancellation must abort real provider IO, not a
		// completed turn. An acknowledgement alone precedes native cancellation.
		session.send(t, "compat-interrupt", "droid.interrupt_session", map[string]any{})
	}
	for !session.completed || len(session.pending) != 0 {
		session.receive(t)
	}
	// Only native completion plus all correlated acknowledgements permits EOF.
	// Droid's orderly disposal then runs genuine SessionEnd hooks; the shared
	// terminal oracle rejects process-observer retirement as substitute evidence.
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
	return session.output.Bytes()
}

type droidRPCSession struct {
	input     *os.File
	next      func() map[string]any
	pending   map[string]bool
	output    bytes.Buffer
	sessionID string
	turnID    string
	completed bool
	interrupt bool
}

func (session *droidRPCSession) send(t *testing.T, id, method string, params map[string]any) {
	t.Helper()
	session.pending[id] = true
	sendPermissionJSON(t, session.input, map[string]any{
		"jsonrpc": "2.0", "type": "request", "factoryApiVersion": "1.0.0",
		"id": id, "method": method, "params": params,
	})
}

func (session *droidRPCSession) open(t *testing.T, work string, previous *registry.Session) {
	t.Helper()
	if previous == nil {
		session.send(t, "compat-session", "droid.initialize_session", map[string]any{
			"machineId": "aht-compat", "cwd": work, "modelId": "custom:aht-compat-0",
			"interactionMode": "auto", "autonomyLevel": "high",
		})
	} else {
		session.sessionID = previous.SessionID
		session.send(t, "compat-session", "droid.load_session", map[string]any{"sessionId": session.sessionID})
	}
	for session.pending["compat-session"] {
		id, result := session.receive(t)
		if id != "compat-session" {
			continue
		}
		if previous == nil {
			session.sessionID, _ = result["sessionId"].(string)
			if session.sessionID == "" {
				t.Fatal("Droid initialized without native session ID")
			}
		}
		settings, _ := result["settings"].(map[string]any)
		if settings["modelId"] != "custom:aht-compat-0" || settings["interactionMode"] != "auto" || settings["autonomyLevel"] != "high" {
			t.Fatalf("Droid did not retain the isolated model and native auto-high policy: %v", settings)
		}
	}
}

func (session *droidRPCSession) receive(t *testing.T) (string, map[string]any) {
	t.Helper()
	frame := session.next()
	if err := json.NewEncoder(&session.output).Encode(frame); err != nil {
		t.Fatal(err)
	}
	if frame["jsonrpc"] != "2.0" || frame["factoryApiVersion"] != "1.0.0" {
		t.Fatalf("invalid Droid RPC envelope: %v", frame)
	}
	switch frame["type"] {
	case "response":
		id, ok := frame["id"].(string)
		if !ok || !session.pending[id] {
			t.Fatalf("uncorrelated Droid RPC response: %v", frame)
		}
		result, ok := frame["result"].(map[string]any)
		if !ok {
			t.Fatalf("Droid RPC response lacks successful result: %v", frame)
		}
		delete(session.pending, id)
		return id, result
	case "notification":
		session.observeNotification(t, frame)
	default:
		// Auto-high must handle this marker-only Execute natively. Never answer
		// request_permission/ask_user by bypassing the configured native policy.
		t.Fatalf("unexpected Droid server request or frame: %v", frame)
	}
	return "", nil
}

func (session *droidRPCSession) observeNotification(t *testing.T, frame map[string]any) {
	t.Helper()
	if frame["method"] != "droid.session_notification" {
		t.Fatalf("unexpected Droid notification: %v", frame)
	}
	params, _ := frame["params"].(map[string]any)
	if id, present := params["sessionId"]; present && id != session.sessionID {
		t.Fatalf("Droid notification changed native session identity: %v", frame)
	}
	notification, _ := params["notification"].(map[string]any)
	switch notification["type"] {
	case "agent_turn_completed":
		session.observeTurnCompleted(t, frame, notification)
	case "create_message":
		session.observeCreatedMessage(t, frame, notification)
	case "tool_call", "tool_result", "tool_progress_update", "tool_execution_heartbeat", "tool_execution_phase_changed":
		if session.interrupt || session.completed {
			t.Fatalf("Droid executed a tool after cancellation or completion: %v", frame)
		}
	case "droid_working_state_changed", "settings_updated", "session_title_updated", "mcp_status_changed",
		"assistant_text_delta", "assistant_text_complete", "thinking_text_delta", "thinking_text_complete",
		"session_token_usage_changed", "hook_execution_started", "hook_execution_completed":
		// Documented SDK metadata and output; AHT hooks remain the lifecycle oracle.
	default:
		t.Fatalf("unexpected Droid session notification: %v", frame)
	}
}

func (session *droidRPCSession) observeTurnCompleted(t *testing.T, frame map[string]any, notification map[string]any) {
	t.Helper()
	reason := "completed"
	if session.interrupt {
		reason = "cancelled" //nolint:misspell // reason: "cancelled" is the Droid agent_turn_completed reason value.
	}
	if session.completed || session.turnID == "" || notification["turnId"] != session.turnID || notification["reason"] != reason {
		t.Fatalf("Droid did not complete the correlated native turn with reason %s: %v", reason, frame)
	}
	session.completed = true
}

func (session *droidRPCSession) observeCreatedMessage(t *testing.T, frame map[string]any, notification map[string]any) {
	t.Helper()
	if notification["requestId"] != "compat-turn" {
		return
	}
	message, _ := notification["message"].(map[string]any)
	id, _ := message["id"].(string)
	if session.turnID != "" || id == "" || message["role"] != "user" {
		t.Fatalf("invalid Droid user-turn correlation: %v", frame)
	}
	session.turnID = id
}

func (host *isolatedHost) driveDroidCheckpoints(t *testing.T, process *permissionProcess, sessionID string, previous *registry.Session, interrupt bool) {
	t.Helper()
	gates := 2
	if interrupt {
		gates = 1
	}
	for expected := range gates {
		select {
		case step := <-host.provider.checkpoints:
			if step != expected {
				t.Fatalf("Droid provider checkpoint = %d, want %d", step, expected)
			}
		case <-process.done:
			t.Fatalf("Droid exited before provider checkpoint %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Droid did not reach provider checkpoint %d", expected)
		}
		host.waitForObservation(t, "Droid native running at held model request", func(session registry.Session) bool {
			return droidRunningAtHeldRequest(session, sessionID, previous)
		})
		if !interrupt {
			host.provider.release <- struct{}{}
		}
	}
}

func droidRunningAtHeldRequest(session registry.Session, sessionID string, previous *registry.Session) bool {
	native := session.Observations.Native
	if session.SessionID != sessionID || native == nil || native.SessionID != sessionID {
		return false
	}
	if !nativeActivityRunning(session) || !effectiveActivityMatches(session, registry.ActivityRunning) || session.Presence() != registry.PresenceLive {
		return false
	}
	return previous == nil || (session.ID == previous.ID && native.ObservedAt.After(previous.Observations.Native.ObservedAt))
}
