package droid

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string

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
			name: "droid",

			payload: `{"session_id":"droid-session","transcript_path":"/tmp/` +
				`droid.jsonl","cwd":"/repo/droid",` +
				`"hook_event_name":"PreToolUse","tool_name":"Bash"}`,
			wantID:     "droid-session",
			wantPath:   "/tmp/droid.jsonl",
			wantCWD:    "/repo/droid",
			wantRoot:   "",
			wantEvent:  "PreToolUse",
			wantAttr:   "droid_tool_name",
			wantAttrKV: "Bash",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}

			got, err := New().PayloadDefaults(payload)
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
		name string

		payload string
		want    bool
	}{
		{
			name: "droid accepts native hook payload",

			payload: `{"session_id":"droid-session","transcript_path":"/tmp/` +
				`droid.jsonl","cwd":"/repo","hook_event_name":"Stop"}`,
			want: true,
		},
		{
			name: "droid rejects payload without event",

			payload: `{"session_id":"droid-session","cwd":"/repo"}`,
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := New().PayloadCompatible(json.RawMessage(test.payload))
			if got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestSessionTitlesIgnoreWorkingDirectoryWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		plant   func(t *testing.T, workDir string)
	}{
		{registry.Harness("droid"), func(t *testing.T, workDir string) {
			t.Helper()
			testharness.PlantFile(
				t,
				workDir,
				`{"type":"session_start","id":"abc","title":"Decoy"}`+"\n",
				".factory",
				"sessions",
				"abc.jsonl",
			)
		}},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			workDir := testharness.WithoutHome(t, New())
			test.plant(t, workDir)

			reader := New()

			titles, err := reader.SessionTitles(
				t.Context(),
				[]registry.ObservationIdentity{{SessionID: "abc"}},
			)
			if err != nil || len(titles) != 1 || titles[0] != "" {
				t.Fatalf("SessionTitles without a home = %q, %v, want one empty title", titles, err)
			}
		})
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
			"droid",
			"Notification",
			registry.ActivityWaiting,
			map[string]string{
				"droid_notification_type": "permission_prompt",
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

			var detail *registry.ActivityDetail
			if reader, ok := any(New()).(harness.ActivityDetailer); ok {
				detail = reader.ActivityDetail(report.Event, *report.Activity, report.Attributes)
			}

			if !reflect.DeepEqual(detail, test.want) {
				t.Fatalf("detail = %v, want %v", detail, test.want)
			}
		})
	}
}
