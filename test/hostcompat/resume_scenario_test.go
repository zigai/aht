//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

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

	switch host.contract.ID {
	case registry.Harness("kimi-code"):
		host.runKimiACP(t, host.headlessCommand(t, original.Env), &previous, false)
	case registry.Harness("cline"):
		host.runClineResume(t, original.Env, previous)
	case registry.Harness("droid"):
		output = host.runDroidRPC(t, original.Env, &previous, false)
	default:
		command := exec.CommandContext(
			t.Context(),
			original.Path,
			resumeArguments(host.contract.ID, original.Args[1:], previous)...,
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

	if host.contract.ID == registry.Harness("droid") {
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

func resumeArguments(id registry.Harness, original []string, session registry.Session) []string {
	args := append([]string(nil), original...)

	switch id {
	case registry.Harness("codex"):
		return []string{
			"exec",
			"--dangerously-bypass-approvals-and-sandbox",
			"--dangerously-bypass-hook-trust",
			"--skip-git-repo-check",
			"resume",
			session.SessionID,
			compatibilityPrompt,
		}
	case registry.Harness("copilot"):
		return append(args, "--resume="+session.SessionID)
	case registry.Harness("pi"):
		return append(args, "--session", resumeReference(session))
	case registry.Harness("omp"):
		return append(args, "--resume", resumeReference(session))
	case registry.Harness("goose"):
		return append(args, "--resume", "--session-id", session.SessionID)
	case registry.Harness("hermes"):
		// Oneshot (-z) persists history but ignores --resume. The native chat
		// query path loads that same session's full conversation from SQLite.
		return []string{
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
			"--resume",
			session.SessionID,
		}
	case registry.Harness("opencode"), registry.Harness("kilo"):
		return append(args, "--session", session.SessionID)
	case registry.Harness("openclaw"):
		// agent --session-id addresses the durable Gateway session on both turns.
		return args
	default:
		return append(args, "--resume", session.SessionID)
	}
}

func resumeReference(session registry.Session) string {
	if session.SessionPath != "" {
		return session.SessionPath
	}

	return session.SessionID
}
