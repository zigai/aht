package install

import (
	"context"
	"errors"
	"fmt"
	"os"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func installRegisteredPlugin(
	ctx context.Context,
	options Options,
	harnessID registry.Harness,
	plan harnesspkg.PluginDirectoryInstallPlan,
	plugin pluginDirectoryInstall,
	pluginChanged bool,
) (Result, error) {
	registration := plan.Registration
	state, err := registration.Inspect(ctx, plan.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("inspecting %s registration: %w", registration.ID(), err)
	}
	if state == harnesspkg.PluginRegistrationForeign && !options.Force {
		return Result{}, fmt.Errorf("%w: %s; pass --force to replace it", errForeignFile, registration.Label())
	}

	changed := pluginChanged || state != harnesspkg.PluginRegistrationCurrent
	label := installLabel(plan.Label, harnessID, "plugin")
	result := Result{Harness: string(harnessID), Path: plan.Dir, Changed: changed, Message: installMessage(label, changed, options.DryRun), NextStep: "", Snippet: plugin.snippet(), Error: ""}
	if !changed || options.DryRun {
		return result, nil
	}
	if err := applyRegisteredPlugin(ctx, registration, state, plan.Dir, plugin, pluginChanged); err != nil {
		return Result{}, err
	}
	return result, nil
}

func applyRegisteredPlugin(
	ctx context.Context,
	registration harnesspkg.PluginRegistration,
	state harnesspkg.PluginRegistrationState,
	pluginDir string,
	plugin pluginDirectoryInstall,
	pluginChanged bool,
) error {
	if err := registration.EnsureMutable(pluginDir); err != nil {
		return fmt.Errorf("checking whether %s registration is mutable: %w", registration.ID(), err)
	}

	var rollback, commit func() error
	if pluginChanged {
		var err error
		rollback, commit, err = plugin.installStaged()
		if err != nil {
			return err
		}
	}
	fail := func(cause error) error {
		if cleanupErr := registration.CleanupFailedInstall(ctx, state, pluginDir); cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("cleaning up %s registration: %w", registration.ID(), cleanupErr))
		}
		return rollbackPluginDirectory(rollback, cause)
	}
	if err := registration.Install(ctx, pluginDir); err != nil {
		return fail(fmt.Errorf("installing %s registration: %w", registration.ID(), err))
	}
	if commit == nil {
		return nil
	}
	return commit()
}

func removeRegisteredPlugin(
	ctx context.Context,
	options Options,
	harnessID registry.Harness,
	plan harnesspkg.PluginDirectoryInstallPlan,
	exists bool,
) (Result, error) {
	registration := plan.Registration
	state, err := registration.Inspect(ctx, plan.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("inspecting %s registration before removal: %w", registration.ID(), err)
	}
	if state == harnesspkg.PluginRegistrationForeign {
		return Result{}, fmt.Errorf("%w: %s", errForeignFile, registration.Label())
	}
	registered := state != harnesspkg.PluginRegistrationMissing
	changed := exists || registered
	if !changed || options.DryRun {
		return removeResult(harnessID, plan.Dir, changed, options.DryRun), nil
	}
	if err := registration.EnsureMutable(plan.Dir); err != nil {
		return Result{}, fmt.Errorf("checking whether %s registration is mutable: %w", registration.ID(), err)
	}
	if registered {
		if err := registration.Remove(ctx, plan.Dir); err != nil {
			return Result{}, fmt.Errorf("removing %s registration: %w", registration.ID(), err)
		}
	}
	if exists {
		if err := os.RemoveAll(plan.Dir); err != nil {
			return Result{}, fmt.Errorf("removing managed plugin %s: %w", plan.Dir, err)
		}
	}
	return removeResult(harnessID, plan.Dir, true, false), nil
}

func inspectRegistration(ctx context.Context, plan harnesspkg.PluginDirectoryInstallPlan) (inspectedArtifact, error) {
	registration := plan.Registration
	state, err := registration.Inspect(ctx, plan.Dir)
	if err != nil {
		return inspectedArtifact{}, fmt.Errorf("inspecting %s registration status: %w", registration.ID(), err)
	}
	status := ArtifactMissing
	switch state {
	case harnesspkg.PluginRegistrationCurrent:
		status = ArtifactCurrent
	case harnesspkg.PluginRegistrationStale:
		status = ArtifactStale
	case harnesspkg.PluginRegistrationForeign:
		status = ArtifactForeign
	case harnesspkg.PluginRegistrationMissing:
	}
	return inspectedArtifact{path: registration.Label(), status: status}, nil
}
