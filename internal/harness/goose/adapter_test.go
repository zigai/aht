package goose

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

func TestMatchersOmitInvalidWildcard(t *testing.T) {
	hooks, ok := gooseHookConfig()["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected Goose hooks map")
	}

	for _, event := range []string{"PreToolUse", "PostToolUse", "BeforeShellExecution"} {
		groups, ok := hooks[event].([]any)
		if !ok || len(groups) != 1 {
			t.Fatalf("expected one %s hook group, got %#v", event, hooks[event])
		}

		group, ok := groups[0].(map[string]any)
		if !ok {
			t.Fatalf("expected %s hook group object", event)
		}

		if _, ok := group["matcher"]; ok {
			t.Fatalf("expected %s matcher to be omitted", event)
		}
	}
}

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
			name: "goose",

			payload: `{"event":"PreToolUse","session_id":"goose-session",` +
				`"working_dir":"/repo/goose","tool_name":"shell"}`,
			wantID:     "goose-session",
			wantPath:   "",
			wantCWD:    "/repo/goose",
			wantRoot:   "/repo/goose",
			wantEvent:  "PreToolUse",
			wantAttr:   "goose_tool_name",
			wantAttrKV: "shell",
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
			name: "goose accepts native hook payload",

			payload: `{"event":"Stop","session_id":"goose-session","working_dir":"/repo"}`,
			want:    true,
		},
		{
			name: "goose rejects payload without session id",

			payload: `{"event":"Stop","working_dir":"/repo"}`,
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
		{harness: registry.Harness("goose"), variable: "GOOSE_PATH_ROOT"},
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
		{registry.Harness("goose"), func(t *testing.T, workDir string) {
			t.Helper()
			testharness.PlantDatabase(
				t,
				workDir,
				"CREATE TABLE sessions (id TEXT, description TEXT)",
				"INSERT INTO sessions VALUES ('abc', 'Decoy')",
				".local",
				"share",
				"goose",
				"sessions",
				"sessions.db",
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
