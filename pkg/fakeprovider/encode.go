package fakeprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

const usageTotalTokens = 2

type callJSON struct {
	ID        string
	Name      string
	Arguments jsontext.Value
}

type anthropicMessage struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []any          `json:"content"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthropicUsage `json:"usage"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicToolUse struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input jsontext.Value `json:"input"`
}

type chatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int          `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   *string        `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type responseObject struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	CreatedAt         int            `json:"created_at"`
	Status            string         `json:"status"`
	Model             string         `json:"model"`
	Output            []any          `json:"output"`
	ParallelToolCalls bool           `json:"parallel_tool_calls"`
	ToolChoice        string         `json:"tool_choice"`
	Tools             []any          `json:"tools"`
	Usage             responsesUsage `json:"usage"`
}

type responsesUsage struct {
	InputTokens         int                 `json:"input_tokens"`
	InputTokensDetails  inputTokensDetails  `json:"input_tokens_details"`
	OutputTokens        int                 `json:"output_tokens"`
	OutputTokensDetails outputTokensDetails `json:"output_tokens_details"`
	TotalTokens         int                 `json:"total_tokens"`
}

type inputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type outputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type messageItem struct {
	ID      string       `json:"id"`
	Type    string       `json:"type"`
	Role    string       `json:"role"`
	Status  string       `json:"status"`
	Content []outputText `json:"content"`
}

type outputText struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type functionItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Status    string `json:"status"`
}

type anthropicError struct {
	Type  string      `json:"type"`
	Error errorDetail `json:"error"`
}

type openAIError struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func encodeCalls(calls []ToolCall) ([]callJSON, error) {
	encoded := make([]callJSON, 0, len(calls))
	for _, call := range calls {
		data, err := json.Marshal(call.Arguments, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("encode arguments of tool call %q: %w", call.Name, err)
		}
		encoded = append(encoded, callJSON{ID: call.ID, Name: call.Name, Arguments: data})
	}
	return encoded, nil
}

func includesText(reply Reply) bool {
	return reply.Text != "" || len(reply.ToolCalls) == 0
}

func completeBody(protocol Protocol, model string, reply Reply) ([]byte, error) {
	calls, err := encodeCalls(reply.ToolCalls)
	if err != nil {
		return nil, err
	}
	var body any
	switch protocol {
	case ProtocolAnthropicMessages:
		body = newAnthropicMessage(model, anthropicContent(reply, calls), new(anthropicStopReason(calls)))
	case ProtocolOpenAIChat:
		body = newChatCompletion(model, reply, calls)
	case ProtocolOpenAIResponses:
		body = newResponseObject(model, responseOutput(reply, calls))
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode reply: %w", err)
	}
	return data, nil
}

func newAnthropicMessage(model string, content []any, stopReason *string) anthropicMessage {
	return anthropicMessage{
		ID:           "msg_fake",
		Type:         "message",
		Role:         "assistant",
		Model:        model,
		Content:      content,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage:        anthropicUsage{InputTokens: 1, OutputTokens: 1},
	}
}

func anthropicContent(reply Reply, calls []callJSON) []any {
	content := make([]any, 0, len(calls)+1)
	if includesText(reply) {
		content = append(content, anthropicText{Type: "text", Text: reply.Text})
	}
	for _, call := range calls {
		content = append(content, anthropicToolUse{Type: "tool_use", ID: call.ID, Name: call.Name, Input: call.Arguments})
	}
	return content
}

func anthropicStopReason(calls []callJSON) string {
	if len(calls) > 0 {
		return "tool_use"
	}
	return "end_turn"
}

func newChatCompletion(model string, reply Reply, calls []callJSON) chatCompletion {
	return chatCompletion{
		ID:      "chatcmpl_fake",
		Object:  "chat.completion",
		Created: 1,
		Model:   model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: "assistant", Content: chatContent(reply, calls), ToolCalls: chatToolCalls(calls)},
			FinishReason: chatFinishReason(calls),
		}},
		Usage: chatUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: usageTotalTokens},
	}
}

func chatContent(reply Reply, calls []callJSON) *string {
	if reply.Text == "" && len(calls) > 0 {
		return nil
	}
	return &reply.Text
}

func chatFinishReason(calls []callJSON) string {
	if len(calls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func chatToolCalls(calls []callJSON) []chatToolCall {
	out := make([]chatToolCall, 0, len(calls))
	for i, call := range calls {
		out = append(out, chatToolCall{
			Index:    i,
			ID:       call.ID,
			Type:     "function",
			Function: chatFunction{Name: call.Name, Arguments: string(call.Arguments)},
		})
	}
	return out
}

func newResponseObject(model string, output []any) responseObject {
	return responseObject{
		ID:                "resp_fake",
		Object:            "response",
		CreatedAt:         1,
		Status:            "completed",
		Model:             model,
		Output:            output,
		ParallelToolCalls: true,
		ToolChoice:        "auto",
		Tools:             []any{},
		Usage: responsesUsage{
			InputTokens:         1,
			InputTokensDetails:  inputTokensDetails{CachedTokens: 0},
			OutputTokens:        1,
			OutputTokensDetails: outputTokensDetails{ReasoningTokens: 0},
			TotalTokens:         usageTotalTokens,
		},
	}
}

func responseOutput(reply Reply, calls []callJSON) []any {
	output := make([]any, 0, len(calls)+1)
	if includesText(reply) {
		output = append(output, newMessageItem(reply.Text))
	}
	for _, call := range calls {
		output = append(output, newFunctionItem(call))
	}
	return output
}

func newMessageItem(text string) messageItem {
	return messageItem{
		ID:      "msg_fake",
		Type:    "message",
		Role:    "assistant",
		Status:  "completed",
		Content: []outputText{{Type: "output_text", Text: text, Annotations: []any{}}},
	}
}

func newFunctionItem(call callJSON) functionItem {
	return functionItem{
		ID:        "fc_" + call.ID,
		Type:      "function_call",
		CallID:    call.ID,
		Name:      call.Name,
		Arguments: string(call.Arguments),
		Status:    "completed",
	}
}

func errorBody(protocol Protocol, message string) ([]byte, error) {
	var body any
	switch protocol {
	case ProtocolAnthropicMessages:
		body = anthropicError{Type: "error", Error: errorDetail{Type: "api_error", Message: message}}
	case ProtocolOpenAIChat, ProtocolOpenAIResponses:
		body = openAIError{Error: errorDetail{Type: "server_error", Message: message}}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode error reply: %w", err)
	}
	return data, nil
}
