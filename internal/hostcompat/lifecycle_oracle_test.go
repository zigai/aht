//go:build compatibility

package hostcompat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

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

func discoveryScope(id registry.Harness) string {
	if id == registry.HarnessCursor {
		return "https://cursor.com/docs/cli/headless documents CURSOR_API_KEY-backed hosted execution, not an isolated local-provider endpoint"
	}
	return "agy --help documents hosted model selection but no local-provider endpoint; https://docs.agy.ai/plugins was unreachable during contract review, so native lifecycle support is not asserted"
}

// Record only flags describing native observations, never stdin, tool inputs,
// prompts or credentials. The installed integration still invokes the real AHT
// consumer, with unchanged argv/stdin and exit status. A single append prevents
// concurrent hook invocations from interleaving individual fields.
func writeObservationLauncher(t *testing.T, path, evidence string) {
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
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

// A persistent session is not retired just because a one-shot CLI client exits.
// OpenCode/Kilo emit session.deleted only on deletion; Cline afterRun and
// OpenClaw agent_end and Hermes on_session_end end a turn rather than the session.
func terminalSession(id registry.Harness, session registry.Session) bool {
	native := session.Observations.Native
	if native == nil {
		return false
	}
	// Hermes chat explicitly finalizes the session; oneshot ends only its turn.
	if id == registry.HarnessHermes && native.Event == "on_session_finalize" {
		return native.Presence != nil && *native.Presence == registry.PresenceGone && session.Presence == registry.PresenceGone
	}
	switch id {
	case registry.HarnessOpenCode, registry.HarnessKilo, registry.HarnessCline, registry.HarnessOpenClaw, registry.HarnessHermes:
		if native.Activity == nil || *native.Activity != registry.ActivityIdle {
			return false
		}
		if !effectiveActivityMatches(session, registry.ActivityIdle) {
			return false
		}
		// Both current OpenCode/Kilo idle notifications are native terminal
		// evidence; either may be the last drained event.
		return native.Event == terminalEvent(id) ||
			((id == registry.HarnessOpenCode || id == registry.HarnessKilo) && native.Event == "session.idle")
	default:
		// A process-observer tombstone is not a native SessionEnd.
		return native.Presence != nil && *native.Presence == registry.PresenceGone &&
			session.Presence == registry.PresenceGone && native.Event == terminalEvent(id)
	}
}

func nativeActivityMatches(session registry.Session, want registry.Activity) bool {
	native := session.Observations.Native
	if native == nil {
		return false
	}
	if native.Activity != nil {
		return *native.Activity == want
	}
	// A later presence-only hook replaces the native snapshot without
	// invalidating the retained activity decision for the same incarnation.
	decision := session.ActivityDecision
	return session.Activity != nil && *session.Activity == want && decision != nil &&
		decision.Authority == "hook" && decision.Process.Equal(native.Process)
}

func TestNativeActivityOracleRetainsPresenceOnlyProvenance(t *testing.T) {
	t.Parallel()
	process := registry.ProcessIdentity{PID: 123, StartIdentity: "first"}
	session := registry.Session{
		Activity: new(registry.ActivityRunning),
		Observations: registry.Observations{
			Native: &registry.NativeObservation{Event: "sessionStart", Process: process},
		},
	}
	if nativeActivityMatches(session, registry.ActivityRunning) {
		t.Fatal("effective activity without native provenance passed")
	}
	session.ActivityDecision = &registry.ActivityDecision{Authority: "process", Process: process}
	if nativeActivityMatches(session, registry.ActivityRunning) {
		t.Fatal("process-derived activity passed as native evidence")
	}
	session.ActivityDecision.Authority = "hook"
	if !nativeActivityMatches(session, registry.ActivityRunning) {
		t.Fatal("presence-only callback erased same-incarnation native running proof")
	}
	session.ActivityDecision.Process.StartIdentity = "previous"
	if nativeActivityMatches(session, registry.ActivityRunning) {
		t.Fatal("prior-incarnation activity passed as current native evidence")
	}
	session.ActivityDecision.Process = process
	session.Observations.Native.Activity = new(registry.ActivityIdle)
	if nativeActivityMatches(session, registry.ActivityRunning) {
		t.Fatal("retained decision overrode an explicit newer native idle state")
	}
}

func effectiveActivityMatches(session registry.Session, want registry.Activity) bool {
	if session.Activity == nil {
		return session.Presence == registry.PresenceGone
	}
	if *session.Activity == want {
		return true
	}
	// A headless host can retain authoritative native lifecycle evidence while
	// the effective state deliberately defers to an unavailable terminal screen.
	return *session.Activity == registry.ActivityUnknown && session.ActivityDecision != nil &&
		session.ActivityDecision.Authority == "screen" &&
		session.ActivityDecision.Reason == "screen_not_in_supported_multiplexer"
}

func terminalEvent(id registry.Harness) string {
	switch id {
	case registry.HarnessPi, registry.HarnessOmp:
		return "session_shutdown"
	case registry.HarnessCopilot:
		return "sessionEnd"
	case registry.HarnessOpenCode, registry.HarnessKilo:
		return "session.status"
	case registry.HarnessCline:
		return "afterRun"
	case registry.HarnessOpenClaw:
		return "agent_end"
	case registry.HarnessHermes:
		return "on_session_end"
	default:
		return "SessionEnd"
	}
}

func startEvent(id registry.Harness) string {
	switch id {
	case registry.HarnessPi, registry.HarnessOmp, registry.HarnessOpenClaw:
		return "session_start"
	case registry.HarnessCopilot:
		return "sessionStart"
	case registry.HarnessOpenCode, registry.HarnessKilo:
		return "session.created"
	case registry.HarnessCline:
		return "beforeRun"
	case registry.HarnessHermes:
		return "on_session_start"
	default:
		return "SessionStart"
	}
}

func turnEndEvent(id registry.Harness) string {
	switch id {
	case registry.HarnessPi:
		return "agent_end"
	case registry.HarnessOmp:
		return "session_stop"
	case registry.HarnessCopilot:
		return "agentStop"
	case registry.HarnessHermes:
		return "on_session_end"
	case registry.HarnessCline, registry.HarnessOpenCode, registry.HarnessKilo, registry.HarnessOpenClaw:
		return terminalEvent(id)
	default:
		return "Stop"
	}
}

func (host isolatedHost) assertNativeEvents(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(host.root, "native-events"))
	if err != nil {
		t.Fatalf("reading native callback evidence: %v", err)
	}
	for _, event := range []string{startEvent(host.contract.ID), turnEndEvent(host.contract.ID), terminalEvent(host.contract.ID)} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			for _, field := range strings.Fields(line) {
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
	for _, id := range []registry.Harness{registry.HarnessClaude, registry.HarnessCodex, registry.HarnessPi, registry.HarnessOmp, registry.HarnessCopilot, registry.HarnessKimiCode, registry.HarnessGrok, registry.HarnessGoose, registry.HarnessDroid} {
		t.Run(string(id), func(t *testing.T) {
			session := registry.Session{Presence: gone, Observations: registry.Observations{Native: &registry.NativeObservation{Event: startEvent(id), Activity: &idle}}}
			if terminalSession(id, session) {
				t.Fatal("process retirement masked missing advertised native session end")
			}
			session.Observations.Native.Event = terminalEvent(id)
			session.Observations.Native.Presence = &gone
			if !terminalSession(id, session) {
				t.Fatal("rejected native terminal evidence")
			}
			session.Presence = registry.PresenceLive
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
	for _, id := range []registry.Harness{registry.HarnessOpenCode, registry.HarnessKilo, registry.HarnessCline, registry.HarnessOpenClaw, registry.HarnessHermes} {
		t.Run(string(id), func(t *testing.T) {
			session := registry.Session{
				Presence: registry.PresenceLive, Activity: &running,
				Observations: registry.Observations{Native: &registry.NativeObservation{
					Event: terminalEvent(id), Activity: &idle,
				}},
			}
			if terminalSession(id, session) {
				t.Fatal("native idle masked stuck-running effective activity")
			}
			session.Activity = &idle
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
