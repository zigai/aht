package hostcompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type providerProtocol string

const (
	protocolAnthropicMessages providerProtocol = "anthropic-messages"
	protocolOpenAIChat        providerProtocol = "openai-chat"
	protocolOpenAIResponses   providerProtocol = "openai-responses"
)

var errInvalidProviderRequest = errors.New("invalid scripted provider request")

type providerRequest struct {
	Method string
	Path   string
	Body   string
}

type scriptedProvider struct {
	server   *httptest.Server
	protocol providerProtocol
	toolName string
	toolArgs map[string]any
	marker   string
	callID   string

	mu                 sync.Mutex
	requests           []providerRequest
	step               int
	err                error
	checkpoints        chan int
	release            chan struct{}
	cancellations      chan int
	expectRejectedTool bool
}

func newScriptedProvider(t *testing.T, protocol providerProtocol, toolName string, toolArgs map[string]any, marker string) *scriptedProvider { //nolint:unparam // marker varies across build-tag variants (e.g. compatibility tag in current_host_test.go)
	t.Helper()
	provider := &scriptedProvider{
		protocol:      protocol,
		toolName:      toolName,
		toolArgs:      toolArgs,
		marker:        marker,
		callID:        "call_compat",
		requests:      make([]providerRequest, 0, 2),
		cancellations: make(chan int, 4),
	}
	if protocol == protocolAnthropicMessages {
		provider.callID = "tool_compat"
	}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.serveHTTP))
	t.Cleanup(provider.server.Close)
	return provider
}

func (provider *scriptedProvider) URL() string {
	return provider.server.URL
}

func (provider *scriptedProvider) Requests() []providerRequest {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]providerRequest(nil), provider.requests...)
}

func (provider *scriptedProvider) Error() error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.err
}

func (provider *scriptedProvider) waitForRelease(request *http.Request, step int) bool {
	if provider.checkpoints == nil {
		return true
	}
	select {
	case provider.checkpoints <- step:
	case <-request.Context().Done():
		if provider.cancellations != nil {
			select {
			case provider.cancellations <- step:
			default:
			}
		}
		return false
	}
	select {
	case <-provider.release:
		return true
	case <-request.Context().Done():
		if provider.cancellations != nil {
			select {
			case provider.cancellations <- step:
			default:
			}
		}
		return false
	}
}

func (provider *scriptedProvider) serveHTTP(writer http.ResponseWriter, request *http.Request) { //nolint:cyclop,gocognit // The cohesive HTTP protocol state machine is clearer as one handler.
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 4<<20))
	if err != nil {
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return
	}

	// Grok probes the provider origin before its first model request.
	if request.Method == http.MethodHead || request.Method == http.MethodGet && request.URL.Path == "/" {
		writer.WriteHeader(http.StatusOK)
		return
	}
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/models") {
		writeJSON(writer, map[string]any{"object": "list", "data": []any{map[string]any{"id": "compat", "object": "model"}}})
		return
	}
	if request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/models/") {
		writeJSON(writer, map[string]any{"id": "compat", "object": "model"})
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/api/show" {
		writeJSON(writer, map[string]any{"model_info": map[string]any{}, "capabilities": []string{"tools"}})
		return
	}

	if request.Method == http.MethodPost && provider.validPath(request.URL.Path) && (strings.Contains(string(body), "You are a title generator") || strings.Contains(string(body), "ultra-short dashboard line") || requestAdvertisesTool(body, "session_title")) {
		provider.writeFinal(writer, body)
		return
	}

	provider.mu.Lock()
	provider.requests = append(provider.requests, providerRequest{Method: request.Method, Path: request.URL.Path, Body: string(body)})
	step := provider.step
	provider.step++
	provider.mu.Unlock()

	if request.Method != http.MethodPost {
		provider.fail(fmt.Errorf("%w: method = %s, want POST", errInvalidProviderRequest, request.Method))
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !provider.validPath(request.URL.Path) {
		provider.fail(fmt.Errorf("%w: path = %s for protocol %s", errInvalidProviderRequest, request.URL.Path, provider.protocol))
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	if !provider.waitForRelease(request, step) {
		return
	}

	switch step {
	case 0:
		if !requestAdvertisesTool(body, provider.toolName) {
			provider.fail(fmt.Errorf("%w: model request does not advertise tool %q", errInvalidProviderRequest, provider.toolName))
			http.Error(writer, "missing tool", http.StatusBadRequest)
			return
		}
		provider.writeToolCall(writer, body)
	case 1:
		if !provider.validToolContinuation(body) {
			provider.fail(fmt.Errorf("%w: tool continuation does not contain valid tool result with marker %q for call %q", errInvalidProviderRequest, provider.marker, provider.callID))
			http.Error(writer, "missing tool result", http.StatusBadRequest)
			return
		}
		provider.writeFinal(writer, body)
	default:
		provider.fail(fmt.Errorf("%w: unexpected model request %d", errInvalidProviderRequest, step+1))
		http.Error(writer, "unexpected request", http.StatusConflict)
	}
}

func (provider *scriptedProvider) validToolContinuation(body []byte) bool {
	if !provider.expectRejectedTool {
		return requestContainsToolResult(provider.protocol, body, provider.callID, provider.marker)
	}
	// A rejected call must still have a correlated tool result, but must not
	// contain the execution marker. The permission fixture independently
	// verifies the command never created its marker file.
	return requestContainsToolResult(provider.protocol, body, provider.callID, "") &&
		!requestContainsToolResult(provider.protocol, body, provider.callID, provider.marker)
}

func (provider *scriptedProvider) validPath(path string) bool {
	switch provider.protocol {
	case protocolAnthropicMessages:
		return strings.HasSuffix(path, "/messages")
	case protocolOpenAIChat:
		return strings.HasSuffix(path, "/chat/completions")
	case protocolOpenAIResponses:
		return strings.HasSuffix(path, "/responses")
	default:
		return false
	}
}

func (provider *scriptedProvider) writeToolCall(writer http.ResponseWriter, body []byte) {
	arguments := mustJSON(provider.toolArgs)
	stream := requestWantsStream(body)
	switch provider.protocol {
	case protocolAnthropicMessages:
		if stream {
			writeSSE(writer,
				`{"type":"message_start","message":{"id":"msg_compat","type":"message","role":"assistant","content":[],"model":"compat","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
				fmt.Sprintf(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, provider.callID, provider.toolName),
				fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":%q}}`, arguments),
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "msg_compat", "type": "message", "role": "assistant", "model": "compat", "content": []any{map[string]any{"type": "tool_use", "id": provider.callID, "name": provider.toolName, "input": provider.toolArgs}}, "stop_reason": "tool_use", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	case protocolOpenAIChat:
		call := map[string]any{"index": 0, "id": provider.callID, "type": "function", "function": map[string]any{"name": provider.toolName, "arguments": arguments}}
		if stream {
			writeSSE(writer,
				fmt.Sprintf(`{"id":"chatcmpl_compat","object":"chat.completion.chunk","created":1,"model":"compat","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[%s]},"finish_reason":null}]}`, mustJSON(call)),
				`{"id":"chatcmpl_compat","object":"chat.completion.chunk","created":1,"model":"compat","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				"[DONE]",
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "chatcmpl_compat", "object": "chat.completion", "created": 1, "model": "compat", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{call}}, "finish_reason": "tool_calls"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	case protocolOpenAIResponses:
		item := map[string]any{"id": "fc_compat", "type": "function_call", "call_id": provider.callID, "name": provider.toolName, "arguments": arguments, "status": "completed"}
		if stream {
			writeSSE(writer,
				fmt.Sprintf(`{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":%s}`, mustJSON(item)),
				fmt.Sprintf(`{"type":"response.output_item.done","sequence_number":1,"output_index":0,"item":%s}`, mustJSON(item)),
				fmt.Sprintf(`{"type":"response.completed","sequence_number":2,"response":{"id":"resp_compat","object":"response","created_at":1,"status":"completed","model":"compat","output":[%s],"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`, mustJSON(item)),
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "resp_compat", "object": "response", "created_at": 1, "status": "completed", "model": "compat", "output": []any{item}, "parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{}, "usage": responsesUsage()})
	}
}

func (provider *scriptedProvider) writeFinal(writer http.ResponseWriter, body []byte) {
	stream := requestWantsStream(body)
	switch provider.protocol {
	case protocolAnthropicMessages:
		if stream {
			writeSSE(writer,
				`{"type":"message_start","message":{"id":"msg_final","type":"message","role":"assistant","content":[],"model":"compat","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"compat complete"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "msg_final", "type": "message", "role": "assistant", "model": "compat", "content": []any{map[string]string{"type": "text", "text": "compat complete"}}, "stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	case protocolOpenAIChat:
		if stream {
			writeSSE(writer,
				`{"id":"chatcmpl_final","object":"chat.completion.chunk","created":1,"model":"compat","choices":[{"index":0,"delta":{"role":"assistant","content":"compat complete"},"finish_reason":null}]}`,
				`{"id":"chatcmpl_final","object":"chat.completion.chunk","created":1,"model":"compat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				"[DONE]",
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "chatcmpl_final", "object": "chat.completion", "created": 1, "model": "compat", "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "compat complete"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
	case protocolOpenAIResponses:
		message := map[string]any{"id": "msg_final", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "compat complete", "annotations": []any{}}}}
		if stream {
			writeSSE(writer,
				fmt.Sprintf(`{"type":"response.output_item.added","sequence_number":0,"output_index":0,"item":%s}`, mustJSON(message)),
				`{"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_final","output_index":0,"content_index":0,"delta":"compat complete"}`,
				`{"type":"response.output_text.done","sequence_number":2,"item_id":"msg_final","output_index":0,"content_index":0,"text":"compat complete"}`,
				fmt.Sprintf(`{"type":"response.output_item.done","sequence_number":3,"output_index":0,"item":%s}`, mustJSON(message)),
				fmt.Sprintf(`{"type":"response.completed","sequence_number":4,"response":{"id":"resp_final","object":"response","created_at":1,"status":"completed","model":"compat","output":[%s],"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"usage":{"input_tokens":1,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":2}}}`, mustJSON(message)),
			)
			return
		}
		writeJSON(writer, map[string]any{"id": "resp_final", "object": "response", "created_at": 1, "status": "completed", "model": "compat", "output": []any{message}, "parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{}, "usage": responsesUsage()})
	}
}

func responsesUsage() map[string]any {
	return map[string]any{
		"input_tokens":         1,
		"input_tokens_details": map[string]int{"cached_tokens": 0},
		"output_tokens":        1,
		"output_tokens_details": map[string]int{
			"reasoning_tokens": 0,
		},
		"total_tokens": 2,
	}
}

func (provider *scriptedProvider) fail(err error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.err == nil {
		provider.err = err
	}
}

func requestAdvertisesTool(body []byte, toolName string) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	tools, ok := value["tools"].([]any)
	if !ok {
		return false
	}
	for _, item := range tools {
		toolMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := toolMap["name"].(string); ok && name == toolName {
			return true
		}
		if fn, ok := toolMap["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name == toolName {
				return true
			}
		}
	}
	return false
}

func matchesCallID(actual string, expected string) bool {
	if expected == "" || actual == expected {
		return true
	}
	actualTrimmed := strings.TrimPrefix(actual, "call_")
	expectedTrimmed := strings.TrimPrefix(expected, "call_")
	if lastUnder := strings.LastIndex(actualTrimmed, "_"); lastUnder >= 0 {
		if _, err := strconv.Atoi(actualTrimmed[lastUnder+1:]); err == nil {
			actualTrimmed = actualTrimmed[:lastUnder]
		}
	}
	return actualTrimmed == expectedTrimmed
}

func anthropicBlockMatchesToolResult(b any, callID, marker string) bool {
	bMap, ok := b.(map[string]any)
	if !ok || bMap["type"] != "tool_result" {
		return false
	}
	toolUseID, ok := bMap["tool_use_id"].(string)
	if !ok || !matchesCallID(toolUseID, callID) {
		return false
	}
	return containsJSONString(bMap["content"], marker)
}

func anthropicContainsToolResult(value map[string]any, callID, marker string) bool {
	messages, ok := value["messages"].([]any)
	if !ok {
		return false
	}
	for _, m := range messages {
		msgMap, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if anthropicBlockMatchesToolResult(b, callID, marker) {
				return true
			}
		}
	}
	return false
}

func openAIChatMessageMatchesToolResult(m any, callID, marker string) bool {
	msgMap, ok := m.(map[string]any)
	if !ok || msgMap["role"] != "tool" {
		return false
	}
	tcID, ok := msgMap["tool_call_id"].(string)
	if !ok || !matchesCallID(tcID, callID) {
		return false
	}
	return containsJSONString(msgMap["content"], marker)
}

func openAIChatContainsToolResult(value map[string]any, callID, marker string) bool {
	messages, ok := value["messages"].([]any)
	if !ok {
		return false
	}
	for _, m := range messages {
		if openAIChatMessageMatchesToolResult(m, callID, marker) {
			return true
		}
	}
	return false
}

func openAIResponsesItemMatchesToolResult(item any, callID, marker string) bool {
	itemMap, ok := item.(map[string]any)
	if !ok {
		return false
	}
	if itemMap["type"] == "function_call_output" {
		cID, ok := itemMap["call_id"].(string)
		if ok && matchesCallID(cID, callID) && containsJSONString(itemMap["output"], marker) {
			return true
		}
	}
	if itemMap["role"] == "tool" {
		cID, ok := itemMap["tool_call_id"].(string)
		if ok && matchesCallID(cID, callID) && containsJSONString(itemMap["content"], marker) {
			return true
		}
	}
	return false
}

func openAIResponsesContainsToolResult(value map[string]any, callID, marker string) bool {
	if inputList, ok := value["input"].([]any); ok {
		for _, item := range inputList {
			if openAIResponsesItemMatchesToolResult(item, callID, marker) {
				return true
			}
		}
	}
	if messages, ok := value["messages"].([]any); ok {
		for _, m := range messages {
			if openAIChatMessageMatchesToolResult(m, callID, marker) {
				return true
			}
		}
	}
	return false
}

func requestContainsToolResult(protocol providerProtocol, body []byte, callID string, marker string) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	switch protocol {
	case protocolAnthropicMessages:
		return anthropicContainsToolResult(value, callID, marker)
	case protocolOpenAIChat:
		return openAIChatContainsToolResult(value, callID, marker)
	case protocolOpenAIResponses:
		return openAIResponsesContainsToolResult(value, callID, marker)
	}
	return false
}

func containsJSONString(value any, expected string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, expected)
	case []any:
		for _, item := range typed {
			if containsJSONString(item, expected) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsJSONString(item, expected) {
				return true
			}
		}
	}
	return false
}

func requestWantsStream(body []byte) bool {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	stream, _ := value["stream"].(bool)
	return stream
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		panic(fmt.Errorf("encode scripted provider response: %w", err))
	}
}

func writeSSE(writer http.ResponseWriter, events ...string) {
	writer.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(event), &payload) == nil && payload.Type != "" {
			_, _ = fmt.Fprintf(writer, "event: %s\n", payload.Type)
		}
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", event)
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestScriptedProviderRequiresToolResult(t *testing.T) {
	provider := newScriptedProvider(t, protocolOpenAIChat, "shell", map[string]any{"command": "printf marker"}, "marker")
	requestBody := `{"stream":false,"tools":[{"type":"function","function":{"name":"shell"}}]}`
	postProviderRequest(t, provider.URL()+"/v1/chat/completions", requestBody, http.StatusOK)
	postProviderRequest(t, provider.URL()+"/v1/chat/completions", `{"messages":[],"tools":[{"type":"function","function":{"name":"shell"}}]}`, http.StatusBadRequest)
	if len(provider.Requests()) != 2 {
		t.Fatalf("request count = %d, want 2", len(provider.Requests()))
	}
	if provider.Error() == nil {
		t.Fatal("provider accepted a continuation without the tool marker")
	}
}

func TestScriptedProviderRootProbeDoesNotAdvanceConversation(t *testing.T) {
	provider := newScriptedProvider(t, protocolOpenAIResponses, "run_terminal_command", map[string]any{"command": "printf marker"}, "marker")
	first := `{"tools":[{"type":"function","name":"run_terminal_command"}]}`
	second := `{"input":[{"type":"function_call_output","call_id":"call_compat","output":"marker"}]}`
	for _, body := range []string{first, second} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, provider.URL()+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := provider.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("probe status = %d, want %d", response.StatusCode, http.StatusOK)
		}
		postProviderRequest(t, provider.URL()+"/v1/responses", body, http.StatusOK)
	}
	if err := provider.Error(); err != nil {
		t.Fatal(err)
	}
	if len(provider.Requests()) != 2 {
		t.Fatalf("model request count = %d, want 2", len(provider.Requests()))
	}
}

func TestScriptedProviderCompletesEveryProtocolAfterToolResult(t *testing.T) {
	tests := []struct {
		name     string
		protocol providerProtocol
		path     string
		tool     string
		first    string
		second   string
	}{
		{
			name: "anthropic", protocol: protocolAnthropicMessages, path: "/v1/messages", tool: "Bash",
			first:  `{"stream":false,"tools":[{"name":"Bash"}]}`,
			second: `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_compat","content":"marker"}]}]}`,
		},
		{
			name: "openai-chat", protocol: protocolOpenAIChat, path: "/v1/chat/completions", tool: "shell",
			first:  `{"stream":false,"tools":[{"type":"function","function":{"name":"shell"}}]}`,
			second: `{"messages":[{"role":"tool","tool_call_id":"call_compat","content":"marker"}]}`,
		},
		{
			name: "openai-responses", protocol: protocolOpenAIResponses, path: "/v1/responses", tool: "shell",
			first:  `{"stream":false,"tools":[{"type":"function","name":"shell"}]}`,
			second: `{"input":[{"type":"function_call_output","call_id":"call_compat","output":"marker"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := newScriptedProvider(t, test.protocol, test.tool, map[string]any{"command": "printf marker"}, "marker")
			postProviderRequest(t, provider.URL()+test.path, test.first, http.StatusOK)
			postProviderRequest(t, provider.URL()+test.path, test.second, http.StatusOK)
			if err := provider.Error(); err != nil {
				t.Fatal(err)
			}
			if len(provider.Requests()) != 2 {
				t.Fatalf("request count = %d, want 2", len(provider.Requests()))
			}
		})
	}
}

func TestScriptedProviderRejectsPromptOnlyMarker(t *testing.T) {
	provider := newScriptedProvider(t, protocolOpenAIChat, "shell", map[string]any{"command": "printf marker"}, "marker")
	first := `{"stream":false,"tools":[{"type":"function","function":{"name":"shell"}}]}`
	postProviderRequest(t, provider.URL()+"/v1/chat/completions", first, http.StatusOK)
	promptOnly := `{"messages":[{"role":"user","content":"please print marker"}],"tools":[{"type":"function","function":{"name":"shell"}}],"tool_calls":[]}`
	postProviderRequest(t, provider.URL()+"/v1/chat/completions", promptOnly, http.StatusBadRequest)
	if provider.Error() == nil {
		t.Fatal("provider accepted a continuation with marker only in user prompt")
	}
}

func TestScriptedProviderRejectsToolDeclarationInNonToolField(t *testing.T) {
	provider := newScriptedProvider(t, protocolOpenAIChat, "shell", map[string]any{"command": "printf marker"}, "marker")
	nonTool := `{"stream":false,"prompt":"run shell","tools":[]}`
	postProviderRequest(t, provider.URL()+"/v1/chat/completions", nonTool, http.StatusBadRequest)
	if provider.Error() == nil {
		t.Fatal("provider accepted a tool request where tool name was in prompt instead of tools declaration")
	}
}

func postProviderRequest(t *testing.T, url string, body string, wantStatus int) string {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("response status = %d, want %d", response.StatusCode, wantStatus)
	}
	return string(data)
}
