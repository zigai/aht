package catalog

import (
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestNativeDetailsUseExplicitPromptSignals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		harness    registry.Harness
		event      string
		activity   registry.Activity
		attributes map[string]string
		want       *registry.ActivityDetail
	}{
		{"claude", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"claude", "Elicitation", registry.ActivityWaiting, nil, new(registry.ActivityDetailQuestion)},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "permission_prompt"}, new(registry.ActivityDetailPermission)},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "idle_prompt", "claude_permission_mode": "bypassPermissions"}, nil},
		{"claude", "Notification", registry.ActivityWaiting, map[string]string{"claude_notification_type": "agent_needs_input"}, nil},
		{"claude", "state", registry.ActivityWaiting, map[string]string{"claude_permission_mode": "plan"}, nil},
		{"claude", "PermissionRequest", registry.ActivityRunning, nil, nil},
		{"claude", "StopFailure", registry.ActivityFailed, map[string]string{"claude_error_type": "rate_limit"}, new(registry.ActivityDetailUsageLimit)},
		{"claude", "PostToolUseFailure", registry.ActivityRunning, map[string]string{"claude_error_type": "rate_limit"}, nil},
		{"codex", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"copilot", "permissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"droid", "Notification", registry.ActivityWaiting, map[string]string{"droid_notification_type": "permission_prompt"}, new(registry.ActivityDetailPermission)},
		{"hermes", "pre_approval_request", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"amp", "thread.state", registry.ActivityWaiting, map[string]string{"amp_thread_state": "awaiting-approval"}, new(registry.ActivityDetailPermission)},
		{"agy", "PreToolUse", registry.ActivityWaiting, map[string]string{"agy_tool_name": "ask_question"}, new(registry.ActivityDetailQuestion)},
		{"agy", "PreToolUse", registry.ActivityWaiting, map[string]string{"agy_tool_name": "ask_permission"}, new(registry.ActivityDetailPermission)},
		{"opencode", "permission.asked", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"opencode", "question.asked", registry.ActivityWaiting, nil, new(registry.ActivityDetailQuestion)},
		{"kilo", "permission.asked", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
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
