package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/shlex"

	harnesspkg "github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

// Upgrade refreshes existing AHT-owned integrations through their native install
// plans. It does not select new harnesses or switch a shim to a native adapter.
func Upgrade(ctx context.Context, binary string, dryRun bool) ([]Result, error) {
	results := make([]Result, 0)
	var failures []error
	for _, id := range AllHarnesses() {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		options := Options{Harness: id, Binary: binary, DryRun: dryRun, TargetBinary: "", Force: false, UseShim: false}
		native, err := installedNative(id, binary)
		if err != nil {
			var result Result
			results = appendUpgradeResult(results, id, result, err)
			failures = append(failures, err)
		} else if native {
			result, installErr := upgradeNative(ctx, options)
			results = appendUpgradeResult(results, id, result, installErr)
			failures = append(failures, installErr)
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		target, exists, err := installedShim(id)
		if err == nil && exists {
			options.TargetBinary = target
			// Call the shim installer directly: an existing fallback remains a fallback
			// even if the current adapter has gained a native integration.
			result, installErr := installShim(options, id)
			results = appendUpgradeResult(results, id, result, installErr)
			failures = append(failures, installErr)
		} else if err != nil {
			var result Result
			results = appendUpgradeResult(results, id, result, err)
			failures = append(failures, err)
		}
	}
	return results, errors.Join(failures...)
}

func upgradeNative(ctx context.Context, options Options) (Result, error) {
	plan, _, err := installPlanForHarness(options.Harness, options.Binary)
	if err != nil {
		return Result{}, err
	}
	for _, action := range plan.Actions {
		plugin, ok := action.(harnesspkg.PluginDirectoryAction)
		if !ok || plugin.Plan.Registration == nil {
			continue
		}
		registration := plugin.Plan.Registration
		state, err := registration.Inspect(ctx, plugin.Plan.Dir)
		if err != nil {
			return Result{}, fmt.Errorf("inspect %s for upgrade: %w", registration.Label(), err)
		}
		if state == harnesspkg.PluginRegistrationForeign {
			return Result{}, fmt.Errorf("%w: %s", errForeignFile, registration.Label())
		}
		if !options.DryRun {
			if err := registration.EnsureMutable(plugin.Plan.Dir); err != nil {
				return Result{}, fmt.Errorf("check %s for upgrade: %w", registration.Label(), err)
			}
		}
		// Re-registering through Install can enable disabled plugins or grant new
		// permissions. Refresh their local source while preserving native policy.
		plugin.Plan.Registration = nil
		result, err := installPluginDirectory(ctx, options, options.Harness, plugin.Plan)
		if result.Changed {
			result.NextStep = "restart the harness to load updated plugin code; registration and permissions preserved"
		}
		return result, err
	}
	return RunContext(ctx, options)
}

func appendUpgradeResult(results []Result, id registry.Harness, result Result, err error) []Result {
	if err != nil {
		result.Harness = string(id)
		result.Message = "upgrade failed"
		result.Error = err.Error()
	}
	// Generated content may include unrelated user configuration. Upgrade reports
	// only outcomes; explicit integration installs retain their content preview.
	result.Snippet = ""
	return append(results, result)
}

func installedNative(id registry.Harness, binary string) (bool, error) {
	plan, _, err := installPlanForHarness(id, binary)
	if err != nil {
		return false, err
	}
	paths := planPaths(plan)
	for _, action := range plan.Actions {
		if plugin, ok := action.(harnesspkg.PluginDirectoryAction); ok {
			paths = append(paths, plugin.Plan.ObsoleteFiles...)
		}
	}
	for _, path := range paths {
		status, err := classifyArtifactForHarness(path, id)
		if err != nil {
			return false, err
		}
		if status == ArtifactCurrent || status == ArtifactStale {
			return true, nil
		}
	}
	return false, nil
}

func installedShim(id registry.Harness) (string, bool, error) {
	path := filepath.Join(registry.DefaultStateDir(), "shims", string(id))
	status, err := ClassifyArtifact(path)
	if err != nil {
		return "", false, err
	}
	if status == ArtifactMissing || status == ArtifactForeign {
		return "", false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", true, fmt.Errorf("read installed shim: %w", err)
	}
	for line := range strings.Lines(string(data)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "harness_bin="); ok {
			words, err := shlex.Split(value)
			if err != nil {
				return "", true, fmt.Errorf("parse installed shim target: %w", err)
			}
			if len(words) == 1 && words[0] != "" {
				return words[0], true, nil
			}
		}
	}
	return "", true, fmt.Errorf("%w: cannot recover target from %s", errRecursiveShimTarget, path)
}
