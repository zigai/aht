//go:build compatibility

package hostcompat

import (
	"os/exec"
	"testing"

	"github.com/zigai/aht/pkg/registry"
)

// Resume is a second real turn against the host's own recorded conversation.
// The local provider requires the first turn's tool result in restored history,
// not merely a matching ID printed by --help or accepted by a flag parser.
func (host *isolatedHost) runResume(t *testing.T, original *exec.Cmd) {
	t.Helper()
	previous := host.waitForObservation(t, "completed native session to resume", host.validateSession)
	oldCallID := host.provider.callID
	oldMarker := host.provider.marker
	host.provider.mu.Lock()
	host.provider.step = 0
	host.provider.requests = nil
	host.provider.callID = "callresume"
	if host.contract.Protocol == protocolAnthropicMessages {
		host.provider.callID = "tool_resume"
	}
	host.provider.mu.Unlock()
	var output []byte
	if host.contract.ID == registry.HarnessCline {
		host.runClineResume(t, original.Env, previous)
	} else if host.contract.ID == registry.HarnessDroid {
		output = host.runDroidRPC(t, original.Env, &previous, false)
	} else {
		command := exec.Command(original.Path, resumeArguments(host.contract.ID, original.Args[1:], previous)...)
		command.Env = original.Env
		command.Dir = host.work
		output = host.runHostCommand(t, command)
	}
	if err := host.provider.Error(); err != nil {
		t.Fatalf("resumed provider exchange failed: %v\n%s", err, providerRequestSummary(host.provider))
	}
	requests := host.provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("resume made %d provider requests, want a restored turn and tool continuation", len(requests))
	}
	if !requestContainsToolResult(host.contract.Protocol, []byte(requests[0].Body), oldCallID, oldMarker) {
		t.Fatal("resumed model request did not restore the prior native tool result")
	}
	host.waitForSession(t, output)
	resumed := host.waitForObservation(t, "terminal resumed native identity", func(session registry.Session) bool {
		return session.SessionID == previous.SessionID && host.validateSession(session)
	})
	if resumed.ID != previous.ID || resumed.Observations.Native.SessionID != previous.Observations.Native.SessionID {
		t.Fatal("resume changed the AHT or native session identity")
	}
	if resumed.Observations.Native.ObservedAt.Before(previous.Observations.Native.ObservedAt) || resumed.Observations.Native.ObservedAt.Equal(previous.Observations.Native.ObservedAt) {
		t.Fatal("resume oracle accepted stale terminal evidence from the original process")
	}
}

func resumeArguments(id registry.Harness, original []string, session registry.Session) []string {
	args := append([]string(nil), original...)
	switch id {
	case registry.HarnessCodex:
		return []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--skip-git-repo-check", "resume", session.SessionID, compatibilityPrompt}
	case registry.HarnessCopilot:
		return append(args, "--resume="+session.SessionID)
	case registry.HarnessPi:
		return append(args, "--session", resumeReference(session))
	case registry.HarnessOmp:
		return append(args, "--resume", resumeReference(session))
	case registry.HarnessKimiCode:
		return append(args, "--session", session.SessionID)
	case registry.HarnessGoose:
		return append(args, "--resume", "--session-id", session.SessionID)
	case registry.HarnessHermes:
		// Oneshot (-z) persists history but ignores --resume. The native chat
		// query path loads that same session's full conversation from SQLite.
		return []string{"chat", "-q", compatibilityPrompt, "-Q", "--provider", "custom", "--model", "compat", "--yolo", "--accept-hooks", "--resume", session.SessionID}
	case registry.HarnessOpenCode, registry.HarnessKilo:
		return append(args, "--session", session.SessionID)
	case registry.HarnessOpenClaw:
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
