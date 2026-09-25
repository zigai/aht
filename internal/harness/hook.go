package harness

import (
	"encoding/json"

	"github.com/zigai/aht/v2/pkg/registry"
)

type HookInvocation struct {
	Event      string
	RawPayload json.RawMessage
	Payload    map[string]any
	ParentArgs []string
}

type HookResult struct {
	Report   registry.Observation
	ReportOK bool
	Response map[string]any
}

type HookAdapter interface {
	HandleHook(invocation HookInvocation) HookResult
}
