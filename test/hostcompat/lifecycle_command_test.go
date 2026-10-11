//go:build compatibility

package hostcompat

import (
	"os/exec"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	compatibilityPrompt    = "Use the available shell tool exactly once to run printf aht-compat-marker, then reply done."
	compatibilityMarker    = "aht-compat-marker"
	compatibilityFinalText = "compat complete"
)

func lifecycleShellCommand(id registry.Harness) string {
	if id == registry.Harness("kimi-code") {
		return "printf 'aht-compat-marker\\n'"
	}
	return "printf " + compatibilityMarker
}

func (host *isolatedHost) scriptCallID(resumed bool) string {
	anthropic := host.launched.Protocol == fakeprovider.ProtocolAnthropicMessages
	switch {
	case resumed && anthropic:
		return "tool_resume"
	case resumed:
		return "callresume"
	case anthropic:
		return "tool_compat"
	case host.contract.ID == registry.Harness("openclaw"):
		return "callcompat"
	default:
		return "call_compat"
	}
}

func (host *isolatedHost) scriptShellRoundTrip(callID, command string, rejected bool) {
	host.callID = callID
	host.provider.Reset(fakeprovider.ToolRoundTrip{
		Call: fakeprovider.ToolCall{
			ID:        callID,
			Name:      host.launched.ShellTool.Name,
			Arguments: host.launched.ShellTool.Arguments(command),
		},
		Marker:    compatibilityMarker,
		Rejected:  rejected,
		FinalText: compatibilityFinalText,
	})
}

func (host *isolatedHost) lifecycleCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	return host.headlessCommand(t, slices.Clone(host.launched.Env))
}

func (host *isolatedHost) headlessCommand(t *testing.T, env []string) *exec.Cmd {
	t.Helper()
	args, err := host.launched.HeadlessArgs(compatibilityPrompt)
	if err != nil {
		t.Fatal(err)
	}
	return host.command(t, env, args...)
}

func (host *isolatedHost) command(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	command := exec.CommandContext(t.Context(), host.launched.Executable, args...)
	command.Env = env
	command.Dir = host.launched.Work
	return command
}
