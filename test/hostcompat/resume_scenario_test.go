//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Resume is a second real turn against the host's own recorded conversation.
// The local provider requires the first turn's tool result in restored history,
// not merely a matching ID printed by --help or accepted by a flag parser.
func (host *isolatedHost) runResume(t *testing.T, original *exec.Cmd) {
	t.Helper()
	previous := host.waitForObservation(
		t,
		"completed native session to resume",
		host.validateSession,
	)
	oldCallID := host.callID
	host.scriptShellRoundTrip(
		host.scriptCallID(true),
		lifecycleShellCommand(host.contract.ID),
		false,
	)

	var output []byte

	switch {
	case host.contract.LifecycleDriver == driverACP:
		host.runKimiACP(t, host.headlessCommand(t, original.Env), &previous, false)
	case host.contract.InterruptionDriver == driverCLIACP:
		host.runClineResume(t, original.Env, previous)
	case host.contract.LifecycleDriver == driverExecRPC:
		output = host.runDroidRPC(t, original.Env, &previous, false)
	default:
		command := exec.CommandContext(
			t.Context(),
			original.Path,
			resumeArguments(t, host.contract, original.Args[1:], previous)...,
		)
		command.Env = original.Env
		command.Dir = host.launched.Work
		output = host.runHostCommand(t, command)
	}

	host.assertResumeRestoredHistory(t, oldCallID)
	host.waitForSession(t, output)

	resumed := host.waitForObservation(
		t,
		"terminal resumed native identity",
		func(session registry.Session) bool {
			return session.SessionID == previous.SessionID && host.validateSession(session)
		},
	)
	if resumed.ID != previous.ID ||
		resumed.Observations.Native.SessionID != previous.Observations.Native.SessionID {
		t.Fatal("resume changed the AHT or native session identity")
	}

	if resumed.Observations.Native.ObservedAt.Before(previous.Observations.Native.ObservedAt) ||
		resumed.Observations.Native.ObservedAt.Equal(previous.Observations.Native.ObservedAt) {
		t.Fatal("resume oracle accepted stale terminal evidence from the original process")
	}
}

func (host *isolatedHost) assertResumeRestoredHistory(t *testing.T, oldCallID string) {
	t.Helper()

	if err := host.provider.Err(); err != nil {
		t.Fatalf(
			"resumed provider exchange failed: %v\n%s",
			err,
			providerRequestSummary(host.provider),
		)
	}

	requests := host.provider.Requests()
	if len(requests) != 2 {
		t.Fatalf(
			"resume made %d provider requests, want a restored turn and tool continuation",
			len(requests),
		)
	}

	if host.contract.LifecycleDriver == driverExecRPC {
		if !jsonContainsString(requests[0].Body, compatibilityFinalText) {
			t.Fatal("resumed model request did not restore the prior native conversation")
		}
	} else if !requests[0].HasToolResult(oldCallID, compatibilityMarker) {
		t.Fatal("resumed model request did not restore the prior native tool result")
	}
}

func jsonContainsString(body json.RawMessage, expected string) bool {
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}

	return containsString(value, expected)
}

func containsString(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, expected)
	case []any:
		for _, item := range typed {
			if containsString(item, expected) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsString(item, expected) {
				return true
			}
		}
	}

	return false
}

func resumeArguments(
	t *testing.T,
	contract hostContract,
	original []string,
	session registry.Session,
) []string {
	t.Helper()

	native := catalog.ResumeCommandFor(contract.ID, session.SessionID, session.SessionPath)
	if len(native) < 2 {
		t.Fatalf("%s has no native resume command for %q", contract.ID, session.SessionID)
	}

	selector := native[1:]
	args := slices.Clone(original)

	switch contract.Resume {
	case resumeExec:
		args = []string{
			"exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"--dangerously-bypass-hook-trust",
			"--skip-git-repo-check",
		}
		args = append(args, selector...)

		return append(args, compatibilityPrompt)
	case resumeSubcommand:
		// The headless run subcommand shares the session selector with interactive resume.
		return append(args, selector[1:]...)
	case resumeQuery:
		// Oneshot ignores resume; the query path restores the persisted conversation.
		args = []string{
			"chat",
			"-q",
			compatibilityPrompt,
			"-Q",
			"--provider",
			"custom",
			"--model",
			fakeModel,
			"--yolo",
			"--accept-hooks",
		}

		return append(args, selector...)
	case resumeGateway:
		// The durable Gateway session is addressed by the agent command, not the TUI selector.
		for i := range args {
			if args[i] == "--session-id" && i+1 < len(args) {
				args[i+1] = session.SessionID
				return args
			}
		}

		t.Fatal("Gateway launch has no durable session selector")

		return nil
	case resumeAppend:
		return append(args, selector...)
	default:
		t.Fatalf("unknown resume mode %q", contract.Resume)
		return nil
	}
}
