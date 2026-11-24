package agy_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/internal/harness/agy"
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
			name:    "agy",
			harness: registry.Harness("agy"),
			payload: `{"conversationId":"agy-session","transcriptPath":"/repo/` +
				`.gemini/antigravity/transcript.jsonl","workspacePaths":["/` +
				`repo"],"event":"PreToolUse","toolCall":{"name":"run_command"` +
				`,"args":{"Cwd":"/repo/subdir"}}}`,
			wantID:     "agy-session",
			wantPath:   "/repo/.gemini/antigravity/transcript.jsonl",
			wantCWD:    "/repo/subdir",
			wantRoot:   "/repo",
			wantEvent:  "PreToolUse",
			wantAttr:   "agy_tool_name",
			wantAttrKV: "run_command",
		},
		{
			name:    "agy prefers top-level cwd",
			harness: registry.Harness("agy"),
			payload: `{"conversationId":"agy-session","transcriptPath":"/repo/` +
				`.gemini/antigravity/transcript.jsonl","cwd":"/repo/work",` +
				`"workspacePaths":["/repo"],"hookEventName":"PreToolUse",` +
				`"toolCall":{"name":"run_command","args":{"Cwd":"/repo/subdir"}}}`,
			wantID:     "agy-session",
			wantPath:   "/repo/.gemini/antigravity/transcript.jsonl",
			wantCWD:    "/repo/work",
			wantRoot:   "/repo",
			wantEvent:  "PreToolUse",
			wantAttr:   "agy_hook_event",
			wantAttrKV: "PreToolUse",
		},
		{
			name:    "agy falls back to workspace path",
			harness: registry.Harness("agy"),
			payload: `{"conversationId":"agy-session","transcriptPath":"/repo/` +
				`.gemini/antigravity/transcript.jsonl","workspacePaths":["/` +
				`repo"],"hookEventName":"SessionStart"}`,
			wantID:     "agy-session",
			wantPath:   "/repo/.gemini/antigravity/transcript.jsonl",
			wantCWD:    "/repo",
			wantRoot:   "/repo",
			wantEvent:  "SessionStart",
			wantAttr:   "agy_hook_event",
			wantAttrKV: "SessionStart",
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
			name:    "agy accepts native hook payload",
			harness: registry.Harness("agy"),
			payload: `{"conversationId":"agy-session","workspacePaths":["/repo"],"event":"Stop"}`,
			want:    true,
		},
		{
			name:    "agy accepts snake case hook payload",
			harness: registry.Harness("agy"),
			payload: `{"conversation_id":"agy-session","workspace_paths":["/repo"],"event":"Stop"}`,
			want:    true,
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

type agyHookTestCase struct {
	name          string
	event         string
	payload       map[string]any
	parentArgs    []string
	wantReport    bool
	wantActivity  registry.Activity
	wantLifecycle *registry.NativeLifecycle
	wantResponse  map[string]any
}

func TestHandleHookAgy(t *testing.T) {
	t.Parallel()

	tests := []agyHookTestCase{
		{
			name:  "session start reports idle with start lifecycle",
			event: "SessionStart",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"cwd":            "/repo/work",
				"workspacePaths": []any{"/repo"},
			},
			parentArgs:    nil,
			wantReport:    true,
			wantActivity:  registry.ActivityIdle,
			wantLifecycle: new(registry.NativeLifecycleStart),
			wantResponse:  map[string]any{},
		},
		{
			name:  "pre invocation reports running",
			event: "PreInvocation",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"invocationNum":  float64(3),
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityRunning,
			wantResponse: map[string]any{},
		},
		{
			name:  "pre tool use permission reports waiting",
			event: "PreToolUse",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"toolCall": map[string]any{
					"name": "ask_permission",
					"args": map[string]any{"Cwd": "/repo/pkg"},
				},
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityWaiting,
			wantResponse: map[string]any{"decision": "allow"},
		},
		{
			name:  "fully idle stop reports idle",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "model_stop",
				"fullyIdle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityIdle,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop remains idle regardless of parent args",
			event: "Stop",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"fullyIdle":      true,
			},
			parentArgs:   []string{"agy", "--print", "hello"},
			wantReport:   true,
			wantActivity: registry.ActivityIdle,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop with error termination reason reports failed",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "ERROR",
				"fullyIdle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityFailed,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop with error string reports failed",
			event: "Stop",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"error":          "provider failed",
				"fullyIdle":      true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityFailed,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop with snake case error reason reports failed",
			event: "Stop",
			payload: map[string]any{
				"conversation_id":    "agy-session",
				"transcript_path":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspace_paths":    []any{"/repo"},
				"termination_reason": "error",
				"fully_idle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityFailed,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop with user canceled reports interrupted",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "USER_CANCELED",
				"fullyIdle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityInterrupted,
			wantResponse: map[string]any{},
		},
		{
			name:  "fully idle stop with user cancelled reports interrupted",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "user_cancelled",
				"fullyIdle":         true,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityInterrupted,
			wantResponse: map[string]any{},
		},
		{
			name:  "non-idle stop stays running even with error reason",
			event: "Stop",
			payload: map[string]any{
				"conversationId":    "agy-session",
				"transcriptPath":    "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths":    []any{"/repo"},
				"terminationReason": "ERROR",
				"fullyIdle":         false,
			},
			parentArgs:   nil,
			wantReport:   true,
			wantActivity: registry.ActivityRunning,
			wantResponse: map[string]any{},
		},
		{
			name:  "empty post tool use does not report",
			event: "PostToolUse",
			payload: map[string]any{
				"conversationId": "agy-session",
				"transcriptPath": "/repo/.gemini/antigravity/transcript.jsonl",
				"workspacePaths": []any{"/repo"},
				"toolCall":       nil,
			},
			parentArgs:   nil,
			wantReport:   false,
			wantActivity: "",
			wantResponse: map[string]any{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertAgyHookResult(t, test)
		})
	}
}

func assertAgyHookResult(t *testing.T, test agyHookTestCase) {
	t.Helper()

	result, ok, err := adapters.HandleHook(
		t.Context(),
		registry.Harness("agy"),
		test.event,
		json.RawMessage(`{"test":true}`),
		test.payload,
		test.parentArgs,
	)
	if !ok {
		t.Fatal("expected agy hook adapter")
	}

	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(result.Response, test.wantResponse) {
		t.Fatalf("expected response %#v, got %#v", test.wantResponse, result.Response)
	}

	if result.ReportOK != test.wantReport {
		t.Fatalf("expected report ok %v, got %v", test.wantReport, result.ReportOK)
	}

	if result.ReportOK {
		if result.Report.ActivityClaim() == nil {
			t.Fatal("expected activity")
		}

		if *result.Report.ActivityClaim() != test.wantActivity {
			t.Fatalf(
				"expected activity %q, got %q",
				test.wantActivity,
				*result.Report.ActivityClaim(),
			)
		}

		report := result.Report.Report()
		if report == nil || report.Claim == nil || *report.Claim != registry.PresenceLive {
			t.Fatalf("expected live presence claim, got %#v", report)
		}

		if !reflect.DeepEqual(report.Lifecycle, test.wantLifecycle) {
			t.Fatalf("expected lifecycle %#v, got %#v", test.wantLifecycle, report.Lifecycle)
		}
	}
}

func TestAgySettingsUseApplicationDirectory(t *testing.T) {
	home := testharness.Isolate(t, adapters.All()...)

	locations, ok := adapters.LocationsFor(registry.Harness("agy"), "")
	if !ok {
		t.Fatal("missing locations")
	}

	want := []string{filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")}
	if got := testharness.LocationPaths(
		locations,
		harness.LocationKindConfig,
	); !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("settings locations = %v, want %v", got, want)
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
		{
			"agy",
			"PreToolUse",
			registry.ActivityWaiting,
			map[string]string{
				"agy_tool_name": "ask_question",
			},
			new(
				registry.ActivityDetailQuestion,
			),
		},
		{
			"agy",
			"PreToolUse",
			registry.ActivityWaiting,
			map[string]string{
				"agy_tool_name": "ask_permission",
			},
			new(
				registry.ActivityDetailPermission,
			),
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

func TestAgyDefinitionAndHookConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	adapter := agy.New()
	definition := adapter.Definition()

	if definition.ProgramStatusApp != "antigravity" {
		t.Fatalf("ProgramStatusApp = %q, want %q", definition.ProgramStatusApp, "antigravity")
	}

	if !slices.Equal(definition.PayloadOmissions, []string{"lastUserInput"}) {
		t.Fatalf("PayloadOmissions = %v, want [lastUserInput]", definition.PayloadOmissions)
	}

	if !definition.Capabilities.SessionStart {
		t.Fatal("expected Capabilities.SessionStart to be true")
	}

	plan, err := adapter.InstallPlan("/usr/local/bin/aht")
	if err != nil {
		t.Fatalf("InstallPlan error: %v", err)
	}

	pluginAction, ok := plan.Actions[0].(harness.PluginDirectoryAction)
	if !ok {
		t.Fatalf("expected PluginDirectoryAction, got %T", plan.Actions[0])
	}

	var hooksConfig map[string]any

	for _, file := range pluginAction.Plan.Files {
		if file.Name == "hooks.json" {
			hooksConfig, _ = file.JSONContent.(map[string]any)
			break
		}
	}

	pluginHooks, ok := hooksConfig["aht-state"].(map[string]any)
	if !ok {
		t.Fatalf("missing aht-state hooks map: %#v", hooksConfig)
	}

	sessionStart, ok := pluginHooks["SessionStart"].([]any)
	if !ok || len(sessionStart) != 1 {
		t.Fatalf("expected single SessionStart handler, got %#v", pluginHooks["SessionStart"])
	}

	entry, ok := sessionStart[0].(map[string]any)
	if !ok {
		t.Fatalf("expected SessionStart handler object, got %T", sessionStart[0])
	}

	wantCmd := harness.NativeHookCommand("/usr/local/bin/aht", definition.ID, "SessionStart")
	if entry["type"] != harness.HookTypeCommand || entry["command"] != wantCmd {
		t.Fatalf("unexpected SessionStart handler: %#v", entry)
	}
}
