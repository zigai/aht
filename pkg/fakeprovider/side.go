package fakeprovider

import (
	"encoding/json/v2"
	"fmt"
	"slices"
)

const sideTitle = "Fake session"

func (s *Server) isSideRequest(value map[string]any) bool {
	return s.sideRequest != nil && s.sideRequest(value)
}

func structuredSchema(value map[string]any) (map[string]any, bool) {
	if format := object(
		object(value["text"])["format"],
	); stringOf(
		format[typeKey],
	) == "json_schema" {
		return object(format["schema"]), true
	}

	if format := object(value["response_format"]); stringOf(format[typeKey]) == "json_schema" {
		return object(object(format["json_schema"])["schema"]), true
	}

	return nil, false
}

func sideText(value map[string]any) (string, error) {
	schema, structured := structuredSchema(value)
	if !structured {
		return sideTitle, nil
	}

	required, listed := requiredNames(schema)
	fields := make(map[string]string)

	for name, property := range object(schema["properties"]) {
		if stringOf(object(property)[typeKey]) != "string" {
			continue
		}

		if listed && !slices.Contains(required, name) {
			continue
		}

		fields[name] = sideTitle
	}

	data, err := json.Marshal(fields, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("encode structured side reply: %w", err)
	}

	return string(data), nil
}

func requiredNames(schema map[string]any) ([]string, bool) {
	list, listed := schema["required"].([]any)

	names := make([]string, 0, len(list))
	for _, item := range list {
		names = append(names, stringOf(item))
	}

	return names, listed
}
