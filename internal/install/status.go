package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"unicode/utf8"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

// IntegrationStatus describes the managed artifacts for one harness integration.
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

	if noInstallRequiredHarness(harnessID) {
		if action, ok := nativeCatalogAction(plan); ok {
			return IntegrationStatus{
				Harness:  harnessID,
				Status:   ArtifactCurrent,
				Paths:    []string{action.Label},
				Message:  "no setup required",
				NextStep: "",
			}, nil
		}
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

	result, err = mergeShimStatus(result, shimPath)
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
) (harnesspkg.InstallPlan, harnesspkg.InstallAdvisor, error) {
	adapter, ok := catalog.Find(harnessID)
	if !ok {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("%w: %q", errUnsupportedHarness, harnessID)
	}

	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("%w: %q", errUnsupportedHarness, harnessID)
	}

	if !utf8.ValidString(binary) {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("%w: %q", errBinaryEncoding, binary)
	}

	advisor, _ := adapter.(harnesspkg.InstallAdvisor)

	plan, err := installer.InstallPlan(binary)
	if err != nil {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("plan %s integration: %w", harnessID, err)
	}

	return plan, advisor, nil
}

func inspectIntegrationPlan(
	ctx context.Context,
	plan harnesspkg.InstallPlan,
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

func mergeShimStatus(result IntegrationStatus, path string) (IntegrationStatus, error) {
	status, err := classifyShimArtifact(path, result.Harness)
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
	plan harnesspkg.JSONCommandHookInstallPlan,
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
	plan harnesspkg.PluginDirectoryInstallPlan,
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

		return append(result, registration), nil
	}

	retired, err := inspectRetiredPlugins(plan, harnessID)
	if err != nil {
		return nil, err
	}

	result = append(result, retired...)
	if plan.ImportManifest == nil {
		return result, nil
	}

	artifact, err := inspectPluginImportManifest(plan.ImportManifest)
	if err != nil {
		return nil, err
	}

	return append(result, artifact), nil
}

// inspectRetiredPlugins reports managed plugins at former locations as stale
// so that an upgrade moves them.
func inspectRetiredPlugins(
	plan harnesspkg.PluginDirectoryInstallPlan,
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

func planPaths(plan harnesspkg.InstallPlan) []string {
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
	plan harnesspkg.PluginDirectoryInstallPlan,
	result []inspectedArtifact,
) ([]inspectedArtifact, error) {
	if result[0].status == ArtifactForeign {
		missingMarker, err := registeredPluginMissingMarker(ctx, plan)
		if err != nil {
			return nil, err
		}

		if missingMarker {
			result[0] = inspectedArtifact{
				path:   filepath.Join(plan.Dir, harnesspkg.ArtifactMarkerFile),
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

func inspectPluginImportManifest(
	plan *harnesspkg.ImportManifestInstallPlan,
) (inspectedArtifact, error) {
	manifest, err := readImportManifest(plan.Path)
	if err != nil {
		return inspectedArtifact{}, err
	}

	status := ArtifactMissing

	for _, entry := range manifest.Imports {
		if entry.name() != plan.Name {
			continue
		}

		status = ArtifactCurrent
		if entry.source() != plan.Source {
			status = ArtifactStale
		}

		for _, component := range plan.Components {
			if !slices.Contains(entry.components(), component) {
				status = ArtifactStale
			}
		}

		break
	}

	return inspectedArtifact{path: plan.Path, status: status}, nil
}
