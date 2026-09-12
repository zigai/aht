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
	integrationVersion        = 9
	openClawCommand           = "openclaw"
	openClawPluginName        = "aht-state"
	openClawMarkerFileName    = ".aht-managed"
	openClawIntegrationSource = "openclaw-plugin"
)

//go:embed assets/index.js.tmpl
var openClawPluginTemplate string

type openClawHarness struct{ harness.BaseAdapter }

func New() openClawHarness {
	return openClawHarness{BaseAdapter: harness.NewBaseAdapter(harness.Definition{
		ID:           registry.HarnessOpenClaw,
		Aliases:      nil,
		ProcessNames: nil,
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
	})}
}

func (openClawHarness) InstallPlan(binary string) harness.InstallPlan {
	version := strconv.Itoa(integrationVersion)
	dir := filepath.Join(registry.DefaultStateDir(), "integrations", "openclaw", openClawPluginName)

	return harness.InstallPlan{Actions: []harness.InstallAction{harness.PluginDirectoryAction{Plan: harness.PluginDirectoryInstallPlan{
		Dir:   dir,
		Label: "OpenClaw plugin",
		Files: []harness.RenderedFileInstallSpec{
			{Name: "package.json", Content: "", JSONContent: map[string]any{
				"name": openClawPluginName, "version": "0.0." + version, "private": true, "type": "module",
				"openclaw": map[string]any{"extensions": []string{"./index.js"}},
			}},
			{Name: "openclaw.plugin.json", Content: "", JSONContent: map[string]any{
				"id": openClawPluginName, "name": "AHT State", "version": "0.0." + version,
				"description":  "Reports local OpenClaw session lifecycle and activity to aht.",
				"activation":   map[string]any{"onCapabilities": []string{"hook"}},
				"configSchema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
			}},
			{Name: "index.js", Content: renderOpenClawPlugin(binary, version), JSONContent: nil},
			{Name: openClawMarkerFileName, Content: openClawMarkerContent(version), JSONContent: nil},
		},
		SnippetOrder:   []string{"package.json", "openclaw.plugin.json", "index.js", openClawMarkerFileName},
		MarkerFile:     openClawMarkerFileName,
		ObsoleteFiles:  nil,
		ImportManifest: nil,
		Registration:   newRegistration(openClawCommand, openClawPluginName, "0.0."+version, true),
	}}}}
}

func renderOpenClawPlugin(binary string, version string) string {
	replacer := strings.NewReplacer(
		"{{BINARY}}", strconv.Quote(binary),
		"{{INTEGRATION_VERSION}}", strconv.Quote(version),
	)

	return replacer.Replace(openClawPluginTemplate)
}

func openClawMarkerContent(version string) string {
	return fmt.Sprintf("%s\nAHT_INTEGRATION_ID=openclaw\nAHT_INTEGRATION_VERSION=%s\nAHT_SOURCE=%s\n", harness.ManagedMarker, version, openClawIntegrationSource)
}
