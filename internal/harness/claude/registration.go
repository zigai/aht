package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/pkg/registry"
)

var (
	errSettingNotObject = errors.New("claude setting is not an object")
	errSettingsTrailing = errors.New("trailing data after the settings object")
)

// registration enables the plugin through Claude Code's user settings: the
// plugin's folder is declared as a directory marketplace, which Claude Code
// loads in place, and the plugin is enabled. It also strips the settings
// command hooks that earlier versions of the integration installed.
type registration struct {
	settingsPath string
}

func (registration) ID() string {
	return pluginID
}

func (registration) Label() string {
	return "Claude Code plugin " + pluginID
}

func (registration registration) Inspect(
	_ context.Context,
	pluginDir string,
) (harness.PluginRegistrationState, error) {
	config, err := readSettings(registration.settingsPath)
	if err != nil {
		return harness.PluginRegistrationMissing, err
	}

	marketplaces, _ := config["extraKnownMarketplaces"].(map[string]any)
	plugins, _ := config["enabledPlugins"].(map[string]any)

	entry, declared := marketplaces[marketplaceName]
	hasCommandHooks := stripCommandHooks(config)

	switch {
	case declared && !isOwnMarketplace(entry, pluginDir):
		return harness.PluginRegistrationForeign, nil
	case hasCommandHooks:
		return harness.PluginRegistrationStale, nil
	case !declared:
		return harness.PluginRegistrationMissing, nil
	case plugins[pluginID] == false:
		return harness.PluginRegistrationDisabled, nil
	case plugins[pluginID] != true:
		return harness.PluginRegistrationStale, nil
	default:
		return harness.PluginRegistrationCurrent, nil
	}
}

func (registration registration) EnsureMutable(pluginDir string) error {
	config, err := readSettings(registration.settingsPath)
	if err != nil {
		return err
	}

	return register(config, pluginDir)
}

func (registration registration) Install(_ context.Context, pluginDir string) error {
	return registration.update(func(config map[string]any) error {
		return register(config, pluginDir)
	})
}

func (registration registration) CleanupFailedInstall(
	ctx context.Context,
	previousState harness.PluginRegistrationState,
	pluginDir string,
) error {
	if previousState != harness.PluginRegistrationMissing {
		return nil
	}

	return registration.Remove(ctx, pluginDir)
}

func (registration registration) Remove(_ context.Context, pluginDir string) error {
	return registration.update(func(config map[string]any) error {
		if marketplaces, ok := config["extraKnownMarketplaces"].(map[string]any); ok &&
			isOwnMarketplace(marketplaces[marketplaceName], pluginDir) {
			delete(marketplaces, marketplaceName)
			dropEmpty(config, "extraKnownMarketplaces", marketplaces)
		}

		if plugins, ok := config["enabledPlugins"].(map[string]any); ok {
			delete(plugins, pluginID)
			dropEmpty(config, "enabledPlugins", plugins)
		}

		stripCommandHooks(config)

		return nil
	})
}

func (registration registration) update(change func(map[string]any) error) error {
	config, err := readSettings(registration.settingsPath)
	if err != nil {
		return err
	}

	if err := change(config); err != nil {
		return err
	}

	return writeSettings(registration.settingsPath, config)
}

func register(config map[string]any, pluginDir string) error {
	marketplaces, err := objectAt(config, "extraKnownMarketplaces")
	if err != nil {
		return err
	}

	plugins, err := objectAt(config, "enabledPlugins")
	if err != nil {
		return err
	}

	marketplaces[marketplaceName] = map[string]any{
		"source": map[string]any{"source": "directory", "path": pluginDir},
	}
	plugins[pluginID] = true
	config["extraKnownMarketplaces"] = marketplaces
	config["enabledPlugins"] = plugins
	stripCommandHooks(config)

	return nil
}

func isOwnMarketplace(entry any, pluginDir string) bool {
	marketplace, ok := entry.(map[string]any)
	if !ok {
		return false
	}

	source, ok := marketplace["source"].(map[string]any)

	return ok && source["source"] == "directory" && source["path"] == pluginDir
}

// stripCommandHooks removes the settings command hooks an earlier version of
// the integration installed, and reports whether it found any.
func stripCommandHooks(config map[string]any) bool {
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		return false
	}

	isOwned := func(command string) bool {
		_, native := harness.ParseNativeHookCommand(command, registry.Harness("claude"))

		return native || harness.IsManagedSourceCommand(command, claudeIntegrationSource)
	}

	stripped := false

	for event, value := range hooks {
		groups, ok := value.([]any)
		if !ok {
			continue
		}

		kept := make([]any, 0, len(groups))

		for _, groupValue := range groups {
			group, removed := stripGroup(groupValue, isOwned)
			stripped = stripped || removed

			if group != nil {
				kept = append(kept, group)
			}
		}

		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}

	dropEmpty(config, "hooks", hooks)

	return stripped
}

// stripGroup returns the matcher group without owned hooks, nil when none of
// its hooks remain, and whether it removed any.
func stripGroup(groupValue any, isOwned func(string) bool) (any, bool) {
	group, ok := groupValue.(map[string]any)
	if !ok {
		return groupValue, false
	}

	hooks, ok := group["hooks"].([]any)
	if !ok {
		return groupValue, false
	}

	kept := make([]any, 0, len(hooks))

	for _, hookValue := range hooks {
		hook, ok := hookValue.(map[string]any)
		command, isCommand := hook["command"].(string)

		if ok && isCommand && isOwned(command) {
			continue
		}

		kept = append(kept, hookValue)
	}

	if len(kept) == len(hooks) {
		return groupValue, false
	}

	if len(kept) == 0 {
		return nil, true
	}

	cleaned := maps.Clone(group)
	cleaned["hooks"] = kept

	return cleaned, true
}

func objectAt(config map[string]any, key string) (map[string]any, error) {
	value, present := config[key]
	if !present {
		return make(map[string]any), nil
	}

	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errSettingNotObject, key)
	}

	return object, nil
}

func dropEmpty(config map[string]any, key string, object map[string]any) {
	if len(object) == 0 {
		delete(config, key)
	}
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && len(bytes.TrimSpace(data)) == 0 {
		return make(map[string]any), nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if decoder.More() {
		return nil, fmt.Errorf("parsing %s: %w", path, errSettingsTrailing)
	}

	if config == nil {
		return make(map[string]any), nil
	}

	return config, nil
}

func writeSettings(path string, config map[string]any) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() {
		_ = os.Remove(temp.Name())
	}()

	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()

		return fmt.Errorf("writing %s: %w", path, err)
	}

	if err := temp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	if info, err := os.Stat(path); err == nil {
		if err := os.Chmod(temp.Name(), info.Mode().Perm()); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
