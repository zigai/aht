package harness

import (
	"fmt"
	"strconv"
	"strings"
)

func PayloadStringAny(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := PayloadString(payload, key); value != "" {
			return value
		}
	}

	return ""
}

func PayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}

	text, ok := value.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(text)
}

func AddAttributeString(attributes map[string]string, key string, value string) {
	if value == "" {
		return
	}

	attributes[key] = value
}

func NestedString(payload map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}

	var current any = payload
	for _, part := range path {
		currentMap, ok := current.(map[string]any)
		if !ok {
			return ""
		}

		current = currentMap[part]
	}

	text, ok := current.(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(text)
}

func FirstArrayString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}

		items, ok := value.([]any)
		if !ok {
			continue
		}

		for _, item := range items {
			text, textOK := item.(string)
			if textOK && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}

	return ""
}

func PayloadBoolString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed)
		case string:
			return strings.TrimSpace(typed)
		default:
			if typed != nil {
				return fmt.Sprint(typed)
			}
		}
	}

	return ""
}

func PayloadBool(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}

		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			return strings.EqualFold(strings.TrimSpace(typed), "true")
		default:
			return strings.EqualFold(fmt.Sprint(typed), "true")
		}
	}

	return false
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
