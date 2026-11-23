package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

type actionHandler interface {
	install(ctx context.Context, opts Options, harnessID registry.Harness) (Result, error)
	remove(ctx context.Context, opts Options, harnessID registry.Harness) (Result, error)
	inspect(ctx context.Context, harnessID registry.Harness) ([]inspectedArtifact, error)
	paths() []string
}

type (
	jsonCommandHooksHandler struct {
		plan harness.JSONCommandHookInstallPlan
	}
	jsonMergeHandler struct {
		plan harness.JSONMergeInstallPlan
	}
	managedTextBlockHandler struct {
		plan harness.ManagedTextBlockInstallPlan
	}
	renderedFileHandler struct {
		plan harness.RenderedFileInstallPlan
	}
	pluginDirectoryHandler struct {
		plan harness.PluginDirectoryInstallPlan
	}
	nativeCatalogHandler struct {
		action harness.NativeCatalogAction
	}
)

func runNativeActions(
	ctx context.Context,
	opts Options,
	plan harness.InstallPlan,
	run func(actionHandler) (Result, error),
) (Result, error) {
	var (
		result   Result
		failures []error
	)

	found := false

	for _, action := range plan.Actions {
		handler, ok := handlerFor(action)
		if !ok {
			continue
		}

		found = true

		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		item, err := run(handler)
		mergeActionResult(&result, item)

		if err != nil {
			failures = append(failures, err)
		}
	}

	if !found {
		return Result{}, fmt.Errorf("%w: %q", errUnsupportedHarness, opts.Harness)
	}

	return result, errors.Join(failures...)
}

func mergeActionResult(result *Result, item Result) {
	if result.Harness == "" {
		result.Harness = item.Harness
	}

	if result.Path == "" {
		result.Path = item.Path
	}

	result.Changed = result.Changed || item.Changed
	result.Message = joinActionText(result.Message, item.Message)
	result.NextStep = joinActionText(result.NextStep, item.NextStep)
	result.Snippet = joinActionText(result.Snippet, item.Snippet)
	result.Error = joinActionText(result.Error, item.Error)
}

func joinActionText(first string, next string) string {
	if first == "" {
		return next
	}

	if next == "" || first == next {
		return first
	}

	return first + "\n" + next
}

func handlerFor(action harness.InstallAction) (actionHandler, bool) {
	switch typed := action.(type) {
	case harness.JSONCommandHookAction:
		return jsonCommandHooksHandler{plan: typed.Plan}, true
	case harness.JSONMergeAction:
		return jsonMergeHandler{plan: typed.Plan}, true
	case harness.ManagedTextBlockAction:
		return managedTextBlockHandler{plan: typed.Plan}, true
	case harness.RenderedFileAction:
		return renderedFileHandler{plan: typed.Plan}, true
	case harness.PluginDirectoryAction:
		return pluginDirectoryHandler{plan: typed.Plan}, true
	case harness.NativeCatalogAction:
		return nativeCatalogHandler{action: typed}, true
	default:
		return nil, false
	}
}

func (handler jsonCommandHooksHandler) install(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return installTrustedJSONCommandHooks(ctx, opts, harnessID, handler.plan)
}

func (handler jsonCommandHooksHandler) remove(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return removeJSONCommandHooks(opts, harnessID, handler.plan)
}

func (handler jsonCommandHooksHandler) inspect(
	ctx context.Context,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectCommandHooks(ctx, handler.plan, harnessID)
}

func (handler jsonCommandHooksHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler jsonMergeHandler) install(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return installJSONMerge(opts, harnessID, handler.plan)
}

func (handler jsonMergeHandler) remove(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return removeJSONMerge(opts, harnessID, handler.plan)
}

func (handler jsonMergeHandler) inspect(
	_ context.Context,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectSharedFile(handler.plan.Path, harnessID)
}

func (handler jsonMergeHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler managedTextBlockHandler) install(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return installManagedTextBlock(opts, harnessID, handler.plan)
}

func (handler managedTextBlockHandler) remove(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return removeTextBlock(opts, harnessID, handler.plan)
}

func (handler managedTextBlockHandler) inspect(
	_ context.Context,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectSharedFile(handler.plan.Path, harnessID)
}

func (handler managedTextBlockHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler renderedFileHandler) install(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return installRenderedPlan(opts, harnessID, handler.plan)
}

func (handler renderedFileHandler) remove(
	_ context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return removeOwnedFiles(opts, harnessID, []string{handler.plan.Path}, handler.plan.Path)
}

func (handler renderedFileHandler) inspect(
	_ context.Context,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectOwnedPath(handler.plan.Path, harnessID)
}

func (handler renderedFileHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler pluginDirectoryHandler) install(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return installPluginDirectory(ctx, opts, harnessID, handler.plan)
}

func (handler pluginDirectoryHandler) remove(
	ctx context.Context,
	opts Options,
	harnessID registry.Harness,
) (Result, error) {
	return removePluginDirectoryWithRetired(ctx, opts, harnessID, handler.plan)
}

func (handler pluginDirectoryHandler) inspect(
	ctx context.Context,
	harnessID registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectPluginAction(ctx, handler.plan, harnessID)
}

func (handler pluginDirectoryHandler) paths() []string {
	paths := []string{handler.plan.Dir}
	if registration := handler.plan.Registration; registration != nil &&
		filepath.IsAbs(registration.Label()) {
		paths = append(paths, registration.Label())
	}

	return paths
}

func (handler nativeCatalogHandler) install(
	_ context.Context,
	_ Options,
	harnessID registry.Harness,
) (Result, error) {
	return noInstallRequiredResult(harnessID, handler.action.Label), nil
}

func (handler nativeCatalogHandler) remove(
	_ context.Context,
	_ Options,
	harnessID registry.Harness,
) (Result, error) {
	return noInstallRequiredResult(harnessID, handler.action.Label), nil
}

func (handler nativeCatalogHandler) inspect(
	context.Context,
	registry.Harness,
) ([]inspectedArtifact, error) {
	return []inspectedArtifact{{path: handler.action.Label, status: ArtifactCurrent}}, nil
}

func (nativeCatalogHandler) paths() []string {
	return nil
}
