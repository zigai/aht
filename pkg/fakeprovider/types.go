package fakeprovider

import (
	"encoding/json/jsontext"
	"time"
)

const (
	ProtocolAnthropicMessages Protocol = "anthropic-messages"
	ProtocolOpenAIChat        Protocol = "openai-chat"
	ProtocolOpenAIResponses   Protocol = "openai-responses"
)

const (
	KindConversation Kind = "conversation"
	KindSide         Kind = "side"
)

const (
	OutcomePending   Outcome = "pending"
	OutcomeCompleted Outcome = "completed"
	OutcomeCanceled  Outcome = "canceled"
	OutcomeRejected  Outcome = "rejected"
	OutcomeDropped   Outcome = "dropped"
)

const (
	StageBeforeReply Stage = "before-reply"
	StageMidStream   Stage = "mid-stream"
)

// Protocol names a model API wire format.
type Protocol string

// Kind separates scripted conversation requests from side requests.
type Kind string

// Outcome is how a recorded request ended.
type Outcome string

// Stage is the point at which a held reply waits for release.
type Stage string

// ToolResult is one tool output the harness sent back to the model.
type ToolResult struct {
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// Request is one model request received by the server.
type Request struct {
	Index       int            `json:"index"`
	Kind        Kind           `json:"kind"`
	Protocol    Protocol       `json:"protocol"`
	Method      string         `json:"method"`
	Path        string         `json:"path"`
	Model       string         `json:"model"`
	Stream      bool           `json:"stream"`
	Tools       []string       `json:"tools"`
	UserText    string         `json:"user_text"`
	ToolResults []ToolResult   `json:"tool_results"`
	Body        jsontext.Value `json:"body"`
	ReceivedAt  time.Time      `json:"received_at"`
	Outcome     Outcome        `json:"outcome"`
}

// ToolCall is one tool invocation the model asks the harness to run.
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ErrorReply makes the server answer with a provider-shaped error.
type ErrorReply struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// Reply is the model output for one conversation request.
type Reply struct {
	Text      string      `json:"text"`
	ToolCalls []ToolCall  `json:"tool_calls"`
	Error     *ErrorReply `json:"error"`
	// HoldMidStream pauses the stream after its first content event.
	HoldMidStream bool `json:"hold_mid_stream"`
	// Disconnect drops the connection after the first content event.
	Disconnect bool `json:"disconnect"`
}

// Script decides the reply to each conversation request.
type Script interface {
	Reply(request Request) (Reply, error)
}

// ScriptFunc adapts a function to Script.
type ScriptFunc func(request Request) (Reply, error)

// Reply calls f.
func (f ScriptFunc) Reply(request Request) (Reply, error) {
	return f(request)
}
