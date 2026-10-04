//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (host *isolatedHost) kimiWireCommand(t *testing.T, env, nativeArgs []string) *exec.Cmd {
	t.Helper()
	args := append([]string{"hook", "wire", "kimi-code", "--"}, nativeArgs...)
	command := exec.CommandContext(t.Context(), host.aht, args...)
	command.Dir = host.work
	command.Env = env
	return command
}

// The production AHT transport owns Kimi, but prompt completion and cancellation
// must still come from native Wire responses, before stdin EOF retires the host.
func (host *isolatedHost) runKimiWire(t *testing.T, command *exec.Cmd, interrupt bool) {
	t.Helper()
	wire := newPythonPermissionWire(t, command)
	process := startPermissionProcess(t, host, command)
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "initialize", "method": "initialize", "params": map[string]any{"protocol_version": "1.10", "client": map[string]any{"name": "aht-compat", "version": "1"}}})
	wire.response(t, "initialize")
	wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "prompt", "method": "prompt", "params": map[string]any{"user_input": compatibilityPrompt}})
	host.driveKimiCheckpoints(t, wire, process, interrupt)
	awaitKimiTurn(t, wire, interrupt)
	if interrupt {
		host.waitForObservation(t, "native Wire cancellation before EOF", func(session registry.Session) bool {
			native := session.Observations.Native
			return native != nil && native.Reporter.Integration == "kimi-wire" &&
				native.Activity != nil && *native.Activity == registry.ActivityInterrupted
		})
	}
	if err := wire.input.Close(); err != nil {
		t.Fatal(err)
	}
	process.wait(t)
}

func (host *isolatedHost) driveKimiCheckpoints(t *testing.T, wire *pythonPermissionWire, process *permissionProcess, interrupt bool) {
	t.Helper()
	for expected := range 2 {
		select {
		case step := <-host.provider.checkpoints:
			if step != expected {
				t.Fatalf("Kimi provider checkpoint = %d, want %d", step, expected)
			}
			host.waitForActiveSession(t)
			if interrupt {
				wire.send(t, map[string]any{"jsonrpc": "2.0", "id": "cancel", "method": "cancel", "params": map[string]any{}})
			} else {
				host.provider.release <- struct{}{}
			}
		case <-process.done:
			t.Fatalf("AHT Wire exited before provider checkpoint %d: %v", expected, process.waitErr())
		case <-time.After(30 * time.Second):
			t.Fatalf("Kimi did not reach provider checkpoint %d", expected)
		}
		if interrupt {
			return
		}
	}
}

func awaitKimiTurn(t *testing.T, wire *pythonPermissionWire, interrupted bool) {
	t.Helper()
	promptCompleted := false
	cancelAcknowledged := !interrupted
	for !promptCompleted || !cancelAcknowledged {
		message := wire.read(t)
		if message.Method == "event" {
			continue
		}
		switch kimiResponseID(t, message) {
		case "cancel":
			if !interrupted || cancelAcknowledged {
				t.Fatal("unexpected Kimi cancellation acknowledgement")
			}
			cancelAcknowledged = true
		case "prompt":
			if promptCompleted {
				t.Fatalf("Kimi native prompt completed twice: %s", message.Result)
			}
			requireKimiPromptStatus(t, message, interrupted)
			promptCompleted = true
		default:
			t.Fatalf("unexpected Kimi response ID %s", message.ID)
		}
	}
}

func kimiResponseID(t *testing.T, message pythonPermissionMessage) string {
	t.Helper()
	if len(message.Error) != 0 || message.Method != "" {
		t.Fatalf("Kimi native turn failed or requested unexpected input: %+v", message)
	}
	var id string
	if err := json.Unmarshal(message.ID, &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func requireKimiPromptStatus(t *testing.T, message pythonPermissionMessage, interrupted bool) {
	t.Helper()
	var result struct {
		Status string `json:"status"`
	}
	want := "finished"
	if interrupted {
		want = "cancelled" //nolint:misspell // reason: "cancelled" is the Kimi wire prompt status value.
	}
	if err := json.Unmarshal(message.Result, &result); err != nil || result.Status != want {
		t.Fatalf("Kimi native prompt did not finish as %s: %s (%v)", want, message.Result, err)
	}
}
