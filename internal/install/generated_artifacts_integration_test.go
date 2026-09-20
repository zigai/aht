//go:build integration

package install

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

func TestGeneratedArtifactsParse(t *testing.T) {
	requireRuntimeTool(t, "node")
	requireRuntimeTool(t, "python3")
	requireRuntimeTool(t, "sh")

	artifacts := collectGeneratedArtifacts(t, captureBinary(t))
	seenHarnesses := make(map[registry.Harness]bool)
	for _, artifact := range artifacts {
		artifact := artifact
		t.Run(string(artifact.harness)+"/"+strings.ReplaceAll(artifact.path, "/", "_"), func(t *testing.T) {
			validateGeneratedArtifact(t, artifact)
		})
		seenHarnesses[artifact.harness] = true
	}
	for _, harness := range AllHarnesses() {
		if !seenHarnesses[harness] {
			t.Fatalf("generated artifact validation missed harness %q", harness)
		}
	}
}

func validateGeneratedArtifact(t *testing.T, artifact generatedArtifact) {
	t.Helper()
	path := strings.ToLower(artifact.path)
	switch {
	case strings.HasSuffix(path, ".json"):
		if !json.Valid([]byte(artifact.content)) {
			t.Fatalf("invalid generated JSON:\n%s", artifact.content)
		}
	case strings.HasSuffix(path, ".toml"):
		var value map[string]any
		if err := toml.Unmarshal([]byte(artifact.content), &value); err != nil {
			t.Fatalf("invalid generated TOML: %v\n%s", err, artifact.content)
		}
	case strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"):
		var value map[string]any
		if err := yaml.Unmarshal([]byte(artifact.content), &value); err != nil {
			t.Fatalf("invalid generated YAML: %v\n%s", err, artifact.content)
		}
	case strings.HasSuffix(path, ".ts"):
		file := writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content)
		moduleURL := (&url.URL{Scheme: "file", Path: file}).String()
		runGeneratedCommand(t, exec.Command("node", "--experimental-strip-types", "--eval", "import("+strconv.Quote(moduleURL)+")"), "")
	case strings.HasSuffix(path, ".js"):
		file := writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content)
		runGeneratedCommand(t, exec.Command("node", "--check", file), "")
	case strings.HasSuffix(path, ".py"):
		file := writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content)
		runGeneratedCommand(t, exec.Command("python3", "-m", "py_compile", file), "")
	case strings.HasSuffix(path, ".sh"):
		file := writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content)
		runGeneratedCommand(t, exec.Command("sh", "-n", file), "")
	default:
		if !strings.Contains(artifact.content, harnesspkg.ManagedMarker) {
			t.Fatalf("unrecognized generated artifact %q lacks managed marker", artifact.path)
		}
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
	for _, adapter := range harnesscatalog.All() {
		installer, ok := adapter.(harnesspkg.Installable)
		if !ok {
			continue
		}
		harness := adapter.Definition().ID
		for _, action := range installer.InstallPlan(binary.command).Actions {
			switch plan := action.(type) {
			case harnesspkg.JSONCommandHooksAction:
				config := make(map[string]any)
				applyJSONCommandHooks(harness, plan.Plan)(config)
				artifacts = append(artifacts, generatedJSONArtifact(t, harness, plan.Plan.Path, config))
			case harnesspkg.CursorJSONHooksAction:
				config := make(map[string]any)
				applyCursorJSONHooks(harness, plan.Plan)(config)
				artifacts = append(artifacts, generatedJSONArtifact(t, harness, plan.Plan.Path, config))
			case harnesspkg.ManagedTextBlockAction:
				artifacts = append(artifacts, generatedArtifact{harness: harness, path: plan.Plan.Path, content: plan.Plan.Block})
			case harnesspkg.RenderedFileAction:
				content, err := renderInstallContent(plan.Plan.Content, plan.Plan.JSONContent)
				if err != nil {
					t.Fatalf("render %s artifact: %v", harness, err)
				}
				artifacts = append(artifacts, generatedArtifact{harness: harness, path: plan.Plan.Path, content: content})
			case harnesspkg.PluginDirectoryAction:
				for _, file := range plan.Plan.Files {
					content, err := renderInstallContent(file.Content, file.JSONContent)
					if err != nil {
						t.Fatalf("render %s plugin artifact %s: %v", harness, file.Name, err)
					}
					artifacts = append(artifacts, generatedArtifact{harness: harness, path: file.Name, content: content})
				}
			case harnesspkg.ShimAction:
				artifacts = append(artifacts, generatedArtifact{harness: harness, path: string(harness) + ".sh", content: shimScript(binary.command, string(harness), "/usr/bin/true", harnesscatalog.IntegrationVersionFor(harness))})
			default:
				t.Fatalf("unvalidated install action for %s: %T", harness, action)
			}
		}
	}
	return artifacts
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
	adapter, ok := harnesscatalog.Find(harness)
	if !ok {
		t.Fatalf("find harness %s", harness)
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		t.Fatalf("harness %s is not installable", harness)
	}
	for _, action := range installer.InstallPlan(captureBinaryCommand(t)).Actions {
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
