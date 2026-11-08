package copilot

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
			name: "copilot",

			payload: `{"sessionId":"copilot-session",` +
				`"timestamp":"2026-06-29T10:00:00Z","cwd":"/repo/copilot",` +
				`"toolName":"Bash"}`,
			wantID:     "copilot-session",
			wantPath:   "",
			wantCWD:    "/repo/copilot",
			wantRoot:   "",
			wantEvent:  "",
			wantAttr:   "copilot_tool_name",
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
			name: "copilot accepts native hook payload",

			payload: `{"sessionId":"copilot-session",` +
				`"timestamp":"2026-06-29T10:00:00Z","cwd":"/repo",` +
				`"toolName":"Bash"}`,
			want: true,
		},
		{
			name: "copilot rejects payload without cwd",

			payload: `{"sessionId":"copilot-session"}`,
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

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("copilot"), variable: "COPILOT_HOME"},
	} {
		for _, knownHome := range []bool{true, false} {
			name := string(test.harness) + "/" + test.variable
			if !knownHome {
				name += "/without home"
			}

			t.Run(name, func(t *testing.T) {
				testharness.Isolate(t, New())

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

				locations := New().Locations("")

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
		{"copilot", "permissionRequest", registry.ActivityWaiting, nil, new(registry.ActivityDetailPermission)},
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
