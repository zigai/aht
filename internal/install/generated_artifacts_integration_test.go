//go:build integration

package install

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestGeneratedArtifactsParse(t *testing.T) {
	requireRuntimeTool(t, "node")
	requireRuntimeTool(t, "python3")
	requireRuntimeTool(t, "sh")

	artifacts := collectGeneratedArtifacts(t, captureBinary(t))
	seenHarnesses := make(map[registry.Harness]bool)
	for _, artifact := range artifacts {
		t.Run(string(artifact.harness)+"/"+strings.ReplaceAll(artifact.path, "/", "_"), func(t *testing.T) {
			validateGeneratedArtifact(t, artifact)
		})
		seenHarnesses[artifact.harness] = true
	}
	for _, harness := range Harnesses() {
		if !seenHarnesses[harness] {
			t.Fatalf("generated artifact validation missed harness %q", harness)
		}
	}
}

var unresolvedPlaceholder = regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)

func validateGeneratedArtifact(t *testing.T, artifact generatedArtifact) {
	t.Helper()
	if match := unresolvedPlaceholder.FindString(artifact.content); match != "" {
		t.Fatalf("generated artifact contains unresolved placeholder %q:\n%s", match, artifact.content)
	}
	switch extension := strings.ToLower(filepath.Ext(artifact.path)); extension {
	case ".json":
		if !json.Valid([]byte(artifact.content)) {
			t.Fatalf("invalid generated JSON:\n%s", artifact.content)
		}
	case ".toml":
		var value map[string]any
		if err := toml.Unmarshal([]byte(artifact.content), &value); err != nil {
			t.Fatalf("invalid generated TOML: %v\n%s", err, artifact.content)
		}
	case ".yaml", ".yml":
		var value map[string]any
		if err := yaml.Unmarshal([]byte(artifact.content), &value); err != nil {
			t.Fatalf("invalid generated YAML: %v\n%s", err, artifact.content)
		}
	case ".ts", ".js", ".py", ".sh":
		checkGeneratedScriptSyntax(t, extension, writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content))
	default:
		if !strings.Contains(artifact.content, harnesspkg.ManagedMarker) {
			t.Fatalf("unrecognized generated artifact %q lacks managed marker", artifact.path)
		}
	}
}

func checkGeneratedScriptSyntax(t *testing.T, extension string, file string) {
	t.Helper()
	switch extension {
	case ".ts":
		moduleURL := (&url.URL{Scheme: "file", Path: file}).String()
		runGeneratedCommand(t, "", "node", "--experimental-strip-types", "--eval", "import("+strconv.Quote(moduleURL)+")")
	case ".js":
		runGeneratedCommand(t, "", "node", "--check", file)
	case ".py":
		runGeneratedCommand(t, "", "python3", "-m", "py_compile", file)
	case ".sh":
		runGeneratedCommand(t, "", "sh", "-n", file)
	default:
		t.Fatalf("no syntax check for generated %s file", extension)
	}
}

type generatedArtifact struct {
	harness registry.Harness
	path    string
	content string
}

func collectGeneratedArtifacts(t *testing.T, binary captureExecutable) []generatedArtifact {
	t.Helper()
	artifacts := make([]generatedArtifact, 0)
	for _, adapter := range catalog.All() {
		installer, ok := adapter.(harnesspkg.Installable)
		if !ok {
			continue
		}
		harness := adapter.Definition().ID
		plan, err := installer.InstallPlan(binary.command)
		if err != nil {
			t.Fatalf("plan %s integration: %v", harness, err)
		}
		for _, action := range plan.Actions {
			artifacts = append(artifacts, generatedActionArtifacts(t, harness, binary, action)...)
		}
	}
	return artifacts
}

func generatedActionArtifacts(t *testing.T, harness registry.Harness, binary captureExecutable, action harnesspkg.InstallAction) []generatedArtifact {
	t.Helper()
	switch plan := action.(type) {
	case harnesspkg.JSONCommandHooksAction:
		config := make(map[string]any)
		applyJSONCommandHooks(harness, plan.Plan)(config)
		return []generatedArtifact{generatedJSONArtifact(t, harness, plan.Plan.Path, config)}
	case harnesspkg.CursorJSONHooksAction:
		config := make(map[string]any)
		applyCursorJSONHooks(harness, plan.Plan)(config)
		return []generatedArtifact{generatedJSONArtifact(t, harness, plan.Plan.Path, config)}
	case harnesspkg.ManagedTextBlockAction:
		return []generatedArtifact{{harness: harness, path: plan.Plan.Path, content: plan.Plan.Block}}
	case harnesspkg.RenderedFileAction:
		content, err := renderInstallContent(plan.Plan.Content, plan.Plan.JSONContent)
		if err != nil {
			t.Fatalf("render %s artifact: %v", harness, err)
		}
		return []generatedArtifact{{harness: harness, path: plan.Plan.Path, content: content}}
	case harnesspkg.PluginDirectoryAction:
		artifacts := make([]generatedArtifact, 0, len(plan.Plan.Files))
		for _, file := range plan.Plan.Files {
			content, err := renderInstallContent(file.Content, file.JSONContent)
			if err != nil {
				t.Fatalf("render %s plugin artifact %s: %v", harness, file.Name, err)
			}
			artifacts = append(artifacts, generatedArtifact{harness: harness, path: file.Name, content: content})
		}
		return artifacts
	case harnesspkg.ShimAction:
		return []generatedArtifact{{harness: harness, path: string(harness) + ".sh", content: shimScript(binary.command, string(harness), "/usr/bin/true", catalog.IntegrationVersionFor(harness))}}
	default:
		t.Fatalf("unvalidated install action for %s: %T", harness, action)
		return nil
	}
}

func generatedJSONArtifact(t *testing.T, harness registry.Harness, path string, value any) generatedArtifact {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s generated JSON: %v", harness, err)
	}
	return generatedArtifact{harness: harness, path: path, content: string(content)}
}

func generatedCommandHook(t *testing.T, harness registry.Harness) string {
	t.Helper()
	adapter, ok := catalog.Find(harness)
	if !ok {
		t.Fatalf("find harness %s", harness)
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatalf("harness %s is not installable", harness)
	}
	plan, err := installer.InstallPlan(captureBinaryCommand(t))
	if err != nil {
		t.Fatalf("plan %s integration: %v", harness, err)
	}
	for _, action := range plan.Actions {
		if plan, ok := action.(harnesspkg.JSONCommandHooksAction); ok && len(plan.Plan.Hooks) > 0 {
			return plan.Plan.Hooks[0].Command
		}
	}
	t.Fatalf("harness %s has no command hook", harness)
	return ""
}

func generatedArtifactContent(t *testing.T, harness registry.Harness, suffix string) string {
	t.Helper()
	for _, artifact := range collectGeneratedArtifacts(t, captureExecutable{command: captureBinaryCommand(t), path: os.Getenv("AHT_CAPTURE")}) {
		if artifact.harness == harness && strings.HasSuffix(filepath.ToSlash(artifact.path), suffix) {
			return artifact.content
		}
	}
	t.Fatalf("generated artifact %s/%s not found", harness, suffix)
	return ""
}
