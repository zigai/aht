package fakeprovider

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
)

type sseEvent struct {
	name    string
	data    string
	content bool
}

type eventWriter struct {
	events   []sseEvent
	marked   bool
	sequence int
	err      error
}

type anthropicMessageStart struct {
	Type    string           `json:"type"`
	Message anthropicMessage `json:"message"`
}

type anthropicBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type anthropicBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type anthropicTextDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicInputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type anthropicBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type anthropicMessageDelta struct {
	Type  string              `json:"type"`
	Delta anthropicStopDelta  `json:"delta"`
	Usage anthropicDeltaUsage `json:"usage"`
}

type anthropicStopDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type anthropicDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessageStop struct {
	Type string `json:"type"`
}

type chatChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int               `json:"created"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
}

type chatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Role      string         `json:"role,omitempty"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type responsesItemEvent struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	OutputIndex    int    `json:"output_index"`
	Item           any    `json:"item"`
}

type responsesTextDelta struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Delta          string `json:"delta"`
}

type responsesTextDone struct {
	Type           string `json:"type"`
	SequenceNumber int    `json:"sequence_number"`
	ItemID         string `json:"item_id"`
	OutputIndex    int    `json:"output_index"`
	ContentIndex   int    `json:"content_index"`
	Text           string `json:"text"`
}

type responsesCompleted struct {
	Type           string         `json:"type"`
	SequenceNumber int            `json:"sequence_number"`
	Response       responseObject `json:"response"`
}

func (w *eventWriter) add(name string, payload any, carriesContent bool) {
	if w.err != nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		w.err = fmt.Errorf("encode stream event: %w", err)
		return
	}
	w.addRaw(name, string(data), carriesContent)
}

func (w *eventWriter) addRaw(name, data string, carriesContent bool) {
	event := sseEvent{name: name, data: data, content: carriesContent && !w.marked}
	if event.content {
		w.marked = true
	}
	w.events = append(w.events, event)
}

func (w *eventWriter) nextSequence() int {
	n := w.sequence
	w.sequence++
	return n
}

func streamEvents(protocol Protocol, model string, reply Reply) ([]sseEvent, error) {
	calls, err := encodeCalls(reply.ToolCalls)
	if err != nil {
		return nil, err
	}
	var out eventWriter
	switch protocol {
	case ProtocolAnthropicMessages:
		anthropicStream(&out, model, reply, calls)
	case ProtocolOpenAIChat:
		chatStream(&out, model, reply, calls)
	case ProtocolOpenAIResponses:
		responsesStream(&out, model, reply, calls)
	}
	if out.err != nil {
		return nil, out.err
	}
	return out.events, nil
}

func anthropicStream(out *eventWriter, model string, reply Reply, calls []callJSON) {
	out.add("message_start", anthropicMessageStart{Type: "message_start", Message: newAnthropicMessage(model, []any{}, nil)}, false)
	index := 0
	if includesText(reply) {
		anthropicBlock(out, index, anthropicText{Type: "text", Text: ""}, anthropicTextDelta{Type: "text_delta", Text: reply.Text})
		index++
	}
	for _, call := range calls {
		start := anthropicToolUse{Type: "tool_use", ID: call.ID, Name: call.Name, Input: jsontext.Value("{}")}
		anthropicBlock(out, index, start, anthropicInputJSONDelta{Type: "input_json_delta", PartialJSON: string(call.Arguments)})
		index++
	}
	out.add("message_delta", anthropicMessageDelta{
		Type:  "message_delta",
		Delta: anthropicStopDelta{StopReason: anthropicStopReason(calls), StopSequence: nil},
		Usage: anthropicDeltaUsage{OutputTokens: 1},
	}, false)
	out.add("message_stop", anthropicMessageStop{Type: "message_stop"}, false)
}

func anthropicBlock(out *eventWriter, index int, start, delta any) {
	out.add("content_block_start", anthropicBlockStart{Type: "content_block_start", Index: index, ContentBlock: start}, false)
	out.add("content_block_delta", anthropicBlockDelta{Type: "content_block_delta", Index: index, Delta: delta}, true)
	out.add("content_block_stop", anthropicBlockStop{Type: "content_block_stop", Index: index}, false)
}

func chatStream(out *eventWriter, model string, reply Reply, calls []callJSON) {
	delta := chatDelta{Role: "assistant", Content: reply.Text, ToolCalls: chatToolCalls(calls)}
	out.add("", newChatChunk(model, delta, nil), true)
	out.add("", newChatChunk(model, chatDelta{Role: "", Content: "", ToolCalls: nil}, new(chatFinishReason(calls))), false)
	out.addRaw("", "[DONE]", false)
}

func newChatChunk(model string, delta chatDelta, finishReason *string) chatChunk {
	return chatChunk{
		ID:      "chatcmpl_fake",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   model,
		Choices: []chatChunkChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
	}
}

func responsesStream(out *eventWriter, model string, reply Reply, calls []callJSON) {
	index := 0
	if includesText(reply) {
		item := newMessageItem(reply.Text)
		out.add("response.output_item.added", responsesItemEvent{Type: "response.output_item.added", SequenceNumber: out.nextSequence(), OutputIndex: index, Item: item}, true)
		out.add("response.output_text.delta", responsesTextDelta{Type: "response.output_text.delta", SequenceNumber: out.nextSequence(), ItemID: item.ID, OutputIndex: index, ContentIndex: 0, Delta: reply.Text}, false)
		out.add("response.output_text.done", responsesTextDone{Type: "response.output_text.done", SequenceNumber: out.nextSequence(), ItemID: item.ID, OutputIndex: index, ContentIndex: 0, Text: reply.Text}, false)
		out.add("response.output_item.done", responsesItemEvent{Type: "response.output_item.done", SequenceNumber: out.nextSequence(), OutputIndex: index, Item: item}, false)
		index++
	}
	for _, call := range calls {
		item := newFunctionItem(call)
		out.add("response.output_item.added", responsesItemEvent{Type: "response.output_item.added", SequenceNumber: out.nextSequence(), OutputIndex: index, Item: item}, true)
		out.add("response.output_item.done", responsesItemEvent{Type: "response.output_item.done", SequenceNumber: out.nextSequence(), OutputIndex: index, Item: item}, false)
		index++
	}
	out.add("response.completed", responsesCompleted{Type: "response.completed", SequenceNumber: out.nextSequence(), Response: newResponseObject(model, responseOutput(reply, calls))}, false)
}

func writeEvent(w io.Writer, event sseEvent) bool {
	frame := "data: " + event.data + "\n\n"
	if event.name != "" {
		frame = "event: " + event.name + "\n" + frame
	}
	_, err := io.WriteString(w, frame)
	return err == nil
}
