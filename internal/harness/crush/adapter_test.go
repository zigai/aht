package crush

import (
	"encoding/json"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

const preToolUsePayload = `{"event":"PreToolUse","session_id":"native-session","cwd":"/work/project","tool_name":"bash","tool_input":{"command":"go test ./..."}}`

func TestPayloadCompatibleAcceptsOnlyCrushHookPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "pre tool use", payload: preToolUsePayload, want: true},
		{name: "claude shaped", payload: `{"hook_event_name":"PreToolUse","session_id":"native-session","cwd":"/work/project","tool_name":"Bash"}`, want: false},
		{name: "missing session", payload: `{"event":"PreToolUse","session_id":" ","cwd":"/work/project","tool_name":"bash"}`, want: false},
		{name: "not an object", payload: `[]`, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := New().PayloadCompatible(json.RawMessage(test.payload)); got != test.want {
				t.Fatalf("PayloadCompatible(%s) = %t, want %t", test.payload, got, test.want)
			}
		})
	}
}

func TestPayloadDefaultsCarryNativeIdentity(t *testing.T) {
	t.Parallel()

	var payload map[string]any
	if err := json.Unmarshal([]byte(preToolUsePayload), &payload); err != nil {
		t.Fatal(err)
	}
	defaults, err := New().PayloadDefaults(payload)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.SessionID != "native-session" || defaults.CWD != "/work/project" || defaults.Event != harness.HookEventPreToolUse {
		t.Fatalf("defaults = %+v", defaults)
	}
	if defaults.Attributes["crush_tool_name"] != "bash" {
		t.Fatalf("attributes = %v, want the native tool name", defaults.Attributes)
	}
}

func TestToolUseIsNotASessionLifecycleEvent(t *testing.T) {
	t.Parallel()

	defaults := New().LifecycleDefaults(harness.HookEventPreToolUse, map[string]string{"crush_hook_event": harness.HookEventPreToolUse})
	if defaults.Lifecycle != "" || defaults.Presence != "" {
		t.Fatalf("lifecycle defaults = %+v, want no lifecycle or presence claim", defaults)
	}
}
