package claude_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/agentstate"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/internal/harness/claude"
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
		{
			name:    "claude permission request tool name",
			harness: registry.Harness("claude"),
			payload: `{"session_id":"claude-session","transcript_path":"/tmp/` +
				`claude.jsonl","cwd":"/tmp","hook_event_name":"PermissionRequest",` +
				`"tool_name":"AskUserQuestion"}`,
			wantID:     "claude-session",
			wantPath:   "/tmp/claude.jsonl",
			wantCWD:    "/tmp",
			wantRoot:   "",
			wantEvent:  "PermissionRequest",
			wantAttr:   "claude_tool_name",
			wantAttrKV: "AskUserQuestion",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := adapters.PayloadDefaults(test.harness, json.RawMessage(test.payload))
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

			got := adapters.PayloadCompatible(test.harness, json.RawMessage(test.payload))
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
				testharness.Isolate(t, adapters.All()...)

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
				locations, _ := adapters.LocationsFor(test.harness, "")

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
		{
			"claude",
			"PermissionRequest",
			registry.ActivityWaiting,
			map[string]string{"claude_tool_name": "Bash"},
			new(registry.ActivityDetailPermission),
		},
		{
			"claude",
			"PermissionRequest",
			registry.ActivityWaiting,
			map[string]string{"claude_tool_name": "AskUserQuestion"},
			new(registry.ActivityDetailQuestion),
		},
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
				"claude_notification_type": "elicitation_dialog",
			},
			new(registry.ActivityDetailQuestion),
		},
		{
			"claude",
			"Notification",
			registry.ActivityWaiting,
			map[string]string{
				"claude_notification_type": "elicitation_url_dialog",
			},
			new(registry.ActivityDetailQuestion),
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

			observation := adapters.PrepareObservation(
				registry.Observation{Harness: test.harness, Evidence: report},
			)
			if !reflect.DeepEqual(observation.Report().Detail, test.want) {
				t.Fatalf("detail = %v, want %v", observation.Report().Detail, test.want)
			}
		})
	}
}

func TestPluginHooksReportPermissionDeniedRunningAndMatchElicitations(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	plan, err := claude.New().InstallPlan("/usr/local/bin/aht")
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}

	var moduleSource string

	for _, action := range plan.Actions {
		pluginAction, ok := action.(harness.PluginDirectoryAction)
		if !ok {
			continue
		}

		for _, file := range pluginAction.Plan.Files {
			if strings.HasSuffix(file.Name, "/hooks/register.ts") {
				moduleSource = file.Content
			}
		}
	}

	if moduleSource == "" {
		t.Fatal("rendered plugin module missing from install plan")
	}

	var commands []struct {
		Event   string   `json:"event"`
		Matcher string   `json:"matcher"`
		Args    []string `json:"args"`
	}

	for line := range strings.Lines(moduleSource) {
		encoded, ok := strings.CutPrefix(strings.TrimSpace(line), "const commands: Command[] = ")
		if !ok {
			continue
		}

		if err := json.Unmarshal([]byte(strings.TrimSuffix(encoded, ";")), &commands); err != nil {
			t.Fatalf("decode commands: %v", err)
		}

		break
	}

	if len(commands) == 0 {
		t.Fatal("no commands encoded in rendered plugin module")
	}

	foundDenied := false
	foundNotification := false

	for _, command := range commands {
		switch command.Event {
		case "PermissionDenied":
			foundDenied = true

			if !slices.Contains(command.Args, "running") {
				t.Fatalf("PermissionDenied args = %v, want running activity", command.Args)
			}
		case "Notification":
			foundNotification = true

			wantMatcher := "permission_prompt|elicitation_dialog|elicitation_url_dialog"
			if command.Matcher != wantMatcher {
				t.Fatalf("Notification matcher = %q, want %q", command.Matcher, wantMatcher)
			}

			if !slices.Contains(command.Args, "waiting") {
				t.Fatalf("Notification args = %v, want waiting activity", command.Args)
			}
		}
	}

	if !foundDenied {
		t.Fatal("PermissionDenied hook missing from plugin commands")
	}

	if !foundNotification {
		t.Fatal("Notification hook missing from plugin commands")
	}
}

func TestScreenManifestClassifiesStockModeFooters(t *testing.T) {
	t.Parallel()

	manifest, err := (agentstate.Loader{ConfigDir: t.TempDir()}).Load(registry.Harness("claude"))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		screen string
		want   registry.Activity
		rule   string
	}{
		{
			name:   "post escape manual mode footer",
			screen: "❯ \n⏸ manual mode on",
			want:   registry.ActivityIdle,
			rule:   "input_prompt",
		},
		{
			name:   "auto mode footer",
			screen: "❯ \n⏵ auto mode on",
			want:   registry.ActivityIdle,
			rule:   "input_prompt",
		},
		{
			name:   "accept edits footer",
			screen: "❯ \n⏵ accept edits on",
			want:   registry.ActivityIdle,
			rule:   "input_prompt",
		},
		{
			name:   "plan mode footer",
			screen: "❯ \n⏸ plan mode on",
			want:   registry.ActivityIdle,
			rule:   "input_prompt",
		},
		{
			name:   "running with manual mode footer still running",
			screen: "Thinking… esc to interrupt\n❯ \n⏸ manual mode on",
			want:   registry.ActivityRunning,
			rule:   "working_interruptible",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			decision := manifest.Evaluate(agentstate.NormalizeSnapshot(test.screen, "claude"))
			if decision.Activity != test.want || decision.RuleID != test.rule {
				t.Fatalf("decision = %#v, want %s (%s)", decision, test.want, test.rule)
			}
		})
	}
}

func TestNotificationDetailTypesAreSubscribed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	adapter := claude.New()

	plan, err := adapter.InstallPlan("aht")
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}

	var moduleSource string

	for _, action := range plan.Actions {
		pluginAction, ok := action.(harness.PluginDirectoryAction)
		if !ok {
			continue
		}

		for _, file := range pluginAction.Plan.Files {
			if strings.HasSuffix(file.Name, "/hooks/register.ts") {
				moduleSource = file.Content
			}
		}
	}

	if moduleSource == "" {
		t.Fatal("rendered plugin module missing from install plan")
	}

	var commands []struct {
		Event   string   `json:"event"`
		Matcher string   `json:"matcher"`
		Args    []string `json:"args"`
	}

	for line := range strings.Lines(moduleSource) {
		encoded, ok := strings.CutPrefix(strings.TrimSpace(line), "const commands: Command[] = ")
		if !ok {
			continue
		}

		if err := json.Unmarshal([]byte(strings.TrimSuffix(encoded, ";")), &commands); err != nil {
			t.Fatalf("decode commands: %v", err)
		}

		break
	}

	var matchers []*regexp.Regexp

	for _, command := range commands {
		if command.Event != "Notification" {
			continue
		}

		pattern := command.Matcher
		if pattern == "" {
			pattern = ".*"
		}

		compiled, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			t.Fatalf("compile Notification matcher %q: %v", command.Matcher, compileErr)
		}

		matchers = append(matchers, compiled)
	}

	if len(matchers) == 0 {
		t.Fatal("no Notification hook matchers registered in plugin")
	}

	detailTypes := []string{
		"permission_prompt",
		"elicitation_dialog",
		"elicitation_url_dialog",
	}

	for _, kind := range detailTypes {
		detail := adapter.ActivityDetail(
			"Notification",
			registry.ActivityWaiting,
			map[string]string{"claude_notification_type": kind},
		)
		if detail == nil {
			t.Fatalf("ActivityDetail(Notification, waiting, %q) = nil, want non-nil", kind)
		}

		matched := slices.ContainsFunc(matchers, func(matcher *regexp.Regexp) bool {
			return matcher.MatchString(kind)
		})
		if !matched {
			t.Errorf("Notification detail type %q is not matched by any registered hook", kind)
		}
	}
}
