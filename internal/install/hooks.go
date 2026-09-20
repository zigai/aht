package install

import (
	"encoding/json"
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
		managedInGroup := countManagedHooks(hookValues, isManaged)
		if managedInGroup == 0 {
			continue
		}
		managedCount += managedInGroup
		if desiredCount >= len(desiredGroups) {
			return false
		}
		desiredGroup, ok := desiredGroups[desiredCount].(map[string]any)
		if !ok || !commandHookGroupEqual(group, desiredGroup) {
			return false
		}
		desiredCount++
	}

	return managedCount == len(desiredGroups) && desiredCount == len(desiredGroups)
}

func countManagedHooks(hookValues []any, isManaged func(string) bool) int {
	count := 0
	for _, hookValue := range hookValues {
		hook, hookOK := hookValue.(map[string]any)
		if !hookOK {
			continue
		}
		hookCommand, commandOK := hook["command"].(string)
		if !commandOK || !isManaged(hookCommand) {
			continue
		}
		count++
	}
	return count
}

func numbersEqual(a, b any) bool {
	fa, oka := toFloat64(a)
	fb, okb := toFloat64(b)
	if oka && okb {
		return fa == fb
	}
	return reflect.DeepEqual(a, b)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	default:
		return 0, false
	}
}

func commandHookEqual(actual, desired map[string]any) bool {
	if len(actual) != len(desired) {
		return false
	}
	for k, dv := range desired {
		av, ok := actual[k]
		if !ok {
			return false
		}
		if k == "timeout" || k == "timeoutSec" {
			if !numbersEqual(av, dv) {
				return false
			}
		} else if !reflect.DeepEqual(av, dv) {
			return false
		}
	}
	return true
}

func commandHookGroupEqual(actual, desired map[string]any) bool {
	if len(actual) != len(desired) {
		return false
	}
	for k, dv := range desired {
		av, ok := actual[k]
		if !ok {
			return false
		}
		if k == "hooks" {
			if !hookSlicesEqual(av, dv) {
				return false
			}
		} else if !reflect.DeepEqual(av, dv) {
			return false
		}
	}
	return true
}

func hookSlicesEqual(actual, desired any) bool {
	ah, aOK := actual.([]any)
	dh, dOK := desired.([]any)
	if !aOK || !dOK || len(ah) != len(dh) {
		return false
	}
	for i := range ah {
		ahm, aOK := ah[i].(map[string]any)
		dhm, dOK := dh[i].(map[string]any)
		if aOK && dOK {
			if !commandHookEqual(ahm, dhm) {
				return false
			}
			continue
		}
		if !reflect.DeepEqual(ah[i], dh[i]) {
			return false
		}
	}
	return true
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

		cleanedGroup := maps.Clone(group)
		cleanedGroup["hooks"] = cleanedHooks
		cleanedGroups = append(cleanedGroups, cleanedGroup)
	}

	return cleanedGroups, removed
}
