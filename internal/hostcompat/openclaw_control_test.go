//go:build compatibility

package hostcompat

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"
)

// The agent CLI is a Gateway client: signalling it does not cancel the run.
// Resolve the session we created, then abort that exact Gateway-owned session.
// https://docs.openclaw.ai/gateway/protocol/rpc-session-control
func (host isolatedHost) interruptOpenClaw(t *testing.T) {
	t.Helper()
	output := host.openClawRPC(t, "sessions.resolve", map[string]any{"sessionId": "aht-compat"})
	var session struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(output, &session); err != nil || session.Key == "" {
		t.Fatalf("resolving owned gateway session: %v; response: %s", err, output)
	}
	abortOutput := host.openClawRPC(t, "sessions.abort", map[string]any{"key": session.Key})
	var result struct {
		OK           bool   `json:"ok"`
		Status       string `json:"status"`
		AbortedRunID string `json:"abortedRunId"`
	}
	if err := json.Unmarshal(abortOutput, &result); err != nil || !result.OK || result.Status != "aborted" {
		t.Fatalf("aborting gateway session: %v; response: %s", err, abortOutput)
	}
}

func (host isolatedHost) openClawRPC(t *testing.T, method string, params map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, host.hostPath, "gateway", "call", method, "--params", string(data), "--json", "--timeout", "5000")
	command.Env = host.env
	command.Dir = host.work
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("gateway %s: %v\n%s", method, err, output)
	}
	return output
}
