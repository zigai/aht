package fakeprovider

import (
	"errors"
	"fmt"
)

const (
	defaultFinalText = "done"
	maxErrorStatus   = 599
	minErrorStatus   = 400
)

var (
	errInvalidReply  = errors.New("invalid scripted reply")
	errToolRoundTrip = errors.New("tool round trip")
)

// ToolRoundTrip answers with Call, then with FinalText once the tool result arrives.
type ToolRoundTrip struct {
	Call      ToolCall // ID defaults to "call_fake" (OpenAI) or "toolu_fake" (Anthropic)
	Marker    string   // the tool result must contain it ("" = any output)
	Rejected  bool     // the tool result must exist and must NOT contain Marker
	FinalText string   // default "done"
}

// Reply implements [Script].
func (r ToolRoundTrip) Reply(request Request) (Reply, error) {
	callID := r.callID(request.Protocol)
	switch request.Index {
	case 0:
		if !request.HasTool(r.Call.Name) {
			return Reply{}, fmt.Errorf(
				"%w: request does not advertise tool %q",
				errToolRoundTrip,
				r.Call.Name,
			)
		}

		call := r.Call
		call.ID = callID

		return callReply(call), nil
	case 1:
		return r.finalReply(request, callID)
	}

	return Reply{}, fmt.Errorf("%w: unexpected model request %d", errToolRoundTrip, request.Index+1)
}

// Echo replies to each conversation request with its user text.
func Echo() Script {
	return ScriptFunc(func(request Request) (Reply, error) {
		return textReply(request.UserText), nil
	})
}

func (r ToolRoundTrip) callID(protocol Protocol) string {
	if r.Call.ID != "" {
		return r.Call.ID
	}

	if protocol == ProtocolAnthropicMessages {
		return "toolu_fake"
	}

	return "call_fake"
}

func (r ToolRoundTrip) finalReply(request Request, callID string) (Reply, error) {
	if r.Rejected {
		if !request.HasToolResult(callID, "") {
			return Reply{}, fmt.Errorf(
				"%w: missing tool result for call %q",
				errToolRoundTrip,
				callID,
			)
		}

		if r.Marker != "" && request.HasToolResult(callID, r.Marker) {
			return Reply{}, fmt.Errorf(
				"%w: rejected tool result contains marker %q",
				errToolRoundTrip,
				r.Marker,
			)
		}
	} else if !request.HasToolResult(callID, r.Marker) {
		return Reply{}, fmt.Errorf(
			"%w: missing tool result for call %q with marker %q",
			errToolRoundTrip,
			callID,
			r.Marker,
		)
	}

	text := r.FinalText
	if text == "" {
		text = defaultFinalText
	}

	return textReply(text), nil
}

func textReply(text string) Reply {
	return Reply{Text: text, ToolCalls: nil, Error: nil, HoldMidStream: false, Disconnect: false}
}

func callReply(calls ...ToolCall) Reply {
	return Reply{Text: "", ToolCalls: calls, Error: nil, HoldMidStream: false, Disconnect: false}
}

func validateReply(reply Reply, stream bool) error {
	if reply.Error != nil {
		return validateErrorReply(reply)
	}

	return validateOutputReply(reply, stream)
}

func validateErrorReply(reply Reply) error {
	if reply.Error.Status < minErrorStatus || reply.Error.Status > maxErrorStatus {
		return fmt.Errorf(
			"%w: error status %d is not 4xx or 5xx",
			errInvalidReply,
			reply.Error.Status,
		)
	}

	if reply.Text != "" || len(reply.ToolCalls) > 0 || reply.HoldMidStream || reply.Disconnect {
		return fmt.Errorf("%w: error reply carries other output", errInvalidReply)
	}

	return nil
}

func validateOutputReply(reply Reply, stream bool) error {
	if reply.HoldMidStream && !stream {
		return fmt.Errorf("%w: mid-stream hold needs a streamed request", errInvalidReply)
	}

	if reply.HoldMidStream && reply.Disconnect {
		return fmt.Errorf("%w: mid-stream hold and disconnect are exclusive", errInvalidReply)
	}

	for _, call := range reply.ToolCalls {
		if call.ID == "" || call.Name == "" {
			return fmt.Errorf("%w: tool call needs an id and a name", errInvalidReply)
		}
	}

	return nil
}
