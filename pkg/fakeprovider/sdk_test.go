package fakeprovider_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const sdkModel = "fake"

var errSDKStream = errors.New("sdk stream")

type sdkTurn struct {
	text    string
	calls   []sdkCall
	toolUse bool
}

type sdkCall struct {
	id        string
	name      string
	arguments string
}

type sdkAPIError struct {
	status  int
	errType string
	body    string
}

type sdkConversation interface {
	send(ctx context.Context, stream bool, onContent func()) (sdkTurn, error)
	addToolResult(callID, output string)
	apiError(err error) (sdkAPIError, bool)
}

func newSDKConversation(
	protocol fakeprovider.Protocol,
	server *fakeprovider.Server,
	prompt string, tool string,
) sdkConversation {
	switch protocol {
	case fakeprovider.ProtocolAnthropicMessages:
		return newAnthropicConversation(server.URL(), prompt, tool)
	case fakeprovider.ProtocolOpenAIChat:
		return newChatConversation(server.URL(), prompt, tool)
	case fakeprovider.ProtocolOpenAIResponses:
		return newResponsesConversation(server.URL(), prompt, tool)
	}

	panic("unknown protocol " + protocol)
}

var commandSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"command": map[string]any{"type": "string"}},
}

type anthropicConversation struct {
	client   anthropic.Client
	tool     string
	messages []anthropic.MessageParam
}

func newAnthropicConversation(baseURL string, prompt string, tool string) *anthropicConversation {
	return &anthropicConversation{
		client: anthropic.NewClient(
			anthropicoption.WithBaseURL(baseURL),
			anthropicoption.WithAPIKey("fake"),
			anthropicoption.WithMaxRetries(0),
		),
		tool: tool,
		messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	}
}

func (c *anthropicConversation) send(
	ctx context.Context,
	stream bool,
	onContent func(),
) (sdkTurn, error) {
	params := anthropic.MessageNewParams{Model: sdkModel, MaxTokens: 64, Messages: c.messages}
	if c.tool != "" {
		params.Tools = []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{
			Name:        c.tool,
			InputSchema: anthropic.ToolInputSchemaParam{Properties: commandSchema["properties"]},
		}}}
	}

	var message anthropic.Message

	if stream {
		var err error
		if message, err = c.stream(ctx, params, onContent); err != nil {
			return sdkTurn{}, err
		}
	} else {
		reply, err := c.client.Messages.New(ctx, params)
		if err != nil {
			return sdkTurn{}, fmt.Errorf("create message: %w", err)
		}

		message = *reply
	}

	c.messages = append(c.messages, message.ToParam())

	turn := sdkTurn{toolUse: message.StopReason == anthropic.StopReasonToolUse}
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			turn.text += block.Text
		case "tool_use":
			turn.calls = append(
				turn.calls,
				sdkCall{id: block.ID, name: block.Name, arguments: string(block.Input)},
			)
		}
	}

	return turn, nil
}

func (c *anthropicConversation) stream(
	ctx context.Context,
	params anthropic.MessageNewParams,
	onContent func(),
) (anthropic.Message, error) {
	events := c.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = events.Close() }()

	var message anthropic.Message

	for events.Next() {
		event := events.Current()
		if event.AsAny() == nil {
			return anthropic.Message{}, fmt.Errorf("%w: unknown event %q", errSDKStream, event.Type)
		}

		if err := message.Accumulate(event); err != nil {
			return anthropic.Message{}, fmt.Errorf("accumulate %s: %w", event.Type, err)
		}

		if event.Type == "content_block_delta" && onContent != nil {
			onContent()
			onContent = nil
		}
	}

	if err := events.Err(); err != nil {
		return anthropic.Message{}, fmt.Errorf("stream messages: %w", err)
	}

	return message, nil
}

func (c *anthropicConversation) addToolResult(callID string, output string) {
	c.messages = append(
		c.messages,
		anthropic.NewUserMessage(anthropic.NewToolResultBlock(callID, output, false)),
	)
}

func (*anthropicConversation) apiError(err error) (sdkAPIError, bool) {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return sdkAPIError{}, false
	}

	return sdkAPIError{
		status:  apiErr.StatusCode,
		errType: string(apiErr.Type()),
		body:    apiErr.RawJSON(),
	}, true
}

type chatConversation struct {
	client   openai.Client
	tool     string
	messages []openai.ChatCompletionMessageParamUnion
}

func newChatConversation(baseURL string, prompt string, tool string) *chatConversation {
	return &chatConversation{
		client:   openai.NewClient(openaiOptions(baseURL)...),
		tool:     tool,
		messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage(prompt)},
	}
}

func openaiOptions(baseURL string) []openaioption.RequestOption {
	return []openaioption.RequestOption{
		openaioption.WithBaseURL(baseURL + "/v1/"),
		openaioption.WithAPIKey("fake"),
		openaioption.WithMaxRetries(0),
	}
}

func (c *chatConversation) send(
	ctx context.Context,
	stream bool,
	onContent func(),
) (sdkTurn, error) {
	params := openai.ChatCompletionNewParams{Model: sdkModel, Messages: c.messages}
	if c.tool != "" {
		params.Tools = []openai.ChatCompletionToolUnionParam{
			openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
				Name:       c.tool,
				Parameters: commandSchema,
			}),
		}
	}

	var choices []openai.ChatCompletionChoice

	if stream {
		var err error
		if choices, err = c.stream(ctx, params, onContent); err != nil {
			return sdkTurn{}, err
		}
	} else {
		completion, err := c.client.Chat.Completions.New(ctx, params)
		if err != nil {
			return sdkTurn{}, fmt.Errorf("create chat completion: %w", err)
		}

		choices = completion.Choices
	}

	if len(choices) != 1 {
		return sdkTurn{}, fmt.Errorf("%w: %d choices, want 1", errSDKStream, len(choices))
	}

	message := choices[0].Message
	c.messages = append(c.messages, message.ToParam())

	turn := sdkTurn{text: message.Content, toolUse: choices[0].FinishReason == "tool_calls"}
	for _, call := range message.ToolCalls {
		turn.calls = append(
			turn.calls,
			sdkCall{id: call.ID, name: call.Function.Name, arguments: call.Function.Arguments},
		)
	}

	return turn, nil
}

func (c *chatConversation) stream(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	onContent func(),
) ([]openai.ChatCompletionChoice, error) {
	chunks := c.client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = chunks.Close() }()

	var completion openai.ChatCompletionAccumulator

	for chunks.Next() {
		chunk := chunks.Current()
		if !completion.AddChunk(chunk) {
			return nil, fmt.Errorf(
				"%w: accumulator rejected chunk %s",
				errSDKStream,
				chunk.RawJSON(),
			)
		}

		if onContent != nil {
			onContent()
			onContent = nil
		}
	}

	if err := chunks.Err(); err != nil {
		return nil, fmt.Errorf("stream chat completion: %w", err)
	}

	return completion.Choices, nil
}

func (c *chatConversation) addToolResult(callID string, output string) {
	c.messages = append(c.messages, openai.ToolMessage(output, callID))
}

func (*chatConversation) apiError(err error) (sdkAPIError, bool) {
	return openaiAPIError(err)
}

func openaiAPIError(err error) (sdkAPIError, bool) {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return sdkAPIError{}, false
	}

	return sdkAPIError{status: apiErr.StatusCode, errType: apiErr.Type, body: apiErr.Message}, true
}

type responsesConversation struct {
	client openai.Client
	tool   string
	items  []responses.ResponseInputItemUnionParam
}

func newResponsesConversation(baseURL string, prompt string, tool string) *responsesConversation {
	return &responsesConversation{
		client: openai.NewClient(openaiOptions(baseURL)...),
		tool:   tool,
		items: []responses.ResponseInputItemUnionParam{
			responses.ResponseInputItemParamOfMessage(prompt, responses.EasyInputMessageRoleUser),
		},
	}
}

func (c *responsesConversation) send(
	ctx context.Context,
	stream bool,
	onContent func(),
) (sdkTurn, error) {
	params := responses.ResponseNewParams{
		Model: sdkModel,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: c.items},
	}
	if c.tool != "" {
		params.Tools = []responses.ToolUnionParam{
			responses.ToolParamOfFunction(c.tool, commandSchema, false),
		}
	}

	var response responses.Response

	if stream {
		var err error
		if response, err = c.stream(ctx, params, onContent); err != nil {
			return sdkTurn{}, err
		}
	} else {
		reply, err := c.client.Responses.New(ctx, params)
		if err != nil {
			return sdkTurn{}, fmt.Errorf("create response: %w", err)
		}

		response = *reply
	}

	if response.Status != responses.ResponseStatusCompleted {
		return sdkTurn{}, fmt.Errorf("%w: response status %q", errSDKStream, response.Status)
	}

	turn := sdkTurn{text: response.OutputText()}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			c.items = append(
				c.items,
				responses.ResponseInputItemParamOfMessage(
					item.AsMessage().Content[0].Text,
					responses.EasyInputMessageRoleAssistant,
				),
			)
		case "function_call":
			call := item.AsFunctionCall()
			turn.calls = append(
				turn.calls,
				sdkCall{id: call.CallID, name: call.Name, arguments: call.Arguments},
			)
			c.items = append(
				c.items,
				responses.ResponseInputItemParamOfFunctionCall(
					call.Arguments,
					call.CallID,
					call.Name,
				),
			)
		}
	}

	turn.toolUse = len(turn.calls) > 0

	return turn, nil
}

func (c *responsesConversation) stream(
	ctx context.Context,
	params responses.ResponseNewParams,
	onContent func(),
) (responses.Response, error) {
	events := c.client.Responses.NewStreaming(ctx, params)
	defer func() { _ = events.Close() }()

	var completed *responses.Response

	deltas := ""

	for events.Next() {
		event := events.Current()
		switch typed := event.AsAny().(type) {
		case responses.ResponseOutputItemAddedEvent:
			if onContent != nil {
				onContent()
				onContent = nil
			}
		case responses.ResponseTextDeltaEvent:
			deltas += typed.Delta
		case responses.ResponseCompletedEvent:
			completed = &typed.Response
		case nil:
			return responses.Response{}, fmt.Errorf(
				"%w: unknown event %q",
				errSDKStream,
				event.Type,
			)
		}
	}

	if err := events.Err(); err != nil {
		return responses.Response{}, fmt.Errorf("stream response: %w", err)
	}

	if completed == nil {
		return responses.Response{}, fmt.Errorf("%w: no response.completed event", errSDKStream)
	}

	if deltas != completed.OutputText() {
		return responses.Response{}, fmt.Errorf(
			"%w: text deltas %q differ from completed text %q",
			errSDKStream,
			deltas,
			completed.OutputText(),
		)
	}

	return *completed, nil
}

func (c *responsesConversation) addToolResult(callID string, output string) {
	item := responses.ResponseInputItemParamOfFunctionCallOutput(output)
	item.OfFunctionCallOutput.CallID = openai.String(callID)
	c.items = append(c.items, item)
}

func (*responsesConversation) apiError(err error) (sdkAPIError, bool) {
	return openaiAPIError(err)
}

type sdkClientCase struct {
	name     string
	protocol fakeprovider.Protocol
	stream   bool
}

func sdkClientCases() []sdkClientCase {
	cases := make([]sdkClientCase, 0, 2*len(protocols))
	for _, protocol := range protocols {
		cases = append(cases,
			sdkClientCase{name: string(protocol) + "/complete", protocol: protocol, stream: false},
			sdkClientCase{name: string(protocol) + "/stream", protocol: protocol, stream: true},
		)
	}

	return cases
}

func TestSDKClientsReadTextReplies(t *testing.T) {
	for _, client := range sdkClientCases() {
		t.Run(client.name, func(t *testing.T) {
			protocol, stream := client.protocol, client.stream
			server := startServer(t, fakeprovider.Options{Script: fakeprovider.Echo()})

			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()

			turn, err := newSDKConversation(
				protocol,
				server,
				"say this back",
				"",
			).send(ctx, stream, nil)
			if err != nil {
				t.Fatal(err)
			}

			if turn.text != "say this back" || len(turn.calls) != 0 || turn.toolUse {
				t.Fatalf("turn = %+v, want only the echoed text", turn)
			}

			if err := server.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSDKClientsCompleteToolRoundTrips(t *testing.T) {
	for _, client := range sdkClientCases() {
		t.Run(client.name, func(t *testing.T) {
			protocol, stream := client.protocol, client.stream
			server := startServer(t, fakeprovider.Options{Script: fakeprovider.ToolRoundTrip{
				Call: fakeprovider.ToolCall{
					Name:      toolName,
					Arguments: map[string]any{"command": "printf marker"},
				},
				Marker:    "marker-output",
				FinalText: "finished",
			}})

			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()

			conversation := newSDKConversation(protocol, server, "run the tool", toolName)

			first, err := conversation.send(ctx, stream, nil)
			if err != nil {
				t.Fatal(err)
			}

			call := requireScriptedCall(t, first)
			conversation.addToolResult(call.id, "marker-output\n")

			final, err := conversation.send(ctx, stream, nil)
			if err != nil {
				t.Fatal(err)
			}

			if final.text != "finished" || len(final.calls) != 0 || final.toolUse {
				t.Fatalf("final turn = %+v, want the final text", final)
			}

			if err := server.Err(); err != nil {
				t.Fatalf("server did not accept the SDK's tool result: %v", err)
			}
		})
	}
}

func requireScriptedCall(t *testing.T, turn sdkTurn) sdkCall {
	t.Helper()

	if len(turn.calls) != 1 || !turn.toolUse {
		t.Fatalf("turn = %+v, want one tool call", turn)
	}

	call := turn.calls[0]

	var arguments map[string]any
	if err := json.Unmarshal([]byte(call.arguments), &arguments); err != nil {
		t.Fatalf("arguments %q: %v", call.arguments, err)
	}

	if call.id == "" || call.name != toolName || arguments["command"] != "printf marker" {
		t.Fatalf("call = %+v, want %s with the scripted command", call, toolName)
	}

	return call
}

func TestSDKClientsReadParallelToolCallsInOrder(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "two lookups", ToolCalls: []fakeprovider.ToolCall{
			{ID: "call_a", Name: toolName, Arguments: map[string]any{"command": "one"}},
			{ID: "call_b", Name: toolName, Arguments: nil},
		}}, nil
	})

	for _, client := range sdkClientCases() {
		t.Run(client.name, func(t *testing.T) {
			protocol, stream := client.protocol, client.stream
			server := startServer(t, fakeprovider.Options{Script: script})

			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()

			turn, err := newSDKConversation(
				protocol,
				server,
				"look up two",
				toolName,
			).send(ctx, stream, nil)
			if err != nil {
				t.Fatal(err)
			}

			want := []sdkCall{
				{id: "call_a", name: toolName, arguments: `{"command":"one"}`},
				{id: "call_b", name: toolName, arguments: `{}`},
			}
			if turn.text != "two lookups" || !turn.toolUse ||
				fmt.Sprint(turn.calls) != fmt.Sprint(want) {
				t.Fatalf("turn = %+v, want the text and calls %+v", turn, want)
			}
		})
	}
}

func TestSDKClientsSurfaceErrorReplies(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{
			Error: &fakeprovider.ErrorReply{
				Status:  http.StatusTooManyRequests,
				Message: "slow down",
			},
		}, nil
	})
	wantType := map[fakeprovider.Protocol]string{
		fakeprovider.ProtocolAnthropicMessages: "api_error",
		fakeprovider.ProtocolOpenAIChat:        "server_error",
		fakeprovider.ProtocolOpenAIResponses:   "server_error",
	}

	for _, client := range sdkClientCases() {
		t.Run(client.name, func(t *testing.T) {
			protocol, stream := client.protocol, client.stream
			server := startServer(t, fakeprovider.Options{Script: script})

			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()

			conversation := newSDKConversation(protocol, server, "hello", "")
			_, err := conversation.send(ctx, stream, nil)

			apiErr, ok := conversation.apiError(err)
			if !ok {
				t.Fatalf("error = %v, want an SDK API error", err)
			}

			if apiErr.status != http.StatusTooManyRequests || apiErr.errType != wantType[protocol] {
				t.Fatalf("api error = %+v, want status 429 and type %s", apiErr, wantType[protocol])
			}

			if !strings.Contains(apiErr.body, "slow down") {
				t.Fatalf("api error = %+v, want the scripted message", apiErr)
			}
		})
	}
}

func TestSDKStreamsDeliverContentBeforeMidStreamHoldRelease(t *testing.T) {
	script := fakeprovider.ScriptFunc(func(fakeprovider.Request) (fakeprovider.Reply, error) {
		return fakeprovider.Reply{Text: "held reply", HoldMidStream: true}, nil
	})

	for _, protocol := range protocols {
		t.Run(string(protocol), func(t *testing.T) {
			server := startServer(t, fakeprovider.Options{Script: script})

			ctx, cancel := context.WithTimeout(t.Context(), waitTimeout)
			defer cancel()

			released := false

			turn, err := newSDKConversation(protocol, server, "hello", "").send(ctx, true, func() {
				hold := receiveHold(t, server)
				if hold.Stage != fakeprovider.StageMidStream {
					t.Fatalf("hold stage = %q, want mid-stream", hold.Stage)
				}

				hold.Release()

				released = true
			})
			if err != nil {
				t.Fatal(err)
			}

			if !released || turn.text != "held reply" {
				t.Fatalf(
					"released = %v, turn = %+v; want the full text after release",
					released,
					turn,
				)
			}
		})
	}
}
