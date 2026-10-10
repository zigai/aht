package qwen

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPayloadCompatibleRequiresNativeIdentity(t *testing.T) {
	t.Parallel()

	h := New()
	if !h.PayloadCompatible(json.RawMessage(`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo","hook_event_name":"Stop","timestamp":"2026-09-01T00:00:00Z"}`)) {
		t.Fatal("rejected a documented Qwen Code hook payload")
	}
	for _, payload := range []string{
		`{"transcript_path":"/t.jsonl","cwd":"/repo","hook_event_name":"Stop"}`,
		`{"session_id":"s1","transcript_path":"/t.jsonl","hook_event_name":"Stop"}`,
		`{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/repo"}`,
	} {
		if h.PayloadCompatible(json.RawMessage(payload)) {
			t.Fatalf("accepted payload without native identity: %s", payload)
		}
	}
}

func TestTranscriptSourcesExpandHomeOverrides(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		config  string
		runtime string
		want    string
	}{
		{name: "default", want: ".qwen"},
		{name: "config home", config: "~/config", want: "config"},
		{name: "runtime takes precedence", config: "~/config", runtime: "~/runtime", want: "runtime"},
		{name: "backslash separators", runtime: `~\runtime\sessions`, want: filepath.Join("runtime", "sessions")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", filepath.Join(t.TempDir(), "different-home"))
			t.Setenv("QWEN_HOME", scenario.config)
			t.Setenv("QWEN_RUNTIME_DIR", scenario.runtime)
			want := filepath.Join(home, scenario.want, "projects")

			sources := New().Transcript().Sources(home)
			if len(sources) != 1 || sources[0] != want {
				t.Fatalf("sources = %q, want [%q]", sources, want)
			}
		})
	}
}

func TestLifecycleDefaults(t *testing.T) {
	t.Parallel()

	h := New()
	tests := []struct {
		event, source string
		lifecycle     registry.NativeLifecycle
		presence      registry.Presence
	}{
		{event: "SessionStart", source: "startup", lifecycle: registry.NativeLifecycleStart, presence: registry.PresenceLive},
		{event: "SessionStart", source: "resume", lifecycle: registry.NativeLifecycleResume, presence: registry.PresenceLive},
		{event: "SessionEnd", source: "prompt_input_exit", lifecycle: registry.NativeLifecycleEnd, presence: registry.PresenceGone},
		{event: "Stop", source: "", lifecycle: "", presence: ""},
	}
	for _, test := range tests {
		defaults, err := h.PayloadDefaults(map[string]any{"session_id": "s1", "cwd": "/repo", "hook_event_name": test.event, "source": test.source, "reason": test.source})
		if err != nil {
			t.Fatal(err)
		}
		got := h.LifecycleDefaults("", defaults.Attributes)
		if got.Event != test.event || got.Lifecycle != test.lifecycle || got.Presence != test.presence {
			t.Fatalf("%s/%s lifecycle = %#v", test.event, test.source, got)
		}
	}
}

func TestActivityDetail(t *testing.T) {
	t.Parallel()

	h := New()
	detail := func(payload map[string]any, activity registry.Activity) *registry.ActivityDetail {
		t.Helper()
		defaults, err := h.PayloadDefaults(payload)
		if err != nil {
			t.Fatal(err)
		}
		return h.ActivityDetail(defaults.Event, activity, defaults.Attributes)
	}
	if got := detail(map[string]any{"hook_event_name": "Notification", "notification_type": "permission_prompt"}, registry.ActivityWaiting); got == nil || *got != registry.ActivityDetailPermission {
		t.Fatalf("permission prompt detail = %v", got)
	}
	if got := detail(map[string]any{"hook_event_name": "Notification", "notification_type": "idle_prompt"}, registry.ActivityIdle); got != nil {
		t.Fatalf("idle prompt detail = %v", *got)
	}
	if got := detail(map[string]any{"hook_event_name": "StopFailure", "error": "rate_limit"}, registry.ActivityFailed); got == nil || *got != registry.ActivityDetailUsageLimit {
		t.Fatalf("rate limit detail = %v", got)
	}
	if got := detail(map[string]any{"hook_event_name": "StopFailure", "error": "server_error"}, registry.ActivityFailed); got != nil {
		t.Fatalf("server error detail = %v", *got)
	}
	if got := detail(map[string]any{"hook_event_name": "PostToolUseFailure", "error": "rate_limit"}, registry.ActivityFailed); got != nil {
		t.Fatalf("tool error text became usage-limit detail: %v", *got)
	}
}
