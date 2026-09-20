//go:build compatibility

package hostcompat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func providerRequestSummary(provider *scriptedProvider) string {
	if provider == nil {
		return "none"
	}
	requests := provider.Requests()
	summary := make([]string, 0, len(requests))
	for _, request := range requests {
		body := request.Body
		var payload map[string]any
		if json.Unmarshal([]byte(body), &payload) == nil {
			if tools, exists := payload["tools"].([]any); exists {
				names := make([]string, 0, len(tools))
				keys := make([]string, 0, len(payload))
				for key := range payload {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, rawTool := range tools {
					tool, ok := rawTool.(map[string]any)
					if !ok {
						continue
					}
					if name, ok := tool["name"].(string); ok {
						names = append(names, name)
					}
					if function, ok := tool["function"].(map[string]any); ok {
						if name, ok := function["name"].(string); ok {
							names = append(names, name)
						}
					}
				}
				input := payload["input"]
				if input == nil {
					input = payload["messages"]
				}
				body = "input_tail=" + summarizeProviderInput(input) + " keys=" + strings.Join(keys, ",") + " tools=" + strings.Join(names, ",")
			}
		}
		if len(body) > 2048 {
			body = body[:2048] + "..."
		}
		summary = append(summary, fmt.Sprintf("%s %s %s", request.Method, request.Path, body))
	}
	return strings.Join(summary, "\n")
}

func summarizeProviderInput(value any) string {
	items, ok := value.([]any)
	if !ok {
		return fmt.Sprint(value)
	}
	relevant := make([]any, 0, len(items))
	for _, item := range items {
		message, messageOK := item.(map[string]any)
		if !messageOK {
			continue
		}
		role, _ := message["role"].(string)
		messageType, _ := message["type"].(string)
		if role == "user" || role == "assistant" || role == "tool" || strings.Contains(messageType, "function_call") {
			relevant = append(relevant, message)
		}
	}
	if len(relevant) == 0 {
		relevant = items
	}
	if len(relevant) > 3 {
		relevant = relevant[len(relevant)-3:]
	}
	summary := mustJSON(relevant)
	if len(summary) > 2000 {
		return "..." + summary[len(summary)-2000:]
	}
	return summary
}
