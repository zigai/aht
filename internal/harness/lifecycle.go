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

	normalized := normalizeLifecycleEvent(event)

	kind, ok := adapter.definition.LifecycleEvents[normalized]
	if !ok {
		kind = genericLifecycleEvent(normalized)
	}

	return translateLifecycle(event, FirstAttribute(attributes, sources...), kind)
}

func TranslateLifecycle(event string, source string) LifecycleDefaults {
	return translateLifecycle(event, source, genericLifecycleEvent(normalizeLifecycleEvent(event)))
}

func FirstAttribute(attributes map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attributes[key]); value != "" {
			return value
		}
	}

	return ""
}

func translateLifecycle(event string, source string, kind string) LifecycleDefaults {
	result := LifecycleDefaults{Event: event, Lifecycle: "", Presence: ""}

	switch kind {
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

func normalizeLifecycleEvent(event string) string {
	return strings.Map(func(character rune) rune {
		switch {
		case character >= 'A' && character <= 'Z':
			return character + ('a' - 'A')
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			return character
		default:
			return -1
		}
	}, strings.TrimSpace(event))
}

func genericLifecycleEvent(event string) string {
	switch event {
	case "sessionstart":
		return "start"
	case "sessionend", "sessionshutdown":
		return "end"
	default:
		return ""
	}
}
