package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

type IntegrationStatus struct {
	Harness  registry.Harness `json:"harness"`
	Status   ArtifactStatus   `json:"status"`
	Paths    []string         `json:"paths"`
	Message  string           `json:"message"`
	NextStep string           `json:"next_step,omitempty"`
}

type inspectedArtifact struct {
	path   string
	status ArtifactStatus
}

// Inspect reports whether a harness integration or its shim fallback is
// current, honoring cancellation while consulting native harness CLIs.
func Inspect(
	ctx context.Context,
	harnessID registry.Harness,
	binary string,
) (IntegrationStatus, error) {
	if err := ctx.Err(); err != nil {
		return IntegrationStatus{}, fmt.Errorf("inspect integration context: %w", err)
	}

	plan, advisor, err := installPlanForHarness(harnessID, binary)
	if err != nil {
		return IntegrationStatus{}, err
	}

	if action, native := plan.NativeCatalog(); native {
		return IntegrationStatus{
			Harness:  harnessID,
			Status:   ArtifactCurrent,
			Paths:    []string{action.Label},
			Message:  "no setup required",
			NextStep: "",
		}, nil
	}

	shimPath := filepath.Join(registry.DefaultStateDir(), "shims", string(harnessID))
	paths := append(planPaths(plan), shimPath)

	result := IntegrationStatus{
		Harness:  harnessID,
		Status:   ArtifactCurrent,
		Paths:    paths,
		Message:  "current",
		NextStep: "",
	}
	if err := inspectIntegrationPlan(ctx, plan, &result); err != nil {
		return IntegrationStatus{}, err
	}

	if err := markDesiredIntegrationState(ctx, harnessID, binary, &result); err != nil {
		return IntegrationStatus{}, err
	}

	nativeStatus := result.Status

	if binary == "" {
		binary = defaultBinary
	}

	result, err = mergeShimStatus(result, shimPath, binary)
	if err != nil {
		return IntegrationStatus{}, err
	}

	if advisor != nil && result.NextStep == "" {
		result.NextStep = advisor.StatusNextStep(
			nativeStatus == ArtifactCurrent,
			nativeStatus == ArtifactStale,
		)
	}

	return result, nil
}

func installPlanForHarness(
	harnessID registry.Harness,
	binary string,
) (harness.InstallPlan, harness.InstallAdvisor, error) {
	adapter, ok := adapters.Find(harnessID)
	if !ok {
		return harness.InstallPlan{}, nil, fmt.Errorf("%w: %q", errUnsupportedHarness, harnessID)
	}

	if !utf8.ValidString(binary) {
		return harness.InstallPlan{}, nil, fmt.Errorf("%w: %q", errBinaryEncoding, binary)
	}

	advisor, _ := adapter.(harness.InstallAdvisor)

	plan, err := adapter.InstallPlan(binary)
	if err != nil {
		return harness.InstallPlan{}, nil, fmt.Errorf("plan %s integration: %w", harnessID, err)
	}

	return plan, advisor, nil
}

func inspectIntegrationPlan(
	ctx context.Context,
	plan harness.InstallPlan,
	result *IntegrationStatus,
) error {
	var missing, managed bool

	for _, action := range plan.Actions {
		handler, ok := handlerFor(action)
		if !ok {
			continue
		}

		statuses, err := handler.inspect(ctx, result.Harness)
		if err != nil {
			return err
		}

		for _, item := range statuses {
			missing = missing || item.status == ArtifactMissing
			managed = managed || managedArtifact(item.status)
			mergeInspectedArtifact(result, item)

			if result.Status == ArtifactForeign {
				return nil
			}
		}
	}

	if missing && managed && result.Status != ArtifactDisabled {
		result.Status = ArtifactPartial
		result.Message = "managed integration is incomplete; install to repair missing artifacts"
	}

	return nil
}

func managedArtifact(status ArtifactStatus) bool {
	return status == ArtifactCurrent || status == ArtifactStale || status == ArtifactDisabled
}

func markDesiredIntegrationState(
	ctx context.Context,
	harnessID registry.Harness,
	binary string,
	result *IntegrationStatus,
) error {
	if result.Status != ArtifactCurrent {
		return nil
	}

	dryRun, err := Run(ctx, Options{
		Harness: harnessID,
		Binary:  binary,
		DryRun:  true,
	})
	if err != nil {
		return fmt.Errorf("checking desired integration state: %w", err)
	}

	if dryRun.Changed {
		result.Status = ArtifactStale
		result.Message = "managed integration differs from the desired configuration"
	}

	return nil
}

func mergeShimStatus(
	result IntegrationStatus,
	path string,
	binary string,
) (IntegrationStatus, error) {
	status, err := classifyShimArtifact(path, result.Harness, binary)
	if err != nil {
		return IntegrationStatus{}, err
	}

	if status == ArtifactForeign {
		result.Status, result.Message = status, "foreign content at "+path
		return result, nil
	}

	if result.Status == ArtifactMissing && (status == ArtifactCurrent || status == ArtifactStale) {
		result.Status = status
		result.Message = "shim fallback is " + string(status)
	} else if result.Status == ArtifactCurrent && status == ArtifactStale {
		result.Status, result.Message = status, "stale shim fallback at "+path
	}

	return result, nil
}

func mergeInspectedArtifact(result *IntegrationStatus, item inspectedArtifact) {
	switch item.status {
	case ArtifactForeign:
		result.Status, result.Message = item.status, "foreign content at "+item.path
	case ArtifactStale:
		if result.Status != ArtifactDisabled {
			result.Status, result.Message = item.status, "stale artifact at "+item.path
		}
	case ArtifactMissing:
		if result.Status == ArtifactCurrent {
			result.Status, result.Message = item.status, "missing artifact at "+item.path
		}
	case ArtifactDisabled:
		result.Status, result.Message = item.status, "disabled registration: "+item.path
		result.NextStep = "enable " + item.path + " through the harness plugin settings; upgrade preserves disablement"
	case ArtifactPartial, ArtifactCurrent:
	}
}

func inspectCommandHooks(
	ctx context.Context,
	plan harness.JSONCommandHookInstallPlan,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	statuses, err := inspectSharedFile(plan.Path, harnessID)
	if err != nil || plan.Trust == nil || statuses[0].status == ArtifactMissing {
		return statuses, err
	}

	trusted, err := plan.Trust.Inspect(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s hook trust: %w", plan.Label, err)
	}

	if !trusted {
		statuses = append(
			statuses,
			inspectedArtifact{path: plan.Label + " trust", status: ArtifactStale},
		)
	}

	return statuses, nil
}

func inspectPluginAction(
	ctx context.Context,
	plan harness.PluginDirectoryInstallPlan,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	result, err := inspectOwnedPath(plan.Dir, harnessID)
	if err != nil {
		return result, err
	}

	result, err = inspectPluginFiles(ctx, plan, result)
	if err != nil {
		return nil, err
	}

	if plan.Registration != nil {
		registration, inspectErr := inspectRegistration(ctx, plan)
		if inspectErr != nil {
			return nil, inspectErr
		}

		result = append(result, registration)
	}

	retired, err := inspectRetiredPlugins(plan, harnessID)
	if err != nil {
		return nil, err
	}

	return append(result, retired...), nil
}

// inspectRetiredPlugins reports managed plugins at former locations as stale
// so that an upgrade moves them.
func inspectRetiredPlugins(
	plan harness.PluginDirectoryInstallPlan,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	var stale []inspectedArtifact

	for _, retired := range plan.Retired {
		status, err := classifyArtifact(retired.Dir, harnessID)
		if err != nil {
			return nil, err
		}

		if status == ArtifactCurrent || status == ArtifactStale {
			stale = append(stale, inspectedArtifact{path: retired.Dir, status: ArtifactStale})
		}
	}

	return stale, nil
}

func inspectSharedFile(path string, harnessID registry.Harness) ([]inspectedArtifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []inspectedArtifact{{path: path, status: ArtifactMissing}}, nil
		}

		return nil, fmt.Errorf("reading integration artifact %s: %w", path, err)
	}

	status := classifyArtifactContent(string(data), harnessID)
	if status == ArtifactForeign {
		status = ArtifactMissing
	}

	return []inspectedArtifact{{path: path, status: status}}, nil
}

func inspectOwnedPath(path string, harnessID registry.Harness) ([]inspectedArtifact, error) {
	status, err := classifyArtifact(path, harnessID)
	if err != nil {
		return nil, err
	}

	return []inspectedArtifact{{path: path, status: status}}, nil
}

func planPaths(plan harness.InstallPlan) []string {
	paths := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		if handler, ok := handlerFor(action); ok {
			paths = append(paths, handler.paths()...)
		}
	}

	return paths
}

func inspectPluginFiles(
	ctx context.Context,
	plan harness.PluginDirectoryInstallPlan,
	result []inspectedArtifact,
) ([]inspectedArtifact, error) {
	if result[0].status == ArtifactForeign {
		missingMarker, err := registeredPluginMissingMarker(ctx, plan, plan.Dir)
		if err != nil {
			return nil, err
		}

		if missingMarker {
			result[0] = inspectedArtifact{
				path:   filepath.Join(plan.Dir, harness.ArtifactMarkerFile),
				status: ArtifactMissing,
			}
		}
	}

	if result[0].status == ArtifactCurrent || result[0].status == ArtifactStale {
		for _, file := range plan.Files {
			path := filepath.Join(plan.Dir, file.Name)
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				result = append(result, inspectedArtifact{path: path, status: ArtifactMissing})
			} else if err != nil {
				return nil, fmt.Errorf("checking required plugin file %s: %w", path, err)
			}
		}
	}

	return result, nil
}
