package fakeprovider

import (
	"slices"
	"strconv"
	"strings"
)

// HasTool reports whether the request advertises a tool with this name.
func (r Request) HasTool(name string) bool {
	return slices.Contains(r.Tools, name)
}

// HasToolResult reports whether the request has a result for callID containing contains.
func (r Request) HasToolResult(callID, contains string) bool {
	for _, result := range r.ToolResults {
		if matchesCallID(result.CallID, callID) && strings.Contains(result.Output, contains) {
			return true
		}
	}
	return false
}

func (r Request) clone() Request {
	r.Tools = slices.Clone(r.Tools)
	r.ToolResults = slices.Clone(r.ToolResults)
	r.Body = slices.Clone(r.Body)
	return r
}

func matchesCallID(actual, expected string) bool {
	if expected == "" || actual == expected {
		return true
	}
	trimmedActual := strings.TrimPrefix(actual, "call_")
	trimmedExpected := strings.TrimPrefix(expected, "call_")
	if last := strings.LastIndex(trimmedActual, "_"); last >= 0 {
		if _, err := strconv.Atoi(trimmedActual[last+1:]); err == nil {
			trimmedActual = trimmedActual[:last]
		}
	}
	return trimmedActual == trimmedExpected
}
