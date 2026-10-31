//go:build compatibility

package hostcompat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

func (host *isolatedHost) waitForActiveSession(t *testing.T) {
	t.Helper()
	host.waitForObservation(
		t,
		"live native session at held provider request",
		func(session registry.Session) bool {
			native := session.Observations.Native

			return native != nil && native.SessionID != "" && nativeActivityRunning(session) &&
				session.Presence() == registry.PresenceLive && filepath.Clean(
				session.CWD,
			) == filepath.Clean(
				host.launched.Work,
			) &&
				effectiveActivityMatches(session, registry.ActivityRunning)
		},
	)
}

func (host *isolatedHost) waitForObservation(
	t *testing.T,
	description string,
	matches func(registry.Session) bool,
) registry.Session {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	session, err := host.launched.WaitForSession(ctx, matches)
	if err != nil {
		data, _ := os.ReadFile(host.nativeEvents)
		t.Fatalf("missing %s: %v\nsanitized callbacks:\n%s", description, err, data)
	}

	return session
}

func (host *isolatedHost) assertInterrupted(t *testing.T) {
	t.Helper()

	if len(host.provider.Requests()) != 1 {
		t.Fatalf(
			"interruption must precede the tool call, got %d provider requests",
			len(host.provider.Requests()),
		)
	}

	host.waitForObservation(
		t,
		"native interrupted/end evidence after active interruption",
		func(session registry.Session) bool {
			native := session.Observations.Native
			if native == nil {
				return false
			}

			if host.contract.ID == registry.Harness("kilo") {
				// Its abort API ends the held turn with native idle, without
				// deleting the durable session. Running was required before abort.
				return terminalSession(host.contract.ID, session)
			}

			if host.contract.ID == registry.Harness("copilot") &&
				session.Presence() == registry.PresenceGone {
				return true
			}

			if native.Event == terminalEvent(host.contract.ID) && nativeGone(native, session) {
				return true
			}
			// Grok's TUI quit must publish SessionEnd after Esc canceled the turn.
			// An earlier interrupted Stop alone does not prove clean shutdown.
			if host.contract.ID == registry.Harness("grok") {
				return false
			}

			return native.Activity != nil && *native.Activity == registry.ActivityInterrupted &&
				effectiveActivityMatches(session, registry.ActivityInterrupted)
		},
	)
}
