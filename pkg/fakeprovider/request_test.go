package fakeprovider_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const marker = "marker-7"

func recordedRequest(
	t *testing.T,
	protocol fakeprovider.Protocol,
	body string,
) fakeprovider.Request {
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
			body: `{"messages":[{"role":"user","content":[{"type":"tool_result"` +
				`,"tool_use_id":"call_fake","content":[{"type":"text",` +
				`"text":"` + marker + `\n"}]}]}]}`,
		},
		{
			name:     "responses output with header lines",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body: `{"input":[{"type":"function_call_output",` +
				`"call_id":"call_fake","output":"Chunk ID: 702c5d\nProcess ` +
				`exited with code 0\nOutput:\n` + marker + `"}]}`,
		},
		{
			name:     "chat tool message with trailer",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_fake",` +
				`"content":"` + marker + `\n<shellId: 0 completed with exit code 0>"}]}`,
		},
		{
			name:     "chat tool message holding JSON text",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"messages":[{"role":"tool","tool_call_id":"call_fake",` +
				`"content":"[{\"query\":\"printf ` + marker + `\",\"result\":\"` + marker + `\",\"success\":true}]"}]}`,
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
		{
			name:     "gemini functionResponse under user role",
			protocol: fakeprovider.ProtocolGemini,
			body: `{"contents":[{"role":"user","parts":[{"functionResponse":` +
				`{"id":"call_fake","name":"run_command","response":{"output":"` + marker + `\n"}}}]}]}`,
		},
		{
			name:     "gemini functionResponse under model role",
			protocol: fakeprovider.ProtocolGemini,
			body: `{"contents":[{"role":"model","parts":[{"functionResponse":` +
				`{"id":"call_fake","name":"run_command","response":{"output":"` + marker + `"}}}]}]}`,
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
			body: `{"messages":[{"role":"user","content":[{"type":"tool_result"` +
				`,"tool_use_id":"call_fake","content":"` + marker + `"}]}]}`,
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
		{
			name:     "gemini functionResponse on chat",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"contents":[{"role":"user","parts":[{"functionResponse":` +
				`{"id":"call_fake","name":"shell","response":{"output":"` + marker + `"}}}]}]}`,
		},
		{
			name:     "chat tool message on gemini",
			protocol: fakeprovider.ProtocolGemini,
			body:     `{"messages":[{"role":"tool","tool_call_id":"call_fake","content":"` + marker + `"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := recordedRequest(t, test.protocol, test.body)
			if request.HasToolResult("call_fake", marker) {
				t.Fatalf(
					"result in another protocol's shape was extracted: %+v",
					request.ToolResults,
				)
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
			body := fmt.Sprintf(
				`{"messages":[{"role":"tool","tool_call_id":%q,"content":%q}]}`,
				test.id,
				marker,
			)

			request := recordedRequest(t, fakeprovider.ProtocolOpenAIChat, body)
			if got := request.HasToolResult("call_fake", marker); got != test.wantOK {
				t.Fatalf("HasToolResult for id %q = %t, want %t", test.id, got, test.wantOK)
			}
		})
	}
}

func TestToolResultFollowsReplacedCallID(t *testing.T) {
	tests := []struct {
		name     string
		protocol fakeprovider.Protocol
		body     string
		wantOK   bool
	}{
		{
			name:     "chat",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_9f3a_0"}]},` +
				`{"role":"tool","tool_call_id":"call_9f3a_0","content":"` + marker + `"}]}`,
			wantOK: true,
		},
		{
			name:     "responses",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body: `{"input":[{"type":"function_call","call_id":"fc_9f3a"},` +
				`{"type":"function_call_output","call_id":"fc_9f3a","output":"` + marker + `"}]}`,
			wantOK: true,
		},
		{
			name:     "anthropic",
			protocol: fakeprovider.ProtocolAnthropicMessages,
			body: `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_9f3a"}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_9f3a",` +
				`"content":"` + marker + `"}]}]}`,
			wantOK: true,
		},
		{
			name:     "gemini",
			protocol: fakeprovider.ProtocolGemini,
			body: `{"contents":[{"role":"model","parts":[{"functionCall":` +
				`{"id":"fc_9f3a","name":"run_command","args":{}}}]},` +
				`{"role":"model","parts":[{"functionResponse":{"id":"fc_9f3a","name":"run_command",` +
				`"response":{"output":"` + marker + `"}}}]}]}`,
			wantOK: true,
		},
		{
			name:     "kept id answered under another id",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_fake"},{"id":"call_9f3a_0"}]},` +
				`{"role":"tool","tool_call_id":"call_9f3a_0","content":"` + marker + `"}]}`,
			wantOK: false,
		},
		{
			name:     "only an older call answered",
			protocol: fakeprovider.ProtocolOpenAIChat,
			body: `{"messages":[{"role":"assistant","tool_calls":[{"id":"call_9f3a_0"}]},` +
				`{"role":"tool","tool_call_id":"call_9f3a_0","content":"` + marker + `"},` +
				`{"role":"assistant","tool_calls":[{"id":"call_77b1_0"}]},` +
				`{"role":"tool","tool_call_id":"call_77b1_0","content":"other output"}]}`,
			wantOK: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := recordedRequest(t, test.protocol, test.body)
			if got := request.HasToolResult("call_fake", marker); got != test.wantOK {
				t.Fatalf("HasToolResult = %t, want %t", got, test.wantOK)
			}
		})
	}
}

func TestPromptTextIsNotAToolResult(t *testing.T) {
	body := fmt.Sprintf(
		`{"messages":[{"role":"user","content":"please print %s"}],`+
			`"tools":[{"type":"function","function":{"name":"shell"}}],`+
			`"tool_calls":[]}`,
		marker,
	)

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
		t.Fatalf(
			"UserText = %q, want the last user message that is not a tool result",
			request.UserText,
		)
	}

	geminiBody := `{"contents":[` +
		`{"role":"user","parts":[{"text":"first gemini question"}]},` +
		`{"role":"model","parts":[{"functionCall":{"id":"call_fake","name":"run_command","args":{}}}]},` +
		`{"role":"user","parts":[{"functionResponse":{"id":"call_fake","name":"run_command",` +
		`"response":{"output":"` + marker + `"}}}]}]}`

	geminiRequest := recordedRequest(t, fakeprovider.ProtocolGemini, geminiBody)
	if geminiRequest.UserText != "first gemini question" {
		t.Fatalf(
			"gemini UserText = %q, want the last user message without functionResponse",
			geminiRequest.UserText,
		)
	}
}

func TestToolDeclarationsComeOnlyFromTheToolsField(t *testing.T) {
	declared := recordedRequest(
		t,
		fakeprovider.ProtocolOpenAIChat,
		`{"tools":[{"type":"function","function":{"name":"shell"}}]}`,
	)
	if !declared.HasTool("shell") {
		t.Fatal("tool in function.name was not advertised")
	}

	responses := recordedRequest(
		t,
		fakeprovider.ProtocolOpenAIResponses,
		`{"tools":[{"type":"function","name":"run_terminal_command"}]}`,
	)
	if !responses.HasTool("run_terminal_command") {
		t.Fatal("responses tool name was not advertised")
	}

	gemini := recordedRequest(
		t,
		fakeprovider.ProtocolGemini,
		`{"tools":[{"functionDeclarations":[{"name":"run_command"},{"name":"read_file"}]}]}`,
	)
	if !gemini.HasTool("run_command") || !gemini.HasTool("read_file") {
		t.Fatalf("gemini tools = %v, want run_command and read_file", gemini.Tools)
	}

	prompted := recordedRequest(
		t,
		fakeprovider.ProtocolOpenAIChat,
		`{"stream":false,"prompt":"run shell","tools":[]}`,
	)
	if prompted.HasTool("shell") {
		t.Fatal("tool named only in the prompt was advertised")
	}
}

func TestRequestRecordsModelAndStream(t *testing.T) {
	request := recordedRequest(
		t,
		fakeprovider.ProtocolAnthropicMessages,
		`{"model":"caller-model","stream":true,"messages":[]}`,
	)
	if request.Model != "caller-model" || !request.Stream || request.Method != http.MethodPost ||
		request.Path != "/v1/messages" {
		t.Fatalf(
			"request = model %q stream %t method %q path %q",
			request.Model,
			request.Stream,
			request.Method,
			request.Path,
		)
	}

	t.Run("gemini path determines model and stream", func(t *testing.T) {
		server := startServer(t, fakeprovider.Options{})
		postJSON(
			t,
			server.URL()+"/v1/models/gemini-2.5-flash:generateContent",
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
		)
		postJSON(
			t,
			server.URL()+"/v1beta/models/gemini-3.1-pro:streamGenerateContent?alt=sse",
			`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
		)

		requests := closeAndRequests(t, server)
		if len(requests) != 2 {
			t.Fatalf("recorded %d requests, want 2", len(requests))
		}

		if requests[0].Protocol != fakeprovider.ProtocolGemini ||
			requests[0].Model != "gemini-2.5-flash" || requests[0].Stream {
			t.Fatalf("generateContent request = %+v", requests[0])
		}

		if requests[1].Protocol != fakeprovider.ProtocolGemini ||
			requests[1].Model != "gemini-3.1-pro" || !requests[1].Stream {
			t.Fatalf("streamGenerateContent request = %+v", requests[1])
		}
	})
}
