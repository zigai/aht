package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

// Remove deletes aht-owned artifacts, including standalone native hooks, while
// honoring caller cancellation.
func Remove(ctx context.Context, opts Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("remove integration context: %w", err)
	}

	if opts.Binary == "" {
		opts.Binary = defaultBinary
	}

	plan, _, err := installPlanForHarness(opts.Harness, opts.Binary)
	if err != nil {
		return Result{}, err
	}

	if action, native := plan.NativeCatalog(); native {
		return noInstallRequiredResult(opts.Harness, action.Label), nil
	}

	shimPath := filepath.Join(registry.DefaultStateDir(), "shims", string(opts.Harness))

	shimStatus, err := classifyShimArtifact(shimPath, opts.Harness, "")
	if err != nil {
		return Result{}, err
	}

	if shimStatus == ArtifactForeign {
		return Result{}, fmt.Errorf("%w: %s", errForeignFile, shimPath)
	}

	result, err := removeNativeIntegration(ctx, opts, plan)
	if err != nil {
		return result, err
	}

	shimChanged, err := removeOwnedShim(shimPath, opts.DryRun, shimStatus)
	if err != nil {
		return Result{}, err
	}

	if shimChanged {
		if !result.Changed {
			result.Path = shimPath
		}

		result.Changed = true
		result.Message = removeResult(opts.Harness, result.Path, true, opts).Message
	}

	return result, nil
}

func removeNativeIntegration(
	ctx context.Context,
	opts Options,
	plan harness.InstallPlan,
) (Result, error) {
	return runNativeActions(ctx, opts, plan, func(handler actionHandler) (Result, error) {
		return handler.remove(ctx, opts, opts.Harness)
	})
}

func removeOwnedShim(path string, dryRun bool, status ArtifactStatus) (bool, error) {
	if status == ArtifactMissing {
		return false, nil
	}

	if dryRun {
		return true, nil
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("removing managed shim %s: %w", path, err)
	}

	return true, nil
}

func removeJSONCommandHooks(
	opts Options,
	harnessID registry.Harness,
	plan harness.JSONCommandHookInstallPlan,
) (Result, error) {
	return removeJSONHooks(opts, harnessID, plan.Path, func(harnessConfig map[string]any) bool {
		isManaged := isManagedSourceHookCommand(
			managedSource(plan.Source, harnessID),
			[]map[string]any{harnessConfig},
		)

		changed := removeWrappedCommandHooks(harnessConfig, isManaged)
		if plan.HooksKey == "" {
			changed = removeManagedCommandHookEvents(harnessConfig, isManaged) || changed
		}

		return changed
	})
}

func removeWrappedCommandHooks(harnessConfig map[string]any, isManaged func(string) bool) bool {
	hooks, ok := harnessConfig["hooks"].(map[string]any)
	if !ok {
		return false
	}

	changed := removeManagedCommandHookEvents(hooks, isManaged)
	if changed && len(hooks) == 0 {
		delete(harnessConfig, "hooks")
	}

	return changed
}

// removeManagedCommandHookEvents removes managed commands from every event,
// including events that earlier integration versions installed.
func removeManagedCommandHookEvents(hooks map[string]any, isManaged func(string) bool) bool {
	changed := false
	for event := range hooks {
		changed = removeManagedJSONHookEvent(
			hooks,
			event,
			isManaged,
			removeManagedCommandHookGroups,
		) ||
			changed
	}

	return changed
}

func removeJSONMerge(
	opts Options,
	harnessID registry.Harness,
	plan harness.JSONMergeInstallPlan,
) (Result, error) {
	return removeJSONHooks(opts, harnessID, plan.Path, func(config map[string]any) bool {
		return plan.Remove(
			config,
			isManagedSourceHookCommand(plan.Source, []map[string]any{config}),
		)
	})
}

func removeManagedJSONHookEvent(
	hooks map[string]any,
	event string,
	isManaged func(string) bool,
	removeGroups func([]any, func(string) bool) ([]any, bool),
) bool {
	groups, ok := hooks[event].([]any)
	if !ok {
		return false
	}

	cleaned, removed := removeGroups(groups, isManaged)
	if !removed {
		return false
	}

	if len(cleaned) == 0 {
		delete(hooks, event)
		return true
	}

	hooks[event] = cleaned

	return true
}

func removeJSONHooks(
	opts Options,
	harnessID registry.Harness,
	path string,
	apply func(map[string]any) bool,
) (Result, error) {
	harnessConfig, err := readJSONObject(path)
	if err != nil {
		return Result{}, err
	}

	changed := apply(harnessConfig)
	if changed && !opts.DryRun {
		data, err := json.MarshalIndent(harnessConfig, "", "  ")
		if err != nil {
			return Result{}, fmt.Errorf("encoding cleaned integration config: %w", err)
		}

		if err := writeFileAtomic(
			path,
			append(data, '\n'),
			"creating config directory",
			"writing cleaned integration config",
		); err != nil {
			return Result{}, err
		}
	}

	return removeResult(harnessID, path, changed, opts), nil
}

func removeTextBlock(
	opts Options,
	harnessID registry.Harness,
	plan harness.ManagedTextBlockInstallPlan,
) (Result, error) {
	current, err := readTextFile(plan.Path)
	if err != nil {
		return Result{}, err
	}

	next, err := removeManagedTextBlock(current, plan.StartMarker, plan.EndMarker)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", plan.Path, err)
	}

	changed := next != current
	if changed && !opts.DryRun {
		if err := writeFileAtomic(
			plan.Path,
			[]byte(next),
			"creating config directory",
			"writing cleaned config",
		); err != nil {
			return Result{}, err
		}
	}

	return removeResult(harnessID, plan.Path, changed, opts), nil
}

func removeOwnedFiles(
	opts Options,
	harnessID registry.Harness,
	paths []string,
	resultPath string,
) (Result, error) {
	managed := make([]string, 0, len(paths))
	for _, path := range paths {
		status, err := classifyArtifact(path, harnessID)
		if err != nil {
			return Result{}, err
		}

		switch status {
		case ArtifactMissing:
			continue
		case ArtifactForeign:
			return Result{}, fmt.Errorf("%w: %s", errForeignFile, path)
		case ArtifactCurrent, ArtifactStale:
			managed = append(managed, path)
		case ArtifactPartial, ArtifactDisabled:
			continue
		}
	}

	if !opts.DryRun {
		for _, path := range managed {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Result{}, fmt.Errorf("removing managed integration %s: %w", path, err)
			}
		}
	}

	return removeResult(harnessID, resultPath, len(managed) > 0, opts), nil
}

func removePluginDirectoryWithRetired(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
	plan harness.PluginDirectoryInstallPlan,
) (Result, error) {
	result, err := removePluginDirectory(ctx, opts, harnessID, plan)
	if err != nil {
		return Result{}, err
	}

	retiredChanged, err := removeRetiredPlugins(ctx, opts, harnessID, plan)
	if err != nil {
		return Result{}, err
	}

	if retiredChanged && !result.Changed {
		return removeResult(harnessID, plan.Dir, true, opts), nil
	}

	return result, nil
}

// removeRetiredPlugins removes aht-managed plugins at former locations of the
// plugin. A directory at such a location that aht does not own is left alone.
func removeRetiredPlugins(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
	plan harness.PluginDirectoryInstallPlan,
) (bool, error) {
	changed := false

	for _, retired := range plan.Retired {
		managed, err := newPluginDirectoryInstall(retired, nil, nil).managed()
		if err != nil {
			return false, err
		}

		if !managed {
			continue
		}

		result, err := removePluginDirectory(ctx, opts, harnessID, retired)
		if err != nil {
			return false, err
		}

		changed = changed || result.Changed
	}

	return changed, nil
}

func removePluginDirectory(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
	plan harness.PluginDirectoryInstallPlan,
) (Result, error) {
	plugin := newPluginDirectoryInstall(plan, nil, nil)

	managed, err := plugin.managed()
	if err != nil {
		return Result{}, err
	}

	_, statErr := os.Stat(plan.Dir)

	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Result{}, fmt.Errorf("checking plugin directory: %w", statErr)
	}

	if exists && !managed {
		return Result{}, fmt.Errorf("%w: %s", errForeignFile, plan.Dir)
	}

	if plan.Registration != nil {
		return removeRegisteredPlugin(ctx, opts, harnessID, plan, exists)
	}

	if exists && !opts.DryRun {
		if err := applyPluginRemoval(plan, true, nil, nil); err != nil {
			return Result{}, err
		}
	}

	return removeResult(harnessID, plan.Dir, exists, opts), nil
}

func preparePluginRemoval(
	plan harness.PluginDirectoryInstallPlan,
	finalizer func(parent string, backup string, backupExists bool) error,
) (func() error, func() error, error) {
	plugin := newPluginDirectoryInstall(plan, nil, nil)
	parent := filepath.Dir(plan.Dir)

	backup, backupExists, err := plugin.backupCurrent(parent)
	if err != nil {
		return nil, nil, err
	}

	rollbackPlugin := func() error {
		return plugin.restoreBackup(backup, backupExists)
	}
	finalizePlugin := func() error {
		if finalizer != nil {
			return finalizer(parent, backup, backupExists)
		}

		return plugin.commitReplacement(parent, backup, backupExists)
	}

	return rollbackPlugin, finalizePlugin, nil
}

func applyPluginRemoval(
	plan harness.PluginDirectoryInstallPlan,
	exists bool,
	unregister func() error,
	finalizer func(parent string, backup string, backupExists bool) error,
) error {
	var rollback, finalize func() error

	if exists {
		var err error

		rollback, finalize, err = preparePluginRemoval(plan, finalizer)
		if err != nil {
			return err
		}
	}

	if unregister != nil {
		if err := unregister(); err != nil {
			return rollbackPluginDirectory(rollback, err)
		}
	}

	if finalize == nil {
		return nil
	}

	if err := finalize(); err != nil {
		return fmt.Errorf("finalizing plugin removal: %w", err)
	}

	return nil
}

func removeResult(harnessID registry.Harness, path string, changed bool, opts Options) Result {
	message := "integration is not installed"
	if changed && opts.DryRun {
		message = "would remove integration"
	} else if changed {
		message = "integration removed"
	}

	return Result{
		Harness:  string(harnessID),
		Path:     path,
		Changed:  changed,
		Message:  message,
		NextStep: "",
		Snippet:  "",
		Error:    "",
	}
}
