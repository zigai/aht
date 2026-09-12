package kimi

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/zigai/aht/pkg/registry"
)

const (
	maxProtocolFrame   = 8 << 20
	maxProtocolEntries = 1024
	maxProtocolID      = 1024
)
const maxSubagentNesting = 16

var (
	errFrameLimitExceeded        = errors.New("kimi wire frame exceeds size limit")
	errInvalidRPCEnvelope        = errors.New("invalid Kimi Wire JSON-RPC envelope")
	errInvalidOperationID        = errors.New("invalid Kimi Wire operation identifier")
	errDuplicateOperationID      = errors.New("duplicate outstanding Kimi Wire operation identifier")
	errTooManyOperations         = errors.New("too many outstanding Kimi Wire operations")
	errInvalidEventEnvelope      = errors.New("invalid Kimi Wire event envelope")
	errInvalidWaitingRequest     = errors.New("invalid Kimi Wire waiting request")
	errConflictingWaitingRequest = errors.New("conflicting Kimi Wire waiting request identifier")
	errTooManyWaitingRequests    = errors.New("too many pending Kimi Wire waiting requests")
	errInvalidOperationError     = errors.New("invalid Kimi Wire operation error")
	errInvalidOperationResult    = errors.New("invalid Kimi Wire operation result")
	errSubagentNestingLimit      = errors.New("kimi wire subagent nesting exceeds limit")
	errInvalidSubagentEvent      = errors.New("invalid Kimi Wire subagent event")
	errInvalidApprovalResponse   = errors.New("invalid Kimi Wire approval response")
	errInvalidToolResult         = errors.New("invalid Kimi Wire tool result")
	errInvalidLifecyclePayload   = errors.New("invalid Kimi Wire lifecycle payload")
)

// Update is activity evidence from a native Wire message, not client intent.
type Update struct {
	Event    string
	Activity registry.Activity
}

type pendingRequest struct {
	tool       string
	background bool
}

// Protocol is ready for use at its zero value. Host messages must be observed in
// stream order; ObserveClient may run concurrently. Only correlation IDs survive
// a call. A Protocol must not be copied after first use.
//
// Wire does not mark replay envelopes. During replay the independent native root
// hub can interleave background events with history. Both are ignored: claiming
// those untagged messages are live would resurrect historical approval requests.
type Protocol struct {
	mu           sync.Mutex
	operations   map[string]string
	streamID     string
	streamMethod string
	pending      map[string]pendingRequest
	clientErr    error
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type wireEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func decodeRPC(line []byte) (rpcMessage, error) {
	var msg rpcMessage
	if len(line) > maxProtocolFrame {
		return msg, errFrameLimitExceeded
	}
	if json.Unmarshal(line, &msg) != nil || msg.JSONRPC != "2.0" {
		return msg, errInvalidRPCEnvelope
	}
	return msg, nil
}

func validID(id string) bool { return id != "" && len(id) <= maxProtocolID }

// ObserveClient records outstanding prompt/replay RPCs. Client approval replies
// do not clear waiting: only the native response or correlated tool completion
// proves the host consumed a reply (including rejection or a client RPC error).
func (p *Protocol) ObserveClient(line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	msg, err := decodeRPC(line)
	if err != nil {
		// Invalid client messages still reach Kimi, which owns their diagnostics.
		return
	}
	if msg.Method != "prompt" && msg.Method != "replay" {
		return
	}
	if !validID(msg.ID) {
		p.clientErr = errInvalidOperationID
		return
	}
	if _, exists := p.operations[msg.ID]; exists {
		p.clientErr = errDuplicateOperationID
		return
	}
	if len(p.operations) >= maxProtocolEntries {
		p.clientErr = errTooManyOperations
		return
	}
	if p.operations == nil {
		p.operations = make(map[string]string)
	}
	p.operations[msg.ID] = msg.Method
	if p.streamID == "" {
		p.streamID, p.streamMethod = msg.ID, msg.Method
	}
}

// ObserveHost emits native activity evidence. Errors contain no native payloads.
//
//nolint:gocognit,cyclop,nestif // ObserveHost parses and routes all native Wire RPC requests, responses, and events.
func (p *Protocol) ObserveHost(line []byte) (Update, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clientErr != nil {
		return Update{}, false, p.clientErr
	}
	msg, err := decodeRPC(line)
	if err != nil {
		return Update{}, false, err
	}
	if msg.Method == "" {
		return p.response(msg)
	}
	if msg.Method != "event" && msg.Method != "request" {
		return Update{Event: "", Activity: ""}, false, nil
	}
	if p.streamMethod == "replay" {
		return Update{Event: "", Activity: ""}, false, nil
	}
	var event wireEnvelope
	if json.Unmarshal(msg.Params, &event) != nil || event.Type == "" {
		return Update{Event: "", Activity: ""}, false, errInvalidEventEnvelope
	}
	if msg.Method == "request" {
		if event.Type != "ApprovalRequest" && event.Type != "QuestionRequest" {
			return Update{Event: "", Activity: ""}, false, nil
		}
		var request struct {
			ID     string `json:"id"`
			Tool   string `json:"tool_call_id"`
			Source string `json:"source_kind"`
		}
		if json.Unmarshal(event.Payload, &request) != nil || !validID(msg.ID) ||
			!validID(request.ID) || !validID(request.Tool) ||
			(request.Source != "" && request.Source != "foreground_turn" && request.Source != "background_agent") {
			return Update{Event: "", Activity: ""}, false, errInvalidWaitingRequest
		}
		pending := pendingRequest{tool: request.Tool, background: request.Source == "background_agent"}
		if previous, exists := p.pending[request.ID]; exists {
			if previous != pending {
				return Update{Event: "", Activity: ""}, false, errConflictingWaitingRequest
			}
			return Update{Event: "", Activity: ""}, false, nil
		}
		if len(p.pending) >= maxProtocolEntries {
			return Update{Event: "", Activity: ""}, false, errTooManyWaitingRequests
		}
		if p.pending == nil {
			p.pending = make(map[string]pendingRequest)
		}
		p.pending[request.ID] = pending
		return p.transition(event.Type, registry.ActivityWaiting)
	}
	return p.event(event, false, 0)
}

//nolint:cyclop // response categorizes RPC result and error states
func (p *Protocol) response(msg rpcMessage) (Update, bool, error) {
	method, tracked := p.operations[msg.ID]
	if !tracked {
		return Update{Event: "", Activity: ""}, false, nil
	}
	if len(msg.Error) != 0 && string(msg.Error) != "null" {
		var rpcError struct {
			Code *int `json:"code"`
		}
		if json.Unmarshal(msg.Error, &rpcError) != nil || rpcError.Code == nil || len(msg.Result) != 0 {
			return Update{Event: "", Activity: ""}, false, errInvalidOperationError
		}
		delete(p.operations, msg.ID)
		if msg.ID != p.streamID {
			return Update{Event: "", Activity: ""}, false, nil
		}
		p.streamID, p.streamMethod = "", ""
		if method == "replay" || *rpcError.Code == -32000 || *rpcError.Code == -32602 {
			return Update{Event: "", Activity: ""}, false, nil
		}
		p.clearForeground()
		return p.transition("prompt.error", registry.ActivityFailed)
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(msg.Result, &result) != nil ||
		//nolint:misspell // Kimi Wire protocol specifies "cancelled" with double-l; tolerate both spellings
		(result.Status != "finished" && result.Status != "cancelled" && result.Status != "canceled" && (method != "prompt" || result.Status != "max_steps_reached")) {
		return Update{Event: "", Activity: ""}, false, errInvalidOperationResult
	}
	delete(p.operations, msg.ID)
	if msg.ID != p.streamID {
		return Update{Event: "", Activity: ""}, false, nil
	}
	p.streamID, p.streamMethod = "", ""
	if method == "replay" {
		return Update{Event: "", Activity: ""}, false, nil
	}
	p.clearForeground()
	if result.Status != "finished" {
		return p.transition("prompt."+result.Status, registry.ActivityInterrupted)
	}
	return p.transition("prompt.finished", registry.ActivityIdle)
}

//nolint:gocognit,cyclop // event routes native wire events and lifecycle transitions
func (p *Protocol) event(event wireEnvelope, nested bool, depth int) (Update, bool, error) {
	if depth > maxSubagentNesting {
		return Update{Event: "", Activity: ""}, false, errSubagentNestingLimit
	}
	switch event.Type {
	case "SubagentEvent":
		var child struct {
			Event wireEnvelope `json:"event"`
		}
		if json.Unmarshal(event.Payload, &child) != nil || child.Event.Type == "" {
			return Update{Event: "", Activity: ""}, false, errInvalidSubagentEvent
		}
		return p.event(child.Event, true, depth+1)
	case "ApprovalResponse":
		var response struct {
			ID       string `json:"request_id"`
			Response string `json:"response"`
		}
		if json.Unmarshal(event.Payload, &response) != nil || !validID(response.ID) ||
			(response.Response != "approve" && response.Response != "approve_for_session" && response.Response != "reject") {
			return Update{Event: "", Activity: ""}, false, errInvalidApprovalResponse
		}
		if _, exists := p.pending[response.ID]; !exists {
			return Update{Event: "", Activity: ""}, false, nil
		}
		delete(p.pending, response.ID)
		return p.transition(event.Type, registry.ActivityRunning)
	case "ToolResult":
		var result struct {
			Tool string `json:"tool_call_id"`
		}
		if json.Unmarshal(event.Payload, &result) != nil || !validID(result.Tool) {
			return Update{Event: "", Activity: ""}, false, errInvalidToolResult
		}
		resolved := false
		for id, request := range p.pending {
			if request.tool == result.Tool {
				delete(p.pending, id)
				resolved = true
			}
		}
		if !resolved {
			return Update{Event: "", Activity: ""}, false, nil
		}
		return p.transition(event.Type, registry.ActivityRunning)
	case "TurnEnd", "StepInterrupted", "TurnBegin", "StepBegin", "CompactionBegin", "CompactionEnd", "StepRetry":
		if len(event.Payload) == 0 || event.Payload[0] != '{' || !json.Valid(event.Payload) {
			return Update{Event: "", Activity: ""}, false, errInvalidLifecyclePayload
		}
		if nested {
			return Update{Event: "", Activity: ""}, false, nil
		}
		switch event.Type {
		case "TurnEnd":
			p.clearForeground()
			return p.transition(event.Type, registry.ActivityIdle)
		case "StepInterrupted":
			p.clearForeground()
			return p.transition(event.Type, registry.ActivityInterrupted)
		default:
			return p.transition(event.Type, registry.ActivityRunning)
		}
	default:
		return Update{Event: "", Activity: ""}, false, nil
	}
}

func (p *Protocol) clearForeground() {
	for id, request := range p.pending {
		if !request.background {
			delete(p.pending, id)
		}
	}
}

func (p *Protocol) transition(event string, activity registry.Activity) (Update, bool, error) {
	if len(p.pending) != 0 {
		activity = registry.ActivityWaiting
	}
	// Native hooks can publish between Wire frames. Refresh state-bearing
	// evidence even when the last Wire activity matches, so a foreground Stop
	// hook cannot hide an unresolved background approval.
	return Update{Event: event, Activity: activity}, true, nil
}
