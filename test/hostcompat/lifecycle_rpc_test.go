//go:build compatibility

package hostcompat

import (
	"os/exec"
	"testing"
	"time"
)

// Pi's RPC agent_settled event is the no-continuation boundary. Closing stdin
// after that event disposes the native session and awaits session_shutdown;
// process-observer retirement is not accepted as a substitute.
func (host isolatedHost) runPiCompletion(t *testing.T, command *exec.Cmd) {
	t.Helper()
	input, next := permissionJSONPipes(t, command)
	process := startPermissionProcess(t, host, command)
	sendPermissionJSON(t, input, map[string]any{"id": "compat-prompt", "type": "prompt", "message": compatibilityPrompt})
	for expected := range 2 {
		select {
		case step := <-host.provider.checkpoints:
			if step != expected {
				t.Fatalf("Pi provider checkpoint = %d, want %d", step, expected)
			}
			host.waitForActiveSession(t)
			host.provider.release <- struct{}{}
		case <-process.done:
			t.Fatalf("Pi exited before provider checkpoint %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Pi did not reach provider checkpoint %d", expected)
		}
	}
	awaitPiCompletion(t, next)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func awaitPiCompletion(t *testing.T, next func() map[string]any) {
	t.Helper()
	ended := false
	for {
		frame := next()
		switch frame["type"] {
		case "response":
			if frame["success"] != true {
				t.Fatalf("Pi RPC command failed: %v", frame)
			}
		case "agent_end":
			assertPiCompletedAssistant(t, frame)
			ended = true
		case "agent_settled":
			if !ended {
				t.Fatal("Pi settled without native agent_end")
			}
			return
		}
	}
}

func assertPiCompletedAssistant(t *testing.T, frame map[string]any) {
	t.Helper()
	messages, ok := frame["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatal("Pi agent_end has no completed assistant response")
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok || last["role"] != "assistant" || last["stopReason"] != "stop" {
		t.Fatal("Pi did not complete the native assistant response successfully")
	}
}
