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
func (r Request) HasToolResult(callID string, contains string) bool {
	callID = r.echoedCallID(callID)

	for _, result := range r.ToolResults {
		if matchesCallID(result.CallID, callID) && strings.Contains(result.Output, contains) {
			return true
		}
	}

	return false
}

func (r Request) echoedCallID(callID string) string {
	if callID == "" || len(r.ToolCallIDs) == 0 {
		return callID
	}

	kept := slices.ContainsFunc(r.ToolCallIDs, func(echoed string) bool {
		return matchesCallID(echoed, callID)
	})
	if kept {
		return callID
	}

	// A harness may replace the call ids of a reply with its own. The request
	// then echoes no call under callID, and its newest call is the one asked for.
	return r.ToolCallIDs[len(r.ToolCallIDs)-1]
}

func (r Request) clone() Request {
	r.Tools = slices.Clone(r.Tools)
	r.ToolResults = slices.Clone(r.ToolResults)
	r.ToolCallIDs = slices.Clone(r.ToolCallIDs)
	r.Body = slices.Clone(r.Body)

	return r
}

func matchesCallID(actual string, expected string) bool {
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
