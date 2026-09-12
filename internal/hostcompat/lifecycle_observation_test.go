//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func (host isolatedHost) sessions(t *testing.T) []registry.Session {
	t.Helper()
	output := host.runAHT(t, "--json", "list", "--agent", string(host.contract.ID))
	var sessions []registry.Session
	if err := json.Unmarshal(output, &sessions); err != nil {
		t.Fatalf("decoding session observations: %v", err)
	}
	matching := sessions[:0]
	for _, session := range sessions {
		if filepath.Clean(session.CWD) == filepath.Clean(host.work) {
			// Failure snapshots never emit raw native hook payloads.
			if native := session.Observations.Native; native != nil {
				native.RawPayload = nil
			}
			matching = append(matching, session)
		}
	}
	return matching
}

func (host isolatedHost) waitForActiveSession(t *testing.T) {
	t.Helper()
	host.waitForObservation(t, "live native session at held provider request", func(session registry.Session) bool {
		native := session.Observations.Native
		return native != nil && native.SessionID != "" && nativeActivityMatches(session, registry.ActivityRunning) &&
			session.Presence == registry.PresenceLive && filepath.Clean(session.CWD) == filepath.Clean(host.work) &&
			effectiveActivityMatches(session, registry.ActivityRunning)
	})
}

func (host isolatedHost) waitForObservation(t *testing.T, description string, matches func(registry.Session) bool) registry.Session {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, session := range host.sessions(t) {
			if matches(session) {
				return session
			}
		}
		select {
		case <-deadline.C:
			data, _ := os.ReadFile(filepath.Join(host.root, "native-events"))
			current, _ := json.MarshalIndent(host.sessions(t), "", "  ")
			t.Fatalf("missing %s; sanitized callbacks:\n%s\nisolated session state:\n%s", description, data, current)
		case <-ticker.C:
		}
	}
}

func (host isolatedHost) assertInterrupted(t *testing.T) {
	t.Helper()
	if len(host.provider.Requests()) != 1 {
		t.Fatalf("interruption must precede the tool call, got %d provider requests", len(host.provider.Requests()))
	}
	host.waitForObservation(t, "native interrupted/end evidence after active interruption", func(session registry.Session) bool {
		native := session.Observations.Native
		if native == nil {
			return false
		}
		if host.contract.ID == registry.HarnessOpenCode || host.contract.ID == registry.HarnessKilo {
			// Their abort API ends the held turn with native idle, without
			// deleting the durable session. Running was required before abort.
			return terminalSession(host.contract.ID, session)
		}
		if native.Event == terminalEvent(host.contract.ID) && native.Presence != nil && *native.Presence == registry.PresenceGone && session.Presence == registry.PresenceGone {
			return true
		}
		return native.Activity != nil && *native.Activity == registry.ActivityInterrupted &&
			effectiveActivityMatches(session, registry.ActivityInterrupted)
	})
}
