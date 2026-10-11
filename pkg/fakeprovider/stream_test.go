package fakeprovider_test

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

func TestStreamsNamedEventsPerProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol fakeprovider.Protocol
		body     string
		events   []string
	}{
		{
			name:     "anthropic",
			protocol: fakeprovider.ProtocolAnthropicMessages,
			body:     `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`,
			events:   []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"},
		},
		{
			name:     "responses",
			protocol: fakeprovider.ProtocolOpenAIResponses,
			body:     `{"stream":true,"input":"hi"}`,
			events:   []string{"response.output_item.added", "response.output_text.delta", "response.output_text.done", "response.output_item.done", "response.completed"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{})
			status, stream := postJSON(t, server.URL()+pathFor(test.protocol), test.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, stream)
			}
			for _, event := range test.events {
				if !strings.Contains(stream, "event: "+event+"\ndata: ") {
					t.Fatalf("stream is missing named %s event:\n%s", event, stream)
				}
			}
		})
	}
}

func TestChatStreamIsDataOnlyAndEndsWithDone(t *testing.T) {
	server := startServer(t, fakeprovider.Options{})
	_, stream := postJSON(t, server.URL()+"/v1/chat/completions", `{"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if strings.Contains(stream, "event: ") {
		t.Fatalf("chat stream has named events:\n%s", stream)
	}
	if !strings.HasSuffix(strings.TrimSpace(stream), "data: [DONE]") {
		t.Fatalf("chat stream does not end with [DONE]:\n%s", stream)
	}
}

func TestMultipleToolCallsEncodeInEveryShape(t *testing.T) {
	calls := []fakeprovider.ToolCall{
		{ID: "call_a", Name: toolName, Arguments: map[string]any{"q": "one"}},
		{ID: "call_b", Name: toolName, Arguments: map[string]any{"q": "two"}},
	}
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "", ToolCalls: calls, Error: nil, HoldMidStream: false, Disconnect: false}, nil
	})
	for _, protocol := range protocols {
		t.Run(string(protocol), func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{Script: script})
			status, complete := postJSON(t, server.URL()+pathFor(protocol), firstRequestBody(protocol, false))
			if status != http.StatusOK {
				t.Fatalf("status = %d: %s", status, complete)
			}
			if got := callIDs(t, protocol, complete); strings.Join(got, ",") != "call_a,call_b" {
				t.Fatalf("call ids = %v, want call_a then call_b", got)
			}
			_, stream := postJSON(t, server.URL()+pathFor(protocol), firstRequestBody(protocol, true))
			first := strings.Index(stream, "call_a")
			second := strings.Index(stream, "call_b")
			if first < 0 || second < first {
				t.Fatalf("stream does not carry call_a before call_b:\n%s", stream)
			}
			if err := server.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func callIDs(t *testing.T, protocol fakeprovider.Protocol, body string) []string {
	t.Helper()
	var value struct {
		Content []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"content"`
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Output []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	var ids []string
	switch protocol {
	case fakeprovider.ProtocolAnthropicMessages:
		for _, block := range value.Content {
			if block.Type == "tool_use" {
				ids = append(ids, block.ID)
			}
		}
	case fakeprovider.ProtocolOpenAIChat:
		if len(value.Choices) > 0 {
			for _, call := range value.Choices[0].Message.ToolCalls {
				ids = append(ids, call.ID)
			}
		}
	case fakeprovider.ProtocolOpenAIResponses:
		for _, item := range value.Output {
			if item.Type == "function_call" {
				ids = append(ids, item.CallID)
			}
		}
	}
	return ids
}

func TestDisconnectAfterFirstContentEventEndsStreamEarly(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "partial", ToolCalls: nil, Error: nil, HoldMidStream: false, Disconnect: true}, nil
	})
	server := startServer(t, fakeprovider.Options{Script: script})
	_, data, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/chat/completions", `{"stream":true,"messages":[]}`)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("stream error = %v, want an unexpected end of stream", err)
	}
	if !strings.Contains(data, "partial") || strings.Contains(data, "[DONE]") {
		t.Fatalf("stream = %q, want the first content event and no end marker", data)
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeDropped {
		t.Fatalf("requests = %+v, want one dropped", requests)
	}
}

func TestDisconnectWithoutStreamDropsBeforeBody(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "never sent", ToolCalls: nil, Error: nil, HoldMidStream: false, Disconnect: true}, nil
	})
	server := startServer(t, fakeprovider.Options{Script: script})
	if _, _, err := send(t.Context(), http.MethodPost, server.URL()+"/v1/responses", `{"input":"hi"}`); err == nil {
		t.Fatal("dropped reply returned a response")
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeDropped {
		t.Fatalf("requests = %+v, want one dropped", requests)
	}
}

func TestMidStreamHoldPausesAfterFirstContentEvent(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "first", ToolCalls: nil, Error: nil, HoldMidStream: true, Disconnect: false}, nil
	})
	server := startServer(t, fakeprovider.Options{Script: script})
	response := startStream(t, t.Context(), server.URL()+"/v1/messages", `{"stream":true,"messages":[]}`)
	defer func() { _ = response.Body.Close() }()
	scanner := bufio.NewScanner(response.Body)
	if !scanLineContaining(scanner, `"text":"first"`) {
		t.Fatal("first content event was not delivered before the hold")
	}
	hold := receiveHold(t, server)
	if hold.Stage != fakeprovider.StageMidStream {
		t.Fatalf("hold stage = %q, want mid-stream", hold.Stage)
	}
	hold.Release()
	if !scanLineContaining(scanner, "message_stop") {
		t.Fatal("stream did not finish after release")
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeCompleted {
		t.Fatalf("requests = %+v, want one completed", requests)
	}
}

func TestClientCancelDuringMidStreamHoldCancelsRequest(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "first", ToolCalls: nil, Error: nil, HoldMidStream: true, Disconnect: false}, nil
	})
	server := startServer(t, fakeprovider.Options{Script: script})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	response := startStream(t, ctx, server.URL()+"/v1/messages", `{"stream":true,"messages":[]}`)
	defer func() { _ = response.Body.Close() }()
	scanner := bufio.NewScanner(response.Body)
	if !scanLineContaining(scanner, `"text":"first"`) {
		t.Fatal("first content event was not delivered before the hold")
	}
	hold := receiveHold(t, server)
	cancel()
	select {
	case <-hold.Done():
	case <-time.After(waitTimeout):
		t.Fatal("Done did not close after the client went away")
	}
	requests := closeAndRequests(t, server)
	if len(requests) != 1 || requests[0].Outcome != fakeprovider.OutcomeCanceled {
		t.Fatalf("requests = %+v, want one canceled", requests)
	}
}

func startStream(t *testing.T, ctx context.Context, target, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func scanLineContaining(scanner *bufio.Scanner, want string) bool {
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), want) {
			return true
		}
	}
	return false
}
