package agy_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
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

			got := catalog.PayloadCompatible(test.harness, json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

type agyHookTestCase struct {
	name         string
	event        string
	payload      map[string]any
	parentArgs   []string
	wantReport   bool
	wantActivity registry.Activity
	wantResponse map[string]any
}

func TestHandleHookAgy(t *testing.T) {
	t.Parallel()

	tests := []agyHookTestCase{
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

	result, ok, err := catalog.HandleHook(
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
	}
}

func TestAgySettingsUseApplicationDirectory(t *testing.T) {
	home := testharness.Isolate(t, catalog.All()...)

	locations, ok := catalog.LocationsFor(registry.Harness("agy"), "")
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

			observation := catalog.PrepareObservation(
				registry.Observation{Harness: test.harness, Evidence: report},
			)
			if !reflect.DeepEqual(observation.Report().Detail, test.want) {
				t.Fatalf("detail = %v, want %v", observation.Report().Detail, test.want)
			}
		})
	}
}
