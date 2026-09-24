package transcript

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type (
	Record   map[string]json.RawMessage
	TextPart struct{ Role, Text string }
)

func MessageParts(raw json.RawMessage, role string, tools bool) []TextPart {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return []TextPart{{Role: role, Text: string(raw[1 : len(raw)-1])}}
	}
	var body string
	if json.Unmarshal(raw, &body) == nil {
		return []TextPart{{Role: role, Text: body}}
	}
	var blocks []Record
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return MessageBlockParts(blocks, role, tools)
}

func MessageBlockParts(blocks []Record, role string, tools bool) []TextPart {
	var text strings.Builder
	var toolText strings.Builder
	for _, block := range blocks {
		kind := Str(block, "type")
		if kind == "text" || kind == "input_text" || kind == "output_text" {
			text.WriteString(Str(block, "text"))
			text.WriteByte('\n')
			continue
		}
		if tools {
			if body := ToolContent(block); body != "" {
				toolText.WriteString(body)
				toolText.WriteByte('\n')
			}
		}
	}
	var parts []TextPart
	if text.Len() > 0 {
		parts = append(parts, TextPart{Role: role, Text: strings.TrimSuffix(text.String(), "\n")})
	}
	if toolText.Len() > 0 {
		parts = append(parts, TextPart{Role: "tool", Text: strings.TrimSuffix(toolText.String(), "\n")})
	}
	return parts
}

func ContentText(raw json.RawMessage) string {
	parts := MessageParts(raw, "tool", false)
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

func Str(r Record, key string) string {
	raw := r[key]
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1])
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func Obj(r Record, key string) Record {
	var value Record
	_ = json.Unmarshal(r[key], &value)
	return value
}

func FirstString(r Record, keys ...string) string {
	for _, key := range keys {
		if value := Str(r, key); value != "" {
			return value
		}
	}
	return ""
}

func ParseTime(raw json.RawMessage) time.Time {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return NativeTime(string(raw[1 : len(raw)-1]))
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return NativeTime(text)
	}
	number, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || !(number > 0) {
		return time.Time{}
	}
	const millisThreshold = 1e11
	const millisPerSecond = 1000
	if number >= millisThreshold {
		number /= millisPerSecond
	}
	const latestUnixSecond = 253402300799 // Last second representable by RFC3339.
	if number > latestUnixSecond {
		return time.Time{}
	}
	seconds := int64(number)
	return time.Unix(seconds, int64((number-float64(seconds))*float64(time.Second))).UTC()
}

func NativeTime(text string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if value, err := time.Parse(layout, text); err == nil {
			return value
		}
	}
	return time.Time{}
}

func RecognizedRole(role string) (string, bool) {
	switch role {
	case "user", "assistant":
		return role, true
	case "toolResult", "tool":
		return "tool", true
	default:
		return "", false
	}
}

func ToolContent(block Record) string {
	switch Str(block, "type") {
	case "tool":
		return ToolBlockText(block)
	case "toolRequest":
		call := Obj(Obj(block, "toolCall"), "value")
		return Str(call, "name") + " " + string(call["arguments"])
	case "toolResponse":
		result := Obj(block, "toolResult")
		if Str(result, "status") == "error" {
			return Str(result, "error")
		}
		return ContentText(Obj(result, "value")["content"])
	case "tool-call":
		return Str(block, "toolName") + " " + string(block["input"])
	case "tool-result":
		return string(block["output"])
	case "tool_result":
		return ContentText(block["content"])
	case "tool_use", "toolCall":
		return Str(block, "name") + " " + string(block["input"]) + string(block["arguments"])
	default:
		return ""
	}
}

func StoredTime(value string) time.Time {
	if strings.ContainsAny(value, "-T:") {
		return NativeTime(value)
	}
	return ParseTime(json.RawMessage(value))
}

func ToolBlockText(r Record) string {
	state := Obj(r, "state")
	return FirstString(r, "tool", "name") + " " + string(state["input"]) + " " + Str(state, "output") + " " + ContentText(state["content"]) + " " + Str(state, "error") + " " + Str(Obj(state, "error"), "message")
}
