package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/testharness"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestSessionDataDirOverride(t *testing.T) {
	t.Setenv("CLINE_SESSION_DATA_DIR", "/tmp/cline-sessions")

	want := "/tmp/cline-sessions/session-1/session-1.messages.json"
	if got := clineSessionPath("session-1"); got != want {
		t.Fatalf("expected CLINE_SESSION_DATA_DIR override, got %q", got)
	}
}

func TestPayloadDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")

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
			name: "cline",

			payload: `{"clineVersion":"3.2.1","hookName":"PreToolUse",` +
				`"taskId":"cline-task","sessionContext":{"rootSessionId":"cli` +
				`ne-root"},"workspaceRoots":["/repo/cline"],` +
				`"tool_call":{"name":"execute_command"}}`,
			wantID: "cline-root",
			wantPath: filepath.Join(
				os.Getenv("HOME"),
				".cline",
				"data",
				"sessions",
				"cline-root",
				"cline-root.messages.json",
			),
			wantCWD:    "/repo/cline",
			wantRoot:   "/repo/cline",
			wantEvent:  "PreToolUse",
			wantAttr:   "cline_tool_name",
			wantAttrKV: "execute_command",
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
			name: "cline accepts native hook payload",

			payload: `{"hookName":"TaskStart","taskId":"cline-task",` +
				`"sessionContext":{"rootSessionId":"cline-root"},` +
				`"workspaceRoots":["/repo"]}`,
			want: true,
		},
		{
			name: "cline rejects payload without hook name",

			payload: `{"taskId":"cline-task","sessionContext":{"rootSessionId":"cline-root"}}`,
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

func TestClineDirectoryHoldsRulesSkillsAndWorkflows(t *testing.T) {
	testharness.Isolate(t, New())
	override := t.TempDir()
	t.Setenv("CLINE_DIR", override)

	locations := New().Locations("")
	for _, kind := range []harness.LocationKind{
		harness.LocationKindSkills,
		harness.LocationKindCommands,
		harness.LocationKindInstructions,
	} {
		got := testharness.LocationPaths(locations, kind)
		if !slices.ContainsFunc(
			got,
			func(path string) bool { return filepath.Dir(path) == override },
		) ||
			slices.ContainsFunc(
				got,
				func(path string) bool { return strings.Contains(path, ".cline") },
			) {
			t.Errorf("cline %s with CLINE_DIR = %v", kind, got)
		}
	}
}

func TestLocationsFollowEnvironmentOverrides(t *testing.T) {
	for _, test := range []struct {
		harness  registry.Harness
		variable string
		value    func(dir string) string
	}{
		{harness: registry.Harness("cline"), variable: "CLINE_DIR"},
		{harness: registry.Harness("cline"), variable: "CLINE_DATA_DIR"},
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

func TestPayloadDefaultsOmitSessionPathWithoutHome(t *testing.T) {
	for _, test := range []struct {
		harness registry.Harness
		payload string
		plant   func(t *testing.T, workDir string)
	}{
		{
			harness: registry.Harness("cline"),
			payload: `{"sessionContext":{"rootSessionId":"abc"},"hookName":"TaskStart"}`,
			plant:   func(*testing.T, string) {},
		},
	} {
		t.Run(string(test.harness), func(t *testing.T) {
			test.plant(t, testharness.WithoutHome(t, New()))

			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}

			got, err := New().PayloadDefaults(payload)
			if err != nil {
				t.Fatalf("PayloadDefaults: %v", err)
			}

			if got.SessionID != "abc" || got.SessionPath != "" {
				t.Fatalf(
					"PayloadDefaults without a home = %+v, want session %q and no path",
					got,
					"abc",
				)
			}
		})
	}
}
