package catalog

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestEveryAdapterDeclaresDetailSupport(t *testing.T) {
	t.Parallel()
	results := map[registry.Harness][6]bool{
		"claude":    {true, true, true, true, true, true},
		"codex":     {true, false, false, true, false, true},
		"copilot":   {true, false, false, false, false, false},
		"droid":     {true, false, false, false, false, false},
		"hermes":    {true, false, false, false, false, false},
		"amp":       {true, false, false, false, false, false},
		"agy":       {true, true, false, false, false, false},
		"kimi-code": {true, true, false, false, false, false},
		"omp":       {true, true, false, true, true, true},
		"opencode":  {true, true, false, true, true, false},
		"kilo":      {true, false, false, false, false, false},
		"pi":        {false, false, false, true, true, true},
		"cursor":    {}, "cline": {}, "grok": {}, "goose": {}, "openclaw": {},
	}
	if len(results) != len(All()) {
		t.Fatal("adapter census does not match catalog")
	}
	for _, adapter := range All() {
		id := adapter.Definition().ID
		want, exists := results[id]
		if !exists {
			t.Fatalf("adapter %s has no audited detail result", id)
		}
		support := DetailCapabilitiesFor(id)
		got := [6]bool{support.Native.Permission, support.Native.Question, support.Native.UsageLimit, support.Screen.Permission, support.Screen.Question, support.Screen.UsageLimit}
		if got != want {
			t.Fatalf("%s detail support = %v, want %v", id, got, want)
		}
	}
}

func TestNativeDetailsUseExplicitPromptSignals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		harness    registry.Harness
		event      string
		activity   registry.Activity
		attributes map[string]string
		want       *registry.ActivityDetail
	}{
		{"claude", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"claude", "Elicitation", registry.ActivityWaiting, nil, new(registry.DetailQuestion)},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "permission_prompt"}, new(registry.DetailPermission)},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "idle_prompt", "claude_permission_mode": "bypassPermissions"}, nil},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "agent_needs_input"}, nil},
		{"claude", "state", registry.ActivityWaiting, map[string]string{"claude_permission_mode": "plan"}, nil},
		{"claude", "PermissionRequest", registry.ActivityRunning, nil, nil},
		{"claude", "StopFailure", registry.ActivityFailed, map[string]string{"claude_error_type": "rate_limit"}, new(registry.DetailUsageLimit)},
		{"claude", "PostToolUseFailure", registry.ActivityRunning, map[string]string{"claude_error_type": "rate_limit"}, nil},
		{"codex", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"copilot", "permissionRequest", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"droid", "Notification", registry.ActivityWaiting, map[string]string{"droid_notification_type": "permission_prompt"}, new(registry.DetailPermission)},
		{"hermes", "pre_approval_request", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"amp", "thread.state", registry.ActivityWaiting, map[string]string{"amp_thread_state": "awaiting-approval"}, new(registry.DetailPermission)},
		{"agy", "PreToolUse", registry.ActivityWaiting, map[string]string{"agy_tool_name": "ask_question"}, new(registry.DetailQuestion)},
		{"agy", "PreToolUse", registry.ActivityWaiting, map[string]string{"agy_tool_name": "ask_permission"}, new(registry.DetailPermission)},
		{"opencode", "permission.asked", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"opencode", "question.asked", registry.ActivityWaiting, nil, new(registry.DetailQuestion)},
		{"kilo", "permission.asked", registry.ActivityWaiting, nil, new(registry.DetailPermission)},
		{"pi", "ui_prompt_start", registry.ActivityWaiting, nil, nil},
	} {
		t.Run(string(test.harness)+"/"+test.event+string(test.activity), func(t *testing.T) {
			report := &registry.Report{Event: test.event, Activity: &test.activity, Attributes: test.attributes}
			observation := PrepareObservation(registry.Observation{Harness: test.harness, Evidence: report})
			if !reflect.DeepEqual(observation.Report().Detail, test.want) {
				t.Fatalf("detail = %v, want %v", observation.Report().Detail, test.want)
			}
		})
	}
}
