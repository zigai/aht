package hostcompat

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	harness "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

type compatibilityLevel string

const (
	compatibilityDiscovery compatibilityLevel = "discovery"
	compatibilityLifecycle compatibilityLevel = "lifecycle"
)

var errHostContractMissing = errors.New("current-host contract is missing")

type hostContract struct {
	ID          registry.Harness
	Executable  string
	VersionArgs []string
	Level       compatibilityLevel
	Protocol    providerProtocol
	Docs        []string
}

var hostContracts = []hostContract{
	{ID: registry.HarnessClaude, Executable: "claude", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolAnthropicMessages, Docs: []string{"https://code.claude.com/docs/en/headless", "https://code.claude.com/docs/en/hooks", "https://code.claude.com/docs/en/llm-gateway"}},
	{ID: registry.HarnessCodex, Executable: "codex", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIResponses, Docs: []string{"https://developers.openai.com/codex/noninteractive", "https://developers.openai.com/codex/config-reference", "https://learn.chatgpt.com/docs/hooks"}},
	{ID: registry.HarnessCursor, Executable: "agent", VersionArgs: []string{"--version"}, Level: compatibilityDiscovery, Docs: []string{"https://docs.cursor.com/en/cli/headless", "https://docs.cursor.com/en/cli/reference/hooks"}},
	{ID: registry.HarnessCopilot, Executable: "copilot", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://docs.github.com/en/copilot/concepts/agents/about-copilot-cli", "https://docs.github.com/en/copilot/how-tos/copilot-cli/use-hooks", "https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli-with-a-custom-model-provider"}},
	{ID: registry.HarnessCline, Executable: "cline", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://docs.cline.bot/cline-cli/overview", "https://docs.cline.bot/features/hooks"}},
	{ID: registry.HarnessKimiCode, Executable: "kimi", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIResponses, Docs: []string{"https://moonshotai.github.io/kimi-cli/en/customization/hooks.html", "https://moonshotai.github.io/kimi-cli/en/configuration/providers.html", "https://moonshotai.github.io/kimi-cli/en/customization/print-mode.html"}},
	{ID: registry.HarnessGrok, Executable: "grok", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIResponses, Docs: []string{"https://docs.x.ai/build/features/hooks", "https://docs.x.ai/build/overview"}},
	{ID: registry.HarnessGoose, Executable: "goose", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://block.github.io/goose/docs/guides/hooks", "https://github.com/block/goose/blob/main/documentation/docs/guides/environment-variables.md", "https://block.github.io/goose/docs/guides/goose-cli-commands"}},
	{ID: registry.HarnessPi, Executable: "pi", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md", "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md"}},
	{ID: registry.HarnessOmp, Executable: "omp", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md", "https://github.com/can1357/oh-my-pi/blob/main/docs/extension-loading.md"}},
	{ID: registry.HarnessOpenCode, Executable: "opencode", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://opencode.ai/docs/plugins/", "https://opencode.ai/docs/providers/#custom-provider"}},
	{ID: registry.HarnessAgy, Executable: "agy", VersionArgs: []string{"--version"}, Level: compatibilityDiscovery, Docs: []string{"https://docs.agy.ai/plugins"}},
	{ID: registry.HarnessKilo, Executable: "kilo", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://kilo.ai/docs/cli", "https://kilo.ai/docs/hooks", "https://kilo.ai/docs/providers/custom"}},
	{ID: registry.HarnessDroid, Executable: "droid", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://docs.factory.ai/harness/hooks", "https://docs.factory.ai/droid-exec/overview", "https://docs.factory.ai/model-independence/byok"}},
	{ID: registry.HarnessOpenClaw, Executable: "openclaw", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://docs.openclaw.ai/automation/hooks", "https://docs.openclaw.ai/concepts/model-providers", "https://docs.openclaw.ai/cli/agent"}},
	{ID: registry.HarnessHermes, Executable: "hermes", VersionArgs: []string{"--version"}, Level: compatibilityLifecycle, Protocol: protocolOpenAIChat, Docs: []string{"https://github.com/NousResearch/hermes-agent/blob/main/docs/features/hooks.md", "https://github.com/NousResearch/hermes-agent/blob/main/docs/providers.md"}},
}

func TestEveryHarnessHasCurrentHostContract(t *testing.T) { //nolint:cyclop // One table-validation test intentionally checks every contract invariant.
	contracts := make(map[registry.Harness]hostContract, len(hostContracts))
	for _, contract := range hostContracts {
		if _, exists := contracts[contract.ID]; exists {
			t.Fatalf("duplicate host contract for %s", contract.ID)
		}
		if contract.Executable == "" || len(contract.VersionArgs) == 0 || len(contract.Docs) == 0 {
			t.Fatalf("incomplete host contract for %s: %#v", contract.ID, contract)
		}
		for _, rawURL := range contract.Docs {
			parsed, err := url.ParseRequestURI(rawURL)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				t.Fatalf("invalid authoritative URL for %s: %q", contract.ID, rawURL)
			}
		}
		contracts[contract.ID] = contract
	}

	for _, adapter := range harness.All() {
		id := adapter.Definition().ID
		if _, err := contractFor(id); err != nil {
			t.Error(err)
		}
		delete(contracts, id)
	}
	for id := range contracts {
		t.Errorf("host contract %s does not identify a registered harness", id)
	}
}

func contractFor(id registry.Harness) (hostContract, error) {
	for _, contract := range hostContracts {
		if contract.ID == id {
			return contract, nil
		}
	}
	return hostContract{}, fmt.Errorf("%w: %s", errHostContractMissing, id)
}
