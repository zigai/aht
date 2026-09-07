package hostcompat

import (
	"net/http"
	"strings"
	"testing"
)

func TestScriptedProviderStreamsNamedEvents(t *testing.T) {
	tests := []struct {
		name     string
		protocol providerProtocol
		path     string
		second   string
		events   []string
	}{
		{
			name: "anthropic", protocol: protocolAnthropicMessages, path: "/v1/messages",
			second: `{"stream":true,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_compat","content":"marker"}]}]}`,
			events: []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"},
		},
		{
			name: "responses", protocol: protocolOpenAIResponses, path: "/v1/responses",
			second: `{"stream":true,"input":[{"type":"function_call_output","call_id":"call_compat","output":"marker"}]}`,
			events: []string{"response.output_item.added", "response.output_item.done", "response.completed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := newScriptedProvider(t, test.protocol, "shell", map[string]any{"command": "printf marker"}, "marker")
			for _, body := range []string{`{"stream":true,"tools":[{"name":"shell"}]}`, test.second} {
				stream := postProviderRequest(t, provider.URL()+test.path, body, http.StatusOK)
				for _, event := range test.events {
					if !strings.Contains(stream, "event: "+event+"\ndata: ") {
						t.Fatalf("stream is missing named %s event:\n%s", event, stream)
					}
				}
			}
		})
	}
}

func TestScriptedProviderPreservesCustomToolCallID(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(mustJSON(stream), func(t *testing.T) {
			provider := newScriptedProvider(t, protocolOpenAIChat, "exec", map[string]any{"command": "printf marker"}, "marker")
			provider.callID = "callcompat"
			body := mustJSON(map[string]any{"stream": stream, "tools": []any{map[string]any{"name": "exec"}}})
			response := postProviderRequest(t, provider.URL()+"/v1/chat/completions", body, http.StatusOK)
			if !strings.Contains(response, `"id":"callcompat"`) {
				t.Fatalf("response lost the portable call ID: %s", response)
			}
			postProviderRequest(t, provider.URL()+"/v1/chat/completions", `{"messages":[{"role":"tool","tool_call_id":"callcompat","content":"marker"}]}`, http.StatusOK)
			if err := provider.Error(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
