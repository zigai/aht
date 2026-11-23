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

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestGeneratedArtifactsParse(t *testing.T) {
	requireRuntimeTool(t, "node")
	requireRuntimeTool(t, "python3")
	requireRuntimeTool(t, "sh")

	artifacts := collectGeneratedArtifacts(t, captureBinary(t))
	seenHarnesses := make(map[registry.Harness]bool)
	typescript := make([]generatedArtifact, 0, len(artifacts))

	for _, artifact := range artifacts {
		t.Run(
			string(artifact.harness)+"/"+strings.ReplaceAll(artifact.path, "/", "_"),
			func(t *testing.T) {
				validateGeneratedArtifact(t, artifact)
			},
		)

		if strings.EqualFold(filepath.Ext(artifact.path), ".ts") {
			typescript = append(typescript, artifact)
		}

		seenHarnesses[artifact.harness] = true
	}

	t.Run("typescript modules", func(t *testing.T) {
		checkTypescriptModules(t, typescript)
	})

	for _, harnessID := range adapters.Harnesses() {
		if noInstallRequiredHarness(t, harnessID) {
			continue
		}

		if !seenHarnesses[harnessID] {
			t.Fatalf("generated artifact validation missed harness %q", harnessID)
		}
	}
}

var unresolvedPlaceholder = regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`)

func validateGeneratedArtifact(t *testing.T, artifact generatedArtifact) {
	t.Helper()

	if match := unresolvedPlaceholder.FindString(artifact.content); match != "" {
		t.Fatalf(
			"generated artifact contains unresolved placeholder %q:\n%s",
			match,
			artifact.content,
		)
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
	case ".ts":
		// Checked together in one node process by TestGeneratedArtifactsParse.
	case ".js", ".py", ".sh":
		checkGeneratedScriptSyntax(
			t,
			extension,
			writeRuntimeArtifact(t, filepath.Base(artifact.path), artifact.content),
		)
	default:
		if !strings.Contains(artifact.content, harness.ManagedMarker) {
			t.Fatalf("unrecognized generated artifact %q lacks managed marker", artifact.path)
		}
	}
}

func checkTypescriptModules(t *testing.T, artifacts []generatedArtifact) {
	t.Helper()

	if len(artifacts) == 0 {
		return
	}

	modules := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		name := string(artifact.harness) + "-" + filepath.Base(artifact.path)
		file := writeRuntimeArtifact(t, name, artifact.content)
		modules = append(modules, strconv.Quote((&url.URL{Scheme: "file", Path: file}).String()))
	}

	failure := "  try { await import(file) } catch (error) {\n" +
		"    console.error(`FAIL ${file}: ${error}`)\n" +
		"    process.exitCode = 1\n" +
		"  }\n"
	script := "for (const file of [" + strings.Join(modules, ", ") + "]) {\n" + failure + "}\n"

	runGeneratedCommand(t, script, "node", "--experimental-strip-types", "--input-type=module")
}

func checkGeneratedScriptSyntax(t *testing.T, extension string, file string) {
	t.Helper()

	switch extension {
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

	for _, adapter := range adapters.All() {
		harnessID := adapter.Definition().ID

		plan, err := adapter.InstallPlan(binary.command)
		if err != nil {
			t.Fatalf("plan %s integration: %v", harnessID, err)
		}

		for _, action := range plan.Actions {
			artifacts = append(artifacts, generatedActionArtifacts(t, harnessID, binary, action)...)
		}
	}

	return artifacts
}

func generatedHookArtifact(
	t *testing.T,
	harnessID registry.Harness,
	path string,
	apply func(map[string]any) (bool, error),
) generatedArtifact {
	t.Helper()

	config := make(map[string]any)
	if _, err := apply(config); err != nil {
		t.Fatalf("apply %s hooks: %v", harnessID, err)
	}

	return generatedJSONArtifact(t, harnessID, path, config)
}

func generatedActionArtifacts(
	t *testing.T,
	harnessID registry.Harness,
	binary captureExecutable,
	action harness.InstallAction,
) []generatedArtifact {
	t.Helper()

	switch plan := action.(type) {
	case harness.JSONCommandHookAction:
		return []generatedArtifact{
			generatedHookArtifact(
				t,
				harnessID,
				plan.Plan.Path,
				applyJSONCommandHooks(harnessID, plan.Plan),
			),
		}
	case harness.JSONMergeAction:
		return []generatedArtifact{
			generatedHookArtifact(
				t,
				harnessID,
				plan.Plan.Path,
				applyJSONMerge(plan.Plan),
			),
		}
	case harness.ManagedTextBlockAction:
		return []generatedArtifact{
			{harness: harnessID, path: plan.Plan.Path, content: plan.Plan.Block},
		}
	case harness.RenderedFileAction:
		content, err := renderInstallContent(plan.Plan.Content, plan.Plan.JSONContent)
		if err != nil {
			t.Fatalf("render %s artifact: %v", harnessID, err)
		}

		return []generatedArtifact{{harness: harnessID, path: plan.Plan.Path, content: content}}
	case harness.PluginDirectoryAction:
		artifacts := make([]generatedArtifact, 0, len(plan.Plan.Files))
		for _, file := range plan.Plan.Files {
			content, err := renderInstallContent(file.Content, file.JSONContent)
			if err != nil {
				t.Fatalf("render %s plugin artifact %s: %v", harnessID, file.Name, err)
			}

			artifacts = append(
				artifacts,
				generatedArtifact{harness: harnessID, path: file.Name, content: content},
			)
		}

		return artifacts
	case harness.NativeCatalogAction:
		return nil
	case harness.ShimAction:
		return []generatedArtifact{
			{
				harness: harnessID,
				path:    string(harnessID) + ".sh",
				content: shimScript(
					binary.command,
					harnessID,
					"/usr/bin/true",
					adapters.IntegrationVersionFor(harnessID),
				),
			},
		}
	default:
		t.Fatalf("unvalidated install action for %s: %T", harnessID, action)
		return nil
	}
}

func generatedJSONArtifact(
	t *testing.T,
	harnessID registry.Harness,
	path string,
	value any,
) generatedArtifact {
	t.Helper()

	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s generated JSON: %v", harnessID, err)
	}

	return generatedArtifact{harness: harnessID, path: path, content: string(content)}
}

func generatedCommandHook(t *testing.T, harnessID registry.Harness) string {
	t.Helper()

	adapter, ok := adapters.Find(harnessID)
	if !ok {
		t.Fatalf("find harness %s", harnessID)
	}

	plan, err := adapter.InstallPlan(captureBinaryCommand(t))
	if err != nil {
		t.Fatalf("plan %s integration: %v", harnessID, err)
	}

	for _, action := range plan.Actions {
		if plan, ok := action.(harness.JSONCommandHookAction); ok && len(plan.Plan.Hooks) > 0 {
			return plan.Plan.Hooks[0].Command
		}
	}

	t.Fatalf("harness %s has no command hook", harnessID)

	return ""
}

func generatedArtifactContent(t *testing.T, harnessID registry.Harness, suffix string) string {
	t.Helper()

	return generatedArtifactContentFor(
		t,
		captureExecutable{command: captureBinaryCommand(t), path: os.Getenv("AHT_CAPTURE")},
		harnessID,
		suffix,
	)
}

func generatedArtifactContentFor(
	t *testing.T,
	binary captureExecutable,
	harnessID registry.Harness,
	suffix string,
) string {
	t.Helper()

	for _, artifact := range collectGeneratedArtifacts(t, binary) {
		if artifact.harness == harnessID &&
			strings.HasSuffix(filepath.ToSlash(artifact.path), suffix) {
			return artifact.content
		}
	}

	t.Fatalf("generated artifact %s/%s not found", harnessID, suffix)

	return ""
}
