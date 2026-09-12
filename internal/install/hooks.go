package install

import (
	"maps"
	"reflect"

	harnesspkg "github.com/zigai/aht/internal/harness"
)

func commandHookGroup(command string, matcher string, statusMessage string, timeoutSeconds int) map[string]any {
	if timeoutSeconds <= 0 {
		timeoutSeconds = harnesspkg.HookTimeoutSeconds
	}
	hook := map[string]any{
		"type":    harnesspkg.HookTypeCommand,
		"command": command,
		"timeout": float64(timeoutSeconds),
	}
	if statusMessage != "" {
		hook["statusMessage"] = statusMessage
	}

	group := map[string]any{
		"hooks": []any{
			hook,
		},
	}
	if matcher != "" {
		group["matcher"] = matcher
	}

	return group
}

func upsertManagedCommandHookGroups(
	hooks map[string]any,
	event string,
	desiredGroups []any,
	isManaged func(string) bool,
) bool {
	groups, ok := hooks[event].([]any)
	if !ok {
		groups = nil
	}

	if managedCommandHookGroupsCurrent(groups, desiredGroups, isManaged) {
		return false
	}

	groups, _ = removeManagedCommandHookGroups(groups, isManaged)
	hooks[event] = append(groups, desiredGroups...)

	return true
}

func managedCommandHookGroupsCurrent(groups []any, desiredGroups []any, isManaged func(string) bool) bool {
	managedCount := 0
	desiredCount := 0
	for _, groupValue := range groups {
		group, ok := groupValue.(map[string]any)
		if !ok {
			continue
		}
		hookValues, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		groupManagedCount := managedCount
		for _, hookValue := range hookValues {
			hook, hookOK := hookValue.(map[string]any)
			if !hookOK {
				continue
			}
			hookCommand, commandOK := hook["command"].(string)
			if !commandOK || !isManaged(hookCommand) {
				continue
			}
			managedCount++
		}
		if managedCount == groupManagedCount {
			continue
		}
		if desiredCount >= len(desiredGroups) || !reflect.DeepEqual(group, desiredGroups[desiredCount]) {
			return false
		}
		desiredCount++
	}

	return managedCount == len(desiredGroups) && desiredCount == len(desiredGroups)
}

func removeManagedCommandHookGroups(groups []any, isManaged func(string) bool) ([]any, bool) {
	cleanedGroups := make([]any, 0, len(groups))
	removed := false
	for _, groupValue := range groups {
		group, ok := groupValue.(map[string]any)
		if !ok {
			cleanedGroups = append(cleanedGroups, groupValue)
			continue
		}
		hookValues, ok := group["hooks"].([]any)
		if !ok {
			cleanedGroups = append(cleanedGroups, groupValue)
			continue
		}

		groupRemoved := false
		cleanedHooks := make([]any, 0, len(hookValues))
		for _, hookValue := range hookValues {
			hook, hookOK := hookValue.(map[string]any)
			if !hookOK {
				cleanedHooks = append(cleanedHooks, hookValue)
				continue
			}
			hookCommand, commandOK := hook["command"].(string)
			if commandOK && isManaged(hookCommand) {
				removed = true
				groupRemoved = true
				continue
			}
			cleanedHooks = append(cleanedHooks, hookValue)
		}
		if !groupRemoved {
			cleanedGroups = append(cleanedGroups, groupValue)
			continue
		}
		if len(cleanedHooks) == 0 {
			continue
		}

		cleanedGroup := make(map[string]any, len(group))
		maps.Copy(cleanedGroup, group)
		cleanedGroup["hooks"] = cleanedHooks
		cleanedGroups = append(cleanedGroups, cleanedGroup)
	}

	return cleanedGroups, removed
}
