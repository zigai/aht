package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/shlex"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

//exhaustruct:optional
type UpgradeOptions struct {
	Binary string
	DryRun bool
}

// Upgrade refreshes existing AHT-owned integrations through their native install
// plans. It does not select new harnesses or switch a shim to a native adapter.
func Upgrade(ctx context.Context, opts UpgradeOptions) ([]Result, error) {
	results := make([]Result, 0)

	var failures []error

	for _, id := range adapters.Harnesses() {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		harnessResults, err := UpgradeHarness(ctx, id, opts)
		results = append(results, harnessResults...)
		failures = append(failures, err)

		if ctx.Err() != nil {
			break
		}
	}

	return results, errors.Join(failures...)
}

// UpgradeHarness refreshes the existing AHT-owned integrations of one harness
// the way [Upgrade] does, keeping a shim fallback a fallback.
func UpgradeHarness(
	ctx context.Context,
	id registry.Harness,
	opts UpgradeOptions,
) ([]Result, error) {
	var (
		results  []Result
		failures []error
	)

	binary := opts.Binary
	if binary == "" {
		binary = defaultBinary
	}

	options := Options{Harness: id, Binary: binary, DryRun: opts.DryRun}

	plan, _, err := installPlanForHarness(id, binary)
	if err != nil {
		return nil, err
	}

	pendingRecovery, err := pluginRecoveryPending(plan)
	if err != nil {
		var result Result
		return appendUpgradeResult(nil, id, result, err), err
	}

	results, err = upgradeInstalledNative(ctx, options, pendingRecovery)
	failures = append(failures, err)

	if err := ctx.Err(); err != nil {
		return results, errors.Join(append(failures, err)...)
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

	return results, errors.Join(failures...)
}

func upgradeInstalledNative(
	ctx context.Context,
	opts Options,
	pendingRecovery bool,
) ([]Result, error) {
	var result Result

	native, err := installedNative(ctx, opts.Harness, opts.Binary)
	upgraded := err == nil && (native || pendingRecovery)

	if upgraded {
		result, err = upgradeNative(ctx, opts)
	}

	switch {
	case errors.Is(err, harness.ErrCommandMissing):
		// A harness uninstalled without removing its integration cannot be
		// upgraded, and nothing runs the stale integration either.
		return []Result{{
			Harness:  string(opts.Harness),
			Path:     "",
			Changed:  false,
			Message:  "skipped: " + err.Error(),
			NextStep: "",
			Snippet:  "",
			Error:    "",
		}}, nil
	case err != nil || upgraded:
		return appendUpgradeResult(nil, opts.Harness, result, err), err
	default:
		return nil, nil
	}
}

func upgradeNative(ctx context.Context, opts Options) (Result, error) {
	plan, advisor, err := installPlanForHarness(opts.Harness, opts.Binary)
	if err != nil {
		return Result{}, err
	}

	result, err := runNativeActions(ctx, opts, plan, func(handler actionHandler) (Result, error) {
		if plugin, ok := handler.(pluginDirectoryHandler); ok && plugin.plan.Registration != nil {
			return upgradeRegisteredPlugin(ctx, opts, plugin.plan)
		}

		return handler.install(ctx, opts, opts.Harness)
	})
	if err == nil && advisor != nil && result.NextStep == "" {
		result.NextStep = advisor.InstallNextStep(result.Changed, opts.DryRun)
	}

	return result, err
}

func upgradeRegisteredPlugin(
	ctx context.Context,
	opts Options,
	plan harness.PluginDirectoryInstallPlan,
) (Result, error) {
	registration := plan.Registration

	state, err := registration.Inspect(ctx, plan.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("inspect %s for upgrade: %w", registration.Label(), err)
	}

	switch {
	case state == harness.PluginRegistrationForeign:
		return Result{}, fmt.Errorf("%w: %s", errForeignFile, registration.Label())
	case state == harness.PluginRegistrationDisabled:
		return Result{
			Harness: string(
				opts.Harness,
			),
			Path:    plan.Dir,
			Changed: false,
			Snippet: "",
			Error:   "",
			Message: "registration disabled; preserved",
			NextStep: "enable " + registration.Label() + " through the harness plugin settings; " +
				"upgrade preserves disablement",
		}, nil
	case state == harness.PluginRegistrationMissing,
		state == harness.PluginRegistrationStale && plan.UpgradeRegisters:
		return installPluginDirectory(ctx, opts, opts.Harness, plan)
	}

	if !opts.DryRun {
		if err := registration.EnsureMutable(plan.Dir); err != nil {
			return Result{}, fmt.Errorf("check %s for upgrade: %w", registration.Label(), err)
		}
	}
	// Re-registering through Install can enable disabled plugins or grant new
	// permissions. Refresh their local source while preserving native policy.
	opts.preserveRegistration = true

	result, err := installPluginDirectory(ctx, opts, opts.Harness, plan)
	if result.Changed {
		result.NextStep = "restart the harness to load updated plugin code; registration and permissions preserved"
	}

	return result, err
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

func installedNative(ctx context.Context, id registry.Harness, binary string) (bool, error) {
	plan, _, err := installPlanForHarness(id, binary)
	if err != nil {
		return false, err
	}

	if len(planPaths(plan)) == 0 {
		return false, nil
	}

	result := IntegrationStatus{
		Harness:  id,
		Status:   ArtifactCurrent,
		Paths:    nil,
		Message:  "",
		NextStep: "",
	}
	if err := inspectIntegrationPlan(ctx, plan, &result); err != nil {
		return false, err
	}

	return result.Status == ArtifactCurrent || result.Status == ArtifactStale ||
		result.Status == ArtifactPartial ||
		result.Status == ArtifactDisabled, nil
}

func installedShim(id registry.Harness) (string, bool, error) {
	path := filepath.Join(registry.DefaultStateDir(), "shims", string(id))

	status, err := classifyShimArtifact(path, id, "")
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

	script := string(data)
	offset := 0

	for line := range strings.Lines(script) {
		if value, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "harness_bin="); ok {
			target, err := shlex.NewLexer(strings.NewReader(script[offset+len(line)-len(value):])).
				Next()
			if err != nil {
				return "", true, fmt.Errorf("parse installed shim target: %w", err)
			}

			if target != "" {
				return target, true, nil
			}
		}

		offset += len(line)
	}

	return "", true, fmt.Errorf("%w: cannot recover target from %s", errRecursiveShimTarget, path)
}

// Check for recoverable installs even when the plugin target is missing.
// The install path applies recovery (or previews it) along with the upgrade,
// so cleanup is not performed before the upgrade decides whether to write.
func pluginRecoveryPending(plan harness.InstallPlan) (bool, error) {
	pending := false

	for _, action := range plan.Actions {
		if plugin, ok := action.(harness.PluginDirectoryAction); ok {
			directory := newPluginDirectoryInstall(plugin.Plan, nil, nil)

			recovery, err := directory.inspectRecovery()
			if err != nil {
				return false, err
			}

			pending = pending || len(recovery.cleanup) != 0
		}
	}

	return pending, nil
}
