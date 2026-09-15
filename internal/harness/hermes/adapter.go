package hermes

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	integrationVersion      = 8
	hermesCommand           = "hermes"
	hermesPluginName        = "aht-state"
	hermesMarkerFileName    = ".aht-managed"
	hermesIntegrationSource = "hermes-plugin"
)

//go:embed assets/__init__.py.tmpl
var hermesPluginTemplate string

type hermesHarness struct{ harness.BaseAdapter }

func New() hermesHarness {
	return hermesHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessHermes,
		Aliases:      []string{"hermes-agent", "hermes_agent"},
		ProcessNames: []string{"hermes", "hermes-agent"},
		Env: harness.EnvKeys{
			SessionID:   nil,
			SessionPath: nil,
			ProjectRoot: nil,
			PID:         nil,
			Event:       nil,
		},
		Capabilities: harness.Capabilities{
			SessionStart:      true,
			SessionEnd:        true,
			RunningIdle:       true,
			WaitingPermission: true,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  hermesIntegrationSource,
		StateAuthority:     harness.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (hermesHarness) InstallPlan(binary string) harness.InstallPlan {
	version := strconv.Itoa(integrationVersion)
	dir := filepath.Join(hermesHome(), "plugins", hermesPluginName)

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   dir,
		Label: "Hermes plugin",
		Files: []harness.RenderedFileInstallSpec{
			{Name: "plugin.yaml", Content: hermesPluginManifest(version), JSONContent: nil},
			{Name: "__init__.py", Content: renderHermesPlugin(binary, version), JSONContent: nil},
			{Name: hermesMarkerFileName, Content: hermesMarkerContent(version), JSONContent: nil},
		},
		SnippetOrder:   []string{"plugin.yaml", "__init__.py", hermesMarkerFileName},
		MarkerFile:     hermesMarkerFileName,
		ObsoleteFiles:  nil,
		ImportManifest: nil,
		Registration:   newRegistration(hermesCommand, hermesPluginName, "0.0."+version),
	}}}}
}

func (hermesHarness) ResumeCommand(sessionID string, _ string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	return []string{hermesCommand, harness.ResumeFlag, sessionID}
}

func hermesHome() string {
	if configured := strings.TrimSpace(os.Getenv("HERMES_HOME")); configured != "" {
		return configured
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".hermes")
	}

	return ".hermes"
}

func hermesPluginManifest(version string) string {
	return fmt.Sprintf(`name: aht-state
version: "0.0.%s"
description: Reports local Hermes session lifecycle and activity to aht.
provides_hooks:
  - on_session_start
  - pre_llm_call
  - on_session_end
  - on_session_finalize
  - on_session_reset
  - pre_approval_request
  - post_approval_response
`, version)
}

func renderHermesPlugin(binary string, version string) string {
	replacer := strings.NewReplacer(
		"{{BINARY}}", strconv.Quote(binary),
		"{{INTEGRATION_VERSION}}", strconv.Quote(version),
	)

	return replacer.Replace(hermesPluginTemplate)
}

func hermesMarkerContent(version string) string {
	return fmt.Sprintf("%s\nAHT_INTEGRATION_ID=hermes\nAHT_INTEGRATION_VERSION=%s\nAHT_SOURCE=%s\n", harness.ManagedMarker, version, hermesIntegrationSource)
}
