package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
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

// Inspect reports whether a harness integration or its shim fallback is current.
func Inspect(harnessID registry.Harness, binary string) (IntegrationStatus, error) {
	return InspectContext(context.Background(), harnessID, binary)
}

// InspectContext reports whether a harness integration is current, honoring
// cancellation while consulting native harness CLIs.
func InspectContext(ctx context.Context, harnessID registry.Harness, binary string) (IntegrationStatus, error) {
	if err := ctx.Err(); err != nil {
		return IntegrationStatus{}, fmt.Errorf("inspect integration context: %w", err)
	}
	plan, advisor, err := installPlanForHarness(harnessID, binary)
	if err != nil {
		return IntegrationStatus{}, err
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
	if advisor != nil {
		result.NextStep = advisor.StatusNextStep(nativeStatus == ArtifactCurrent, nativeStatus == ArtifactStale)
	}

	return result, nil
}

func installPlanForHarness(harnessID registry.Harness, binary string) (harnesspkg.InstallPlan, harnesspkg.InstallAdvisor, error) {
	adapter, ok := harnesscatalog.Find(harnessID)
	if !ok {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("%w: %q", errUnsupportedHarness, harnessID)
	}
	installer, ok := adapter.(harnesspkg.Installable)
	if !ok {
		return harnesspkg.InstallPlan{}, nil, fmt.Errorf("%w: %q", errUnsupportedHarness, harnessID)
	}

	advisor, _ := adapter.(harnesspkg.InstallAdvisor)
	return installer.InstallPlan(binary), advisor, nil
}

func inspectIntegrationPlan(ctx context.Context, plan harnesspkg.InstallPlan, result *IntegrationStatus) error {
	for _, action := range plan.Actions {
		statuses, err := inspectAction(ctx, action)
		if err != nil {
			return err
		}
		for _, item := range statuses {
			mergeInspectedArtifact(result, item)
			if result.Status == ArtifactForeign {
				return nil
			}
		}
	}

	return nil
}

func markDesiredIntegrationState(ctx context.Context, harnessID registry.Harness, binary string, result *IntegrationStatus) error {
	if result.Status != ArtifactCurrent {
		return nil
	}
	dryRun, err := RunContext(ctx, Options{
		Harness:      harnessID,
		Binary:       binary,
		TargetBinary: "",
		DryRun:       true,
		Force:        false,
		UseShim:      false,
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
	status, err := classifyShimArtifact(path)
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
		result.Status, result.Message = item.status, "stale artifact at "+item.path
	case ArtifactMissing:
		if result.Status == ArtifactCurrent {
			result.Status, result.Message = item.status, "missing artifact at "+item.path
		}
	case ArtifactCurrent:
	}
}

func inspectAction(ctx context.Context, action harnesspkg.InstallAction) ([]inspectedArtifact, error) {
	switch value := action.(type) {
	case harnesspkg.JSONCommandHooksAction:
		return inspectSharedFile(value.Plan.Path)
	case harnesspkg.CursorJSONHooksAction:
		return inspectSharedFile(value.Plan.Path)
	case harnesspkg.ManagedTextBlockAction:
		return inspectSharedFile(value.Plan.Path)
	case harnesspkg.RenderedFileAction:
		return inspectOwnedPath(value.Plan.Path)
	case harnesspkg.PluginDirectoryAction:
		return inspectPluginAction(ctx, value.Plan)
	default:
		return nil, nil
	}
}

func inspectPluginAction(ctx context.Context, plan harnesspkg.PluginDirectoryInstallPlan) ([]inspectedArtifact, error) {
	result, err := inspectOwnedPath(plan.Dir)
	if err != nil {
		return result, err
	}
	if plan.Registration != nil {
		registration, inspectErr := inspectRegistration(ctx, plan)
		if inspectErr != nil {
			return nil, inspectErr
		}
		return append(result, registration), nil
	}
	if plan.ImportManifest == nil {
		return result, nil
	}
	manifest, err := readImportManifest(plan.ImportManifest.Path)
	if err != nil {
		return nil, err
	}
	status := ArtifactMissing
	for _, entry := range manifest.Imports {
		if entry.Name != plan.ImportManifest.Name {
			continue
		}
		status = ArtifactCurrent
		if entry.Source != plan.ImportManifest.Source {
			status = ArtifactStale
		}
		for _, component := range plan.ImportManifest.Components {
			if !slices.Contains(entry.Components, component) {
				status = ArtifactStale
			}
		}
		break
	}
	return append(result, inspectedArtifact{path: plan.ImportManifest.Path, status: status}), nil
}

func inspectSharedFile(path string) ([]inspectedArtifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []inspectedArtifact{{path: path, status: ArtifactMissing}}, nil
		}
		return nil, fmt.Errorf("reading integration artifact %s: %w", path, err)
	}
	status := classifyArtifactContent(string(data))
	if status == ArtifactForeign {
		status = ArtifactMissing
	}
	return []inspectedArtifact{{path: path, status: status}}, nil
}

func inspectOwnedPath(path string) ([]inspectedArtifact, error) {
	status, err := ClassifyArtifact(path)
	if err != nil {
		return nil, err
	}
	return []inspectedArtifact{{path: path, status: status}}, nil
}

func planPaths(plan harnesspkg.InstallPlan) []string {
	paths := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		switch value := action.(type) {
		case harnesspkg.JSONCommandHooksAction:
			paths = append(paths, value.Plan.Path)
		case harnesspkg.CursorJSONHooksAction:
			paths = append(paths, value.Plan.Path)
		case harnesspkg.ManagedTextBlockAction:
			paths = append(paths, value.Plan.Path)
		case harnesspkg.RenderedFileAction:
			paths = append(paths, value.Plan.Path)
		case harnesspkg.PluginDirectoryAction:
			paths = append(paths, value.Plan.Dir)
			if value.Plan.ImportManifest != nil {
				paths = append(paths, value.Plan.ImportManifest.Path)
			}
		}
	}
	return paths
}
