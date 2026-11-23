package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

type actionHandler interface {
	install(ctx context.Context, opts Options, harness registry.Harness) (Result, error)
	remove(ctx context.Context, opts Options, harness registry.Harness) (Result, error)
	inspect(ctx context.Context, harness registry.Harness) ([]inspectedArtifact, error)
	paths() []string
}

type (
	jsonCommandHooksHandler struct {
		plan harnesspkg.JSONCommandHookInstallPlan
	}
	jsonMergeHandler struct {
		plan harnesspkg.JSONMergeInstallPlan
	}
	managedTextBlockHandler struct {
		plan harnesspkg.ManagedTextBlockInstallPlan
	}
	renderedFileHandler struct {
		plan harnesspkg.RenderedFileInstallPlan
	}
	pluginDirectoryHandler struct {
		plan harnesspkg.PluginDirectoryInstallPlan
	}
	nativeCatalogHandler struct {
		action harnesspkg.NativeCatalogAction
	}
)

func runNativeActions(
	ctx context.Context,
	opts Options,
	plan harnesspkg.InstallPlan,
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

func handlerFor(action harnesspkg.InstallAction) (actionHandler, bool) {
	switch typed := action.(type) {
	case harnesspkg.JSONCommandHooksAction:
		return jsonCommandHooksHandler{plan: typed.Plan}, true
	case harnesspkg.JSONMergeAction:
		return jsonMergeHandler{plan: typed.Plan}, true
	case harnesspkg.ManagedTextBlockAction:
		return managedTextBlockHandler{plan: typed.Plan}, true
	case harnesspkg.RenderedFileAction:
		return renderedFileHandler{plan: typed.Plan}, true
	case harnesspkg.PluginDirectoryAction:
		return pluginDirectoryHandler{plan: typed.Plan}, true
	case harnesspkg.NativeCatalogAction:
		return nativeCatalogHandler{action: typed}, true
	default:
		return nil, false
	}
}

func (handler jsonCommandHooksHandler) install(
	ctx context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return installTrustedJSONCommandHooks(ctx, opts, harness, handler.plan)
}

func (handler jsonCommandHooksHandler) remove(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return removeJSONCommandHooks(opts, harness, handler.plan)
}

func (handler jsonCommandHooksHandler) inspect(
	ctx context.Context,
	harness registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectCommandHooks(ctx, handler.plan, harness)
}

func (handler jsonCommandHooksHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler jsonMergeHandler) install(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return installJSONMerge(opts, harness, handler.plan)
}

func (handler jsonMergeHandler) remove(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return removeJSONMerge(opts, harness, handler.plan)
}

func (handler jsonMergeHandler) inspect(
	_ context.Context,
	harness registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectSharedFile(handler.plan.Path, harness)
}

func (handler jsonMergeHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler managedTextBlockHandler) install(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return installManagedTextBlock(opts, harness, handler.plan)
}

func (handler managedTextBlockHandler) remove(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return removeTextBlock(opts, harness, handler.plan)
}

func (handler managedTextBlockHandler) inspect(
	_ context.Context,
	harness registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectSharedFile(handler.plan.Path, harness)
}

func (handler managedTextBlockHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler renderedFileHandler) install(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return installRenderedPlan(opts, harness, handler.plan)
}

func (handler renderedFileHandler) remove(
	_ context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return removeOwnedFiles(opts, harness, []string{handler.plan.Path}, handler.plan.Path)
}

func (handler renderedFileHandler) inspect(
	_ context.Context,
	harness registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectOwnedPath(handler.plan.Path, harness)
}

func (handler renderedFileHandler) paths() []string {
	return []string{handler.plan.Path}
}

func (handler pluginDirectoryHandler) install(
	ctx context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return installPluginDirectory(ctx, opts, harness, handler.plan)
}

func (handler pluginDirectoryHandler) remove(
	ctx context.Context,
	opts Options,
	harness registry.Harness,
) (Result, error) {
	return removePluginDirectoryWithRetired(ctx, opts, harness, handler.plan)
}

func (handler pluginDirectoryHandler) inspect(
	ctx context.Context,
	harness registry.Harness,
) ([]inspectedArtifact, error) {
	return inspectPluginAction(ctx, handler.plan, harness)
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
	harness registry.Harness,
) (Result, error) {
	return noInstallRequiredResult(harness, handler.action.Label), nil
}

func (handler nativeCatalogHandler) remove(
	_ context.Context,
	_ Options,
	harness registry.Harness,
) (Result, error) {
	return noInstallRequiredResult(harness, handler.action.Label), nil
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
