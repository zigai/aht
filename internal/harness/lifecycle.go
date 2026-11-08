package harness

import (
	"strings"

	"github.com/zigai/aht/v2/pkg/registry"
)

type LifecycleDefaults struct {
	Event     string
	Lifecycle registry.NativeLifecycle
	Presence  registry.Presence
}

type LifecycleAdapter interface {
	LifecycleDefaults(event string, attributes map[string]string) LifecycleDefaults
}

func (adapter BaseAdapter) LifecycleDefaults(
	event string,
	attributes map[string]string,
) LifecycleDefaults {
	if event == "" && adapter.definition.LifecycleEventAttribute != "" {
		event = attributes[adapter.definition.LifecycleEventAttribute]
	}

	sources := adapter.definition.LifecycleSourceAttributes
	if len(sources) == 0 {
		sources = []string{"source", "reason"}
	}

	return TranslateLifecycle(event, FirstAttribute(attributes, sources...))
}

func TranslateLifecycle(event string, source string) LifecycleDefaults {
	result := LifecycleDefaults{Event: event, Lifecycle: "", Presence: ""}
	switch normalizedLifecycleEvent(event) {
	case "start":
		result.Lifecycle = registry.NativeLifecycleStart

		result.Presence = registry.PresenceLive
		if strings.EqualFold(source, "resume") || strings.EqualFold(source, "resumed") {
			result.Lifecycle = registry.NativeLifecycleResume
		}
	case "resume":
		result.Lifecycle = registry.NativeLifecycleResume
		result.Presence = registry.PresenceLive
	case "end":
		result.Lifecycle = registry.NativeLifecycleEnd
		result.Presence = registry.PresenceGone
	}

	return result
}

func FirstAttribute(attributes map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attributes[key]); value != "" {
			return value
		}
	}

	return ""
}

func normalizedLifecycleEvent(event string) string {
	normalized := strings.Map(func(character rune) rune {
		switch {
		case character >= 'A' && character <= 'Z':
			return character + ('a' - 'A')
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			return character
		default:
			return -1
		}
	}, strings.TrimSpace(event))

	switch normalized {
	case "sessionstart", "sessioncreated", "onsessionstart":
		return "start"
	case "sessionswitch", "sessionbranch", "sessiontree":
		return "resume"
	case "sessionend", "sessionshutdown", "sessiondeleted", "onsessionfinalize":
		return "end"
	default:
		return ""
	}
}
