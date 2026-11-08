package claude_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name       string
		harness    registry.Harness
		payload    string
		wantID     string
		wantPath   string
		wantCWD    string
		wantRoot   string
		wantEvent  string
		wantAttr   string
		wantAttrKV string
	}{
		{
			name:    "claude",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":"/tmp/` +
				`claude.jsonl","cwd":"/tmp","hook_event_name":"SessionStart",` +
				`"source":"startup","model":"claude-sonnet-4-6"}`,
			wantID:     "claude-session",
			wantPath:   "/tmp/claude.jsonl",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "SessionStart",
			wantAttr:   "claude_model",
			wantAttrKV: "claude-sonnet-4-6",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := catalog.PayloadDefaults(test.harness, json.RawMessage(test.payload))
			if err != nil {
				t.Fatalf("unexpected error from PayloadDefaults: %v", err)
			}

			if got.SessionID != test.wantID ||
				got.SessionPath != test.wantPath ||
				got.CWD != test.wantCWD ||
				got.ProjectRoot != test.wantRoot ||
				got.Event != test.wantEvent {
				t.Fatalf("unexpected defaults: %#v", got)
			}

			if got.Attributes[test.wantAttr] != test.wantAttrKV {
				t.Fatalf(
					"expected attribute %s=%q, got %#v",
					test.wantAttr,
					test.wantAttrKV,
					got.Attributes,
				)
			}
		})
	}
}

func TestPayloadCompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		harness registry.Harness
		payload string
		want    bool
	}{
		{
			name:    "claude accepts native hook payload",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":"/home/` +
				`user/.claude/projects/-repo/claude-session.jsonl","cwd":"/` +
				`repo","hook_event_name":"Stop"}`,
			want: true,
		},
		{
			name:    "claude rejects camel case hook payload",
			harness: registry.Harness("claude"),
			payload: `{"hookEventName":"stop","sessionId":"not-claude","cwd":"/` +
				`repo","workspaceRoot":"/repo","promptId":"prompt"}`,
			want: false,
		},
		{
			name:    "claude accepts cursor-compatible common payload",
			harness: registry.Harness("claude"),
			payload: `{"conversation_id":"cursor-conversation",` +
				`"session_id":"cursor-session","transcript_path":null,` +
				`"cwd":"/repo","hook_event_name":"sessionEnd",` +
				`"cursor_version":"2026.06.15","workspace_roots":["/repo"]}`,
			want: true,
		},
		{
			name:    "claude accepts configurable transcript path",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"codex-session","transcript_path":"/home/user/` +
				`.codex/sessions/2026/06/18/rollout.jsonl","cwd":"/repo",` +
				`"hook_event_name":"Stop","model":"gpt-5-codex"}`,
			want: true,
		},
		{
			name:    "claude accepts null transcript path",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":null,"cwd":"/repo","hook_event_name":"Stop"}`,
			want:    true,
		},
		{
			name:    "claude rejects non-object json",
			harness: registry.Harness("claude"),
			payload: `"not an object"`,
			want:    false,
		},
		{
			name:    "claude rejects invalid json",
			harness: registry.Harness("claude"),
			payload: `{"session_id":`,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := catalog.PayloadCompatible(test.harness, json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("claude"), variable: "CLAUDE_CONFIG_DIR"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, catalog.All()...)

				if !knownHome {
					t.Setenv("HOME", "")
					t.Setenv("USERPROFILE", "")
				}

				override := t.TempDir()

				value := override
				if test.value != nil {
					value = test.value(override)
				}

				t.Setenv(test.variable, value)
				locations, _ := catalog.LocationsFor(test.harness, "")

				config := testharness.LocationPaths(locations, harness.LocationKindConfig)
				if len(config) == 0 || slices.ContainsFunc(config, func(path string) bool {
					return !strings.HasPrefix(path, override+string(filepath.Separator)) &&
						!strings.HasPrefix(path, "/etc/")
				}) {
					t.Errorf("config locations with %s=%s = %v", test.variable, value, config)
				}
			})
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
		{"claude", "PermissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
		{"claude", "Elicitation", registry.ActivityWaiting, nil, new(registry.ActivityDetailQuestion)},
		{
			"claude",
			"Notification",
			registry.ActivityWaiting,
			map[string]string{
				"claude_notification_type": "permission_prompt",
			},
			new(
				registry.ActivityDetailPermission,
			),
		},
		{
			"claude",
			"Notification",
			registry.ActivityWaiting,
			map[string]string{
				"claude_notification_type": "idle_prompt",
				"claude_permission_mode":   "bypassPermissions",
			},
			nil,
		},
		{
			"claude",
			"Notification",
			registry.ActivityWaiting,
			map[string]string{
				"claude_notification_type": "agent_needs_input",
			},
			nil,
		},
		{"claude", "state", registry.ActivityWaiting, map[string]string{"claude_permission_mode": "plan"}, nil},
		{"claude", "PermissionRequest", registry.ActivityRunning, nil, nil},
		{
			"claude",
			"StopFailure",
			registry.ActivityFailed,
			map[string]string{
				"claude_error_type": "rate_limit",
			},
			new(
				registry.ActivityDetailUsageLimit,
			),
		},
		{
			"claude",
			"PostToolUseFailure",
			registry.ActivityRunning,
			map[string]string{
				"claude_error_type": "rate_limit",
			},
			nil,
		},
	} {
		t.Run(string(test.harness)+"/"+test.event+string(test.activity), func(t *testing.T) {
			report := &registry.Report{
				Event:      test.event,
				Activity:   &test.activity,
				Attributes: test.attributes,
			}

			observation := catalog.PrepareObservation(
				registry.Observation{Harness: test.harness, Evidence: report},
			)
			if !reflect.DeepEqual(observation.Report().Detail, test.want) {
				t.Fatalf("detail = %v, want %v", observation.Report().Detail, test.want)
			}
		})
	}
}
