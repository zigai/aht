package cursor

import (
	"fmt"
	"maps"

	"github.com/zigai/aht/v2/internal/harness"
)

func mergeHooks(
	config map[string]any,
	hooks []harness.CommandHookInstallSpec,
	isManaged func(string) bool,
) (bool, error) {
	changed := ensureCursorVersion(config)
	for _, hook := range hooks {
		updated, err := upsertCursorHook(config, hook.Event, hook.Command, isManaged)
		if err != nil {
			return false, err
		}

		changed = changed || updated
	}

	return changed, nil
}

func removeHooks(
	config map[string]any,
	specs []harness.CommandHookInstallSpec,
	isManaged func(string) bool,
) bool {
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		return false
	}

	changed := false

	for _, spec := range specs {
		definitions, ok := hooks[spec.Event].([]any)
		if !ok {
			continue
		}

		cleaned, removed := removeManagedCursorHooks(definitions, isManaged)
		if !removed {
			continue
		}

		changed = true

		if len(cleaned) == 0 {
			delete(hooks, spec.Event)
		} else {
			hooks[spec.Event] = cleaned
		}
	}

	return changed
}

func hooksObject(config map[string]any) (map[string]any, error) {
	value := config["hooks"]
	if value == nil {
		hooks := make(map[string]any)
		config["hooks"] = hooks

		return hooks, nil
	}

	hooks, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: hooks is not an object", harness.ErrHookConfigShape)
	}

	return hooks, nil
}

func hookEventList(hooks map[string]any, event string) ([]any, error) {
	value := hooks[event]
	if value == nil {
		return nil, nil
	}

	definitions, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: hooks.%s is not an array", harness.ErrHookConfigShape, event)
	}

	return definitions, nil
}

func ensureCursorVersion(harnessConfig map[string]any) bool {
	if _, ok := harnessConfig["version"]; ok {
		return false
	}

	harnessConfig["version"] = float64(1)

	return true
}

func upsertCursorHook(
	harnessConfig map[string]any,
	event string,
	command string,
	isManaged func(string) bool,
) (bool, error) {
	hooks, err := hooksObject(harnessConfig)
	if err != nil {
		return false, err
	}

	definitions, err := hookEventList(hooks, event)
	if err != nil {
		return false, err
	}

	managedCount, exactCount := countManagedCursorHooks(definitions, command, isManaged)
	if managedCount == 1 && exactCount == 1 {
		return false, nil
	}

	definitions, _ = removeManagedCursorHooks(definitions, isManaged)
	hooks[event] = append(definitions, map[string]any{
		"command": command,
		"timeout": float64(harness.HookTimeoutSeconds),
	})

	return true, nil
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
