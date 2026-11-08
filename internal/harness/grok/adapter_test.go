package grok

import (
	"encoding/json"
	"path/filepath"
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
			name: "grok",

			payload: `{"sessionId":"grok-session","cwd":"/tmp","workspaceRoot":"/` +
				`repo","hookEventName":"UserPromptSubmit",` +
				`"toolName":"run_terminal_command"}`,
			wantID:     "grok-session",
			wantPath:   "",
			wantCWD:    "/tmp",
			wantRoot:   "/repo",
			wantEvent:  "UserPromptSubmit",
			wantAttr:   "grok_tool_name",
			wantAttrKV: "run_terminal_command",
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

func TestHistoryFollowsNativeHome(t *testing.T) {
	home := testharness.Isolate(t, New())
	root := t.TempDir()
	t.Setenv("GROK_HOME", root)

	sources, err := New().Transcript().Sources(home)
	if err != nil || !slices.Equal(sources, []string{filepath.Join(root, "grok.db")}) {
		t.Fatalf("history sources = %v, %v", sources, err)
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
			name: "grok accepts native hook payload",

			payload: `{"hookEventName":"stop","sessionId":"grok-session","cwd":"/repo","workspaceRoot":"/repo"}`,
			want:    true,
		},
		{
			name: "grok accepts snake case hook payload",

			payload: `{"hook_event_name":"stop","session_id":"grok-session","cwd":"/repo","workspace_root":"/repo"}`,
			want:    true,
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
		{harness: registry.Harness("grok"), variable: "GROK_HOME"},
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

func TestSessionTitlesIgnoreWorkingDirectoryWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		plant   func(t *testing.T, workDir string)
	}{
		{registry.Harness("grok"), func(t *testing.T, workDir string) {
			t.Helper()
			testharness.PlantFile(
				t,
				workDir,
				`{"info":{"session_id":"abc"},"generated_title":"Decoy"}`,
				".grok",
				"sessions",
				"group",
				"abc",
				"summary.json",
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
