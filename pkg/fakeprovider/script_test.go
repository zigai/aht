package fakeprovider_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const toolName = "lookup"

var protocols = []fakeprovider.Protocol{
	fakeprovider.ProtocolAnthropicMessages,
	fakeprovider.ProtocolOpenAIChat,
	fakeprovider.ProtocolOpenAIResponses,
}

func toolDeclaration(protocol fakeprovider.Protocol) string {
	if protocol == fakeprovider.ProtocolOpenAIChat {
		return fmt.Sprintf(`{"type":"function","function":{"name":%q}}`, toolName)
	}

	return fmt.Sprintf(`{"type":"function","name":%q}`, toolName)
}

func toolResultBody(protocol fakeprovider.Protocol, callID string, result string) string {
	switch protocol {
	case fakeprovider.ProtocolAnthropicMessages:
		return fmt.Sprintf(
			`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}]}`,
			callID,
			result,
		)
	case fakeprovider.ProtocolOpenAIChat:
		return fmt.Sprintf(
			`{"messages":[{"role":"tool","tool_call_id":%q,"content":%q}]}`,
			callID,
			result,
		)
	case fakeprovider.ProtocolOpenAIResponses:
		return fmt.Sprintf(
			`{"input":[{"type":"function_call_output","call_id":%q,"output":%q}]}`,
			callID,
			result,
		)
	}

	panic("unknown protocol " + string(protocol))
}

func firstRequestBody(protocol fakeprovider.Protocol, stream bool) string {
	return fmt.Sprintf(`{"stream":%t,"tools":[%s]}`, stream, toolDeclaration(protocol))
}

func toolRoundTrip(call fakeprovider.ToolCall) fakeprovider.ToolRoundTrip {
	return fakeprovider.ToolRoundTrip{
		Call:      call,
		Marker:    marker,
		Rejected:  false,
		FinalText: "",
	}
}

func TestEchoRepliesWithUserTextForEveryProtocol(t *testing.T) {
	for _, protocol := range protocols {
		t.Run(string(protocol), func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{})

			body := `{"messages":[{"role":"user","content":"echo-7"}]}`
			if protocol == fakeprovider.ProtocolOpenAIResponses {
				body = `{"input":"echo-7"}`
			}

			status, data := postJSON(t, server.URL()+pathFor(protocol), body)
			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, data)
			}

			if got := replyText(t, protocol, data); got != "echo-7" {
				t.Fatalf("reply = %q, want the user text", got)
			}
		})
	}
}

func TestToolRoundTripCompletesEveryProtocol(t *testing.T) {
	for _, protocol := range protocols {
		t.Run(string(protocol), func(t *testing.T) {
			server := startServer(
				t,
				fakeprovider.Options{
					Script: toolRoundTrip(
						fakeprovider.ToolCall{Name: toolName, Arguments: map[string]any{"q": "x"}},
					),
				},
			)

			status, first := postJSON(
				t,
				server.URL()+pathFor(protocol),
				firstRequestBody(protocol, false),
			)
			if status != http.StatusOK || !strings.Contains(first, toolName) {
				t.Fatalf("first reply = %d %s, want the tool call", status, first)
			}

			callID := "call_fake"
			if protocol == fakeprovider.ProtocolAnthropicMessages {
				callID = "toolu_fake"
			}

			if !strings.Contains(first, callID) {
				t.Fatalf("first reply does not carry default call id %q: %s", callID, first)
			}

			status, second := postJSON(
				t,
				server.URL()+pathFor(protocol),
				toolResultBody(protocol, callID, marker),
			)
			if status != http.StatusOK || replyText(t, protocol, second) != "done" {
				t.Fatalf("second reply = %d %s, want done", status, second)
			}

			if err := server.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestToolRoundTripRejectsMissingTool(t *testing.T) {
	server := startServer(
		t,
		fakeprovider.Options{Script: toolRoundTrip(fakeprovider.ToolCall{Name: toolName})},
	)

	status, _ := postJSON(
		t,
		server.URL()+"/v1/chat/completions",
		`{"stream":false,"prompt":"run lookup","tools":[]}`,
	)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}

	if server.Err() == nil {
		t.Fatal("request without the advertised tool did not set Err")
	}
}

func TestToolRoundTripRejectsResultWithoutMarker(t *testing.T) {
	server := startServer(
		t,
		fakeprovider.Options{Script: toolRoundTrip(fakeprovider.ToolCall{Name: toolName})},
	)
	postJSON(
		t,
		server.URL()+"/v1/chat/completions",
		firstRequestBody(fakeprovider.ProtocolOpenAIChat, false),
	)

	status, _ := postJSON(
		t,
		server.URL()+"/v1/chat/completions",
		toolResultBody(fakeprovider.ProtocolOpenAIChat, "call_fake", "unrelated output"),
	)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}

	if server.Err() == nil {
		t.Fatal("result without the marker did not set Err")
	}
}

func TestToolRoundTripRejectedModeRequiresResultWithoutMarker(t *testing.T) {
	script := fakeprovider.ToolRoundTrip{
		Call:      fakeprovider.ToolCall{Name: toolName},
		Marker:    marker,
		Rejected:  true,
		FinalText: "",
	}

	tests := []struct {
		name   string
		result string
		want   int
	}{
		{name: "result without marker", result: "permission denied", want: http.StatusOK},
		{name: "result with marker", result: marker, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{Script: script})
			postJSON(
				t,
				server.URL()+"/v1/chat/completions",
				firstRequestBody(fakeprovider.ProtocolOpenAIChat, false),
			)

			status, _ := postJSON(
				t,
				server.URL()+"/v1/chat/completions",
				toolResultBody(fakeprovider.ProtocolOpenAIChat, "call_fake", test.result),
			)
			if status != test.want {
				t.Fatalf("status = %d, want %d", status, test.want)
			}
		})
	}
}

func TestToolRoundTripRejectsRequestsAfterTheSecond(t *testing.T) {
	server := startServer(
		t,
		fakeprovider.Options{Script: toolRoundTrip(fakeprovider.ToolCall{Name: toolName})},
	)
	postJSON(
		t,
		server.URL()+"/v1/chat/completions",
		firstRequestBody(fakeprovider.ProtocolOpenAIChat, false),
	)
	postJSON(
		t,
		server.URL()+"/v1/chat/completions",
		toolResultBody(fakeprovider.ProtocolOpenAIChat, "call_fake", marker),
	)

	status, _ := postJSON(t, server.URL()+"/v1/chat/completions", `{"messages":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("third request status = %d, want 400", status)
	}

	if server.Err() == nil {
		t.Fatal("third request did not set Err")
	}
}

func TestToolRoundTripKeepsCustomCallID(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			server := startServer(
				t,
				fakeprovider.Options{
					Script: toolRoundTrip(fakeprovider.ToolCall{ID: "callcompat", Name: "exec"}),
				},
			)
			body := fmt.Sprintf(`{"stream":%t,"tools":[{"name":"exec"}]}`, stream)

			status, first := postJSON(t, server.URL()+"/v1/chat/completions", body)
			if status != http.StatusOK || !strings.Contains(first, "callcompat") {
				t.Fatalf("reply = %d %s, want the custom call id", status, first)
			}

			status, _ = postJSON(
				t,
				server.URL()+"/v1/chat/completions",
				`{"messages":[{"role":"tool","tool_call_id":"callcompat","content":"`+marker+`"}]}`,
			)
			if status != http.StatusOK {
				t.Fatalf("tool result status = %d", status)
			}

			if err := server.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
