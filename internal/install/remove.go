package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

// Remove deletes only artifacts owned by aht for one harness.
func Remove(options Options) (Result, error) {
	return RemoveContext(context.Background(), options)
}

// RemoveContext removes one integration while honoring caller cancellation.
func RemoveContext(ctx context.Context, options Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("remove integration context: %w", err)
	}
	adapter, ok := harnesscatalog.Find(options.Harness)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", errUnsupportedHarness, options.Harness)
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", errUnsupportedHarness, options.Harness)
	}
	if options.Binary == "" {
		options.Binary = defaultBinary
	}
	shimPath := filepath.Join(registry.DefaultStateDir(), "shims", string(options.Harness))
	shimStatus, err := ClassifyArtifact(shimPath)
	if err != nil {
		return Result{}, err
	}
	if shimStatus == ArtifactForeign {
		return Result{}, fmt.Errorf("%w: %s", errForeignFile, shimPath)
	}
	result, err := removeNativeIntegration(ctx, options, installer.InstallPlan(options.Binary))
	if err != nil {
		return result, err
	}
	shimChanged, err := removeOwnedShim(shimPath, options.DryRun, shimStatus)
	if err != nil {
		return Result{}, err
	}
	if shimChanged {
		if !result.Changed {
			result.Path = shimPath
		}
		result.Changed = true
		result.Message = removeResult(options.Harness, result.Path, true, options.DryRun).Message
	}
	return result, nil
}

func removeNativeIntegration(ctx context.Context, options Options, plan harnesspkg.InstallPlan) (Result, error) {
	for _, action := range plan.Actions {
		result, handled, err := removePlanAction(ctx, options, options.Harness, action)
		if handled {
			return result, err
		}
	}
	return Result{}, fmt.Errorf("%w: %q", errUnsupportedHarness, options.Harness)
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

func removePlanAction(ctx context.Context, options Options, harnessID registry.Harness, action harnesspkg.InstallAction) (Result, bool, error) {
	switch typed := action.(type) {
	case harnesspkg.JSONCommandHooksAction:
		result, err := removeJSONCommandHooks(options, harnessID, typed.Plan)
		return result, true, err
	case harnesspkg.CursorJSONHooksAction:
		result, err := removeCursorJSONHooks(options, harnessID, typed.Plan)
		return result, true, err
	case harnesspkg.ManagedTextBlockAction:
		result, err := removeTextBlock(options, harnessID, typed.Plan)
		return result, true, err
	case harnesspkg.RenderedFileAction:
		result, err := removeOwnedFiles(options, harnessID, []string{typed.Plan.Path}, typed.Plan.Path)
		return result, true, err
	case harnesspkg.RenderedFilesAction:
		paths := make([]string, 0, len(typed.Plan.Files))
		for _, file := range typed.Plan.Files {
			paths = append(paths, filepath.Join(typed.Plan.Dir, file.Name))
		}
		result, err := removeOwnedFiles(options, harnessID, paths, typed.Plan.Dir)
		return result, true, err
	case harnesspkg.PluginDirectoryAction:
		result, err := removePluginDirectory(ctx, options, harnessID, typed.Plan)
		return result, true, err
	case harnesspkg.ShimAction:
		var result Result
		return result, false, nil
	default:
		var result Result
		return result, false, nil
	}
}

func removeJSONCommandHooks(options Options, harnessID registry.Harness, plan harnesspkg.JSONCommandHookInstallPlan) (Result, error) {
	return removeJSONHooks(options, harnessID, plan.Path, func(config map[string]any) bool {
		isManaged := isManagedSourceHookCommand(managedSource(plan.Source, harnessID))
		changed := removeWrappedCommandHooks(config, plan, isManaged)
		if plan.HooksAtRoot {
			for _, spec := range plan.Hooks {
				changed = removeManagedJSONHookEvent(config, spec.Event, isManaged, removeManagedCommandHookGroups) || changed
			}
		}
		return changed
	})
}

func removeWrappedCommandHooks(config map[string]any, plan harnesspkg.JSONCommandHookInstallPlan, isManaged func(string) bool) bool {
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		return false
	}
	changed := false
	for _, spec := range plan.Hooks {
		changed = removeManagedJSONHookEvent(hooks, spec.Event, isManaged, removeManagedCommandHookGroups) || changed
	}
	if changed && len(hooks) == 0 {
		delete(config, "hooks")
	}
	return changed
}

func removeCursorJSONHooks(options Options, harnessID registry.Harness, plan harnesspkg.CursorJSONHookInstallPlan) (Result, error) {
	return removeJSONHooks(options, harnessID, plan.Path, func(config map[string]any) bool {
		hooks, ok := config["hooks"].(map[string]any)
		if !ok {
			return false
		}
		isManaged := isManagedSourceHookCommand(managedSource(plan.Source, harnessID))
		changed := false
		for _, spec := range plan.Hooks {
			changed = removeManagedJSONHookEvent(hooks, spec.Event, isManaged, removeManagedCursorHooks) || changed
		}
		return changed
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

func removeJSONHooks(options Options, harnessID registry.Harness, path string, apply func(map[string]any) bool) (Result, error) {
	config, err := readJSONObject(path)
	if err != nil {
		return Result{}, err
	}
	changed := apply(config)
	if changed && !options.DryRun {
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return Result{}, fmt.Errorf("encoding cleaned integration config: %w", err)
		}
		if err := writeFileAtomic(path, append(data, '\n'), "creating config directory", "writing cleaned integration config"); err != nil {
			return Result{}, err
		}
	}
	return removeResult(harnessID, path, changed, options.DryRun), nil
}

func removeTextBlock(options Options, harnessID registry.Harness, plan harnesspkg.ManagedTextBlockInstallPlan) (Result, error) {
	current, err := readTextFile(plan.Path)
	if err != nil {
		return Result{}, err
	}
	next := removeManagedTextBlock(current, plan.StartMarker, plan.EndMarker)
	changed := next != current
	if changed && !options.DryRun {
		if err := writeFileAtomic(plan.Path, []byte(next), "creating config directory", "writing cleaned config"); err != nil {
			return Result{}, err
		}
	}
	return removeResult(harnessID, plan.Path, changed, options.DryRun), nil
}

func removeOwnedFiles(options Options, harnessID registry.Harness, paths []string, resultPath string) (Result, error) {
	managed := make([]string, 0, len(paths))
	for _, path := range paths {
		status, err := classifyArtifactForHarness(path, harnessID)
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
		}
	}
	if !options.DryRun {
		for _, path := range managed {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Result{}, fmt.Errorf("removing managed integration %s: %w", path, err)
			}
		}
	}
	return removeResult(harnessID, resultPath, len(managed) > 0, options.DryRun), nil
}

//nolint:cyclop // removal handles both native registrations and import manifests
func removePluginDirectory(ctx context.Context, options Options, harnessID registry.Harness, plan harnesspkg.PluginDirectoryInstallPlan) (Result, error) {
	plugin := newPluginDirectoryInstall(plan, nil)
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
	obsoleteFiles, err := managedObsoleteFiles(plan.ObsoleteFiles)
	if err != nil {
		return Result{}, err
	}
	if plan.Registration != nil {
		return removeRegisteredPlugin(ctx, options, harnessID, plan, exists)
	}
	manifestChanged := false
	var manifest importManifest
	if plan.ImportManifest != nil {
		manifest, err = readImportManifest(plan.ImportManifest.Path)
		if err != nil {
			return Result{}, err
		}
		manifest, manifestChanged = removeImport(manifest, plan.ImportManifest.Name)
	}
	changed := exists || manifestChanged || len(obsoleteFiles) > 0
	if changed && !options.DryRun {
		if err := applyPluginRemoval(plan, exists, manifestChanged, manifest, obsoleteFiles); err != nil {
			return Result{}, err
		}
	}
	return removeResult(harnessID, plan.Dir, changed, options.DryRun), nil
}

func applyPluginRemoval(plan harnesspkg.PluginDirectoryInstallPlan, exists bool, manifestChanged bool, manifest importManifest, obsoleteFiles []string) error {
	return applyPluginRemovalWithWriter(plan, exists, manifestChanged, manifest, obsoleteFiles, writeImportManifest)
}

func applyPluginRemovalWithWriter(
	plan harnesspkg.PluginDirectoryInstallPlan,
	exists bool,
	manifestChanged bool,
	manifest importManifest,
	obsoleteFiles []string,
	writeManifest func(string, importManifest) error,
) error {
	return applyPluginRemovalWithFinalizer(plan, exists, manifestChanged, manifest, obsoleteFiles, writeManifest, nil)
}

func shouldUpdateManifest(plan harnesspkg.PluginDirectoryInstallPlan, manifestChanged bool) bool {
	return plan.ImportManifest != nil && manifestChanged
}

func preparePluginRemoval(
	plan harnesspkg.PluginDirectoryInstallPlan,
	finalizer func(parent string, backup string, backupExists bool) error,
) (func() error, func() error, error) {
	plugin := newPluginDirectoryInstall(plan, nil)
	parent := filepath.Dir(plan.Dir)
	backup, backupExists, err := plugin.backupCurrent(parent)
	if err != nil {
		return nil, nil, err
	}
	rollbackPlugin := func() error {
		if err := plugin.restoreBackup(backup, backupExists); err != nil {
			return err
		}
		return syncDir(parent)
	}
	finalizePlugin := func() error {
		if finalizer != nil {
			return finalizer(parent, backup, backupExists)
		}
		return plugin.commitReplacement(parent, backup, backupExists)
	}
	return rollbackPlugin, finalizePlugin, nil
}

func applyPluginRemovalWithFinalizer(
	plan harnesspkg.PluginDirectoryInstallPlan,
	exists bool,
	manifestChanged bool,
	manifest importManifest,
	obsoleteFiles []string,
	writeManifest func(string, importManifest) error,
	finalizer func(parent string, backup string, backupExists bool) error,
) error {
	var rollbackManifest func() error
	if shouldUpdateManifest(plan, manifestChanged) {
		var err error
		rollbackManifest, err = prepareImportManifestRollback(plan.ImportManifest.Path)
		if err != nil {
			return err
		}
	}

	var rollbackPlugin func() error
	var finalizePlugin func() error
	if exists {
		var err error
		rollbackPlugin, finalizePlugin, err = preparePluginRemoval(plan, finalizer)
		if err != nil {
			return err
		}
	}

	if shouldUpdateManifest(plan, manifestChanged) {
		if err := writeManifest(plan.ImportManifest.Path, manifest); err != nil {
			return rollbackPluginDirectoryAndManifest(rollbackPlugin, rollbackManifest, err)
		}
	}

	if err := removeManagedObsoleteFiles(obsoleteFiles); err != nil {
		return rollbackPluginDirectoryAndManifest(rollbackPlugin, rollbackManifest, err)
	}
	if finalizePlugin == nil {
		return nil
	}
	// Irreversible commit boundary: pre-commit operations succeeded.
	// Finalization failures do not restore prior manifest to avoid dangling imports.
	if err := finalizePlugin(); err != nil {
		return fmt.Errorf("finalizing plugin removal: %w", err)
	}
	return nil
}

func removeImport(manifest importManifest, name string) (importManifest, bool) {
	next := make([]importEntry, 0, len(manifest.Imports))
	removed := false
	for _, item := range manifest.Imports {
		if item.Name == name {
			removed = true
			continue
		}
		next = append(next, item)
	}
	manifest.Imports = next
	return manifest, removed
}

func removeResult(harnessID registry.Harness, path string, changed bool, dryRun bool) Result {
	message := "integration is not installed"
	if changed && dryRun {
		message = "would remove integration"
	} else if changed {
		message = "integration removed"
	}
	return Result{Harness: string(harnessID), Path: path, Changed: changed, Message: message, NextStep: "", Snippet: "", Error: ""}
}
