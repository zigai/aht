package openclaw

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

const (
	integrationVersion        = 10
	openclawCommand           = "openclaw"
	openclawPluginName        = "aht-state"
	openclawMarkerFileName    = ".aht-managed"
	openclawIntegrationSource = "openclaw-plugin"
	openclawSessionFlag       = "--session"
)

//go:embed assets/index.js.tmpl
var openclawPluginTemplate string

type openclawHarness struct{ harness.BaseAdapter }

func New() openclawHarness {
	return openclawHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ExclusiveProcess: false,
		CatalogCreates:   false,
		ID:               registry.Harness("openclaw"),
		Aliases:          nil,
		ProcessNames:     []string{"openclaw"},
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
			WaitingPermission: false,
			ProcessIdentity:   false,
			NativeCatalog:     false,
			TTYTmuxContext:    false,
		},
		IntegrationVersion: integrationVersion,
		IntegrationSource:  openclawIntegrationSource,
		StateAuthority:     registry.AuthorityHook,
		ScreenFallback:     false,
	})}
}

func (openclawHarness) InstallPlan(binary string) harness.InstallPlan {
	version := strconv.Itoa(integrationVersion)
	dir := filepath.Join(registry.DefaultStateDir(), "integrations", "openclaw", openclawPluginName)

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   dir,
		Label: "OpenClaw plugin",
		Files: []harness.RenderedFileInstallSpec{
			{Name: "package.json", Content: "", JSONContent: map[string]any{
				"name": openclawPluginName, "version": "0.0." + version, "private": true, "type": "module",
				"openclaw": map[string]any{"extensions": []string{"./index.js"}},
			}},
			{Name: "openclaw.plugin.json", Content: "", JSONContent: map[string]any{
				"id": openclawPluginName, "name": "AHT State", "version": "0.0." + version,
				"description":  "Reports local OpenClaw session lifecycle and activity to aht.",
				"activation":   map[string]any{"onCapabilities": []string{"hook"}},
				"configSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
			}},
			{Name: "index.js", Content: renderOpenclawPlugin(binary, version), JSONContent: nil},
			{Name: openclawMarkerFileName, Content: openclawMarkerContent(version), JSONContent: nil},
		},
		SnippetOrder:   []string{"package.json", "openclaw.plugin.json", "index.js", openclawMarkerFileName},
		MarkerFile:     openclawMarkerFileName,
		ImportManifest: nil,
		Registration:   newRegistration(openclawCommand, openclawPluginName, "0.0."+version, true),
	}}}}
}

func (openclawHarness) ResumeCommand(sessionID string, _ string) []string {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	return []string{openclawCommand, "tui", openclawSessionFlag, sessionID}
}

func renderOpenclawPlugin(binary string, version string) string {
	replacer := strings.NewReplacer(
		"{{BINARY}}", strconv.Quote(binary),
		"{{INTEGRATION_VERSION}}", strconv.Quote(version),
	)

	return replacer.Replace(openclawPluginTemplate)
}

func openclawMarkerContent(version string) string {
	return fmt.Sprintf("%s\nAHT_INTEGRATION_ID=openclaw\nAHT_INTEGRATION_VERSION=%s\nAHT_SOURCE=%s\n", harness.ManagedMarker, version, openclawIntegrationSource)
}
