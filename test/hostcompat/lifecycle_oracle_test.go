//go:build compatibility

package hostcompat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/registry"
)

func (host *isolatedHost) validateSession(session registry.Session) bool {
	if host.contract.Level == compatibilityLevelDiscovery {
		return true
	}

	if session.Observations.Native == nil {
		return false
	}

	if session.Observations.Native.SessionID == "" {
		return false
	}

	if session.SessionID != "" && session.SessionID != session.Observations.Native.SessionID {
		return false
	}

	if host.launched.Work != "" &&
		filepath.Clean(session.CWD) != filepath.Clean(host.launched.Work) {
		return false
	}

	return terminalSession(host.contract.ID, session)
}

func TestSessionOracleRequiresNativeObservation(t *testing.T) {
	t.Parallel()

	workDir := "/tmp/project"
	processOnly := registry.Session{
		ID:      "s1",
		Harness: registry.Harness("opencode"),
		CWD:     workDir,
		Observations: registry.Observations{
			Process: &registry.ProcessObservation{Sighting: registry.Sighting{Present: true}},
		},
		Liveness: registry.NewLiveness(
			registry.PresenceLive,
			registry.ActivityValue(nil),
			nil,
			time.Time{},
			"",
		),
	}

	host := &isolatedHost{
		launched: &launch.Harness{Work: workDir},
		contract: hostContract{
			ID:    registry.Harness("opencode"),
			Level: compatibilityLevelLifecycle,
		},
	}
	if host.validateSession(processOnly) {
		t.Fatal("oracle accepted process-only session for non-exempt harness")
	}

	withNative := processOnly
	withNative.SessionID = "opencode-1"

	withNative.Observations.Native = &registry.NativeObservation{
		SessionID: "opencode-1",
		Event:     "agent_start",
	}
	if host.validateSession(withNative) {
		t.Fatal("oracle accepted start-only stuck-live native session")
	}

	withNative.Observations.Native.Event = "session.execution.succeeded"
	withNative.Observations.Native.Activity = new(registry.ActivityIdle)

	withNative.Liveness = registry.NewLiveness(
		withNative.Presence(),
		registry.ActivityValue(new(registry.ActivityUnknown)),
		withNative.Decision(), time.Time{}, "",
	)
	if host.validateSession(withNative) {
		t.Fatal("oracle accepted unexplained unknown effective activity")
	}

	withNative.Liveness = registry.NewLiveness(
		withNative.Presence(),
		registry.ActivityValue(withNative.Activity()),
		&registry.ActivityDecision{
			Authority: "screen",
			Reason:    "screen_not_in_supported_multiplexer",
		}, time.Time{}, "",
	)
	if !host.validateSession(withNative) {
		t.Fatal("oracle rejected screen-authoritative headless terminal session")
	}

	wrongCWD := withNative

	wrongCWD.CWD = "/tmp/other"
	if host.validateSession(wrongCWD) {
		t.Fatal("oracle accepted session with mismatched CWD")
	}

	mismatchedID := withNative

	mismatchedID.SessionID = "different-session"
	if host.validateSession(mismatchedID) {
		t.Fatal("oracle accepted session with mismatched native session ID")
	}

	cursorHost := &isolatedHost{
		launched: &launch.Harness{Work: workDir},
		contract: hostContract{ID: registry.Harness("cursor"), Level: compatibilityLevelDiscovery},
	}
	if !cursorHost.validateSession(processOnly) {
		t.Fatal("oracle rejected discovery-only state for exempt cursor harness")
	}
}

// Only execution prerequisites cross the profile boundary. In particular no
// credentials, provider URLs, plugin paths, tmux identity or shell startup files
// are inherited from the developer running the compatibility suite.
func isolatedEnvironment() []string {
	result := []string{"LANG=C.UTF-8", "TERM=xterm-256color", "NO_COLOR=1"}

	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "PNPM_HOME", "BUN_INSTALL", "UV_TOOL_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}

	return result
}

// Record only flags describing native observations, never stdin, tool inputs,
// prompts or credentials. The installed integration still invokes the real AHT
// consumer, with unchanged argv/stdin and exit status. A single append prevents
// concurrent hook invocations from interleaving individual fields.
func writeObservationLauncher(t *testing.T, path string, evidence string) {
	t.Helper()

	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

	script := fmt.Sprintf(`#!/bin/sh
record=
field=
for arg do
  if [ -n "$field" ]; then
    case "$arg" in *[!a-zA-Z0-9_.:-]*) ;; *) record="$record $field=$arg" ;; esac
    field=
  else
    case "$arg" in --event|--lifecycle|--presence|--activity) field=$arg ;; esac
  fi
done
if [ -n "$record" ]; then printf '%%s\n' "$record" >> %s; fi
exec %s "$@"
`, quote(evidence), quote(path+".real"))
	// reason: G306; the launcher is an owner-only executable the native host runs.
	if err := os.WriteFile(
		path,

		[]byte(script),

		0o700,
	); err != nil {
		t.Fatal(err)
	}
}

func nativeGone(native *registry.NativeObservation, session registry.Session) bool {
	return native.Presence != nil && *native.Presence == registry.PresenceGone &&
		session.Presence() == registry.PresenceGone
}

func nativeActivityRunning(session registry.Session) bool {
	native := session.Observations.Native
	if native == nil {
		return false
	}

	if native.Activity != nil {
		return *native.Activity == registry.ActivityRunning
	}
	// A later presence-only hook replaces the native snapshot without
	// invalidating the retained activity decision for the same incarnation.
	decision := session.Decision()

	return session.Activity() != nil && *session.Activity() == registry.ActivityRunning &&
		decision != nil &&
		decision.Authority == "hook" &&
		decision.Process.Equal(native.Process)
}

func TestNativeActivityOracleRetainsPresenceOnlyProvenance(t *testing.T) {
	t.Parallel()

	process := registry.ProcessIdentity{PID: 123, StartIdentity: "first"}

	session := registry.Session{Observations: registry.Observations{
		Native: &registry.NativeObservation{Event: "sessionStart", Process: process},
	}, Liveness: registry.NewLiveness(
		registry.PresenceUnknown,
		registry.ActivityValue(
			new(
				registry.ActivityRunning,
			),
		),
		nil, time.Time{}, "",
	)}
	if nativeActivityRunning(session) {
		t.Fatal("effective activity without native provenance passed")
	}

	session.Liveness = registry.NewLiveness(
		session.Presence(),
		registry.ActivityValue(session.Activity()),
		&registry.ActivityDecision{Authority: "process", Process: process}, time.Time{}, "",
	)
	if nativeActivityRunning(session) {
		t.Fatal("process-derived activity passed as native evidence")
	}

	session.Decision().Authority = "hook"
	if !nativeActivityRunning(session) {
		t.Fatal("presence-only callback erased same-incarnation native running proof")
	}

	session.Decision().Process.StartIdentity = "previous"
	if nativeActivityRunning(session) {
		t.Fatal("prior-incarnation activity passed as current native evidence")
	}

	session.Decision().Process = process

	session.Observations.Native.Activity = new(registry.ActivityIdle)
	if nativeActivityRunning(session) {
		t.Fatal("retained decision overrode an explicit newer native idle state")
	}
}

func effectiveActivityMatches(session registry.Session, want registry.Activity) bool {
	if session.Activity() == nil {
		return session.Presence() == registry.PresenceGone
	}

	if *session.Activity() == want {
		return true
	}
	// A headless host can retain authoritative native lifecycle evidence while
	// the effective state deliberately defers to an unavailable terminal screen.
	return *session.Activity() == registry.ActivityUnknown && session.Decision() != nil &&
		session.Decision().Authority == "screen" &&
		session.Decision().Reason == "screen_not_in_supported_multiplexer"
}

func (host *isolatedHost) assertNativeEvents(t *testing.T) {
	t.Helper()

	data, err := os.ReadFile(host.nativeEvents)
	if err != nil {
		t.Fatalf("reading native callback evidence: %v", err)
	}

	for _, event := range []string{
		startEvent(
			host.contract.ID,
		),
		turnEndEvent(
			host.contract.ID,
		),
		terminalEvent(
			host.contract.ID,
		),
	} {
		found := false

		for line := range strings.SplitSeq(string(data), "\n") {
			for field := range strings.FieldsSeq(line) {
				found = found || field == "--event="+event
			}
		}

		if !found {
			t.Errorf("missing native %s callback; sanitized evidence:\n%s", event, data)
		}
	}
}

func TestTerminalOracleRejectsObserverRetirement(t *testing.T) {
	t.Parallel()

	gone := registry.PresenceGone
	idle := registry.ActivityIdle

	for _, contract := range hostContracts {
		if contract.Level != compatibilityLevelLifecycle ||
			len(contract.Lifecycle.IdleEvents) != 0 {
			continue
		}

		id := contract.ID
		t.Run(string(id), func(t *testing.T) {
			session := registry.Session{
				Observations: registry.Observations{
					Native: &registry.NativeObservation{Event: startEvent(id), Activity: &idle},
				},
				Liveness: registry.NewLiveness(
					gone,
					registry.ActivityValue(nil),
					nil,
					time.Time{},
					"",
				),
			}
			if terminalSession(id, session) {
				t.Fatal("process retirement masked missing advertised native session end")
			}

			session.Observations.Native.Event = terminalEvent(id)

			session.Observations.Native.Presence = &gone
			if !terminalSession(id, session) {
				t.Fatal("rejected native terminal evidence")
			}

			session.Liveness = registry.NewLiveness(
				registry.PresenceLive,
				registry.ActivityValue(session.Activity()),
				session.Decision(), time.Time{}, "",
			)
			if terminalSession(id, session) {
				t.Fatal("accepted stuck-live effective state")
			}
		})
	}
}

func TestTurnOracleRejectsStuckRunning(t *testing.T) {
	t.Parallel()

	idle := registry.ActivityIdle
	running := registry.ActivityRunning

	for _, contract := range hostContracts {
		if contract.Level != compatibilityLevelLifecycle ||
			len(contract.Lifecycle.IdleEvents) == 0 {
			continue
		}

		id := contract.ID
		t.Run(string(id), func(t *testing.T) {
			session := registry.Session{
				Observations: registry.Observations{
					Native: &registry.NativeObservation{
						Event: terminalEvent(id), Activity: &idle,
					},
				},
				Liveness: registry.NewLiveness(
					registry.PresenceLive,
					registry.ActivityValue(&running),
					nil, time.Time{}, "",
				),
			}
			if terminalSession(id, session) {
				t.Fatal("native idle masked stuck-running effective activity")
			}

			session.Liveness = registry.NewLiveness(
				session.Presence(),
				registry.ActivityValue(&idle),
				session.Decision(), time.Time{}, "",
			)

			session.Observations.Native.Activity = &running
			if terminalSession(id, session) {
				t.Fatal("effective idle masked stuck-running native activity")
			}

			session.Observations.Native.Activity = &idle
			if !terminalSession(id, session) {
				t.Fatal("rejected completed native turn in persistent session")
			}
		})
	}
}

func terminalSession(id registry.Harness, session registry.Session) bool {
	contract, err := contractFor(id)

	native := session.Observations.Native
	if err != nil || native == nil {
		return false
	}

	lifecycle := contract.Lifecycle
	if lifecycle.Finalize != "" && native.Event == lifecycle.Finalize {
		return nativeGone(native, session)
	}

	if len(lifecycle.IdleEvents) > 0 {
		return idleTurnEnded(id, native, session)
	}

	return nativeGone(native, session) && native.Event == lifecycle.Terminal
}

func idleTurnEnded(
	id registry.Harness,
	native *registry.NativeObservation,
	session registry.Session,
) bool {
	contract, err := contractFor(id)

	return err == nil && native.Activity != nil && *native.Activity == registry.ActivityIdle &&
		effectiveActivityMatches(session, registry.ActivityIdle) &&
		slices.Contains(contract.Lifecycle.IdleEvents, native.Event)
}

func terminalEvent(id registry.Harness) string {
	contract, err := contractFor(id)
	if err != nil {
		return ""
	}

	return contract.Lifecycle.Terminal
}

func startEvent(id registry.Harness) string {
	contract, err := contractFor(id)
	if err != nil {
		return ""
	}

	return contract.Lifecycle.Start
}

func turnEndEvent(id registry.Harness) string {
	contract, err := contractFor(id)
	if err != nil {
		return ""
	}

	return contract.Lifecycle.TurnEnd
}
