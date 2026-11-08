//go:build compatibility

package hostcompat

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/fakeprovider/launch"
	"github.com/zigai/aht/v2/pkg/registry"
)

type compatibilityLevel string

const (
	compatibilityLevelDiscovery compatibilityLevel = "discovery"
	compatibilityLevelLifecycle compatibilityLevel = "lifecycle"
)

var errHostContractMissing = errors.New("current-host contract is missing")

// loadCheck runs a host command that reports which integrations the host
// loaded and requires its output to contain the needle.
type loadCheck struct {
	Args    []string
	Needle  func(host *isolatedHost) string
	Prepare func(t *testing.T, host *isolatedHost)
}

type nativeDriver string

const (
	driverProcess         nativeDriver = ""
	driverExecRPC         nativeDriver = "exec-rpc"
	driverACP             nativeDriver = "acp"
	driverCompletionRPC   nativeDriver = "completion-rpc"
	driverExtensionRPC    nativeDriver = "extension-rpc"
	driverPythonACP       nativeDriver = "python-acp"
	driverExecutionServer nativeDriver = "execution-server"
	driverPluginServer    nativeDriver = "plugin-server"
	driverTerminalQuit    nativeDriver = "terminal-quit"
	driverTerminalDialog  nativeDriver = "terminal-dialog"
	driverCLIACP          nativeDriver = "cli-acp"
	driverControlCLI      nativeDriver = "control-cli"
	driverAppServer       nativeDriver = "app-server"
	driverSDKCLI          nativeDriver = "sdk-cli"
)

type resumeMode string

const (
	resumeAppend     resumeMode = ""
	resumeExec       resumeMode = "exec"
	resumeSubcommand resumeMode = "subcommand"
	resumeQuery      resumeMode = "query"
	resumeGateway    resumeMode = "gateway"
)

type lifecycleContract struct {
	Start      string
	TurnEnd    string
	Terminal   string
	IdleEvents []string
	Finalize   string
}

type hostContract struct {
	LifecycleDriver     nativeDriver
	InterruptionDriver  nativeDriver
	PermissionDriver    nativeDriver
	Lifecycle           lifecycleContract
	Resume              resumeMode
	Scope               string
	ShellCommand        string
	CompactCallID       bool
	DangerousPermission bool
	InterruptGone       bool
	InterruptTerminal   bool
	PermissionConfig    string
	ID                  registry.Harness
	Executable          string
	VersionArgs         []string
	LoadChecks          []loadCheck
	Level               compatibilityLevel
	Docs                []string
}

var hostContracts = []hostContract{
	{
		PermissionDriver: driverControlCLI,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("claude"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://code.claude.com/docs/en/headless",
			"https://code.claude.com/docs/en/hooks",
			"https://code.claude.com/docs/en/llm-gateway",
			"https://code.claude.com/docs/en/settings",
			"https://code.claude.com/docs/en/memory",
			"https://code.claude.com/docs/en/skills",
			"https://code.claude.com/docs/en/claude-directory",
		},
	},
	{
		PermissionDriver: driverAppServer,
		Resume:           resumeExec,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("codex"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://developers.openai.com/codex/noninteractive",
			"https://developers.openai.com/codex/config-reference",
			"https://learn.chatgpt.com/docs/hooks",
			"https://developers.openai.com/codex/app-server",
			"https://learn.chatgpt.com/docs/config-file/config-basic",
			"https://learn.chatgpt.com/docs/agent-configuration/agents-md",
			"https://learn.chatgpt.com/docs/build-skills",
		},
	},
	{
		Scope: "https://cursor.com/docs/cli/headless documents CURSOR_API_KEY-backed hosted execution, " +
			"not an isolated local-provider endpoint",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("cursor"),
		Executable:  "agent",
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelDiscovery,
		Docs: []string{
			"https://cursor.com/docs/cli/headless",
			"https://cursor.com/docs/cli/reference/authentication",
			"https://cursor.com/docs/context/rules",
			"https://cursor.com/docs/skills",
			"https://cursor.com/docs/mcp",
			"https://cursor.com/docs/cli/reference/configuration",
		},
	},
	{
		PermissionDriver: driverSDKCLI,
		InterruptGone:    true,
		Lifecycle: lifecycleContract{
			Start:    "sessionStart",
			TurnEnd:  "agentStop",
			Terminal: "sessionEnd",
		},

		ID:          registry.Harness("copilot"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://docs.github.com/en/copilot/concepts/agents/about-copilot-cli",
			"https://docs.github.com/en/copilot/how-tos/copilot-cli/use-hooks",
			"https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli-with-a-custom-model-provider",
			"https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference",
			"https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions",
			"https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills",
			"https://github.com/github/copilot-sdk/blob/main/docs/features/hooks.md",
		},
	},
	{
		InterruptionDriver: driverCLIACP,
		Lifecycle: lifecycleContract{
			Start:      "beforeRun",
			TurnEnd:    "afterRun",
			Terminal:   "afterRun",
			IdleEvents: []string{"afterRun"},
		},

		ID:          registry.Harness("cline"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://docs.cline.bot/cline-cli/overview",
			"https://docs.cline.bot/sdk/plugins",
			"https://docs.cline.bot/cline-cli/configuration",
			"https://docs.cline.bot/customization/cline-rules",
			"https://docs.cline.bot/customization/skills",
			"https://github.com/cline/cline/blob/main/sdk/packages/shared/src/storage/paths.ts",
		},
	},
	{
		LifecycleDriver:    driverACP,
		InterruptionDriver: driverACP,
		PermissionDriver:   driverACP,
		ShellCommand:       "printf 'aht-compat-marker\\n'",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("kimi-code"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://moonshotai.github.io/kimi-code/en/customization/hooks.html",
			"https://moonshotai.github.io/kimi-code/en/configuration/providers.html",
			"https://moonshotai.github.io/kimi-code/en/reference/kimi-acp.html",
			"https://moonshotai.github.io/kimi-code/en/configuration/data-locations.html",
			"https://moonshotai.github.io/kimi-code/en/customization/skills.html",
		},
	},
	{
		InterruptionDriver: driverTerminalQuit,
		InterruptTerminal:  true,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("grok"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://docs.x.ai/build/features/hooks",
			"https://docs.x.ai/build/overview",
			"https://docs.x.ai/build/settings",
			"https://docs.x.ai/build/features/project-rules",
			"https://docs.x.ai/build/features/skills-plugins-marketplaces",
		},
	},
	{
		Resume: resumeSubcommand,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("goose"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://goose-docs.ai/docs/guides/context-engineering/hooks/",
			"https://goose-docs.ai/docs/guides/environment-variables/",
			"https://goose-docs.ai/docs/guides/goose-cli-commands/",
			"https://goose-docs.ai/docs/guides/config-files",
			"https://goose-docs.ai/docs/guides/context-engineering/using-goosehints",
			"https://goose-docs.ai/docs/guides/context-engineering/using-skills",
		},
	},
	{
		LifecycleDriver:    driverCompletionRPC,
		InterruptionDriver: driverCompletionRPC,
		PermissionDriver:   driverCompletionRPC,
		Lifecycle: lifecycleContract{
			Start:    "session_start",
			TurnEnd:  "agent_settled",
			Terminal: "session_shutdown",
		},

		ID:          registry.Harness("pi"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md",
			"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md",
			"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/configuration.md",
			"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md",
		},
	},
	{
		InterruptionDriver: driverExtensionRPC,
		PermissionDriver:   driverExtensionRPC,
		Lifecycle: lifecycleContract{
			Start:    "session_start",
			TurnEnd:  "session_stop",
			Terminal: "session_shutdown",
		},

		ID:          registry.Harness("omp"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md",
			"https://github.com/can1357/oh-my-pi/blob/main/docs/extension-loading.md",
			"https://github.com/can1357/oh-my-pi/blob/main/docs/config-usage.md",
			"https://github.com/can1357/oh-my-pi/blob/main/docs/context-files.md",
			"https://github.com/can1357/oh-my-pi/blob/main/docs/skills.md",
			"https://github.com/can1357/oh-my-pi/blob/main/docs/environment-variables.md",
			"https://github.com/can1357/oh-my-pi/blob/main/packages/utils/src/dirs.ts",
		},
	},
	{
		InterruptionDriver: driverExecutionServer,
		PermissionDriver:   driverExecutionServer,
		Lifecycle: lifecycleContract{
			Start:      "session.execution.started",
			TurnEnd:    "session.execution.succeeded",
			Terminal:   "session.execution.succeeded",
			IdleEvents: []string{"session.execution.succeeded"},
		},

		ID:          registry.Harness("opencode"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://opencode.ai/v2/docs/build/plugins",
			"https://opencode.ai/docs/plugins/",
			"https://opencode.ai/docs/providers/#custom-provider",
			"https://opencode.ai/docs/config/",
			"https://opencode.ai/docs/rules/",
			"https://opencode.ai/docs/skills/",
		},
	},
	{
		Scope: "only plugin loading is asserted, through agy plugin list and agy -p /hooks with the " +
			"documented Gemini API-key provider pointed at a local endpoint; hook inspection must " +
			"not make model requests, and native lifecycle support is not asserted",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("agy"),
		Executable:  "agy",
		VersionArgs: []string{"--version"},
		LoadChecks:  agyLoadChecks,
		Level:       compatibilityLevelDiscovery,
		Docs: []string{
			"https://antigravity.google/docs/plugins?tab=cli",
			"https://antigravity.google/docs/hooks?tab=cli",
			"https://antigravity.google/docs/cli/install",
			"https://antigravity.google/docs/cli/headless",
			"https://antigravity.google/docs/settings?tab=cli",
			"https://antigravity.google/docs/rules",
			"https://antigravity.google/docs/mcp",
		},
	},
	{
		InterruptionDriver: driverPluginServer,
		PermissionDriver:   driverPluginServer,
		PermissionConfig:   "kilo.json",
		Lifecycle: lifecycleContract{
			Start:      "session.created",
			TurnEnd:    "session.status",
			Terminal:   "session.status",
			IdleEvents: []string{"session.status", "session.idle"},
		},

		ID:          registry.Harness("kilo"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://kilo.ai/docs/code-with-ai/platforms/cli-reference",
			"https://kilo.ai/docs/automate/extending/plugins",
			"https://kilo.ai/docs/customize/skills",
			"https://kilo.ai/docs/customize/custom-instructions",
			"https://kilo.ai/docs/code-with-ai/platforms/cli",
			"https://github.com/Kilo-Org/kilocode/blob/main/packages/core/src/global.ts",
		},
	},
	{
		LifecycleDriver:    driverExecRPC,
		InterruptionDriver: driverExecRPC,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("droid"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://docs.factory.ai/harness/hooks",
			"https://docs.factory.ai/droid-exec/overview",
			"https://docs.factory.ai/model-independence/byok",
			"https://docs.factory.com/droid-cli/settings",
			"https://docs.factory.com/harness/agents-md",
			"https://docs.factory.com/harness/skills",
		},
	},
	{
		Resume:        resumeGateway,
		CompactCallID: true,
		Lifecycle: lifecycleContract{
			Start:      "session_start",
			TurnEnd:    "agent_end",
			Terminal:   "agent_end",
			IdleEvents: []string{"agent_end"},
		},

		ID:          registry.Harness("openclaw"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://docs.openclaw.ai/plugins/hooks",
			"https://docs.openclaw.ai/gateway/protocol/rpc-session-control",
			"https://docs.openclaw.ai/cli/agent",
			"https://docs.openclaw.ai/help/environment",
			"https://docs.openclaw.ai/tools/skills",
			"https://docs.openclaw.ai/concepts/agent-workspace",
		},
	},
	{
		InterruptionDriver:  driverPythonACP,
		PermissionDriver:    driverPythonACP,
		DangerousPermission: true,
		Resume:              resumeQuery,
		Lifecycle: lifecycleContract{
			Start:      "on_session_start",
			TurnEnd:    "on_session_end",
			Terminal:   "on_session_end",
			IdleEvents: []string{"on_session_end"},
			Finalize:   "on_session_finalize",
		},

		ID:          registry.Harness("hermes"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/plugins.md",
			"https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/hooks.md",
			"https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/context-files.md",
			"https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/features/skills.md",
		},
	},
	{
		Scope: "the documented plugin API supplies native event handlers; " +
			"no isolated local-provider launch is available for a model turn",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("amp"),
		Executable:  "amp",
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelDiscovery,
		Docs: []string{
			"https://ampcode.com/docs/plugin-api",
			"https://ampcode.com/docs/threads",
			"https://ampcode.com/docs/cli/settings",
			"https://ampcode.com/docs/customize/agents-md",
			"https://ampcode.com/docs/customize/skills",
		},
	},
	{
		Scope: "https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md documents only " +
			"PreToolUse, so the hook supplies native session identity while start, idle, permission, " +
			"and end come from the screen manifest; native lifecycle support is not asserted",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("crush"),
		Executable:  "crush",
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelDiscovery,
		Docs: []string{
			"https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md",
			"https://github.com/charmbracelet/crush/blob/main/docs/config/README.md",
			"https://github.com/charmbracelet/crush/blob/main/README.md",
		},
	},
	{
		LifecycleDriver:    driverTerminalDialog,
		InterruptionDriver: driverTerminalDialog,
		PermissionDriver:   driverTerminalDialog,
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("qwen"),
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelLifecycle,
		Docs: []string{
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/headless.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/configuration/auth.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/configuration/settings.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/memory.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/skills.md",
			"https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/commands.md",
		},
	},
	{
		Scope: "https://github.com/PrimeIntellect-ai/prime-agent/blob/main/README.md documents no hook " +
			"or plugin surface; state comes from the daemon session catalog, so no installed " +
			"integration or native lifecycle support is asserted",
		Lifecycle: lifecycleContract{
			Start:    "SessionStart",
			TurnEnd:  "Stop",
			Terminal: "SessionEnd",
		},

		ID:          registry.Harness("prime-agent"),
		Executable:  "prime-agent",
		VersionArgs: []string{"--version"},
		Level:       compatibilityLevelDiscovery,
		Docs: []string{
			"https://github.com/PrimeIntellect-ai/prime-agent/blob/main/README.md",
			"https://github.com/PrimeIntellect-ai/prime-agent/blob/" +
				"561274fb2461d8d5b51c5125bc319e6bf17f3e3c/crates/pa-cli/src/" +
				"command_registry.rs",
			"https://github.com/PrimeIntellect-ai/prime-agent/blob/" +
				"561274fb2461d8d5b51c5125bc319e6bf17f3e3c/crates/pa-daemon/" +
				"src/supervisor/sessions.rs",
		},
	},
}

func TestEveryHarnessHasCurrentHostContract(t *testing.T) {
	contracts := make(map[registry.Harness]hostContract, len(hostContracts))
	for _, contract := range hostContracts {
		if _, exists := contracts[contract.ID]; exists {
			t.Fatalf("duplicate host contract for %s", contract.ID)
		}

		if len(contract.VersionArgs) == 0 || len(contract.Docs) == 0 {
			t.Fatalf("incomplete host contract for %s: %#v", contract.ID, contract)
		}

		if contract.Level == compatibilityLevelDiscovery && contract.Executable == "" {
			t.Fatalf("discovery-only host contract for %s names no executable", contract.ID)
		}

		if contract.Level == compatibilityLevelDiscovery && contract.Scope == "" {
			t.Fatalf("discovery-only host contract for %s describes no scope", contract.ID)
		}

		if contract.Level == compatibilityLevelLifecycle &&
			(contract.Lifecycle.Start == "" || contract.Lifecycle.TurnEnd == "" || contract.Lifecycle.Terminal == "") {
			t.Fatalf("lifecycle host contract for %s omits native event evidence", contract.ID)
		}

		assertAuthoritativeDocs(t, contract)
		contracts[contract.ID] = contract
	}

	for _, adapter := range catalog.All() {
		assertAdapterContract(t, adapter)
		delete(contracts, adapter.Definition().ID)
	}

	for id := range contracts {
		t.Errorf("host contract %s does not identify a registered harness", id)
	}
}

func assertAuthoritativeDocs(t *testing.T, contract hostContract) {
	t.Helper()

	for _, rawURL := range contract.Docs {
		parsed, err := url.ParseRequestURI(rawURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			t.Fatalf("invalid authoritative URL for %s: %q", contract.ID, rawURL)
		}
	}
}

func assertAdapterContract(t *testing.T, adapter harness.Adapter) {
	t.Helper()

	id := adapter.Definition().ID

	contract, err := contractFor(id)
	if err != nil {
		t.Error(err)
		return
	}

	if contract.Level == compatibilityLevelLifecycle && !launch.Supported(id) {
		t.Errorf("lifecycle host contract %s has no launch plan", id)
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

var agyLoadChecks = []loadCheck{
	{
		Args:    []string{"plugin", "list"},
		Needle:  func(*isolatedHost) string { return "aht-state" },
		Prepare: nil,
	},
	{
		Args:    []string{"-p", "/hooks"},
		Needle:  func(host *isolatedHost) string { return host.aht },
		Prepare: prepareAgyHookInspection,
	},
}
