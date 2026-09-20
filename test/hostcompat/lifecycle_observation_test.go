//go:build compatibility

package hostcompat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/pkg/registry"
)

func (host isolatedHost) sessions(ctx context.Context) ([]registry.Session, error) {
	output, err := host.runAHT(ctx, "--json", "list", "--agent", string(host.contract.ID))
	if err != nil {
		return nil, err
	}
	var sessions []registry.Session
	if err := json.Unmarshal(output, &sessions); err != nil {
		return nil, fmt.Errorf("decode session observations: %w", err)
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
	return matching, nil
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
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest []registry.Session
	for {
		sessions, err := host.sessions(ctx)
		if err != nil && ctx.Err() == nil {
			t.Fatalf("reading session observations: %v", err)
		}
		if err == nil {
			latest = sessions
		}
		for _, session := range sessions {
			if matches(session) {
				return session
			}
		}
		select {
		case <-ctx.Done():
			data, _ := os.ReadFile(filepath.Join(host.root, "native-events"))
			current, encodeErr := json.MarshalIndent(latest, "", "  ")
			if encodeErr != nil {
				t.Logf("encoding last isolated session state: %v", encodeErr)
			}
			t.Fatalf("missing %s; last probe error: %v; sanitized callbacks:\n%s\nlast isolated session state:\n%s", description, err, data, current)
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
		if host.contract.ID == registry.HarnessCopilot && session.Presence == registry.PresenceGone {
			return true
		}
		if native.Event == terminalEvent(host.contract.ID) && native.Presence != nil && *native.Presence == registry.PresenceGone && session.Presence == registry.PresenceGone {
			return true
		}
		return native.Activity != nil && *native.Activity == registry.ActivityInterrupted &&
			effectiveActivityMatches(session, registry.ActivityInterrupted)
	})
}
