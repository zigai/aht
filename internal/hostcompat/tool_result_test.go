package hostcompat

import "testing"

func TestToolResultAcceptsHostOutputEnvelopes(t *testing.T) {
	tests := []struct {
		name     string
		protocol providerProtocol
		body     string
	}{
		{
			name: "claude", protocol: protocolAnthropicMessages,
			body: `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_compat","content":[{"type":"text","text":"aht-compat-marker\n"}]}]}]}`,
		},
		{
			name: "codex", protocol: protocolOpenAIResponses,
			body: `{"input":[{"type":"function_call_output","call_id":"call_compat","output":"Chunk ID: 702c5d\nWall time: 0.0000 seconds\nProcess exited with code 0\nOutput:\naht-compat-marker"}]}`,
		},
		{
			name: "copilot", protocol: protocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_compat","content":"aht-compat-marker\n<shellId: 0 completed with exit code 0>"}]}`,
		},
		{
			name: "cline", protocol: protocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_compat","content":"[{\"query\":\"printf aht-compat-marker\",\"result\":\"aht-compat-marker\",\"success\":true}]"}]}`,
		},
		{
			name: "kimi-code", protocol: protocolOpenAIResponses,
			body: `{"input":[{"type":"function_call_output","call_id":"call_compat","output":"aht-compat-marker\n"}]}`,
		},
		{
			name: "droid", protocol: protocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_compat","content":"aht-compat-marker\n\n[Process exited with code 0]"}]}`,
		},
		{
			name: "hermes", protocol: protocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_compat","content":"{\"output\": \"aht-compat-marker\", \"exit_code\": 0, \"error\": null}"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !requestContainsToolResult(test.protocol, []byte(test.body), "call_compat", "aht-compat-marker") {
				t.Fatal("rejected marker in tool output")
			}
			if requestContainsToolResult(test.protocol, []byte(test.body), "different_call", "aht-compat-marker") {
				t.Fatal("accepted output for a different tool call")
			}
		})
	}
}
