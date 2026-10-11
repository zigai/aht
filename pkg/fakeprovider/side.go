package fakeprovider

import (
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

const sideTitle = "Fake session"

var defaultTitleMarkers = []string{
	"You are a title generator",
	"ultra-short dashboard line",
	"generate_session_title",
	"Generate a session title",
	"Write the title in the predominant language",
	"Generate a short, descriptive title",
	"generates concise session titles",
	"You name coding-agent sessions",
	"Your goal is to pick a coding session name",
	"<title-request>",
}

func (s *Server) isSideRequest(request Request, body []byte, value map[string]any) bool {
	if _, structured := structuredSchema(value); structured {
		return true
	}
	if request.HasTool("session_title") {
		return true
	}
	text := string(body)
	for _, markers := range [][]string{defaultTitleMarkers, s.sideMarkers} {
		for _, marker := range markers {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

func structuredSchema(value map[string]any) (map[string]any, bool) {
	if format := object(object(value["text"])["format"]); stringOf(format[typeKey]) == "json_schema" {
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
