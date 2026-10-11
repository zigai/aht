package fakeprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

const typeKey = "type"

var errNotObject = errors.New("body is not a JSON object")

func protocolOf(path string) (Protocol, bool) {
	switch {
	case strings.HasSuffix(path, "/messages"):
		return ProtocolAnthropicMessages, true
	case strings.HasSuffix(path, "/chat/completions"):
		return ProtocolOpenAIChat, true
	case strings.HasSuffix(path, "/responses"):
		return ProtocolOpenAIResponses, true
	}
	return "", false
}

func decodeObject(body []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	if value == nil {
		return nil, errNotObject
	}
	return value, nil
}

func newRequest(protocol Protocol, method, path string, body []byte, value map[string]any) Request {
	return Request{
		Index:       0,
		Kind:        KindConversation,
		Protocol:    protocol,
		Method:      method,
		Path:        path,
		Model:       stringOf(value["model"]),
		Stream:      value["stream"] == true,
		Tools:       toolNames(value),
		UserText:    userText(protocol, value),
		ToolResults: toolResults(protocol, value),
		Body:        jsontext.Value(body),
		ReceivedAt:  time.Now(),
		Outcome:     OutcomePending,
	}
}

func messagesOf(protocol Protocol, value map[string]any) []map[string]any {
	switch protocol {
	case ProtocolAnthropicMessages, ProtocolOpenAIChat:
		return objects(value["messages"])
	case ProtocolOpenAIResponses:
		var items []map[string]any
		if text, isString := value["input"].(string); isString {
			items = append(items, map[string]any{"role": "user", "content": text})
		}
		items = append(items, objects(value["input"])...)
		return append(items, objects(value["messages"])...)
	}
	return nil
}

func userText(protocol Protocol, value map[string]any) string {
	messages := messagesOf(protocol, value)
	for _, message := range slices.Backward(messages) {
		if message["role"] != "user" || hasToolResultPart(message) {
			continue
		}
		return contentText(message["content"])
	}
	return ""
}

func hasToolResultPart(message map[string]any) bool {
	for _, part := range objects(message["content"]) {
		if part[typeKey] == "tool_result" {
			return true
		}
	}
	return false
}

func contentText(content any) string {
	if text, isString := content.(string); isString {
		return text
	}
	var parts []string
	for _, part := range objects(content) {
		if text := stringOf(part["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolResults(protocol Protocol, value map[string]any) []ToolResult {
	messages := messagesOf(protocol, value)
	results := make([]ToolResult, 0, len(messages))
	for _, message := range messages {
		results = append(results, toolResultsIn(protocol, message)...)
	}
	return results
}

func toolResultsIn(protocol Protocol, message map[string]any) []ToolResult {
	switch protocol {
	case ProtocolAnthropicMessages:
		parts := objects(message["content"])
		results := make([]ToolResult, 0, len(parts))
		for _, part := range parts {
			if part[typeKey] == "tool_result" {
				results = append(results, ToolResult{CallID: stringOf(part["tool_use_id"]), Output: leafText(part["content"])})
			}
		}
		return results
	case ProtocolOpenAIChat:
		return chatToolResults(message)
	case ProtocolOpenAIResponses:
		if message[typeKey] == "function_call_output" {
			return []ToolResult{{CallID: stringOf(message["call_id"]), Output: leafText(message["output"])}}
		}
		return chatToolResults(message)
	}
	return nil
}

func chatToolResults(message map[string]any) []ToolResult {
	if message["role"] != "tool" {
		return nil
	}
	return []ToolResult{{CallID: stringOf(message["tool_call_id"]), Output: leafText(message["content"])}}
}

func leafText(value any) string {
	return strings.Join(leaves(value), "\n")
}

func leaves(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, leaves(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			out = append(out, leaves(typed[key])...)
		}
		return out
	}
	return nil
}

func toolNames(value map[string]any) []string {
	var names []string
	for _, tool := range objects(value["tools"]) {
		if name := stringOf(tool["name"]); name != "" {
			names = append(names, name)
		}
		if name := stringOf(object(tool["function"])["name"]); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func object(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func objects(value any) []map[string]any {
	list, _ := value.([]any)
	var out []map[string]any
	for _, item := range list {
		if m, isObject := item.(map[string]any); isObject {
			out = append(out, m)
		}
	}
	return out
}

func stringOf(value any) string {
	s, _ := value.(string)
	return s
}
