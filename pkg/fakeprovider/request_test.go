package fakeprovider_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const marker = "marker-7"

func recordedRequest(t *testing.T, protocol fakeprovider.Protocol, body string) fakeprovider.Request {
	t.Helper()
	server := startServer(t, fakeprovider.Options{})
	postJSON(t, server.URL()+pathFor(protocol), body)
	requests := closeAndRequests(t, server)
	if len(requests) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(requests))
	}
	return requests[0]
}

func TestToolResultEnvelopesAreFoundPerProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol fakeprovider.Protocol
		body     string
	}{
		{
			name:     "anthropic blocks",
			protocol: fakeprovider.ProtocolAnthropicMessages,
			body:     `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_fake","content":[{"type":"text","text":"` + marker + `\n"}]}]}]}`,
		},
		{
			name:     "responses output with header lines",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"input":[{"type":"function_call_output","call_id":"call_fake","output":"Chunk ID: 702c5d\nProcess exited with code 0\nOutput:\n` + marker + `"}]}`,
		},
		{
			name:     "chat tool message with trailer",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"tool","tool_call_id":"call_fake","content":"` + marker + `\n<shellId: 0 completed with exit code 0>"}]}`,
		},
		{
			name:     "chat tool message holding JSON text",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"tool","tool_call_id":"call_fake","content":"[{\"query\":\"printf ` + marker + `\",\"result\":\"` + marker + `\",\"success\":true}]"}]}`,
		},
		{
			name:     "responses output string",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"input":[{"type":"function_call_output","call_id":"call_fake","output":"` + marker + `\n"}]}`,
		},
		{
			name:     "chat message in responses input",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"input":[{"role":"tool","tool_call_id":"call_fake","content":"` + marker + `"}]}`,
		},
		{
			name:     "chat message in responses messages",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"messages":[{"role":"tool","tool_call_id":"call_fake","content":"` + marker + `"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := recordedRequest(t, test.protocol, test.body)
			if !request.HasToolResult("call_fake", marker) {
				t.Fatalf("tool result with the marker was not found: %+v", request.ToolResults)
			}
			if request.HasToolResult("different_call", marker) {
				t.Fatal("a result for another call matched")
			}
		})
	}
}

func TestToolResultInAnotherProtocolsShapeIsIgnored(t *testing.T) {
	tests := []struct {
		name     string
		protocol fakeprovider.Protocol
		body     string
	}{
		{
			name:     "anthropic block on chat",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_fake","content":"` + marker + `"}]}]}`,
		},
		{
			name:     "responses output on chat",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body:     `{"messages":[{"type":"function_call_output","call_id":"call_fake","output":"` + marker + `"}]}`,
		},
		{
			name:     "chat tool message on anthropic",
			protocol: fakeprovider.ProtocolAnthropicMessages,
			body:     `{"messages":[{"role":"tool","tool_call_id":"call_fake","content":"` + marker + `"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := recordedRequest(t, test.protocol, test.body)
			if request.HasToolResult("call_fake", marker) {
				t.Fatalf("result in another protocol's shape was extracted: %+v", request.ToolResults)
			}
		})
	}
}

func TestToolCallIDMatchingIsTolerant(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		wantOK bool
	}{
		{name: "exact", id: "call_fake", wantOK: true},
		{name: "numeric suffix", id: "call_fake_1", wantOK: true},
		{name: "without call prefix", id: "fake", wantOK: true},
		{name: "other call", id: "call_other", wantOK: false},
		{name: "non-numeric suffix", id: "call_fake_x", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"messages":[{"role":"tool","tool_call_id":%q,"content":%q}]}`, test.id, marker)
			request := recordedRequest(t, fakeprovider.ProtocolOpenAIChat, body)
			if got := request.HasToolResult("call_fake", marker); got != test.wantOK {
				t.Fatalf("HasToolResult for id %q = %t, want %t", test.id, got, test.wantOK)
			}
		})
	}
}

func TestPromptTextIsNotAToolResult(t *testing.T) {
	body := fmt.Sprintf(`{"messages":[{"role":"user","content":"please print %s"}],"tools":[{"type":"function","function":{"name":"shell"}}],"tool_calls":[]}`, marker)
	request := recordedRequest(t, fakeprovider.ProtocolOpenAIChat, body)
	if request.HasToolResult("", marker) {
		t.Fatal("marker in the user prompt was treated as a tool result")
	}
	if request.UserText != "please print "+marker {
		t.Fatalf("UserText = %q", request.UserText)
	}
}

func TestUserTextSkipsToolResultMessages(t *testing.T) {
	body := `{"messages":[` +
		`{"role":"user","content":"first question"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"call_fake","name":"shell","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_fake","content":"` + marker + `"}]}]}`
	request := recordedRequest(t, fakeprovider.ProtocolAnthropicMessages, body)
	if request.UserText != "first question" {
		t.Fatalf("UserText = %q, want the last user message that is not a tool result", request.UserText)
	}
}

func TestToolDeclarationsComeOnlyFromTheToolsField(t *testing.T) {
	declared := recordedRequest(t, fakeprovider.ProtocolOpenAIChat, `{"tools":[{"type":"function","function":{"name":"shell"}}]}`)
	if !declared.HasTool("shell") {
		t.Fatal("tool in function.name was not advertised")
	}
	responses := recordedRequest(t, fakeprovider.ProtocolOpenAIResponses, `{"tools":[{"type":"function","name":"run_terminal_command"}]}`)
	if !responses.HasTool("run_terminal_command") {
		t.Fatal("responses tool name was not advertised")
	}
	prompted := recordedRequest(t, fakeprovider.ProtocolOpenAIChat, `{"stream":false,"prompt":"run shell","tools":[]}`)
	if prompted.HasTool("shell") {
		t.Fatal("tool named only in the prompt was advertised")
	}
}

func TestRequestRecordsModelAndStream(t *testing.T) {
	request := recordedRequest(t, fakeprovider.ProtocolAnthropicMessages, `{"model":"caller-model","stream":true,"messages":[]}`)
	if request.Model != "caller-model" || !request.Stream || request.Method != http.MethodPost || request.Path != "/v1/messages" {
		t.Fatalf("request = model %q stream %t method %q path %q", request.Model, request.Stream, request.Method, request.Path)
	}
}
