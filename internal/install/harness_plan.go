package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zigai/aht/internal/harness/catalog"

	harnesspkg "github.com/zigai/aht/internal/harness"
	"github.com/zigai/aht/pkg/registry"
)

type importManifest struct {
	Imports []importEntry `json:"imports"`
}

type importEntry struct {
	Name       string   `json:"name"`
	Source     string   `json:"source"`
	ImportedAt string   `json:"imported_at"`
	Components []string `json:"components"`
}

func installHarnessAdapter(ctx context.Context, opts Options) (Result, error) {
	plan, advisor, err := installPlanForHarness(opts.Harness, opts.Binary)
	if err != nil {
		return Result{}, err
	}

	if opts.UseShim && installPlanHasShim(plan) {
		return installShim(opts, opts.Harness)
	}

	for _, action := range plan.Actions {
		result, handled, err := installPlanAction(ctx, opts, opts.Harness, action)
		if !handled {
			continue
		}
		if err == nil {
			if advisor != nil {
				result.NextStep = advisor.InstallNextStep(result.Changed, opts.DryRun)
			}
		}
		return result, err
	}

	return Result{}, fmt.Errorf("%w: %q", errUnsupportedHarness, opts.Harness)
}

func installPlanHasShim(plan harnesspkg.InstallPlan) bool {
	for _, action := range plan.Actions {
		if _, ok := action.(harnesspkg.ShimAction); ok {
			return true
		}
	}

	return false
}

func installPlanAction(ctx context.Context, options Options, harness registry.Harness, action harnesspkg.InstallAction) (Result, bool, error) {
	switch typed := action.(type) {
	case harnesspkg.JSONCommandHooksAction:
		result, err := installJSONCommandHooks(options, harness, typed.Plan)

		return result, true, err
	case harnesspkg.CursorJSONHooksAction:
		result, err := installCursorJSONHooks(options, harness, typed.Plan)

		return result, true, err
	case harnesspkg.ManagedTextBlockAction:
		result, err := installManagedTextBlock(options, harness, typed.Plan)

		return result, true, err
	case harnesspkg.RenderedFileAction:
		result, err := installRenderedPlan(options, harness, typed.Plan)

		return result, true, err
	case harnesspkg.PluginDirectoryAction:
		result, err := installPluginDirectory(ctx, options, harness, typed.Plan)

		return result, true, err
	case harnesspkg.ShimAction:
		var result Result

		return result, false, nil
	default:
		var result Result

		return result, false, nil
	}
}

func installJSONCommandHooks(
	options Options,
	harness registry.Harness,
	plan harnesspkg.JSONCommandHookInstallPlan,
) (Result, error) {
	label := installLabel(plan.Label, harness, "hooks")
	configLabel := installLabel(plan.ConfigLabel, harness, "config")

	return installJSONHookFile(options, jsonHookFileInstall{
		Harness:                 harness,
		Path:                    plan.Path,
		Apply:                   applyJSONCommandHooks(harness, plan),
		EncodeError:             "encoding " + label,
		CreateDirError:          "creating " + configLabel + " directory",
		WriteError:              "writing " + label,
		InstalledMessage:        label + " installed",
		AlreadyInstalledMessage: label + " already installed",
		DryRunMessage:           "dry run: " + label + " not written",
	})
}

func applyJSONCommandHooks(
	harness registry.Harness,
	plan harnesspkg.JSONCommandHookInstallPlan,
) func(map[string]any) bool {
	source := managedSource(plan.Source, harness)
	statusMessage := strings.TrimSpace(plan.StatusMessage)
	if !plan.OmitStatusMessage && statusMessage == "" {
		statusMessage = managedMarker
	}
	isManaged := isManagedSourceHookCommand(source)
	desiredByEvent := make(map[string][]any)
	events := make([]string, 0, len(plan.Hooks))
	for _, hook := range plan.Hooks {
		if _, exists := desiredByEvent[hook.Event]; !exists {
			events = append(events, hook.Event)
		}
		desiredByEvent[hook.Event] = append(desiredByEvent[hook.Event], commandHookGroup(
			hook.Command,
			hook.Matcher,
			statusMessage,
			catalog.HookTimeoutSecondsFor(harness, hook.Event),
		))
	}

	return func(harnessConfig map[string]any) bool {
		changed := false
		hooks := harnessConfig
		if plan.HooksAtRoot {
			changed = removeWrappedCommandHooks(harnessConfig, plan, isManaged)
		} else {
			var ok bool
			hooks, ok = harnessConfig["hooks"].(map[string]any)
			if !ok {
				hooks = make(map[string]any)
				harnessConfig["hooks"] = hooks
			}
		}
		for _, event := range events {
			updated := upsertManagedCommandHookGroups(
				hooks,
				event,
				desiredByEvent[event],
				isManaged,
			)
			changed = changed || updated
		}

		return changed
	}
}

func installCursorJSONHooks(
	options Options,
	harness registry.Harness,
	plan harnesspkg.CursorJSONHookInstallPlan,
) (Result, error) {
	label := installLabel(plan.Label, harness, "hooks")
	configLabel := installLabel(plan.ConfigLabel, harness, "config")

	return installJSONHookFile(options, jsonHookFileInstall{
		Harness:                 harness,
		Path:                    plan.Path,
		Apply:                   applyCursorJSONHooks(harness, plan),
		EncodeError:             "encoding " + label,
		CreateDirError:          "creating " + configLabel + " directory",
		WriteError:              "writing " + label,
		InstalledMessage:        label + " installed",
		AlreadyInstalledMessage: label + " already installed",
		DryRunMessage:           "dry run: " + label + " not written",
	})
}

func applyCursorJSONHooks(
	harness registry.Harness,
	plan harnesspkg.CursorJSONHookInstallPlan,
) func(map[string]any) bool {
	source := managedSource(plan.Source, harness)
	isManaged := isManagedSourceHookCommand(source)

	return func(harnessConfig map[string]any) bool {
		changed := ensureCursorVersion(harnessConfig)
		for _, hook := range plan.Hooks {
			updated := upsertCursorHook(harnessConfig, hook.Event, hook.Command, isManaged)
			changed = changed || updated
		}

		return changed
	}
}

func ensureCursorVersion(harnessConfig map[string]any) bool {
	if _, ok := harnessConfig["version"]; ok {
		return false
	}

	harnessConfig["version"] = float64(1)

	return true
}

func upsertCursorHook(harnessConfig map[string]any, event string, command string, isManaged func(string) bool) bool {
	hooks, ok := harnessConfig["hooks"].(map[string]any)
	if !ok {
		hooks = make(map[string]any)
		harnessConfig["hooks"] = hooks
	}
	definitions, ok := hooks[event].([]any)
	if !ok {
		definitions = nil
	}

	managedCount, exactCount := countManagedCursorHooks(definitions, command, isManaged)
	if managedCount == 1 && exactCount == 1 {
		return false
	}

	definitions, _ = removeManagedCursorHooks(definitions, isManaged)
	hooks[event] = append(definitions, map[string]any{
		"command": command,
		"timeout": float64(harnesspkg.HookTimeoutSeconds),
	})

	return true
}

func countManagedCursorHooks(
	definitions []any,
	command string,
	isManaged func(string) bool,
) (int, int) {
	managedCount := 0
	exactCount := 0
	for _, definitionValue := range definitions {
		definition, ok := definitionValue.(map[string]any)
		if !ok {
			continue
		}
		hookCommand, commandOK := definition["command"].(string)
		if !commandOK || !isManaged(hookCommand) {
			continue
		}
		managedCount++
		if hookCommand == command {
			exactCount++
		}
	}

	return managedCount, exactCount
}

func removeManagedCursorHooks(definitions []any, isManaged func(string) bool) ([]any, bool) {
	cleanedDefinitions := make([]any, 0, len(definitions))
	removed := false
	for _, definitionValue := range definitions {
		definition, ok := definitionValue.(map[string]any)
		if !ok {
			cleanedDefinitions = append(cleanedDefinitions, definitionValue)
			continue
		}
		hookCommand, commandOK := definition["command"].(string)
		if commandOK && isManaged(hookCommand) {
			removed = true
			continue
		}

		cleanedDefinition := make(map[string]any, len(definition))
		maps.Copy(cleanedDefinition, definition)
		cleanedDefinitions = append(cleanedDefinitions, cleanedDefinition)
	}

	return cleanedDefinitions, removed
}

func installManagedTextBlock(
	options Options,
	harness registry.Harness,
	plan harnesspkg.ManagedTextBlockInstallPlan,
) (Result, error) {
	current, err := readTextFile(plan.Path)
	if err != nil {
		return Result{}, err
	}

	next := upsertManagedTextBlock(current, plan.StartMarker, plan.EndMarker, plan.Block)
	changed := current != next
	label := installLabel(plan.Label, harness, "hooks")
	configLabel := installLabel(plan.ConfigLabel, harness, "config")

	if err := writeInstallFile(
		plan.Path,
		[]byte(next),
		changed,
		options.DryRun,
		"creating "+configLabel+" directory",
		"writing "+label,
	); err != nil {
		return Result{}, err
	}

	return Result{
		Harness:  string(harness),
		Path:     plan.Path,
		Changed:  changed,
		Message:  installMessage(label, changed, options.DryRun),
		NextStep: "",
		Snippet:  next,
		Error:    "",
	}, nil
}

func readTextFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}

		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	return string(data), nil
}

func upsertManagedTextBlock(current string, startMarker string, endMarker string, block string) string {
	cleaned := removeManagedTextBlock(current, startMarker, endMarker)

	return appendManagedTextBlock(cleaned, block)
}

func removeManagedTextBlock(current string, startMarker string, endMarker string) string {
	for {
		start := strings.Index(current, startMarker)
		if start < 0 {
			return current
		}

		endOffset := strings.Index(current[start:], endMarker)
		if endOffset < 0 {
			return current
		}

		end := start + endOffset + len(endMarker)
		for end < len(current) && (current[end] == '\r' || current[end] == '\n') {
			end++
		}

		before := strings.TrimRight(current[:start], " \t\r\n")
		after := strings.TrimLeft(current[end:], "\r\n")
		switch {
		case before == "":
			current = after
		case after == "":
			current = before
		default:
			current = before + "\n\n" + after
		}
	}
}

func appendManagedTextBlock(current string, block string) string {
	trimmed := strings.TrimRight(current, " \t\r\n")
	if trimmed == "" {
		return block
	}

	return trimmed + "\n\n" + block
}

func installRenderedPlan(
	options Options,
	harness registry.Harness,
	plan harnesspkg.RenderedFileInstallPlan,
) (Result, error) {
	content, err := renderInstallContent(plan.Content, plan.JSONContent)
	if err != nil {
		return Result{}, err
	}

	label := installLabel(plan.Label, harness, "artifact")
	configLabel := installLabel(plan.ConfigLabel, harness, "artifact")

	return installRenderedFile(options, renderedFileInstall{
		Harness:                 harness,
		Path:                    plan.Path,
		Content:                 content,
		CreateDirError:          "creating " + configLabel + " directory",
		WriteError:              "writing " + label,
		InstalledMessage:        label + " installed",
		AlreadyInstalledMessage: label + " already installed",
		DryRunMessage:           "dry run: " + label + " not written",
	})
}

func renderInstallContent(content string, jsonContent any) (string, error) {
	if jsonContent == nil {
		return content, nil
	}

	data, err := json.MarshalIndent(jsonContent, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling JSON install content: %w", err)
	}

	return string(append(data, '\n')), nil
}

func installPluginDirectory(
	ctx context.Context,
	options Options,
	harness registry.Harness,
	plan harnesspkg.PluginDirectoryInstallPlan,
) (Result, error) {
	files, err := renderInstallFiles(plan.Files, "plugin")
	if err != nil {
		return Result{}, err
	}
	plugin := newPluginDirectoryInstall(plan, files)

	if err := plugin.ensureManaged(options.Force); err != nil {
		return Result{}, err
	}

	pluginChanged, err := plugin.needsUpdate()
	if err != nil {
		return Result{}, err
	}
	if plan.Registration != nil {
		return installRegisteredPlugin(ctx, options, harness, plan, plugin, pluginChanged)
	}

	manifest, manifestChanged, err := plannedImportManifest(plan.ImportManifest, time.Now().UTC())
	if err != nil {
		return Result{}, err
	}

	changed := pluginChanged || manifestChanged

	if changed && !options.DryRun {
		if err := writePluginDirectoryChanges(plugin, plan.ImportManifest, manifest, pluginChanged, manifestChanged); err != nil {
			return Result{}, err
		}
	}

	label := installLabel(plan.Label, harness, "plugin")

	return Result{
		Harness:  string(harness),
		Path:     plugin.dir,
		Changed:  changed,
		Message:  installMessage(label, changed, options.DryRun),
		NextStep: "",
		Snippet:  plugin.snippet(),
		Error:    "",
	}, nil
}

func writePluginDirectoryChanges(
	plugin pluginDirectoryInstall,
	importPlan *harnesspkg.ImportManifestInstallPlan,
	manifest importManifest,
	pluginChanged bool,
	manifestChanged bool,
) error {
	var rollback func() error
	var commit func() error
	var rollbackManifest func() error
	if pluginChanged {
		var err error
		rollback, commit, err = plugin.installStaged()
		if err != nil {
			return err
		}
	}

	if importPlan != nil && manifestChanged {
		var err error
		rollbackManifest, err = prepareImportManifestRollback(importPlan.Path)
		if err != nil {
			return rollbackPluginDirectory(rollback, err)
		}
		if err := writeImportManifest(importPlan.Path, manifest); err != nil {
			return rollbackPluginDirectoryAndManifest(rollback, rollbackManifest, err)
		}
	}

	if commit == nil {
		return nil
	}

	return commit()
}

func prepareImportManifestRollback(path string) (func() error, error) {
	previous, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading import manifest rollback state: %w", err)
	}

	return func() error {
		if existed {
			return writeFileAtomic(path, previous, "creating import manifest rollback directory", "restoring import manifest")
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing newly created import manifest: %w", err)
		}

		return syncDir(filepath.Dir(path))
	}, nil
}

func rollbackPluginDirectoryAndManifest(rollbackPlugin, rollbackManifest func() error, cause error) error {
	if rollbackPlugin != nil {
		if err := rollbackPlugin(); err != nil {
			cause = errors.Join(cause, fmt.Errorf("rolling back plugin directory: %w", err))
		}
	}
	if rollbackManifest != nil {
		if err := rollbackManifest(); err != nil {
			cause = errors.Join(cause, fmt.Errorf("rolling back import manifest: %w", err))
		}
	}

	return cause
}

func rollbackPluginDirectory(rollback func() error, cause error) error {
	if rollback == nil {
		return cause
	}
	if err := rollback(); err != nil {
		return errors.Join(cause, fmt.Errorf("rolling back plugin directory: %w", err))
	}

	return cause
}

func plannedImportManifest(
	plan *harnesspkg.ImportManifestInstallPlan,
	now time.Time,
) (importManifest, bool, error) {
	if plan == nil {
		var manifest importManifest

		return manifest, false, nil
	}

	return importManifestWithPlan(*plan, now)
}

func importManifestWithPlan(plan harnesspkg.ImportManifestInstallPlan, now time.Time) (importManifest, bool, error) {
	manifest, err := readImportManifest(plan.Path)
	if err != nil {
		return importManifest{}, false, err
	}

	next, changed := upsertImport(manifest, plan, now)

	return next, changed, nil
}

func readImportManifest(path string) (importManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return importManifest{
				Imports: nil,
			}, nil
		}

		return importManifest{}, fmt.Errorf("reading import manifest: %w", err)
	}

	var manifest importManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return importManifest{}, fmt.Errorf("parsing import manifest: %w", err)
	}

	return manifest, nil
}

func upsertImport(
	manifest importManifest,
	plan harnesspkg.ImportManifestInstallPlan,
	now time.Time,
) (importManifest, bool) {
	for index, item := range manifest.Imports {
		if item.Name != plan.Name {
			continue
		}

		next := item
		if next.Source != plan.Source {
			next.Source = plan.Source
		}
		if next.ImportedAt == "" {
			next.ImportedAt = now.Format(time.RFC3339)
		}
		for _, component := range plan.Components {
			if !slices.Contains(next.Components, component) {
				next.Components = append(next.Components, component)
			}
		}
		if importsEqual(item, next) {
			return manifest, false
		}

		manifest.Imports[index] = next
		return manifest, true
	}

	manifest.Imports = append(manifest.Imports, importEntry{
		Name:       plan.Name,
		Source:     plan.Source,
		ImportedAt: now.Format(time.RFC3339),
		Components: append([]string(nil), plan.Components...),
	})

	return manifest, true
}

func importsEqual(left importEntry, right importEntry) bool {
	if left.Name != right.Name || left.Source != right.Source || left.ImportedAt != right.ImportedAt {
		return false
	}
	return slices.Equal(left.Components, right.Components)
}

func writeImportManifest(path string, manifest importManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding import manifest: %w", err)
	}
	data = append(data, '\n')

	return writeFileAtomic(path, data, "creating config directory", "writing import manifest")
}

func writeFileAtomic(path string, data []byte, createDirError string, writeError string) error {
	return writeFileAtomicMode(path, data, 0o600, createDirError, writeError)
}

func writeFileAtomicMode(path string, data []byte, mode os.FileMode, createDirError string, writeError string) error {
	targetPath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		targetPath = resolved
	}
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s: %w", createDirError, err)
	}

	temp, err := os.CreateTemp(dir, filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", targetPath, err)
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		if keep {
			return
		}
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(mode); err != nil {
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("%s: %w", writeError, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("%s: %w", writeError, err)
	}
	keep = true

	return syncDir(dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening directory: %w", err)
	}
	defer func() {
		_ = handle.Close()
	}()

	if err := handle.Sync(); err != nil {
		return fmt.Errorf("syncing directory: %w", err)
	}

	return nil
}

func managedSource(source string, harness registry.Harness) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return string(harness) + "-hook"
	}

	return source
}

func isManagedSourceHookCommand(source string) func(string) bool {
	pattern := regexp.MustCompile(`(?:--reporter\s+['"]?|--source\s+['"]?|aht[_-]?integration=['"]?)` + regexp.QuoteMeta(source) + `(?:['"\s]|$)`)
	return pattern.MatchString
}

func installLabel(value string, harness registry.Harness, suffix string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}

	return string(harness) + " " + suffix
}

func installMessage(label string, changed bool, dryRun bool) string {
	if dryRun {
		return "dry run: " + label + " not written"
	}
	if !changed {
		return label + " already installed"
	}

	return label + " installed"
}
